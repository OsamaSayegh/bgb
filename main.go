package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	SearchInputMode  = 1
	CommandInputMode = 2
)

const (
	DisplayMessageLengthLimit = 45
	DisplayCommitIdLimit      = 7
)

const (
	LineLinkCommand   = "ll"
	CommitLinkCommand = "cl"
)

// this variable is set at compile time by the Makefile
var Version string

type Application struct {
	Context         context.Context
	GitBin          string
	RepoPath        string
	CurrentCommitId string
	CursorPosition  int
	CurrentBlame    *Blame
	History         []*HistoryItem
	SearchTerm      string
	RemoteInfo      *RemoteInfo
	TViewApp        *tview.Application
	Ui              *AppUi
}

type AppUi struct {
	Grid         *tview.Grid
	Table        *tview.Table
	BottomBar    *tview.TextView
	InputBar     *tview.InputField
	InputBarMode int
}

type HistoryItem struct {
	CommitId       string
	CursorPosition int
	Filename       string
}

func (a *Application) CreateGitArgs() *GitCommandArgs {
	return &GitCommandArgs{
		Context:       a.Context,
		GitBinaryPath: a.GitBin,
		RepoPath:      a.RepoPath,
	}
}

func checkIfFile(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().IsRegular(), nil
}

func getFilepath() (string, error) {
	fp, err := filepath.Abs(os.Args[1])
	if err != nil {
		return "", fmt.Errorf("bgb: %s", err)
	}
	isFile, err := checkIfFile(fp)
	if err != nil {
		return "", fmt.Errorf("bgb: %s", err)
	}
	if !isFile {
		return "", fmt.Errorf("bgb: the given %#v path is not a file.", os.Args[1])
	}
	return fp, nil
}

func HighlightCell(cell *tview.TableCell) {
	cell.SetTextColor(tcell.ColorWhite).SetBackgroundColor(tcell.ColorBlack)
}

func UnhighlighCell(cell *tview.TableCell) {
	cell.SetTextColor(tcell.ColorDefault).SetBackgroundColor(tcell.ColorDefault)
}

func TimestampToRelative(timestamp int64) string {
	diff := time.Now().Unix() - timestamp // in seconds
	if diff < 3600 {
		return "< 1h"
	} else if diff < 3600*24 {
		return fmt.Sprintf("%d%s", diff/3600, "h")
	} else if diff < 3600*24*30 {
		return fmt.Sprintf("%d%s", diff/3600/24, "d")
	} else if diff < 3600*24*30*12 {
		return fmt.Sprintf("%d%s", diff/3600/24/30, "m")
	} else {
		return fmt.Sprintf("%d%s", diff/3600/24/30/12, "y")
	}
}

func RenderBlameView(app *Application, blame *Blame) {
	table := app.Ui.Table
	table.Clear()
	// app.CurrentBlame = blame
	for i, line := range blame.Lines {
		c := blame.LineToChunkMap[i]
		id := ""
		age := ""
		summary := "(not committed)"
		if c.CommitId != NotCommittedId {
			id = c.CommitId[:DisplayCommitIdLimit]
			summary = firstN(c.Summary, DisplayMessageLengthLimit, true)
			age = TimestampToRelative(c.AuthorTime)
		}
		var commitIdCell, summaryCell, ageCell, lineNoCell, lineCell *tview.TableCell
		commitIdCell = tview.
			NewTableCell(id).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false)

		summaryCell = tview.
			NewTableCell(tview.Escape(summary)).
			SetSelectable(false)

		ageCell = tview.
			NewTableCell(age).
			SetTextColor(tcell.ColorAqua).
			SetSelectable(false)

		lineNoCell = tview.
			NewTableCell(strconv.Itoa(i + 1)).
			SetAlign(tview.AlignRight).
			SetSelectable(true)

		lineCell = tview.
			// tview has a bug where tabs in strings are completely stripped :(
			NewTableCell(tview.Escape(strings.ReplaceAll(line, "\t", "    "))).
			SetSelectable(true)

		table.SetCell(i, 0, commitIdCell)
		table.SetCell(i, 1, summaryCell)
		table.SetCell(i, 2, ageCell)
		table.SetCell(i, 3, lineNoCell)
		table.SetCell(i, 4, lineCell)
	}
	// newPos := app.CursorPosition
	// if len(app.CurrentBlame.Lines) <= newPos {
	// 	newPos = len(app.CurrentBlame.Lines) - 1
	// }
	// table.Select(newPos, 0)
}

