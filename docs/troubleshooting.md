# Files and troubleshooting

`tjek doctor` is the first stop: it prints the version, where every file
lives, and whether the database, settings, sync, editor and ffmpeg are
healthy. Paste its output into a bug report.

## "ffmpeg not found" when recording a meeting

Recording a meeting (Meetings tab, `r`) shells out to `ffmpeg`, which tjek
doesn't bundle or install — `tjek doctor` reports whether it can find it on
`PATH`. Install it with:

| | |
|---|---|
| macOS | `brew install ffmpeg` |
| Windows (Scoop) | already installed automatically — `scoop install tjek` pulls it in as a dependency |
| Windows (other) | `winget install ffmpeg`, `choco install ffmpeg` |
| Debian / Ubuntu | `sudo apt install ffmpeg` |
| Fedora | `sudo dnf install ffmpeg` |
| Arch | `sudo pacman -S ffmpeg` |

Everything else in Meetings — typing notes, summarizing, reviewing action
items — works without it; only recording and transcribing a live meeting
need it.

## Where your data lives

Tasks are kept in a SQLite database. Where it and tjek's other files go
depends on what tjek finds, in this order:

1. **`TJEK_HOME`**, if set: everything in that one directory.
2. **`~/.tjek/`**, if that directory exists. Installs from before v1.32
   keep everything in one directory like this.
3. Otherwise each platform's usual places:

tjek was called taskr until v1.42. Its first run renames taskr's directories
to tjek's (`~/.taskr` to `~/.tjek`, `~/.config/taskr` to `~/.config/tjek`, and
so on), and a binary still called `taskr` to `tjek`; nothing else moves.

| | Linux / BSD | macOS | Windows |
|---|---|---|---|
| Settings: `settings.json`, `sync.json` | `~/.config/tjek` | `~/Library/Application Support/tjek` | `%APPDATA%\tjek` |
| Tasks: `tasks.db` | `~/.local/share/tjek` | `~/Library/Application Support/tjek` | `%LOCALAPPDATA%\tjek` |
| State: undo history, sync state, logs | `~/.local/state/tjek` | `~/Library/Application Support/tjek` | `%LOCALAPPDATA%\tjek` |
| Cache: the `$EDITOR` scratch file | `~/.cache/tjek` | `~/Library/Caches/tjek` | `%LOCALAPPDATA%\tjek` |

An `XDG_*` variable you have set wins on every platform, macOS and Windows
included.

To back up, copy the tasks directory, or run `tjek export > backup.json`.

## "This database is from a newer tjek"

A database that a newer version has already upgraded won't open in an older
one: tjek says which version it found rather than showing an empty list.
Update tjek, or restore the `tasks.db-pre-migration-*.bak` the upgrade saved
next to it.

## After a crash

tjek writes `crash-<timestamp>.log` to the state directory, with the error,
the version, the platform, the window size and what was on screen, and saves
any edits that were still waiting to be written. The newest five are kept.
Attaching one to an issue is the whole bug report.

## When something feels slow

First try `TJEK_NO_WATCH=1 tjek`. Watching the data folder for changes is
the only thing tjek does continuously, and a synced or network home folder,
or antivirus scanning the database on every write, can make it expensive. If
that fixes it, the cost is in the watch; the trade-off is that the app stops
noticing `tjek add` from another window until it next reloads.

If it doesn't, measure: `TJEK_TRACE=1 tjek` writes one line per screen
update to `trace.log` in the state directory (`TJEK_TRACE=/path/to/file`
picks another place):

```
# time                     gap_ms  update_ms  view_ms  gc  msg
12:53:42.841      1.0      0.066    0.915   8  key down
12:53:42.863     15.9      0.641    1.632   9  main.reloadedMsg
```

Quitting adds a summary, which is usually all a report needs:

```
# summary over 412 frames (ms)
#   update  p50   0.090   p95   0.140   max   1.900
#   view    p50   0.900   p95   1.300   max   4.100
```

Large `update_ms` or `view_ms` means tjek itself is slow; a slow frame where
`gc` moved is garbage collection; a fast frame with a long `gap_ms` means the
time went somewhere outside the app: the terminal, ssh, or the keyboard
reader.

**On Windows**, tjek reads the keyboard as a stream so a key arriving after
a pause is seen at once. If that misbehaves on a particular console,
`TJEK_WIN_CONSOLE_INPUT=1` switches to Windows' own console-event reader.
