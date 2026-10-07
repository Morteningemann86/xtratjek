# Changelog

Notable changes to tjek (called taskr before v1.42.0). The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions are the git tags the [release workflow](.github/workflows/release.yml) builds from.

Entries describe what changed for someone *using* tjek. Refactors and test work
belong in the commit log, not here, unless they change behaviour.

**One line per entry.** No explanation, no rationale, no second sentence: the
reader is deciding whether to upgrade, not reviewing the change. The reasoning
belongs in the commit message, where it is kept next to the code it explains.
`TestChangelogEntriesAreOneLine` enforces it.

## [1.43.0] - 2026-10-07

### Added

- Meetings tab (8): record or type notes; AI summarizes them and proposes action items to accept.
- Recordings are transcribed in 5-minute chunks as the meeting runs; the audio is then deleted.
- Local Whisper (Settings → AI & Meetings) transcribes on your own machine, with no API key.
- tjek offers to install ffmpeg and whisper-cli for you, after asking.
- Chat tab (9): ask about tasks, projects and meetings; it can change a task once you confirm.
- Esc on the Chat tab clears the conversation, after a y/n.
- AI providers: Anthropic, OpenAI, Gemini and Mistral.
- The detail pane has a section bar; → opens a section at the top of the pane.

### Changed

- tjek now lives at github.com/Morteningemann86/xtratjek, and updates come from its releases.
- On macOS, install with `go install github.com/Morteningemann86/xtratjek@latest`.
- Chat names the missing API key and where to add it in Settings.
- A task that starts later shows its start day instead of a score.
- Tags comes before Projects in the tab bar, and the / field is called Filter.
- Footer hints show only each screen's essential keys.

### Removed

- The Homebrew tap; macOS builds from source with `go install`.

## [1.42.0] - 2026-09-29

### Changed

- taskr is now tjek: the command, its files and the repository. The first run moves everything over.
- Environment variables are `TJEK_HOME`, `TJEK_SYNC_URL`, `TJEK_SYNC_TOKEN` and so on.

### Removed

- The Arch Linux (AUR) package; on Arch, use the Linux binary from Releases, which updates itself.

## [1.41.0] - 2026-09-29

### Added

- Board columns can have an icon (`[R] Review`), shown in each task's status box; Done shows ✓.
- Settings → Export keeps a JSON export of all tasks current in a folder you choose.
- Settings → Import from file merges a taskr export in, as one undo step.

### Changed

- Projects works like Tags: enter walks the tasks under the project list, timeline beside them.
- Settings has a Daily reminder switch, with the reminder's time on its own row.
- Sequence ranks a task with a start date in the future below today's work until that day.
- Task lists show tags as #tag and clip a long one instead of replacing the whole cell with +N.
- Danish and German task lists use short column headings, so Project no longer drops out first.
- Stats summary scrolls with ↑/↓ when the window is too short to show all of it.
- Due dates accept -2d, -1w and -1m, the form the list shows overdue dates in.
- Quick-add and `taskr add` name the tokens they did not understand instead of hiding them.
- Board: a long column shows compact two-row cards before it scrolls.
- Stats: a window under 30 rows gives the summary the Activity chart's height.
- Projects: the timeline waits for a wider window rather than squeezing the task list's columns.
- Tags: the (untagged) row sorts with the others instead of always leading.
- `taskr help` fits an 80-column terminal.
- Messages, help and hints use plain punctuation instead of em dashes; an empty field shows "-".

### Fixed

- A desktop pop-up that can't be shown no longer hides the reminder; its reason is one line.
- Windows: reminder pop-ups skip PowerShell, so a locked-down work PC still shows them.
- Stats: Danish weekday names no longer split around æ/ø/å ("Lø rdag").
- Stats: breakdown percentages are rounded, so 6 of 9 reads 67%.
- `taskr doctor` finds sync.log; messages, help and man page name the real file locations.
- Danish counts agree in the singular ("1 færdig"), English ones too ("1 day overdue").
- Settings keeps the ⏎ on a clipped value; the calendar no longer lists tags with 0m.
- Calendar: Coming up lists a parent's deadline once, not again for each subtask due with it.

## [1.40.0] - 2026-09-28

### Added