func setErrorMessage(app *Application, message string) {
	app.Ui.BottomBar.SetText(message).
		SetTextColor(tcell.ColorWhite).
		SetBackgroundColor(tcell.ColorRed)
}

func setMessage(app *Application, message string) {
	app.Ui.BottomBar.SetText(message).
		SetTextColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)
}

func performSearch(app *Application, reverse bool) bool {
	linesLen := len(app.CurrentBlame.Lines)
	cursorPos := app.CursorPosition
	for i := 1; i < linesLen; i++ {
		var p int
		if reverse {
			p = cursorPos - i
			if p < 0 {
				p += linesLen
			}
		} else {
			p = (cursorPos + i) % linesLen
		}
		line := app.CurrentBlame.Lines[p]
		if strings.Contains(line, app.SearchTerm) {
			app.Ui.Table.Select(p, 0)
			return true
		}
	}
	return false
}

func buildLineLink(ri *RemoteInfo, id, path string, lineNumber int) (string, error) {
	if ri.Host == "github.com" {
		fullUrl := fmt.Sprintf(
			"https://github.com/%s/blob/%s/%s#L%d",
			ri.Repo,
			id,
			path,
			lineNumber,
		)
		return fullUrl, nil
	} else {
		return "", fmt.Errorf("Cannot construct link for remote %s", ri.Host)
	}
}

func buildCommitLink(ri *RemoteInfo, id string) (string, error) {
	if ri.Host == "github.com" {
		fullUrl := fmt.Sprintf(
			"https://github.com/%s/commit/%s",
			ri.Repo,
			id,
		)
		return fullUrl, nil
	} else {
		return "", fmt.Errorf("Cannot construct link for remote %s", ri.Host)
	}
}

func handleCommand(app *Application, command string) (string, error) {
	if command == LineLinkCommand {
		c := app.CurrentBlame.LineToChunkMap[app.CursorPosition]
		id := c.CommitId
		if id == NotCommittedId {
			return "", fmt.Errorf("Cannot produce a remote link for the selected line because it's not committed")
		}
		var ri *RemoteInfo
		var err error
		if app.RemoteInfo != nil {
			ri = app.RemoteInfo
		} else {
			ri, err = GitFindRemoteInfo(app.CreateGitArgs())
			if err != nil {
				return "", err
			}
		}
		return buildLineLink(
			ri,
			id,
			c.Filename,
			app.CursorPosition+1,
		)
	} else if command == CommitLinkCommand {
		id := app.CurrentBlame.LineToChunkMap[app.CursorPosition].CommitId
		if id == NotCommittedId {
			return "", fmt.Errorf("Cannot produce a remote link for the selected line because it's not committed")
		}
		var ri *RemoteInfo
		var err error
		if app.RemoteInfo != nil {
			ri = app.RemoteInfo
		} else {
			ri, err = GitFindRemoteInfo(app.CreateGitArgs())
			if err != nil {
				return "", err
			}
		}
		return buildCommitLink(ri, id)
	} else {
		return "", fmt.Errorf("Unknown command: %s", command)
	}
}

