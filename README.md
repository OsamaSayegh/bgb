## `bgb`

`bgb` is an interactive command-line program that's built on top of Git's `blame` command and inspired by GitHub's [blame view](https://github.com/OsamaSayegh/bgb/blame/main/main.go). It takes a file path tracked in a Git repository and produces an interactable view that annotates each line in the given file with information about the commit that last modified the line, with the ability to navigate the history of the file.

## Installation

Download a binary that suits your OS/architecture from the [Releases](https://github.com/OsamaSayegh/bgb/releases) page of this repository and put the binary somewhere on your `$PATH`.

Here's an example that installs `bgb` version 0.0.2 for linux and amd64 architecture using `wget`:

```
wget https://github.com/OsamaSayegh/bgb/releases/download/v0.0.2/bgb-linux-amd64
sudo cp bgb-linux-amd64 /usr/bin/bgb
```

## Usage

After installing `bgb`, call the program like this:

```
bgb <FILE>
```

where `<FILE>` is substituted with the path of a file tracked in a Git repository. It's not necessary to `cd` into the root directory of the repository; you can call `bgb` from anywhere.

### Interactive Commands

Keys | Function
| - | -
<kbd>j</kbd> | scroll down 1 line
<kbd>k</kbd> | scroll up 1 line
<kbd>shift</kbd>+<kbd>j</kbd> | scroll down 10 lines
<kbd>shift</kbd>+<kbd>k</kbd> | scroll up 10 lines
<kbd>g</kbd> | go to the first line of the file
<kbd>shift</kbd>+<kbd>g</kbd> | go to the last line of the file
<kbd>h</kbd> | go back in the file history and see it before the commit that last modified the selected line
<kbd>l</kbd> | go forward in the file history (undo what <kbd>h</kbd> does)
<kbd>/</kbd> | enable search mode to search within the file
<kbd>n</kbd> | go to the next search result
<kbd>shift</kbd>+<kbd>n</kbd> | go to the previous search result
<kbd>q</kbd> | quit the program
<kbd>ctrl</kbd>+<kbd>c</kbd> | quit the program

## Development

1) Clone this repository:
```
git clone https://github.com/OsamaSayegh/bgb.git
```

2) Make your changes
3) Run `make` to compile the program with your changes. `make` writes the binaries to a `_dist` directory
4) Test the compiled binaries
5) Run `make format` to format the source files
6) Commit & push

## Cutting a New Release

(You need to have push access to this repository to complete these instructions)

1) Make sure the `main` branch is checked out and there are no uncommitted changes (use `git status`)
2) Bump the `VERSION` variable at the top of the `Makefile`
3) Follow the steps in the Development section above to commit the version bump change
4) Run `make release`
5) On GitHub, go to the [Releases](https://github.com/OsamaSayegh/bgb/releases) page and create a new release for the new version and attach the binaries in the `_dist` directory with the release.