- A daily desktop reminder lists what is due today and overdue; set its time in Settings.
- `taskr remind` sends the same reminder from cron or a timer; `--now` sends it straight away.
- Settings → "Subtasks copy tags" chooses whether a new subtask takes its parent's tags.
- The Calendar lists what is due in the week from the selected day under the month grid.

### Changed

- The 26-week Stats chart numbers every week, not every fourth.
- Task rows show `+`/`-` for subtasks left of the status box; the highlight alone marks the cursor.
- Inside a tag or project, `←/→` fold and unfold subtasks, and unfolding shows all of them.
- Moving a task to another project takes its subtasks along.
- Task titles inside a tag or project get the room they need instead of being cut short.
- A task opened inside a tag or project follows the Detail pane setting, as on the Tasks tab.
- Moving the cursor stays instant with thousands of tasks, on every tab.
- The Calendar's month panel is as tall as what it shows instead of running to the bottom.

### Fixed

- A long list no longer scrolls the selected task out of sight, with the detail open or not.
- Inside a tag on a short window, its tasks take the room before its summary does.
- Opening a project with only a few tasks no longer shows an empty list.
- Leaving a tag's task list keeps that tag in view in the list above.
- A task can no longer be added without a title, from the app or `taskr add`.
- `taskr stats --format` and `taskr top -n` refuse values they can't honour instead of guessing.

## [1.39.1] - 2026-09-28

### Changed

- Tags and Projects size the list to its rows and give the rest of the height to the tasks.
- Tags shows its tasks under the tag list at every width, like Projects, so titles fit.

### Fixed

- West of UTC, a typed date like `05-10-26` no longer saves as the day before.

## [1.39.0] - 2026-09-27

### Changed

- Tags and Projects rows show open count, last activity and the next task, not progress bars.
- Tags and Projects hide finished groups, and the done tasks inside one, until you press `h`.
- Inside a tag or project, open tasks come first, ranked, with their subtasks indented under them.
- The project timeline appears only when an open task has a date, and always reaches today.
- The pane under a tag or project shows a small purple bar of how much of it is done.
- The selected card on the Board lights up its title as well as its border.

### Added

- Projects: `s` changes the sort order and `f` shows the project's tasks on the Tasks tab.
- A done task's "Completed on" date and time can be edited in the detail pane.

## [1.38.0] - 2026-09-24

### Added

- Board: press enter to pick up a card, ←/→ to carry it, and enter or esc to put it down.
- Board cards are drawn as boxes in an even grid, without the table lines between columns.
- Board cards show their project and due date on the box's bottom edge.
- Board: long columns scroll to keep the selected card in view.
- Board: `a` adds a card to the focused column, and space shows a card's details.

### Fixed

- At 80 columns the tab bar keeps its short labels on every tab instead of collapsing to digits.
- The Tags tab header no longer clips its last column to "Tim…".
- Danish and German get short tab labels that fit an 80-column window.
- Stats and the Tags side pane show whole values and hints instead of cutting them off mid-word.
- Empty detail-pane fields show a dim dash; the how-to hint appears on the selected row.
- Board cards wrap long titles onto a second line when there is room, and overdue cards show in red.
- Projects rows show plain counts, and timeline rows lose their stray `|` marks.
- The Tags tab shows its sort order in the box title, like the Tasks tab.

## [1.37.0] - 2026-09-23

### Added

- `taskr serve --tls-cert/--tls-key` serves https itself, and picks up a renewed certificate.

### Changed

- A subtask can't outrank its parent, and a parent moved down takes its subtasks with it.
- Sync traffic is gzip-compressed, about 87% smaller; mixed server and client versions still sync.

### Fixed

- `taskr sync --adopt-remote` prints its `taskr import` undo command quoted, so it pastes on macOS.

## [1.36.0] - 2026-09-13

### Changed