func TViewInit(app *Application, filenameArg string) error {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault

	var ui *AppUi
	{
		grid := tview.NewGrid()
		table := tview.NewTable()
		bottomBar := tview.NewTextView()
		inputBar := tview.NewInputField()
		ui = &AppUi{
			Grid:      grid,
			Table:     table,
			BottomBar: bottomBar,
			InputBar:  inputBar,
		}
	}
	app.Ui = ui
	tApp := app.TViewApp

	ui.Grid.
		SetRows(0, 1).
		AddItem(ui.Table, 0, 0, 1, 1, 0, 0, true).
		AddItem(ui.BottomBar, 1, 0, 1, 1, 0, 0, false)

	ui.Table.
		SetSelectable(true, false).
		SetEvaluateAllRows(true).
		SetSelectionChangedFunc(func(row, _ int) {
			UnhighlighCell(ui.Table.GetCell(app.CursorPosition, 3))
			UnhighlighCell(ui.Table.GetCell(app.CursorPosition, 4))
			app.CursorPosition = row

			c := app.CurrentBlame.LineToChunkMap[row]
			if c.CommitId != NotCommittedId {
				details := fmt.Sprintf(
					"[white:blue:b]Date[-:-:-] %s [white:blue:b]Author[-:-:-] %s",
					time.Unix(c.AuthorTime, 0).UTC().Format("2006/01/02 15:04 MST"),
					tview.Escape(c.Author),
				)
				if len(c.Summary) > DisplayMessageLengthLimit {
					details += fmt.Sprintf(" [white:blue:b]Message[-:-:-] %s", tview.Escape(c.Summary))
				}
				setMessage(app, details)
			} else {
				setMessage(app, "(not committed)")
			}
			HighlightCell(ui.Table.GetCell(row, 3))
			HighlightCell(ui.Table.GetCell(row, 4))
		}).
		SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			r := event.Rune()
			if r == 113 { // q key
				tApp.Stop()
				return nil
			} else if r == 104 { // h key
				c := app.CurrentBlame.LineToChunkMap[app.CursorPosition]
				if c.PreviousCommitId == "" {
					setErrorMessage(
						app,
						fmt.Sprintf(
							"Can't go back because %s is the commit that added this file.",
							firstN(c.CommitId, DisplayCommitIdLimit, false),
						),
					)
					return nil
				}
				blame, err := GitBlame(
					app.CreateGitArgs(),
					c.PreviousCommitId,
					c.PreviousFilename,
				)
				if err != nil {
					setErrorMessage(app, fmt.Sprintf("%s", err))
					return nil
				}
				RenderBlameView(app, blame)
				historyItem := &HistoryItem{
					CommitId:       app.CurrentCommitId,
					CursorPosition: app.CursorPosition,
					Filename:       c.Filename,
				}
				app.History = append(app.History, historyItem)
				app.CurrentCommitId = c.PreviousCommitId
				app.CurrentBlame = blame
				newPos := app.CursorPosition
				if len(blame.Lines) <= newPos {
					newPos = len(blame.Lines) - 1
				}
				ui.Table.Select(newPos, 0)
				return nil
			} else if r == 108 { // l key
				historyLen := len(app.History)
				if historyLen == 0 {
					setErrorMessage(app, "You are on the latest revision of this file.")
					return nil
				}

				historyItem := app.History[historyLen-1]
				commitId := historyItem.CommitId
				filename := historyItem.Filename
				newPos := historyItem.CursorPosition

				blame, err := GitBlame(
					app.CreateGitArgs(),
					commitId,
					filename,
				)
				if err != nil {
					setErrorMessage(app, fmt.Sprintf("%s", err))
					return nil
				}

				app.History[historyLen-1] = nil
				app.History = app.History[:historyLen-1]
				historyLen--

				RenderBlameView(app, blame)
				app.CurrentCommitId = commitId
				app.CurrentBlame = blame
				ui.Table.Select(newPos, 0)
				return nil
			} else if app.SearchTerm != "" && (r == 78 || r == 110) { // n or N (shift+n) key
				reverse := r == 78
				performSearch(app, reverse)
				return event
			} else if r == 47 || r == 58 { // forward slash key or colon (shift+;) key
				if r == 47 {
					if ui.InputBarMode != SearchInputMode {
						ui.InputBarMode = SearchInputMode
						ui.InputBar.SetLabel("/")
					}
				} else if r == 58 {
					if ui.InputBarMode != CommandInputMode {
						ui.InputBarMode = CommandInputMode
						ui.InputBar.SetLabel(":")
					}
				}
				ui.Grid.RemoveItem(ui.BottomBar)
				ui.Grid.AddItem(ui.InputBar, 1, 0, 1, 1, 0, 0, false)
				tApp.SetFocus(ui.InputBar)
				return nil
			} else if r == 74 { // J (shift+j)
				newPos := app.CursorPosition + 10
				if newPos >= len(app.CurrentBlame.Lines) {
					newPos = len(app.CurrentBlame.Lines) - 1
				}
				ui.Table.Select(newPos, 0)
				return nil
			} else if r == 75 { // K (shift+k)
				newPos := app.CursorPosition - 10
				if newPos < 0 {
					newPos = 0
				}
				ui.Table.Select(newPos, 0)
				return nil
			}
			return event
		})

	ui.InputBar.
		SetFieldBackgroundColor(tcell.ColorDefault).
		SetFieldTextColor(tcell.ColorDefault).
		SetLabelColor(tcell.ColorDefault).
		SetDoneFunc(func(key tcell.Key) {
			tApp.SetFocus(ui.Table)
			ui.Grid.RemoveItem(ui.InputBar)
			ui.Grid.AddItem(ui.BottomBar, 1, 0, 1, 1, 0, 0, false)
			inputContent := ui.InputBar.GetText()
			if inputContent != "" && key == 13 { // enter key
				if ui.InputBarMode == SearchInputMode {
					app.SearchTerm = strings.TrimSpace(inputContent)
					foundResults := performSearch(app, false)
					if !foundResults {
						setErrorMessage(app, fmt.Sprintf("Pattern not found: %s", app.SearchTerm))
					}
				} else if ui.InputBarMode == CommandInputMode {
					results, commandErr := handleCommand(app, inputContent)
					if commandErr != nil {
						setErrorMessage(app, fmt.Sprintf("%s", commandErr))
					} else {
						setMessage(app, results)
					}
				} else {
					setErrorMessage(app, fmt.Sprintf("Unknown input mode %d", ui.InputBarMode))
					return
				}
				ui.InputBar.SetText("")
			}
		})

	ui.BottomBar.
		SetDynamicColors(true)

	blame, err := GitBlame(app.CreateGitArgs(), "", filenameArg)
	if err != nil {
		return err
	}
	RenderBlameView(app, blame)
	app.CurrentBlame = blame
	ui.Table.Select(0, 0)

	tApp.SetRoot(ui.Grid, true)
	err = tApp.Run()
	return err
}

