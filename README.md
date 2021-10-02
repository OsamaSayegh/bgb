## `bgb`

`bgb` is an interactive command-line program that's built on top of Git's `blame` command and inspired by GitHub's [blame view](https://github.com/OsamaSayegh/bgb/blame/main/main.go). It takes a file path tracked in a Git repository and produces an interactable view that annotates each line in the given file with information about the commit that last modified the line, with the ability to navigate the history of the file.

## Installation

(todo)

## Usage

After installing the `bgb` binary somewhere on your `$PATH`, call the program like this:

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

(todo)
