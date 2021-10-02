package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/url"
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
	AuthorTimeKey = "author-time"
	PreviousKey   = "previous"
	SummaryKey    = "summary"
	FilenameKey   = "filename"
)

const NotCommittedId = "0000000000000000000000000000000000000000"

type GitCommandArgs struct {
	Context       context.Context
	GitBinaryPath string
	RepoPath      string
}

type BlameChunk struct {
	CommitId         string
	PreviousCommitId string
	Author           string
	AuthorMail       string
	AuthorTime       int64
	Summary          string
	Filename         string
	PreviousFilename string
}

type RemoteInfo struct {
	Host string
	Repo string
}

type Blame struct {
	Lines          []string
	LineToChunkMap map[int]*BlameChunk
}

func GitAttemptRepoLookup(gitArgs *GitCommandArgs) (string, error) {
	cmd := exec.CommandContext(
		gitArgs.Context,
		gitArgs.GitBinaryPath,
		"-C",
		gitArgs.RepoPath,
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

func GitBlame(gitArgs *GitCommandArgs, commitId, filename string) (b *Blame, err error) {
	// git -C <repo> blame --porcelain <filename> [<commitId>]
	argsCount := 5
	if commitId != "" {
		argsCount += 1
	}
	args := make([]string, 0, argsCount)
	args = append(args, "-C")
	args = append(args, gitArgs.RepoPath)
	args = append(args, "blame")
	args = append(args, "--porcelain")
	args = append(args, filename)
	if commitId != "" {
		args = append(args, commitId)
	}
	cmd := exec.CommandContext(gitArgs.Context, gitArgs.GitBinaryPath, args...)
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
	lineToChunkMap := make(map[int]*BlameChunk)
	idToChunkMap := make(map[string]*BlameChunk)
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
			id := matches[1]
			if idToChunkMap[id] != nil {
				chunkPopulated = true
				chunk = idToChunkMap[id]
			} else {
				chunkPopulated = false
				chunk = &BlameChunk{}
				chunk.CommitId = id
				idToChunkMap[id] = chunk
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
			lineToChunkMap[lineNumber] = chunk
			lines = append(lines, strings.Replace(line, "\t", "", 1))
		} else if !chunkPopulated {
			if val, ok := FindInterestingValue(AuthorKey, line); ok {
				chunk.Author = val
			} else if val, ok := FindInterestingValue(AuthorMailKey, line); ok {
				chunk.AuthorMail = val
			} else if val, ok := FindInterestingValue(PreviousKey, line); ok {
				chunk.PreviousCommitId = val[:40]
				chunk.PreviousFilename = val[41:]
			} else if val, ok := FindInterestingValue(SummaryKey, line); ok {
				chunk.Summary = val
			} else if val, ok := FindInterestingValue(FilenameKey, line); ok {
				chunk.Filename = val
			} else if val, ok := FindInterestingValue(AuthorTimeKey, line); ok {
				timestamp, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return nil, err
				}
				chunk.AuthorTime = timestamp
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}

	b = &Blame{Lines: lines, LineToChunkMap: lineToChunkMap}
	return b, nil
}

func GitFindRemoteInfo(gitArgs *GitCommandArgs) (*RemoteInfo, error) {
	cmd := exec.CommandContext(
		gitArgs.Context,
		gitArgs.GitBinaryPath,
		"-C",
		gitArgs.RepoPath,
		"ls-remote",
		"--get-url",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("error while executing git command: %s", strings.TrimSpace(stderr.String()))
	}
	raw := strings.TrimSpace(stdout.String())
	ri, err := parseRemoteUrl(raw)
	if err != nil {
		return nil, err
	}
	return ri, nil
}

func parseRemoteUrl(raw string) (*RemoteInfo, error) {
	if strings.HasSuffix(raw, ".git") {
		raw = raw[:len(raw)-4]
	}
	var host, repo string
	if strings.HasPrefix(raw, "git@") {
		raw = strings.Replace(raw, "git@", "", 1)
		hostAndRepoSlice := strings.SplitN(raw, ":", 2)
		host = hostAndRepoSlice[0]
		repo = hostAndRepoSlice[1]
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		host = u.Host
		repo = u.Path
	}
	return &RemoteInfo{Host: strings.Trim(host, "/"), Repo: strings.Trim(repo, "/")}, nil
}

func FindInterestingValue(name string, line string) (string, bool) {
	if strings.HasPrefix(line, name+" ") {
		return strings.Replace(line, name+" ", "", 1), true
	} else {
		return "", false
	}
}