func run() int {
	argsSize := len(os.Args) - 1
	if argsSize != 1 {
		fmt.Fprintf(os.Stderr, "bgb: expected 1 argument, but received %d arguments.\n", argsSize)
		fmt.Fprintf(os.Stderr, "Usage: %s <FILE>\n", os.Args[0])
		return 1
	}

	if os.Args[1] == "--version" {
		fmt.Printf("bgb %s\nCopyright (C) 2021 Osama Sayegh\n", Version)
		return 0
	}

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

	gitArgs := &GitCommandArgs{
		Context:       ctx,
		GitBinaryPath: gitBin,
		RepoPath:      filepath.Dir(fp),
	}
	repo, err := GitAttemptRepoLookup(gitArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("bgb: %s", err))
		return 1
	}

	tApp := tview.NewApplication()
	defer tApp.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		tApp.Stop()
		cancel()
	}()
	app := Application{
		GitBin:          gitBin,
		Context:         ctx,
		RepoPath:        repo,
		CurrentCommitId: "",
		CursorPosition:  0,
		TViewApp:        tApp,
	}
	if err = TViewInit(&app, fp); err != nil {
		fmt.Printf("%s\n", err)
		return 1
	}
	return 0
}

func firstN(s string, n int, ellipsis bool) string {
	if len(s) <= n {
		return s
	}
	if ellipsis {
		return s[:n-3] + "..."
	} else {
		return s[:n]
	}
}

func main() {
	os.Exit(run())
}
