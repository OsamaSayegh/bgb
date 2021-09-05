package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func checkIfFile(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().IsRegular(), nil
}

func getFilepath() (string, error) {
	argsSize := len(os.Args) - 1
	if argsSize != 1 {
		return "", fmt.Errorf("bgb: expected 1 argument, but received %d arguments.", argsSize)
	}
	fp, err := filepath.Abs(os.Args[1])
	if err != nil {
		return "", fmt.Errorf("bgb: %s", err)
	}
	isFile, err := checkIfFile(fp)
	if err != nil {
		return "", fmt.Errorf("bgb: %s", err)
	}
	if !isFile {
		return "", fmt.Errorf("bgb: the given path is not a file.")
	}
	return fp, nil
}

type AppState struct {
	Ctx            context.Context
	GitBin         string
	Filepath       string
	RepoPath       string
	CurrentSha     string
	CursorPosition int
	CurrentBlame   *Blame
	ShaHistory     []string
}

func HighlightCell(cell *tview.TableCell) {
	cell.SetTextColor(tcell.ColorWhite).SetBackgroundColor(tcell.ColorBlack)
}

func UnhighlighCell(cell *tview.TableCell) {
	cell.SetTextColor(tcell.ColorDefault).SetBackgroundColor(tcell.ColorDefault)
}

func populateContent(table *tview.Table, bottomBar *tview.TextView, state *AppState) error {
	blame, err := FindBlame(state)
	if err != nil {
		return err
	}
	table.Clear()
	state.CurrentBlame = blame
	for i, line := range state.CurrentBlame.Lines {
		c := state.CurrentBlame.LineChunkMap[i]
		sha := "-------"
		summary := "(Not committed)"
		if c.CommitSha != NotCommittedId {
			sha = c.CommitSha[:7]
			summary = firstN(c.Summary, 40)
		}
		table.SetCell(i, 0, tview.NewTableCell(sha).SetTextColor(tcell.ColorYellow).SetSelectable(false))
		table.SetCell(i, 1, tview.NewTableCell(summary).SetSelectable(false))
		table.SetCell(i, 2, tview.NewTableCell(strconv.Itoa(i+1)))
		table.SetCell(i, 3, tview.NewTableCell(line))
	}
	if len(state.CurrentBlame.Lines) <= state.CursorPosition {
		state.CursorPosition = len(state.CurrentBlame.Lines) - 1
	}
	table.Select(state.CursorPosition, 0)
	HighlightCell(table.GetCell(state.CursorPosition, 2))
	HighlightCell(table.GetCell(state.CursorPosition, 3))
	return nil
}

func setErrorMessage(bottomBar *tview.TextView, message string) {
	bottomBar.SetText(message).
		SetTextColor(tcell.ColorWhite).
		SetBackgroundColor(tcell.ColorRed)
}

func setMessage(bottomBar *tview.TextView, message string) {
	bottomBar.SetText(message).
		SetTextColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)
}

func initializeTView(tApp *tview.Application, state *AppState) error {
	table := tview.NewTable()
	bottomBar := tview.NewTextView()

	grid := tview.
		NewGrid().
		SetRows(0, 1).
		AddItem(table, 0, 0, 1, 1, 0, 0, true).
		AddItem(bottomBar, 1, 0, 1, 1, 0, 0, false)

	table.SetSelectable(true, false).SetEvaluateAllRows(true)

	err := populateContent(table, bottomBar, state)
	if err != nil {
		return err
	}

	table.SetSelectionChangedFunc(func(row, _ int) {
		UnhighlighCell(table.GetCell(state.CursorPosition, 2))
		UnhighlighCell(table.GetCell(state.CursorPosition, 3))
		state.CursorPosition = row

		c := state.CurrentBlame.LineChunkMap[row]
		setMessage(bottomBar, c.Previous)
		HighlightCell(table.GetCell(row, 2))
		HighlightCell(table.GetCell(row, 3))
	})

	tApp.SetRoot(grid, true)
	tApp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		r := event.Rune()
		if r == 113 { // q key
			tApp.Stop()
			return nil
		} else if r == 104 { // h key
			c := state.CurrentBlame.LineChunkMap[state.CursorPosition]
			if c.Previous == "" {
				setErrorMessage(bottomBar, fmt.Sprintf("Can't go back because %s is the commit that added this file.", firstN(c.CommitSha, 7)))
				return nil
			}
			state.CurrentSha = c.Previous
			state.ShaHistory = append(state.ShaHistory, c.Previous)
			err := populateContent(table, bottomBar, state)
			if err != nil {
				setErrorMessage(bottomBar, fmt.Sprintf("%s", err))
			}
			return nil
		} else if r == 108 { // l key
			historyLen := len(state.ShaHistory)
			if historyLen == 0 {
				setErrorMessage(bottomBar, "You are on the latest revision of this file.")
				return nil
			}
			state.ShaHistory[historyLen-1] = ""
			state.ShaHistory = state.ShaHistory[:historyLen-1]
			historyLen--
			if historyLen == 0 {
				state.CurrentSha = ""
			} else {
				state.CurrentSha = state.ShaHistory[historyLen-1]
			}
			err := populateContent(table, bottomBar, state)
			if err != nil {
				setErrorMessage(bottomBar, fmt.Sprintf("%s", err))
			}
			return nil
		}
		return event
	})
	err = tApp.Run()
	return err
}

func run() int {
	fp, err := getFilepath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("bgb: %s", err))
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	var _ = cancel

	repo, err := FindRepoFromPath(ctx, gitBin, filepath.Dir(fp))
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("bgb: %s", err))
		return 1
	}

	state := AppState{
		GitBin:         gitBin,
		Ctx:            ctx,
		Filepath:       fp,
		RepoPath:       repo,
		CurrentSha:     "",
		CursorPosition: 0,
	}
	tApp := tview.NewApplication()
	defer tApp.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		tApp.Stop()
	}()
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	if err = initializeTView(tApp, &state); err != nil {
		fmt.Printf("%s\n", err)
		return 1
	}
	return 0
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func main() {
	os.Exit(run())
}
