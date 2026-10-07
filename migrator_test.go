package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"

	_ "modernc.org/sqlite"
)

// TestSnapshotInMemoryIsNoop confirms snapshotDBBeforeMigrations skips
// cleanly for :memory: databases (path is empty → nothing to back up).
// The migrator path runs against in-memory DBs in many tests; the snapshot
// must not break them.
func TestSnapshotInMemoryIsNoop(t *testing.T) {
	h := openTestDB(t)
	path, err := snapshotDBBeforeMigrations(h, 3)
	if err != nil {
		t.Fatalf("in-memory snapshot returned err: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path for in-memory DB, got %q", path)
	}
}

// TestSnapshotFileBackedProducesOpenableCopy is the real assertion: against
// a file-backed DB, snapshotDBBeforeMigrations writes a .bak file that can
// be reopened as an independent SQLite database with the same data.
func TestSnapshotFileBackedProducesOpenableCopy(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "tasks.db")

	// openStoreAt applies migrations and produces a normal tjek DB.
	h, err := openStoreAt(dbPath)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	// Seed one row through the normal save path so the backup has
	// recognisable contents.
	saveTodos(t, h, []todo.Todo{todo.New("backup target")})

	backupPath, err := snapshotDBBeforeMigrations(h, 4)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if backupPath == "" {
		t.Fatal("expected a non-empty backup path for file-backed DB")
	}

	// Path naming contract: lives next to the source, encodes the
	// from-version, ends with `.bak`.
	matches, err := filepath.Glob(filepath.Join(tmp, "tasks.db-pre-migration-004-*.bak"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// Compare through EvalSymlinks: on macOS the temp dir is handed out as
	// /var/folders/... while the opened file resolves to /private/var/folders/...
	// — the same file, two spellings, and a plain string compare fails there
	// and only there.
	if len(matches) != 1 {
		t.Fatalf("backup at unexpected path: glob=%v, returned=%s", matches, backupPath)
	}
	gotPath, err := filepath.EvalSymlinks(matches[0])
	if err != nil {
		t.Fatalf("resolve %s: %v", matches[0], err)
	}
	wantPath, err := filepath.EvalSymlinks(backupPath)
	if err != nil {
		t.Fatalf("resolve %s: %v", backupPath, err)
	}
	if gotPath != wantPath {
		t.Errorf("backup at unexpected path: glob=%s, returned=%s", gotPath, wantPath)
	}

	// Open the backup as a fresh independent SQLite handle and confirm the
	// row is there.
	bk, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer bk.Close()
	var count int
	if err := bk.QueryRow(`SELECT COUNT(*) FROM todos`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("backup has %d rows, want 1", count)
	}
}

// TestPrunePreMigrationBackups pins the retention rule: newest `keep` stay,
// older go, unrelated files are untouched.
func TestPrunePreMigrationBackups(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tasks.db")
	names := []string{
		"tasks.db-pre-migration-005-20260624-162950.bak",
		"tasks.db-pre-migration-006-20260625-065315.bak",
		"tasks.db-pre-migration-007-20260702-085628.bak",
		"tasks.db-pre-migration-008-20260703-191525.bak",
		"tasks.db-pre-migration-009-20260712-110537.bak",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(dir, "tasks.db-wal")
	if err := os.WriteFile(unrelated, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	prunePreMigrationBackups(dbPath, 3)

	for i, n := range names {
		_, err := os.Stat(filepath.Join(dir, n))
		if i < 2 && !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", n)
		}
		if i >= 2 && err != nil {
			t.Errorf("%s should survive: %v", n, err)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated sidecar removed: %v", err)
	}
}

func TestNormalizeStoredTagsMigration(t *testing.T) {
	h := openTestDB(t)
	task := todo.New("legacy spaced tags")
	task.ModifiedAt = time.Now().Add(-time.Hour)
	before := task.ModifiedAt
	// Bypass AddTag to model rows written by an older tjek version.
	task.Tags = []string{"Deep Work", "deep-work", "Personal   Admin"}
	saveTodos(t, h, []todo.Todo{task})

	tx, err := h.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := normalizeStoredTags(tx); err != nil {
		tx.Rollback()
		t.Fatalf("normalizeStoredTags: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load normalized tags: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d tasks, want 1", len(got))
	}
	sort.Strings(got[0].Tags)
	want := []string{"deep-work", "personal-admin"}
	if len(got[0].Tags) != len(want) || got[0].Tags[0] != want[0] || got[0].Tags[1] != want[1] {
		t.Errorf("normalized tags = %v, want %v", got[0].Tags, want)
	}
	if !got[0].ModifiedAt.After(before) {
		t.Errorf("migration ModifiedAt = %v, want after %v so sync propagates it", got[0].ModifiedAt, before)
	}
}

// Migration 011 removed the learnings feature. It must not remove what the user
// wrote: every live learning is appended to its task's notes before the table
// goes, and tombstoned ones — deleted on purpose — are not resurrected.
func TestLearningsFoldIntoNotesMigration(t *testing.T) {
	h := openTestDB(t)
	kept := todo.New("has notes already")
	kept.Notes = "existing notes"
	bare := todo.New("no notes")
	untouched := todo.New("never had learnings")
	untouched.Notes = "left alone"
	saveTodos(t, h, []todo.Todo{kept, bare, untouched})

	// Recreate the pre-011 table and seed it the way the old writer did.
	if _, err := h.Exec(`CREATE TABLE task_learnings (
		id TEXT PRIMARY KEY, task_id TEXT NOT NULL, text TEXT NOT NULL,
		created_at TEXT NOT NULL, modified_at TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("recreate task_learnings: %v", err)
	}
	seed := []struct{ id, task, text, created, deleted string }{
		{"l1", kept.ID, "second lesson", "2026-02-01T10:00:00Z", ""},
		{"l2", kept.ID, "first lesson", "2026-01-01T10:00:00Z", ""},
		{"l3", kept.ID, "deleted on purpose", "2026-03-01T10:00:00Z", "2026-03-02T10:00:00Z"},
		{"l4", bare.ID, "only lesson", "2026-01-05T10:00:00Z", ""},
	}
	for _, s := range seed {
		if _, err := h.Exec(`INSERT INTO task_learnings (id, task_id, text, created_at, deleted_at) VALUES (?,?,?,?,?)`,
			s.id, s.task, s.text, s.created, s.deleted); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	body, err := migrationFS.ReadFile("migrations/011_learnings_into_notes.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := h.Exec(string(body)); err != nil {
		t.Fatalf("apply migration 011: %v", err)
	}

	notes := func(id string) string {
		var s string
		if err := h.QueryRow(`SELECT notes FROM todos WHERE id=?`, id).Scan(&s); err != nil {
			t.Fatalf("read notes for %s: %v", id, err)
		}
		return s
	}
	// Existing notes are kept, learnings appended in created_at order.
	want := "existing notes\n\n## Learnings\n- first lesson\n- second lesson"
	if got := notes(kept.ID); got != want {
		t.Errorf("notes =\n%q\nwant\n%q", got, want)
	}
	if got := notes(kept.ID); strings.Contains(got, "deleted on purpose") {
		t.Error("a tombstoned learning came back")
	}
	// A task with no notes gets the heading without leading blank lines.
	if got, want := notes(bare.ID), "## Learnings\n- only lesson"; got != want {
		t.Errorf("empty-notes task =\n%q\nwant\n%q", got, want)
	}
	if got := notes(untouched.ID); got != "left alone" {
		t.Errorf("a task with no learnings was rewritten: %q", got)
	}
	if _, err := h.Exec(`SELECT 1 FROM task_learnings`); err == nil {
		t.Error("task_learnings survived the migration")
	}
}

// TestOpenRefusesAStoreFromANewerBuild is the fail-closed half of the
// migrator. Forward migrations are loud — an older binary hits a dropped
// table and errors on the first query — but an additive one is silent: the
// old build reads the columns it knows, writes them back, and whatever the
// newer schema added is gone on the next round-trip. With self-update and a
// sync server, running two versions against one store is what an upgrade
// *looks like*, so opening one from the future has to fail instead.
func TestOpenRefusesAStoreFromANewerBuild(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "tasks.db")

	// A normal, fully migrated store — the state a current build leaves behind.
	h, err := openStoreAt(dbPath)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	saveTodos(t, h, []todo.Todo{todo.New("written by the newer build")})
	// Stamp a version no migration in this tree provides, as a later tjek
	// would have done on its way past.
	if _, err := h.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		9999, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("stamp future schema: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = openStoreAt(dbPath)
	if !errors.Is(err, errSchemaTooNew) {
		t.Fatalf("openStoreAt on a newer store = %v, want errSchemaTooNew", err)
	}
	// The message has to carry both numbers: "which tjek do I need" is the
	// first question it will be read to answer.
	for _, want := range []string{"9999", "brew upgrade tjek", ".bak"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %v", want, err)
		}
	}
}

// TestOpenAcceptsAStoreAtTheCurrentSchema is the guard's other side: the
// version this build itself just wrote must reopen without complaint, or the
// check above would lock everyone out on the second launch.
func TestOpenAcceptsAStoreAtTheCurrentSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	h, err := openStoreAt(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	h2, err := openStoreAt(dbPath)
	if err != nil {
		t.Fatalf("reopen at the current schema: %v", err)
	}
	h2.Close()
}
