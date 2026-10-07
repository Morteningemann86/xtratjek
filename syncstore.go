package main

import (
	"bytes"
	"database/sql"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// syncstore.go is the one place a sync merge touches the database. Both sides
// of a sync — `tjek serve` folding a client's push into the authoritative
// store, and `tjek sync` applying the server's response locally — run
// load → Merge → save inside one SQLite transaction. As three separate steps,
// a writer in another process (a CLI `tjek add` on the same host) could
// commit between the load and the save and have its edit overwritten, or a
// just-added comment tombstoned as "vanished" and deleted on every device.

// mergeTxRetries bounds how many times a merge is retried when a concurrent
// writer invalidates its snapshot. Under WAL a deferred transaction that read
// before another process committed gets SQLITE_BUSY(_SNAPSHOT) on its first
// write; the whole load+merge+save is then re-run against a fresh snapshot.
// Merge is idempotent, so a retry can only converge, never double-apply.
const mergeTxRetries = 3

// mergeStoreTestHook, when non-nil, runs between the transactional load and
// the save. Tests use it to inject a concurrent same-host write at the exact
// moment a non-transactional merge would have clobbered it. Always nil in production.
var mergeStoreTestHook func()

// mergeIntoStore folds incoming into the store at h atomically and returns the
// merged set. changed is false when the store already contained the merged
// result — nothing was written, so callers can skip change broadcasts and the
// fs watcher stays quiet (the no-op-write guard that prevents sync feedback
// loops). The changed rows' `sequence` column is scored with b against the
// merged set's own activity heat, so a merge needs nothing from the caller's
// in-memory state and can run on any goroutine.
func mergeIntoStore(h *sql.DB, incoming []todo.Todo, b rank.Biases) (merged []todo.Todo, changed bool, err error) {
	for attempt := 0; ; attempt++ {
		merged, changed, err = mergeIntoStoreOnce(h, incoming, b)
		if err == nil || attempt >= mergeTxRetries || !isBusyErr(err) {
			return merged, changed, err
		}
		// Brief, growing pause: the competing writer is another local process
		// mid-commit, not a network peer — it clears in milliseconds.
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
}

func mergeIntoStoreOnce(h *sql.DB, incoming []todo.Todo, b rank.Biases) ([]todo.Todo, bool, error) {
	tx, err := h.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	current, err := loadTodosForSync(tx)
	if err != nil {
		return nil, false, err
	}
	if mergeStoreTestHook != nil {
		mergeStoreTestHook()
	}
	merged := tasksync.Merge(current, incoming)
	// Write only the rows the merge actually changed: rewriting every merged
	// task is O(whole store) per sync and a clobber surface. An empty change set
	// doubles as the no-op guard: nothing written, the deferred rollback just
	// releases the read snapshot.
	dirty := changedTasks(current, merged)
	if len(dirty) == 0 {
		return merged, false, nil
	}
	var live []*todo.Todo
	for i := range merged {
		if !merged[i].Deleted {
			live = append(live, &merged[i])
		}
	}
	rk := rank.Ranker{Biases: b}.Refreshed(time.Now(), live)
	if err := saveNormalizedIn(tx, dirty, nil, rk.ScoreNow()); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return merged, true, nil
}

// changedTasks returns pointers to the merged tasks whose canonical form
// differs from their pre-merge counterpart, or that are new. Canonicalization
// reuses the sync digest's ordering rules, so slice reordering introduced by the
// merge never reads as a change (no false positives to loop the watcher), and
// json.Marshal is deterministic, so a real change is never missed. Merge only
// unions IDs — a task present in current is always present in merged — so
// deletions need no separate pass (they arrive as tombstone-field changes).
func changedTasks(current, merged []todo.Todo) []*todo.Todo {
	prev := make(map[string][]byte, len(current))
	for i := range current {
		prev[current[i].ID] = tasksync.CanonicalJSON(current[i])
	}
	var dirty []*todo.Todo
	for i := range merged {
		if b, ok := prev[merged[i].ID]; ok && bytes.Equal(b, tasksync.CanonicalJSON(merged[i])) {
			continue
		}
		dirty = append(dirty, &merged[i])
	}
	return dirty
}

// isBusyErr reports whether err is SQLite telling us another writer got in the
// way (SQLITE_BUSY, or SQLITE_BUSY_SNAPSHOT when our read snapshot went stale
// before we wrote). modernc surfaces these as strings; there is no exported
// error type to test against.
func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}
