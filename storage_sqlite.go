package main

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Iliorn/tjek/paths"
	"github.com/Iliorn/tjek/rank"
	"github.com/Iliorn/tjek/todo"

	_ "modernc.org/sqlite"
)

// SQLite is the live storage backend. After migration 002, every field is
// queryable from SQL — scalars live in dedicated columns on `todos`, and
// nested data (tags, dependencies, comments, time entries) lives in
// child tables joined by task_id. The legacy `data` column still exists but is
// never read. Deletes are soft (tombstones) so a deletion syncs instead of the
// row reappearing.

func dbPath() string {
	return paths.For(paths.Data, "tasks.db")
}

var (
	db     *sql.DB
	dbOnce sync.Once
	dbErr  error
)

// openStore opens (once) the SQLite database, creates the schema, and — only if
// the todos table did not previously exist — imports any legacy JSON file.
// Gating import on a freshly-created table avoids "zombie" tasks: deleting every
// task in the app must not re-import the old JSON on the next launch.
func openStore() error {
	dbOnce.Do(func() {
		if err := ensureStorageDir(); err != nil {
			dbErr = err
			return
		}
		db, dbErr = openStoreAt(dbPath())
	})
	return dbErr
}

// checkpointStore folds the WAL back into tasks.db and truncates it, so the
// sidecar doesn't sit at megabytes against a hundreds-of-KB database between
// runs (checkpoints otherwise only happen at SQLite's 1000-page threshold,
// which this database rarely reaches). Best-effort by design: with another
// process holding the store (the sync server, a concurrent TUI) SQLite
// reports busy and the WAL simply persists — the next clean exit gets it.
func checkpointStore() { checkpointDB(db) }

