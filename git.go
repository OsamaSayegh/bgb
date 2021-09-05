package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var BlameChunkHeader = regexp.MustCompile(`\A([0-9a-f]{40})\s(\d+)\s(\d+)\s(\d+)\z`)
var LineInChunkHeader = regexp.MustCompile(`\A[0-9a-f]{40}\s\d+\s(\d+)\z`)

const (
	AuthorKey     = "author"
	AuthorMailKey = "author-mail"
	PreviousKey   = "previous"
	SummaryKey    = "summary"
)

const NotCommittedId = "0000000000000000000000000000000000000000"

type BlameChunk struct {
	CommitSha  string
	Previous   string
	Author     string
	AuthorMail string
	Summary    string
}

type Blame struct {
	Lines        []string
	LineChunkMap map[int]*BlameChunk
}

func FindRepoFromPath(ctx context.Context, gitBin string, dir string) (string, error) {
	cmd := exec.CommandContext(
		ctx,
		gitBin,
		"-C",
		dir,
		"rev-parse",
		"--show-toplevel",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error while executing git command: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func FindBlame(state *AppState) (b *Blame, err error) {
	// git -C <repo> blame --porcelain <path> [<sha>]
	argsCount := 5
	if state.CurrentSha != "" {
		argsCount += 1
	}
	args := make([]string, 0, argsCount)
	args = append(args, "-C")
	args = append(args, state.RepoPath)
	args = append(args, "blame")
	args = append(args, "--porcelain")
	args = append(args, state.Filepath)
	if state.CurrentSha != "" {
		args = append(args, state.CurrentSha)
	}
	cmd := exec.CommandContext(state.Ctx, state.GitBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	p, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	err = cmd.Start()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			return
		}
		err = cmd.Wait()
		if err != nil {
			err = fmt.Errorf("git blame command failed: %s", strings.TrimSpace(stderr.String()))
		}
	}()

	scanner := bufio.NewScanner(p)
	lineChunkMap := make(map[int]*BlameChunk)
	shaChunkMap := make(map[string]*BlameChunk)
	linesInChunk := 0
	lineNumber := 0
	chunkPopulated := false
	var lines []string
	var chunk *BlameChunk

	for scanner.Scan() {
		line := scanner.Text()
		if linesInChunk == 0 {
			matches := BlameChunkHeader.FindStringSubmatch(line)
			if matches == nil {
				err = fmt.Errorf("unexpected format of line %#v in git blame output.", line)
				break
			}
			sha := matches[1]
			if shaChunkMap[sha] != nil {
				chunkPopulated = true
				chunk = shaChunkMap[sha]
			} else {
				chunkPopulated = false
				chunk = &BlameChunk{}
				chunk.CommitSha = sha
				shaChunkMap[sha] = chunk
			}
			lineNumber, err = strconv.Atoi(matches[3])
			linesInChunk, err = strconv.Atoi(matches[4])
			if err != nil {
				return nil, err
			}
			// convert to zero-indexed lines
			lineNumber -= 1
		} else if matches := LineInChunkHeader.FindStringSubmatch(line); matches != nil {
			lineNumber, err = strconv.Atoi(matches[1])
			if err != nil {
				return nil, err
			}
			// convert to zero-indexed lines
			lineNumber -= 1
		} else if strings.HasPrefix(line, "\t") {
			linesInChunk -= 1
			lineChunkMap[lineNumber] = chunk
			lines = append(lines, strings.Replace(line, "\t", "", 1))
		} else if !chunkPopulated {
			if val, ok := FindInterestingValue(AuthorKey, line); ok {
				chunk.Author = val
			} else if val, ok := FindInterestingValue(AuthorMailKey, line); ok {
				chunk.AuthorMail = val
			} else if val, ok := FindInterestingValue(PreviousKey, line); ok {
				chunk.Previous = val[:40]
			} else if val, ok := FindInterestingValue(SummaryKey, line); ok {
				chunk.Summary = val
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}

	b = &Blame{Lines: lines, LineChunkMap: lineChunkMap}
	return b, nil
}

func FindInterestingValue(name string, line string) (string, bool) {
	if strings.HasPrefix(line, name+" ") {
		return strings.Replace(line, name+" ", "", 1), true
	} else {
		return "", false
	}
}