- Windows: the console is switched to UTF-8, so æøå and the box borders stop arriving as mojibake.
- Git Bash and MSYS2 are asked for UTF-8 too, so no ~/.bashrc edit is needed to read the app.
- The focus chip drops the symbol terminals draw two cells wide.
- The detail pane can sit right, left, or at the bottom (Settings → Detail pane).
- The `/` search filter survives a restart, and esc still clears it.
- An undated task marks the timeline where it happened: a diamond if high priority, else a dot.
- A drilled-in project draws bars beside the task list instead of repeating every title.
- The board draws an even column grid with dividers, and headings now sit over their own cards.
- Settings drops the sequencer's personality tagline; the top-5 preview stands alone.
- Settings groups Preferences into sections led by Theme and Language, and marks editable rows.
- Settings is one pane again: the sequencer knobs are a group in it, and the tab opens on Theme.
- A never-synced device now asks before pushing its own tasks to every other device.
- Board column names now travel with sync, so a task keeps its column on every device.
- The Settings server row reads Off when this machine is not a sync hub, instead of needs token.
- Listen and Server token are hidden while the server is off; switching it on asks for the token.

### Removed

- The Tasks tab no longer carries an overdue count; two numbers on one tab read as a shortcut.

## [1.35.0] - 2026-08-23

### Added

- The list pane fills spare rows with a dim `Closed today (3)` read-out.
- The Tasks tab carries an overdue count in the tab bar (`1 Tasks ⚠3`).
- Release binaries carry Sigstore build provenance (`gh attestation verify`).
- `--json` output is a documented contract, pinned by golden files in CI.

### Changed

- Sync names which end runs an older taskr, on failure and on success.
- The task list dims its secondary columns so the title carries the row's status.
- Title badges (`!`, `↻`, `↥`, `↧`, `(1/2)`) survive clipping; only the title text is cut.
- The tag cell drops chips it cannot fit and counts them: `⟨#bug⟩ +2`.
- Titles claim width before the tag cell reserves it.
- Clipped text ends in `…` instead of `(…)`, panel rows included.
- The detail pane leads with the score, and hides `Modified` when it matches `Created`.
- Detail-pane scroll markers read `↑ 3 more` / `↓ 12 more`.
- Board columns are sized by what they hold, with a floor per stage.
- Progress bars use a thin rule for the unfilled part.
- The footer names what the key would do (no `t track` on a running timer).
- Narrow tab labels are chosen rather than chopped to three letters.
- The activity chart no longer reserves empty rows above a single block.
- A short task list no longer runs its column headers together.

### Fixed

- A failing sync shows the server's whole message instead of cutting it at 60 columns.
- `go install github.com/Iliorn/taskr@latest` works; the module path matches the repo.

## [1.34.1] - 2026-08-20

### Changed

- A lifted task shows the score it was lifted to, marked `↑`.
- 100% on the score scale means the top of the list, not the best raw score.

## [1.34.0] - 2026-08-19

### Added

- `taskr update` installs the latest release from the shell; `--check` only reports.
- Crash reports: a panic writes `crash-<timestamp>.log` and flushes pending saves.

### Changed

- A store written by a newer taskr is refused rather than silently downgraded.
- Subtasks fold with `+`/`-` instead of a second triangle beside the cursor.
- The Stage row sits in `‹ … ›` brackets like the Settings values you cycle.
- Package-managed installs are told to update through their own manager.
- `taskr completion`, `man` and `update` no longer open or migrate the database.

## [1.33.1] - 2026-08-19

### Changed

- The calendar's day summary moved into the pane border, giving the agenda two rows back.
- Overview columns cost what they show; Score and Due are right-aligned.

### Fixed

- The Stage row no longer breaks the detail panel by cutting inside an escape sequence.
- The selected row is highlighted to the panel's right edge.

## [1.33.0] - 2026-08-16

### Added

- Backlog-review flags on `list`: `--stale=30d`, `--sort=seq|due|size|age|idle|pri`, `--wide`.
- `list --unblocked-since=14d` lists tasks whose last dependency closed inside the window.
- `taskr reopen <ref>...`, the counterpart to `done`.
- `taskr edit` takes several refs; `--title` still takes one.
- Whole-word and regexp search: `--search-word` / `--word`, `--search-re` / `--re`.

### Changed

- One cursor glyph everywhere: `▶` in every list, the board and Settings.
- The header hint is a single `?`; the palette now lists the help too.
- The tab bar counts its own padding and stays inside its width.
- The pre-migration backup line names the schema step and spells out the rollback.

### Fixed

- Equal-scoring tasks sort in a stable order: one clock per sort, so ties break by ID.
- `taskr help` no longer claims only auto-sync pauses past the deletion-memory window.

