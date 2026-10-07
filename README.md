# tjek

A keyboard-driven task manager for the terminal that tells you what to do next.

[![CI](https://github.com/Morteningemann86/xtratjek/actions/workflows/ci.yml/badge.svg?branch=main&event=push)](https://github.com/Morteningemann86/xtratjek/actions/workflows/ci.yml?query=branch%3Amain)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?style=flat&logo=go)
![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey?style=flat)
![License](https://img.shields.io/badge/license-MIT-green?style=flat)

![The Tasks tab: a ranked list beside the selected task's details](docs/img/tasks.png)

tjek runs on Linux, macOS and Windows and keeps everything in a local file.
It can sync between your machines through a small server you run yourself.
No account, no cloud service.

## What it does

- **Ranks your tasks.** Deadline, priority, recent work, size and age decide
  the order, and a task that blocks something urgent rises with it. Press `w`
  to see why a task ranks where it does.
- **Holds the details.** Due and start dates, priority, size, tags, projects,
  subtasks, dependencies, comments, descriptions and repeating tasks.
- **Adds in one line.** `Buy milk #shopping due:friday p:high @home`
- **Shows the same tasks five ways:** a list, a calendar with tracked time,
  projects with a timeline, tags, and a kanban board. Plus a stats page.
- **Tracks time.** Start and stop a timer on a task with `t`.
- **Reminds you daily** with a desktop notification of what's due and overdue.
- **Syncs** between computers, **undoes** every change, and has a **command
  palette** (`ctrl+k`) and a **command line** for scripting.
- **Speaks English, Danish and German**, with themes and rebindable keys.

## Every tab

**Calendar**: tracked time day by day, with a month beside it.

![The Calendar tab: a day's time entries beside the month](docs/img/calendar.png)

**Tags**: every tag with its open, overdue and next task, and the tasks under the selected one.

![The Tags tab: tags with counts above the selected tag's tasks](docs/img/tags.png)

**Projects**: the same for projects, with a timeline of their dated tasks.

![The Projects tab: projects with counts above the selected project's tasks and timeline](docs/img/projects.png)

**Board**: tasks as cards, carried between columns.

![The Board tab: tasks as cards in Backlog, In progress, Review and Done](docs/img/board.png)

**Stats**: workload, flow and a chart of what you finished.

![The Stats tab: summary numbers above a chart of completed tasks](docs/img/stats.png)

**Settings**: theme, language, sync, reminders and export.

![The Settings tab: grouped preferences](docs/img/settings.png)

## Install

| | |
|---|---|
| macOS | `brew install iliorn/tap/tjek` |
| Windows | `scoop install https://github.com/Morteningemann86/xtratjek/releases/latest/download/tjek.json` |
| Linux / Windows binary | [Releases](https://github.com/Morteningemann86/xtratjek/releases) |
| Anywhere Go runs | `go install github.com/Morteningemann86/xtratjek@latest` |

tjek updates itself from Settings → "Update to latest release".
[docs/install.md](docs/install.md) covers building from source and verifying
a download.

## Getting started

Run `tjek`.

| Key | Action |
|-----|--------|
| `a` | Add task |
| `d` | Done |
| `enter` | Details: comments, subtasks, dependencies, description |
| `t` | Start/stop the timer |
| `D` | Set the due date |
| `/` | Filter |
| `f` | Focus on today and overdue |
| `u` | Undo |
| `tab` / `1–7` | Switch tab |
| `?` | Every key |
| `ctrl+k` | Find any action by name |

`#tag`, `@project`, `due:friday`, `p:high` and `s:l` fill in a new task, and
the same words filter: `/` then `@work overdue` shows your overdue work tasks.
Dates can be `today`, `tomorrow`, `monday`, `+3d`, `-2d`, `15-06-25` and more.

## Learn more

- [Using tjek](docs/guide.md): every tab, search, the board, reminders and custom keys
- [Command line](docs/cli.md): `tjek add`, `list`, `done`, JSON output, export and import
- [Sync between devices](docs/sync.md): running a server and connecting your machines
- [Files and troubleshooting](docs/troubleshooting.md): where data lives, backups, crashes
- [Changelog](CHANGELOG.md)

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) covers building and testing, and
[ARCHITECTURE.md](ARCHITECTURE.md) how tjek is put together. Security issues
go through [SECURITY.md](SECURITY.md).

## License

MIT
