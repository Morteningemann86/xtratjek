package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
)

// syncadopt.go is the first-sync gate.
//
// The merge is a union by task ID, so the first sync from a device that has
// tasks of its own hands all of them to every other device — permanently, and
// with no undo (sync.log records local edits that LOST a conflict, never the
// tasks a device introduced). That is right when the device holds the real
// list and the fleet is new, and wrong when it holds leftovers from before the
// fleet existed. Nothing in the data tells the two apart; only the user can.
//
// So the device asks once, and the question is asked where it can actually be
// answered. Three of the four paths that upload are unattended — the TUI syncs
// on launch the moment sync.json has a url and a token, again on its timer, and
// the CLI syncs after every mutating command — so a prompt in `tjek sync`
// would have guarded the one path a person is already watching. The unattended
// paths therefore refuse and explain, exactly as the stale-device guard does,
// and the choice is made once in a shell.

// firstSyncSampleTitles is how many task titles the refusal prints. Enough to
// recognise the set ("these are my real tasks" vs "this is June's leftovers"),
// short enough to stay a paragraph.
const firstSyncSampleTitles = 5

// firstSyncNeedsChoice reports whether this device still owes the one-time
// decision: it has never completed a sync, it holds live tasks of its own, and
// no answer has been recorded yet.
//
// Devices already in a fleet are unaffected — they have a sync state file, so
// the second check answers false and the question is never asked of them.
func firstSyncNeedsChoice(cfg syncConfig, h *sql.DB) bool {
	if cfg.Adopted {
		return false
	}
	if _, synced, err := readSyncState(); synced && err == nil {
		return false
	}
	n, err := countLiveTasks(h)
	return err == nil && n > 0
}

// recordFirstSyncChoice notes that the question has been answered, in
// sync.json beside the url and token rather than in the state directory: the
// state directory is a cache-shaped place that a reinstall or a cleaner can
// take with it, and being asked to choose a second time is exactly the moment
// a device would upload the leftovers it was meant to drop. Written from the
// file, never the env overlay, for the same reason `--save` is.
func recordFirstSyncChoice() error {
	c := loadSyncConfigFile()
	if c.Adopted {
		return nil
	}
	c.Adopted = true
	return saveSyncConfig(c)
}

func countLiveTasks(h *sql.DB) (int, error) {
	var n int
	err := h.QueryRow(`SELECT COUNT(*) FROM todos WHERE deleted = 0`).Scan(&n)
	return n, err
}

// firstSyncSummary is what the device can say about its own tasks without
// asking the server anything. A true "what does the fleet not have yet" would
// need a dry run in the protocol, a version bump and a server new enough to
// answer it — for a question each device asks once. The count, the span and a
// handful of titles are enough to tell the two answers apart.
type firstSyncSummary struct {
	live           int
	oldest, newest time.Time
	titles         []string
}

func readFirstSyncSummary(h *sql.DB) (firstSyncSummary, error) {
	rows, err := h.Query(`SELECT title, created_at FROM todos WHERE deleted = 0 ORDER BY created_at`)
	if err != nil {
		return firstSyncSummary{}, err
	}
	defer rows.Close()
	var s firstSyncSummary
	for rows.Next() {
		var title, created string
		if err := rows.Scan(&title, &created); err != nil {
			return firstSyncSummary{}, err
		}
		s.live++
		if len(s.titles) < firstSyncSampleTitles {
			s.titles = append(s.titles, title)
		}
		if at := parseTime(created); !at.IsZero() {
			if s.oldest.IsZero() || at.Before(s.oldest) {
				s.oldest = at
			}
			if at.After(s.newest) {
				s.newest = at
			}
		}
	}
	return s, rows.Err()
}

// firstSyncNotice is the one-line version, shared by the paths that can only
// refuse. Deliberately English and unlocalized like the rest of the sync
// layer's errors; the TUI wraps it in a translated frame.
func firstSyncNotice(n int) string {
	return fmt.Sprintf("this device has never synced and holds %d task(s) of its own; syncing blind would add every one of them to every device", n)
}

// printFirstSyncChoice writes the refusal a person reads in a shell: what is
// about to happen, enough of the set to recognise it, and the two commands
// that answer the question.
func printFirstSyncChoice(s firstSyncSummary) {
	fmt.Fprintf(os.Stderr, "tjek sync: %s.\n", firstSyncNotice(s.live))
	switch {
	case s.oldest.IsZero():
	case s.oldest.Format("2006-01-02") == s.newest.Format("2006-01-02"):
		fmt.Fprintf(os.Stderr, "All created %s:\n", s.oldest.Format("2006-01-02"))
	default:
		fmt.Fprintf(os.Stderr, "Created between %s and %s:\n", s.oldest.Format("2006-01-02"), s.newest.Format("2006-01-02"))
	}
	for _, title := range s.titles {
		fmt.Fprintf(os.Stderr, "  · %s\n", truncate(title, 60))
	}
	if rest := s.live - len(s.titles); rest > 0 {
		fmt.Fprintf(os.Stderr, "  … %d more\n", rest)
	}
	fmt.Fprint(os.Stderr, `Choose once:
  tjek sync --adopt-local     these are the real list: push them to the fleet
  tjek sync --adopt-remote    these are leftovers: back them up, clear them here, pull the fleet's list
`)
}