func checkpointDB(h *sql.DB) {
	if h == nil {
		return
	}
	_, _ = h.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

// openStoreAt opens the database at path, applies the schema, and imports the
// legacy JSON only when the todos table did not already exist. Split out from
// openStore (which owns the package-level singleton) so tests can drive it
// against a throwaway path.
func openStoreAt(path string) (*sql.DB, error) {
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Single connection: a personal TUI has one writer, and serializing all
	// access sidesteps connection-pool pragma quirks (busy_timeout is
	// per-connection). WAL persists on the file once set.
	handle.SetMaxOpenConns(1)
	// synchronous=NORMAL is the major fsync-lag fix: with WAL it remains
	// crash-safe (consistent on power loss, only the last committed txn at risk)
	// while skipping the per-commit fsync of FULL. mmap_size enables zero-copy
	// reads for typical working sets. foreign_keys is enabled now so future
	// migrations can rely on ON DELETE CASCADE.
	if _, err := handle.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=NORMAL;
		PRAGMA busy_timeout=5000;
		PRAGMA foreign_keys=ON;
		PRAGMA temp_store=MEMORY;
		PRAGMA mmap_size=67108864;
		PRAGMA wal_autocheckpoint=1000;
	`); err != nil {
		handle.Close()
		return nil, err
	}

	fresh := !tableExists(handle, "todos")
	if err := runMigrations(handle); err != nil {
		handle.Close()
		return nil, err
	}
	if fresh {
		if err := importFromJSON(handle); err != nil {
			handle.Close()
			return nil, err
		}
	}
	// Fail-soft: a GC hiccup must never keep the store from opening — the
	// tombstones just live a little longer.
	if err := pruneOldTombstones(handle, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "tjek: tombstone cleanup failed (harmless, will retry next open): %v\n", err)
	}
	return handle, nil
}

// tombstoneRetention is how long task and child tombstones are kept before
// being hard-deleted by pruneOldTombstones. Tombstones exist so deletions
// propagate during sync instead of a stale device resurrecting the row; once
// every device has synced past one it is dead weight that still rides along in
// full on every sync round trip, forever. Six months bounds that growth. The
// tradeoff is explicit: a device that stays offline LONGER than this window
// and still holds the task live will resurrect it on its next sync (the merge
// sees an unopposed live copy). Acceptable for a personal task set; widen the
// window rather than disabling GC if that ever bites.
const tombstoneRetention = 180 * 24 * time.Hour

// pruneOldTombstones hard-deletes task tombstones (row + all child rows) and
// child tombstones older than tombstoneRetention. Timestamps are compared in
// Go — deleted_at mixes RFC3339 and RFC3339Nano strings, whose lexicographic
// order lies within a shared second. A tombstone with no parseable timestamp
// is left alone, mirroring the stale-timer recovery's caution.
func pruneOldTombstones(h *sql.DB, now time.Time) error {
	cutoff := now.Add(-tombstoneRetention)
	oldIDs := func(query string) ([]string, error) {
		rows, err := h.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id, deletedAt string
			if err := rows.Scan(&id, &deletedAt); err != nil {
				return nil, err
			}
			if at := parseTime(deletedAt); !at.IsZero() && at.Before(cutoff) {
				ids = append(ids, id)
			}
		}
		return ids, rows.Err()
	}

	taskIDs, err := oldIDs(`SELECT id, deleted_at FROM todos WHERE deleted = 1`)
	if err != nil {
		return err
	}
	childTables := []string{"task_tags", "task_dependencies", "task_comments", "task_time_entries"}
	oldChildren := make(map[string][]string, len(childTables))
	for _, table := range childTables[2:] { // only the identified children carry deleted_at
		ids, err := oldIDs(`SELECT id, deleted_at FROM ` + table + ` WHERE deleted_at != ''`)
		if err != nil {
			return err
		}
		oldChildren[table] = ids
	}
	if len(taskIDs) == 0 && len(oldChildren["task_comments"])+len(oldChildren["task_time_entries"]) == 0 {
		return nil
	}

	tx, err := h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range taskIDs {
		for _, table := range childTables {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE task_id = ?`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM todos WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for table, ids := range oldChildren {
		for _, id := range ids {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE id = ?`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func tableExists(h *sql.DB, name string) bool {
	var found string
	err := h.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&found)
	return err == nil
}

// importFromJSON seeds a fresh database from the legacy tasks.json. A missing or
// corrupt JSON file is not fatal — a new user simply starts empty.
func importFromJSON(h *sql.DB) error {
	todos, err := loadTodosJSON()
	if err != nil || len(todos) == 0 {
		return nil
	}
	ptrs := make([]*todo.Todo, len(todos))
	for i := range todos {
		ptrs[i] = &todos[i]
	}
	return saveNormalized(h, ptrs, nil, rank.Default().ScoreNow())
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	// RFC3339Nano (not RFC3339) so modified_at keeps sub-second precision: the
	// sync merge orders concurrent edits by ModifiedAt, and second precision
	// would make same-second edits on two devices indistinguishable. parseTime
	// reads both old (second) and new (nano) values.
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Loud like the enum clamps below: a non-empty value that doesn't parse
		// means corruption (manual SQL edit, bad migration), and silently
		// treating it as unset would make e.g. a due date quietly vanish.
		validationWarn("tjek: invalid timestamp %q, treated as unset\n", s)
		return time.Time{}
	}
	// Stored timestamps are UTC (fmtTime); rehydrate in local time so
	// date-only fields (due/start) render and compare on the calendar day
	// the user set — a midnight-CEST due date formatted in its UTC form
	// shows the previous day.
	return t.In(time.Local)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// querier is the read surface shared by *sql.DB and *sql.Tx, so the loaders
// can run standalone or inside a caller-owned transaction (mergeIntoStore
// needs load+merge+save atomic against writers in other processes).
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// saveNormalized writes the dirty tasks and tombstones to the normalized
// schema. One transaction; per task the scalars go into `todos` and the
// children replace the previous child rows. Untouched tasks are not rewritten.
func saveNormalized(h *sql.DB, dirty []*todo.Todo, tombstones map[string]time.Time, score func(*todo.Todo) float64) error {
	if len(dirty) == 0 && len(tombstones) == 0 {
		return nil
	}
	tx, err := h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveNormalizedIn(tx, dirty, tombstones, score); err != nil {
		return err
	}
	return tx.Commit()
}

// saveNormalizedIn is saveNormalized's body on a caller-owned transaction —
// the caller commits (or rolls back). Split out so mergeIntoStore can bundle
// the load, the merge and this write into one atomic unit.
func saveNormalizedIn(tx *sql.Tx, dirty []*todo.Todo, tombstones map[string]time.Time, score func(*todo.Todo) float64) error {
	// Foreign-key checks are immediate by default, but a batch can hold a task
	// and the task it depends on in either order (drainDirty iterates a map; the
	// merge follows slice order), and each task's todos row is written just
	// before its own dependency rows. Defer FK enforcement to COMMIT — when
	// every row in the batch exists — so a dependent written ahead of its
	// dependency does not fail with SQLITE_CONSTRAINT_FOREIGNKEY (787). A
	// genuinely dangling reference still fails at COMMIT, correctly. The pragma
	// is transaction-scoped and resets when this tx ends.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	if len(dirty) > 0 {
		// `data` is the legacy blob column, still NOT NULL but never read.
		// Write an empty string.
		upsertTask, err := tx.Prepare(`INSERT INTO todos
			(id,title,status,priority,size,project,parent_id,created_at,modified_at,due_date,start_date,notes,completed_at,sequence,recurrence,seq_rank_done,stage,meeting_id,data,deleted,deleted_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'',?,?)
			ON CONFLICT(id) DO UPDATE SET
				title=excluded.title, status=excluded.status, priority=excluded.priority,
				size=excluded.size, project=excluded.project, parent_id=excluded.parent_id,
				created_at=excluded.created_at, modified_at=excluded.modified_at,
				due_date=excluded.due_date, start_date=excluded.start_date,
				notes=excluded.notes, completed_at=excluded.completed_at,
				sequence=excluded.sequence, recurrence=excluded.recurrence,
				seq_rank_done=excluded.seq_rank_done, stage=excluded.stage,
				meeting_id=excluded.meeting_id,
				deleted=excluded.deleted, deleted_at=excluded.deleted_at`)
		if err != nil {
			return err
		}
		defer upsertTask.Close()

		deleteChildren := func(table string) (*sql.Stmt, error) {
			return tx.Prepare(`DELETE FROM ` + table + ` WHERE task_id=?`)
		}
		delTags, err := deleteChildren("task_tags")
		if err != nil {
			return err
		}
		defer delTags.Close()
		delDeps, err := deleteChildren("task_dependencies")
		if err != nil {
			return err
		}
		defer delDeps.Close()
		insTag, err := tx.Prepare(`INSERT INTO task_tags (task_id, tag) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer insTag.Close()
		insDep, err := tx.Prepare(`INSERT INTO task_dependencies (task_id, depends_on_id) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer insDep.Close()
		// Comments and time entries carry their own IDs plus a
		// deleted_at tombstone column, so they are upserted (not replace-all)
		// and any child that vanished from the task is tombstoned rather than
		// hard-deleted — see saveChildren. That is what lets a child deletion
		// propagate during sync instead of resurfacing from another device.
		upComment, err := tx.Prepare(`INSERT INTO task_comments (id, task_id, text, created_at, modified_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET text=excluded.text, created_at=excluded.created_at, modified_at=excluded.modified_at, deleted_at=excluded.deleted_at`)
		if err != nil {
			return err
		}
		defer upComment.Close()
		upEntry, err := tx.Prepare(`INSERT INTO task_time_entries (id, task_id, started_at, stopped_at, last_seen, modified_at, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET started_at=excluded.started_at, stopped_at=excluded.stopped_at, last_seen=excluded.last_seen, modified_at=excluded.modified_at, deleted_at=excluded.deleted_at`)
		if err != nil {
			return err
		}
		defer upEntry.Close()

		// Guard dependency inserts against a dangling target — a depends_on_id
		// pointing at a task present in neither this batch nor the DB. Deferred
		// FK checks would otherwise let the row insert but fail the whole batch
		// at COMMIT. This can't arise from normal use (the picker and CLI only
		// ever link existing tasks), but a cross-device delete past the
		// tombstone-GC window could leave a live dependent pointing at a
		// hard-deleted target. Drop the broken link instead of failing every
		// save. The known set is the pre-existing todos rows plus every ID in
		// this batch (batch rows may not be written yet when their dependent is
		// processed first). Built lazily: only scan when some dirty task actually
		// carries dependencies — the common single-edit save touches none.
		var knownTaskIDs map[string]bool
		for i := range dirty {
			if len(dirty[i].Dependencies) == 0 {
				continue
			}
			knownTaskIDs = make(map[string]bool)
			rows, err := tx.Query(`SELECT id FROM todos`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				knownTaskIDs[id] = true
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			for j := range dirty {
				knownTaskIDs[dirty[j].ID] = true
			}
			break
		}

		for _, t := range dirty {
			if _, err := upsertTask.Exec(t.ID, t.Title, int(t.Status), int(t.Priority), int(t.Size),
				t.Project, t.ParentID, fmtTime(t.CreatedAt), fmtTime(t.ModifiedAt),
				fmtTime(t.DueDate), fmtTime(t.StartDate), t.Notes, fmtTime(t.CompletedAt),
				score(t), t.Recurrence, t.SeqRankAtDone, t.Stage, t.MeetingID, boolToInt(t.Deleted), fmtTime(t.DeletedAt)); err != nil {
				return err
			}
			// Tags and dependencies are value-sets (no per-row identity), so
			// they are replaced wholesale — matching their whole-set
			// last-writer-wins resolution during sync.
			for _, del := range []*sql.Stmt{delTags, delDeps} {
				if _, err := del.Exec(t.ID); err != nil {
					return err
				}
			}
			for _, tag := range t.Tags {
				if _, err := insTag.Exec(t.ID, tag); err != nil {
					return err
				}
			}
			for _, dep := range t.Dependencies {
				// Skip a dangling link (target task gone from batch and DB)
				// rather than fail the whole batch at COMMIT — see knownTaskIDs.
				if knownTaskIDs != nil && !knownTaskIDs[dep] {
					continue
				}
				if _, err := insDep.Exec(t.ID, dep); err != nil {
					return err
				}
			}
			// Identified children: upsert the present set and tombstone any that
			// vanished, so deletions become propagating tombstones.
			if err := saveChildren(tx, t.ID, "task_comments", t.Comments,
				func(c todo.Comment) string { return c.ID },
				upComment, func(c todo.Comment) []any {
					return []any{c.ID, t.ID, c.Text, fmtTime(c.CreatedAt), fmtTime(c.ModifiedAt), fmtTime(c.DeletedAt)}
				}); err != nil {
				return err
			}
			if err := saveChildren(tx, t.ID, "task_time_entries", t.TimeEntries,
				func(e todo.TimeEntry) string { return e.ID },
				upEntry, func(e todo.TimeEntry) []any {
					return []any{e.ID, t.ID, fmtTime(e.StartedAt), fmtTime(e.StoppedAt), fmtTime(e.LastSeen), fmtTime(e.ModifiedAt), fmtTime(e.DeletedAt)}
				}); err != nil {
				return err
			}
		}
	}

	if len(tombstones) > 0 {
		tomb, err := tx.Prepare(`UPDATE todos SET deleted=1, deleted_at=? WHERE id=?`)
		if err != nil {
			return err
		}
		defer tomb.Close()
		// deleted_at is a merge-ordering stamp: delete-vs-edit resolves by
		// comparing it against the live version's ModifiedAt, so it must be
		// clamped against the row's own modified_at (StampAt) — a slow local
		// clock would otherwise stamp a deleted_at older than the version it
		// deleted, the deletion loses the merge, and the task resurrects on
		// the next sync. The caller's time is the moment of the deletion, not
		// of this flush; a zero time means the caller didn't record one, and
		// now is the best available answer. Timestamps are TEXT with mixed
		// second/nano precision, so the comparison happens in Go via
		// parseTime, never in SQL.
		for id, at := range tombstones {
			var modifiedAt string
			if err := tx.QueryRow(`SELECT modified_at FROM todos WHERE id=?`, id).Scan(&modifiedAt); err != nil && err != sql.ErrNoRows {
				return err
			}
			if at.IsZero() {
				at = time.Now()
			}
			if _, err := tomb.Exec(fmtTime(todo.StampAt(at, parseTime(modifiedAt))), id); err != nil {
				return err
			}
		}
	}

	return nil
}

// saveChildren upserts a task's identified child records (comments
// or time entries) and tombstones any live DB child of that task that is no
// longer present in the slice. Because a removal becomes a deleted_at tombstone
// rather than a hard delete, a child deleted on one device propagates during
// sync instead of being resurrected from another device's copy. A slice item
// that is itself a tombstone (deleted_at set, e.g. a merged result) is upserted
// with its tombstone intact; pre-existing tombstones in the DB are left alone.
//
// Each tombstone's deleted_at is clamped against that child's own modified_at
// (StampModified): the sync merge treats a child tombstone's DeletedAt as its
// event time, so a slow local clock stamping a deleted_at older than the
// version it deleted would lose the merge and the child would resurrect.
func saveChildren[T any](tx *sql.Tx, taskID, table string, items []T,
	id func(T) string, upsert *sql.Stmt, args func(T) []any,
) error {
	present := make(map[string]bool, len(items))
	for _, it := range items {
		if _, err := upsert.Exec(args(it)...); err != nil {
			return err
		}
		present[id(it)] = true
	}
	rows, err := tx.Query(`SELECT id, modified_at FROM `+table+` WHERE task_id = ? AND deleted_at = ''`, taskID)
	if err != nil {
		return err
	}
	type child struct{ id, modifiedAt string }
	var vanished []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.modifiedAt); err != nil {
			rows.Close()
			return err
		}
		if !present[c.id] {
			vanished = append(vanished, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range vanished {
		stamp := fmtTime(todo.StampModified(parseTime(c.modifiedAt)))
		if _, err := tx.Exec(`UPDATE `+table+` SET deleted_at = ? WHERE id = ?`, stamp, c.id); err != nil {
			return err
		}
	}
	return nil
}

// loadTodosFromDB reads every live task plus its live children. loadTodosForSync
// is the sibling that also returns tombstones (deleted tasks, deleted children)
// with their timestamps, which the sync merge needs to propagate deletions.
func loadTodosFromDB(h querier) ([]todo.Todo, error) {
	return loadTodosCore(h, false)
}

// loadTodosForSync returns the full task set including tombstones — Deleted /
// DeletedAt populated on tasks, DeletedAt on child records — so Merge can
// resolve deletions. Order is unspecified; Merge sorts the result.
func loadTodosForSync(h querier) ([]todo.Todo, error) {
	return loadTodosCore(h, true)
}

// tombstoneDeletedAt returns the stored deleted_at for a task row, or the zero
// time when the row is absent or live. A restore (cliUndo) clamps its
// ModifiedAt stamp against this: the tombstone's DeletedAt is the event time
// the restore must strictly beat in the sync merge, and the pre-delete
// snapshot alone can't supply it.
func tombstoneDeletedAt(h *sql.DB, id string) time.Time {
	// No store open (tests over fakeRepo run undo without a DB): same outcome
	// as a not-yet-flushed tombstone — zero time, caller falls back to the
	// snapshot stamp.
	if h == nil {
		return time.Time{}
	}
	var s string
	if err := h.QueryRow(`SELECT deleted_at FROM todos WHERE id = ?`, id).Scan(&s); err != nil {
		return time.Time{}
	}
	return parseTime(s)
}

// loadTodosCore assembles tasks and their children. When includeDeleted is
// false (the live TUI/CLI path) tombstoned tasks and children are filtered out;
// when true (the sync path) they are returned with their tombstone timestamps.
func loadTodosCore(h querier, includeDeleted bool) ([]todo.Todo, error) {
	taskWhere, childWhere := "WHERE deleted = 0", "WHERE deleted_at = ''"
	if includeDeleted {
		taskWhere, childWhere = "", ""
	}
	rows, err := h.Query(`SELECT id, title, status, priority, size, project, parent_id,
		created_at, modified_at, due_date, start_date, completed_at, notes, recurrence,
		seq_rank_done, stage, meeting_id, deleted, deleted_at
		FROM todos ` + taskWhere)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	todos := make(map[string]*todo.Todo, 64)
	var ordered []string
	for rows.Next() {
		var t todo.Todo
		var status, priority, size, deleted int
		var createdAt, modifiedAt, dueDate, startDate, completedAt, deletedAt string
		if err := rows.Scan(&t.ID, &t.Title, &status, &priority, &size, &t.Project,
			&t.ParentID, &createdAt, &modifiedAt, &dueDate, &startDate,
			&completedAt, &t.Notes, &t.Recurrence, &t.SeqRankAtDone, &t.Stage, &t.MeetingID, &deleted, &deletedAt); err != nil {
			return nil, err
		}
		t.Status = safeStatus(status, t.ID)
		t.Priority = safePriority(priority, t.ID)
		t.Size = safeSize(size, t.ID)
		t.CreatedAt = parseTime(createdAt)
		t.ModifiedAt = parseTime(modifiedAt)
		t.DueDate = parseTime(dueDate)
		t.StartDate = parseTime(startDate)
		t.CompletedAt = parseTime(completedAt)
		t.Deleted = deleted != 0
		t.DeletedAt = parseTime(deletedAt)
		todos[t.ID] = &t
		ordered = append(ordered, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if err := loadChildren(h, todos, "task_tags", "task_id, tag", "",
		func(s *sql.Rows) error {
			var taskID, tag string
			if err := s.Scan(&taskID, &tag); err != nil {
				return err
			}
			if t := todos[taskID]; t != nil {
				t.Tags = append(t.Tags, tag)
			}
			return nil
		}); err != nil {
		return nil, err
	}
	if err := loadChildren(h, todos, "task_dependencies", "task_id, depends_on_id", "",
		func(s *sql.Rows) error {
			var taskID, dep string
			if err := s.Scan(&taskID, &dep); err != nil {
				return err
			}
			if t := todos[taskID]; t != nil {
				t.Dependencies = append(t.Dependencies, dep)
			}
			return nil
		}); err != nil {
		return nil, err
	}
	if err := loadChildren(h, todos, "task_comments", "id, task_id, text, created_at, modified_at, deleted_at", childWhere,
		func(s *sql.Rows) error {
			var c todo.Comment
			var taskID, createdAt, modifiedAt, deletedAt string
			if err := s.Scan(&c.ID, &taskID, &c.Text, &createdAt, &modifiedAt, &deletedAt); err != nil {
				return err
			}
			c.CreatedAt = parseTime(createdAt)
			c.ModifiedAt = parseTime(modifiedAt)
			c.DeletedAt = parseTime(deletedAt)
			if t := todos[taskID]; t != nil {
				t.Comments = append(t.Comments, c)
			}
			return nil
		}); err != nil {
		return nil, err
	}
	if err := loadChildren(h, todos, "task_time_entries", "id, task_id, started_at, stopped_at, last_seen, modified_at, deleted_at", childWhere,
		func(s *sql.Rows) error {
			var e todo.TimeEntry
			var taskID, startedAt, stoppedAt, lastSeen, modifiedAt, deletedAt string
			if err := s.Scan(&e.ID, &taskID, &startedAt, &stoppedAt, &lastSeen, &modifiedAt, &deletedAt); err != nil {
				return err
			}
			e.StartedAt = parseTime(startedAt)
			e.StoppedAt = parseTime(stoppedAt)
			e.LastSeen = parseTime(lastSeen)
			e.ModifiedAt = parseTime(modifiedAt)
			e.DeletedAt = parseTime(deletedAt)
			if t := todos[taskID]; t != nil {
				t.TimeEntries = append(t.TimeEntries, e)
			}
			return nil
		}); err != nil {
		return nil, err
	}

	out := make([]todo.Todo, 0, len(ordered))
	for _, id := range ordered {
		out = append(out, *todos[id])
	}
	return out, nil
}

func loadChildren(h querier, todos map[string]*todo.Todo, table, cols, where string,
	scan func(*sql.Rows) error,
) error {
	rows, err := h.Query(`SELECT ` + cols + ` FROM ` + table + ` ` + where)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// loadTodos is the storage entry point used by initialModel: open the store
// (importing legacy JSON on first run) and return the live tasks, sorted.
func loadTodos(score func(*todo.Todo) float64) ([]todo.Todo, error) {
	if err := openStore(); err != nil {
		return nil, err
	}
	todos, err := loadTodosFromDB(db)
	if err != nil {
		return nil, err
	}
	sortTodosByMode(todos, taskSortSequence, score)
	return todos, nil
}

// ── Load-time enum validation ────────────────────────────────────────────────
//
// Enum-shaped columns (status / priority / size) are stored as raw ints and
// can in principle hold anything (corrupt migration, manual SQL edit, future
// sync conflict). The safe* helpers clamp out-of-range values to a safe
// neutral default and warn to stderr with the offending task ID — quiet
// wrong becomes loud wrong. The warning sink is a package var so tests can
// suppress noise.

var validationWarn = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func safeStatus(raw int, taskID string) todo.Status {
	if raw == int(todo.Pending) || raw == int(todo.Done) {
		return todo.Status(raw)
	}
	// Pending is the safer default than Done: a corrupted task should land
	// back in the active list rather than be silently archived.
	validationWarn("tjek: invalid status %d on task %s, clamped to Pending\n", raw, taskID)
	return todo.Pending
}

func safePriority(raw int, taskID string) todo.Priority {
	if raw >= int(todo.PriorityLow) && raw <= int(todo.PriorityHigh) {
		return todo.Priority(raw)
	}
	validationWarn("tjek: invalid priority %d on task %s, clamped to Medium\n", raw, taskID)
	return todo.PriorityMedium
}

func safeSize(raw int, taskID string) todo.Size {
	// The Size enum's three values are 0..2 (Medium=0, Small=1, Large=2) —
	// the numeric ordering is not semantic but the range check still holds.
	if raw >= 0 && raw <= int(todo.SizeLarge) {
		return todo.Size(raw)
	}
	validationWarn("tjek: invalid size %d on task %s, clamped to Medium\n", raw, taskID)
	return todo.SizeMedium
}

// sqliteRepo is the SQLite Repository adapter. It reuses the package-level
// connection opened lazily by openStore.
//
// It keeps its own copy of the ranker for the `sequence` column, behind a
// mutex: SetRanker is called from the Update loop while Save runs on the
// background save command.
type sqliteRepo struct {
	mu   sync.Mutex
	rank rank.Ranker
}

func newSQLiteRepo() *sqliteRepo { return &sqliteRepo{rank: rank.Default()} }

func (r *sqliteRepo) SetRanker(rk rank.Ranker) {
	r.mu.Lock()
	r.rank = rk
	r.mu.Unlock()
}

func (r *sqliteRepo) ranker() rank.Ranker {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rank
}

func (r *sqliteRepo) Load() ([]todo.Todo, error) {
	return loadTodos(r.ranker().ScoreNow())
}

// ResyncScores rewrites the persisted `sequence` column for every live row
// with the repository's rank.Ranker. See Repository.ResyncScores for the why.
func (r *sqliteRepo) ResyncScores() error {
	if err := openStore(); err != nil {
		return err
	}
	return resyncSequenceColumn(db, r.ranker().ScoreNow())
}

// resyncSequenceColumn is the worker: load the score-relevant fields of
// every live row, compute its score in Go with the given score function,
// and write back only the score column in one transaction. Touches no
// child tables and no other scalars — cheap even on large task sets.
func resyncSequenceColumn(h *sql.DB, score func(*todo.Todo) float64) error {
	rows, err := h.Query(`SELECT id, status, priority, size, due_date, created_at
		FROM todos WHERE deleted = 0`)
	if err != nil {
		return err
	}
	type scored struct {
		id    string
		score float64
	}
	var updates []scored
	for rows.Next() {
		var t todo.Todo
		var status, priority, size int
		var due, created string
		if err := rows.Scan(&t.ID, &status, &priority, &size, &due, &created); err != nil {
			rows.Close()
			return err
		}
		t.Status = safeStatus(status, t.ID)
		t.Priority = safePriority(priority, t.ID)
		t.Size = safeSize(size, t.ID)
		t.DueDate = parseTime(due)
		t.CreatedAt = parseTime(created)
		updates = append(updates, scored{t.ID, score(&t)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	tx, err := h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE todos SET sequence = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range updates {
		if _, err := stmt.Exec(u.score, u.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Save writes dirty tasks to the normalized schema and tombstones the explicit
// ID set, all in one transaction. Differential: untouched rows are not
// rewritten; vanished rows must be in tombstones.
//
// Callers must guarantee the *Todo pointers are stable for the duration of
// the call (no concurrent mutation). The Store.drainDirty path hands us a
// deep-copied snapshot, which satisfies this.
func (r *sqliteRepo) Save(dirty []*todo.Todo, tombstones map[string]time.Time) error {
	if err := openStore(); err != nil {
		return err
	}
	return saveNormalized(db, dirty, tombstones, r.ranker().ScoreNow())
}