## [1.32.0] - 2026-08-16

### Added

- The board scrolls sideways: `←/→` move the focus and the view follows a column at a time.
- A Stage row in the detail pane moves a task between board columns with `←/→`.
- Settings → "Kanban board" turns off the Board tab and the Stage row together.
- The search field completes `#tag` and `@project` the way quick-add does.

### Changed

- A healthy sync no longer marks the screen; only `✕ sync` appears, and the help explains it.
- The footer's boxed fields line up with the pane above them.
- The Activity chart is taller, with its caption moved into the panel border.
- The detail pane scrolls gradually instead of jumping a section per keypress.
- The Done column is the last entry of your column list, and renameable.
- The search parse preview appears on every tab whose search runs the grammar.
- Release notes come from the CHANGELOG section for the tag.

## [1.31.0] - 2026-08-15

### Added

- `taskr serve --new-token` mints a sync token; short or word-like tokens are flagged.
- The updater follows only `github.com` and `githubusercontent.com` over TLS.
- Release builds are reproducible (`-trimpath`, `CGO_ENABLED=0`, pinned Go version).
- `j`/`k` move the cursor everywhere `↑`/`↓` do.
- The score reads as a percentage of the field; 100% is the top pending task.
- `/` filters the Board with the same tokens as everywhere else.
- `w` explains a task's rank, and `taskr why <ref>` prints the same answer.
- The quick-add and search grammars accept the interface language's words too.
- `TASKR_NO_WATCH=1` turns off live reload.
- `TASKR_TRACE=1` writes a per-frame latency log to `~/.taskr/trace.log`.
- `taskr doctor` diagnoses the installation; `--json` makes it machine-readable.
- A version on the sync wire: both ends refuse a payload they might misread.
- Fuzz tests for the quick-add, search, due-date and time-entry parsers.
- Arch (`yay -S taskr-bin`) and Windows (`scoop install …`) packages.
- German UI (`"language": "de"`), with the no-wrap guard sweeping every language.
- A security policy (`SECURITY.md`) with a private reporting channel and a stated scope.
- Command palette (`ctrl+k`): find any action by name and the key that performs it.
- Quick-add completion: `#` and `@` offer existing tags and projects, most recent first.
- The Tags tab drills into a tag's tasks and takes the row-level keys.
- `D` sets a due date straight from the task list.
- Board columns are editable in Settings; renaming one carries its cards over.
- Searchable help: `/` filters the overlay, which documents the token grammars.
- `shift+tab` steps back through the tabs; digit shortcuts work in the detail pane.
- Shell completions and a man page: `taskr completion bash|zsh|fish` and `taskr man`.
- Custom keybindings: `"keys": {"done": "D"}` rebinds any single-key action.
- A linux/arm64 binary and a `SHA256SUMS` file on each release.

### Changed

- Files follow XDG conventions; an existing `~/.taskr` keeps being used unchanged.
- Search matches notes as well as titles.
- `taskr doctor` was renamed to `taskr suggest` for its old job.
- A build without an injected version reports the module version, not `dev`.
- Self-update no longer needs the GitHub CLI.
- The Danish translation is complete, and a missing one now fails the build.
- Roughly twice as fast at 2000 tasks: 5.8 ms per refresh, 3.4 ms per search keystroke.

### Removed

- Learnings. Migration 011 appends each one to its task's notes and drops the table.

### Fixed

- Windows input latency: `CONIN$` is opened for a blocking read instead of a 16 ms poll.
- The first frame is rendered before the program starts.
- The renderer paints at 120 FPS instead of 60.
- A stutter on the first keystroke: a reload carrying no new task versions is skipped.
- A stale "plain http" sync warning no longer sticks after the URL changes.
- Config files are replaced atomically.
- Crash on small terminals: every shared width helper clamps a negative budget.
- The cursor could point past the end of a list, leaving nothing selected.
- `go test` on Windows wrote to the developer's real `~/.taskr`.
- The help overlay advertised keys that did nothing, and hid three CLI flags.
- The README's fuzzy-search example (`grcry`) never matched.
- An undone deletion could delete itself again on the next sync.
- The editor was resolved from scratch on every launch.

### Security

- Self-update verifies its download against the release's `SHA256SUMS`, and fails closed.