// resolveFirstSync answers the gate for a manual `tjek sync`, which is the
// one path a person is watching. It returns a process exit code; 0 means the
// sync may proceed. Refusing is the default: an unanswered first sync exits 2
// having touched neither the network nor the store.
func resolveFirstSync(cfg syncConfig, adoptLocalFlag, adoptRemoteFlag bool) int {
	if adoptLocalFlag && adoptRemoteFlag {
		fmt.Fprintln(os.Stderr, "tjek sync: --adopt-local and --adopt-remote are opposite answers; pass one")
		return 2
	}
	if !firstSyncNeedsChoice(cfg, db) {
		// The flags answer a question this device is not being asked. Saying so
		// is the whole of it — in particular, a stray --adopt-remote must never
		// clear a store that is already part of a fleet.
		if adoptLocalFlag || adoptRemoteFlag {
			fmt.Fprintln(os.Stderr, "tjek sync: nothing to adopt (this device has synced before, or has no tasks of its own); syncing normally")
		}
		return 0
	}
	switch {
	case adoptRemoteFlag:
		n, _ := countLiveTasks(db)
		backup, err := adoptRemote(db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek sync: adopt-remote: %v\n", err)
			return 1
		}
		// On stderr and outside --quiet: this is where the user's tasks went.
		// The import hint is the recovery for the one bad moment this ordering
		// allows — the store is already cleared when the pull runs, so a sync
		// that fails here leaves an empty list and a file to put back. The
		// command is quoted because it is meant to be pasted: on macOS the
		// state directory is under "Application Support", and an unquoted
		// path split there hands `tjek import` a file that does not exist.
		fmt.Fprintf(os.Stderr, "tjek sync: backed up %d task(s) to %s and cleared them here; pulling the fleet's list (tjek import %s puts them back)\n", n, backup, shellArg(backup, runtime.GOOS))
	case adoptLocalFlag:
		// Nothing to do: pushing the local set is the adoption.
	default:
		s, err := readFirstSyncSummary(db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek sync: read local tasks: %v\n", err)
			return 1
		}
		printFirstSyncChoice(s)
		return 2
	}
	if err := recordFirstSyncChoice(); err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync: warning: could not record the first-sync choice: %v\n", err)
	}
	return 0
}

// shellArg quotes s for pasting into the shell a user on goos is likely to be
// running, and leaves it bare when nothing in it needs quoting — a path in a
// sentence reads better without quotes it does not need. POSIX shells get
// single quotes, which nothing inside expands; Windows gets double quotes,
// the one form cmd and PowerShell both take for a path with spaces.
func shellArg(s, goos string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$`\\!*?[]{}()<>|&;#~%^") {
		return s
	}
	if goos == "windows" {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// adoptRemote is the answer that had no implementation: take the fleet's list
// and set this device's own aside. The order is export, then clear, then sync —
// the backup is written before anything is removed, and it is a normal export
// file, so `tjek import` puts the tasks back if the answer turns out to be
// wrong. Clearing before the sync is not an accident of ordering: the push is
// the full local set, so tasks still in the store would go out with it.
func adoptRemote(h *sql.DB) (backup string, err error) {
	backup, err = writePreSyncBackup(h)
	if err != nil {
		return "", err
	}
	if err := dropAllLocalTasks(h); err != nil {
		return backup, err
	}
	return backup, nil
}

// writePreSyncBackup writes every local row — done and deleted included — as a
// normal export envelope. A backup is the one place the tombstones belong:
// restoring it has to restore the deletions too, or an import would resurrect
// what this device had already thrown away.
func writePreSyncBackup(h *sql.DB) (string, error) {
	tasks, err := loadTodosForSync(h)
	if err != nil {
		return "", err
	}
	if _, err := paths.Ensure(paths.State); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(exportEnvelope{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Tasks:      tasks,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	path := paths.For(paths.State, fmt.Sprintf("pre-sync-backup-%s.json", time.Now().Format("20060102-150405")))
	if err := writeFileAtomic(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}

// dropAllLocalTasks removes every task row outright — no tombstones. A
// tombstone is how a deletion travels, and these IDs have never left this
// machine: marking them deleted would push a row of nothing to every device in
// the fleet instead of pushing nothing at all.
func dropAllLocalTasks(h *sql.DB) error {
	tx, err := h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"task_tags", "task_dependencies", "task_comments", "task_time_entries", "todos"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}
