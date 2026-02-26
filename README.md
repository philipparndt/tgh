# tgh

A terminal UI for browsing GitHub Actions workflow runs and job logs

## Features

- **Browse workflow runs** — lists recent runs with name, branch, trigger event, status and age
- **Browse jobs** — drill into a run to see all jobs with status and duration
- **Live log streaming** — watch running jobs in real time with step-by-step progress
- **Log viewer** — scrollable, syntax-highlighted log output for completed jobs
- **Log filtering** — fuzzy-filter log lines with `/`
- **Copy logs** — copy the full log to clipboard with `c`
- **Open in browser** — jump to the GitHub UI with `o`
- **Rerun workflows** — trigger rerun of failed or all jobs without leaving the terminal
- **Auto-scroll** — automatically follow new log output as it arrives
- **GHES support** — works with GitHub Enterprise Server

## Requirements

- [gh](https://cli.github.com/) — GitHub CLI, authenticated (`gh auth login`)

## Installation

### Homebrew

```sh
brew tap philipparndt/tgh
brew install tgh
```

### Download binary

Download the latest release from the [releases page](https://github.com/philipparndt/tgh/releases) and place it on your `PATH`.

### Build from source

```sh
git clone https://github.com/philipparndt/tgh.git
cd tgh
go build -o tgh .
```

## Usage

```
tgh [REPO_PATH] [--debug <filename>]
```

Run in the current directory (must be inside a git repository):

```sh
tgh
```

Run against a specific repository path:

```sh
tgh /path/to/repo
```

Enable debug logging to a file:

```sh
tgh --debug /tmp/tgh.log
```

## Key bindings

### Runs list

| Key | Action |
|-----|--------|
| `enter` | Open jobs for the selected run |
| `r` | Re-run failed jobs |
| `R` | Re-run all jobs |
| `tab` / `ctrl+r` | Refresh |
| `/` | Filter runs |
| `q` | Quit |

### Jobs list

| Key | Action |
|-----|--------|
| `enter` | Open logs for the selected job |
| `o` | Open job in browser |
| `r` | Re-run failed jobs |
| `R` | Re-run all jobs |
| `esc` / `b` | Back to runs |
| `q` | Quit |

### Log viewer

| Key | Action |
|-----|--------|
| `↑` / `↓` | Scroll |
| `PgUp` / `PgDn` | Scroll by page |
| `g` | Jump to top |
| `G` | Jump to bottom |
| `a` | Toggle auto-scroll |
| `/` | Filter log lines |
| `c` | Copy log to clipboard |
| `o` | Open job in browser |
| `r` | Refresh |
| `esc` / `b` | Back to jobs |
| `q` | Quit |

## License

MIT
