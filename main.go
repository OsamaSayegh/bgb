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
	DisplayShaLimit           = 7
)

const (
	LineLinkCommand   = "ll"
	CommitLinkCommand = "cl"
)

type Application struct {
	Ctx            context.Context
	GitBin         string
	Filepath       string
	RepoPath       string
	CurrentSha     string
	CursorPosition int
	CurrentBlame   *Blame
	ShaHistory     []string
	SearchTerm     string
	RemoteInfo     *RemoteInfo
	TViewApp       *tview.Application
	Ui             *AppUi
}

type AppUi struct {
	Grid         *tview.Grid
	Table        *tview.Table
	BottomBar    *tview.TextView
	InputBar     *tview.InputField
	InputBarMode int
}

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

func populateContent(app *Application) error {
	blame, err := FindBlame(app)
	if err != nil {
		return err
	}
	table := app.Ui.Table
	table.Clear()
	app.CurrentBlame = blame
	for i, line := range app.CurrentBlame.Lines {
		c := app.CurrentBlame.LineChunkMap[i]
		sha := ""
		age := ""
		summary := "(not committed)"
		if c.CommitSha != NotCommittedId {
			sha = c.CommitSha[:DisplayShaLimit]
			summary = firstN(c.Summary, DisplayMessageLengthLimit, true)
			age = TimestampToRelative(c.AuthorTime)
		}
		var shaCell, summaryCell, ageCell, lineNoCell, lineCell *tview.TableCell
		shaCell = tview.
			NewTableCell(sha).
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

		table.SetCell(i, 0, shaCell)
		table.SetCell(i, 1, summaryCell)
		table.SetCell(i, 2, ageCell)
		table.SetCell(i, 3, lineNoCell)
		table.SetCell(i, 4, lineCell)
	}
	newPos := app.CursorPosition
	if len(app.CurrentBlame.Lines) <= newPos {
		newPos = len(app.CurrentBlame.Lines) - 1
	}
	table.Select(newPos, 0)
	return nil
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

func buildLineLink(ri *RemoteInfo, sha, path string, lineNumber int) (string, error) {
	if ri.Host == "github.com" {
		fullUrl := fmt.Sprintf(
			"https://github.com/%s/blob/%s/%s#L%d",
			ri.Repo,
			sha,
			path,
			lineNumber,
		)
		return fullUrl, nil
	} else {
		return "", fmt.Errorf("Cannot construct link for remote %s", ri.Host)
	}
}

func buildCommitLink(ri *RemoteInfo, sha string) (string, error) {
	if ri.Host == "github.com" {
		fullUrl := fmt.Sprintf(
			"https://github.com/%s/commit/%s",
			ri.Repo,
			sha,
		)
		return fullUrl, nil
	} else {
		return "", fmt.Errorf("Cannot construct link for remote %s", ri.Host)
	}
}

func handleCommand(app *Application, command string) (string, error) {
	if command == LineLinkCommand {
		sha := app.CurrentBlame.LineChunkMap[app.CursorPosition].CommitSha
		if sha == NotCommittedId {
			return "", fmt.Errorf("Cannot produce a remote link for the selected line because it's not committed")
		}
		ri, err := FindRemoteInfo(app)
		if err != nil {
			return "", err
		}
		path := strings.Trim(strings.Replace(app.Filepath, app.RepoPath, "", 1), "/")
		return buildLineLink(
			ri,
			sha,
			path,
			app.CursorPosition+1,
		)
	} else if command == CommitLinkCommand {
		sha := app.CurrentBlame.LineChunkMap[app.CursorPosition].CommitSha
		if sha == NotCommittedId {
			return "", fmt.Errorf("Cannot produce a remote link for the selected line because it's not committed")
		}
		ri, err := FindRemoteInfo(app)
		if err != nil {
			return "", err
		}
		return buildCommitLink(ri, sha)
	} else {
		return "", fmt.Errorf("Unknown command: %s", command)
	}
}

func TViewInit(app *Application) error {
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

			c := app.CurrentBlame.LineChunkMap[row]
			if c.CommitSha != NotCommittedId {
				details := fmt.Sprintf(
					"Date: %s, Author: %s",
					time.Unix(c.AuthorTime, 0).UTC().Format("2006/01/02 15:04 MST"),
					c.Author,
				)
				if len(c.Summary) > DisplayMessageLengthLimit {
					details += fmt.Sprintf(", Message: %s", c.Summary)
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
				c := app.CurrentBlame.LineChunkMap[app.CursorPosition]
				if c.Previous == "" {
					setErrorMessage(
						app,
						fmt.Sprintf(
							"Can't go back because %s is the commit that added this file.",
							firstN(c.CommitSha, DisplayShaLimit, false),
						),
					)
					return nil
				}
				app.CurrentSha = c.Previous
				app.ShaHistory = append(app.ShaHistory, c.Previous)
				err := populateContent(app)
				if err != nil {
					setErrorMessage(app, fmt.Sprintf("%s", err))
				}
				return nil
			} else if r == 108 { // l key
				historyLen := len(app.ShaHistory)
				if historyLen == 0 {
					setErrorMessage(app, "You are on the latest revision of this file.")
					return nil
				}
				app.ShaHistory[historyLen-1] = ""
				app.ShaHistory = app.ShaHistory[:historyLen-1]
				historyLen--
				if historyLen == 0 {
					app.CurrentSha = ""
				} else {
					app.CurrentSha = app.ShaHistory[historyLen-1]
				}
				err := populateContent(app)
				if err != nil {
					setErrorMessage(app, fmt.Sprintf("%s", err))
				}
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

	err := populateContent(app)
	if err != nil {
		return err
	}

	tApp.SetRoot(ui.Grid, true)
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

	repo, err := FindRepoFromPath(ctx, gitBin, filepath.Dir(fp))
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
		GitBin:         gitBin,
		Ctx:            ctx,
		Filepath:       fp,
		RepoPath:       repo,
		CurrentSha:     "",
		CursorPosition: 0,
		TViewApp:       tApp,
	}
	if err = TViewInit(&app); err != nil {
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
