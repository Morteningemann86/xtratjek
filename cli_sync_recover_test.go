package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── normaliseBareRecover ─────────────────────────────────────────────────────

func TestNormaliseBareRecover(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"--recover"}, []string{"--recover="}},
		{[]string{"-recover"}, []string{"--recover="}},
		{[]string{"--recover=abc"}, []string{"--recover=abc"}},
		{[]string{"--recover", "--status"}, []string{"--recover=", "--status"}},
		{[]string{"--status", "--recover"}, []string{"--status", "--recover="}},
		{[]string{"--status"}, []string{"--status"}},
		{[]string{}, []string{}},
	}
	for _, c := range cases {
		got := normaliseBareRecover(c.in)
		if len(got) != len(c.want) {
			t.Errorf("normaliseBareRecover(%v) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("normaliseBareRecover(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// ── parseSyncLog ─────────────────────────────────────────────────────────────

func TestParseSyncLogAbsent(t *testing.T) {
	entries, err := parseSyncLog(filepath.Join(t.TempDir(), "nonexistent.log"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty entries for missing file, got %d", len(entries))
	}
}

func TestParseSyncLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.log")

	a := todo.New("task A")
	b := todo.New("task B")
	b.Priority = todo.PriorityHigh

	// Write two dropped-edit entries followed by a recovery marker.
	writeLogEntries(t, path, []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteDropped, Dropped: b},
		{At: "2026-06-01T12:00:00Z", Note: syncLogNoteRecovered, Dropped: a},
	})

	entries, err := parseSyncLog(path)
	if err != nil {
		t.Fatalf("parseSyncLog: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Dropped.ID != a.ID {
		t.Errorf("entry[0] ID = %q, want %q", entries[0].Dropped.ID, a.ID)
	}
	if entries[1].Dropped.Priority != todo.PriorityHigh {
		t.Errorf("entry[1] priority = %v, want high", entries[1].Dropped.Priority)
	}
	if entries[2].Note != syncLogNoteRecovered {
		t.Errorf("entry[2] note = %q, want %q", entries[2].Note, syncLogNoteRecovered)
	}
}

func TestParseSyncLogSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.log")

	a := todo.New("good task")
	good, _ := json.Marshal(syncLogEntry{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a})

	content := string(good) + "\n" + "not json at all\n" + string(good) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := parseSyncLog(path)
	if err != nil {
		t.Fatalf("parseSyncLog: %v", err)
	}
	// Malformed line skipped; two good lines parsed.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (malformed skipped), got %d", len(entries))
	}
}

// ── activeDroppedEdits ───────────────────────────────────────────────────────

func TestActiveDroppedEditsBasic(t *testing.T) {
	a := todo.New("alpha")
	b := todo.New("beta")

	entries := []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteDropped, Dropped: b},
	}

	byID, order := activeDroppedEdits(entries)
	if len(order) != 2 {
		t.Fatalf("want 2 active entries, got %d", len(order))
	}
	if _, ok := byID[a.ID]; !ok {
		t.Error("a should be active")
	}
	if _, ok := byID[b.ID]; !ok {
		t.Error("b should be active")
	}
}

func TestActiveDroppedEditsRecoveredHidden(t *testing.T) {
	a := todo.New("alpha")

	entries := []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T12:00:00Z", Note: syncLogNoteRecovered, Dropped: a},
	}

	byID, order := activeDroppedEdits(entries)
	if len(order) != 0 {
		t.Fatalf("want 0 active entries after recovery, got %d", len(order))
	}
	if _, ok := byID[a.ID]; ok {
		t.Error("recovered task should not appear in active set")
	}
}

// A task that was dropped, recovered, then dropped again should appear again.
func TestActiveDroppedEditsDroppedAgainAfterRecovery(t *testing.T) {
	a := todo.New("alpha")
	aV2 := a
	aV2.Title = "Alpha v2"

	entries := []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteRecovered, Dropped: a},
		{At: "2026-06-01T12:00:00Z", Note: syncLogNoteDropped, Dropped: aV2},
	}

	byID, order := activeDroppedEdits(entries)
	if len(order) != 1 {
		t.Fatalf("want 1 active entry (re-dropped), got %d", len(order))
	}
	if byID[a.ID].Dropped.Title != "Alpha v2" {
		t.Errorf("most-recent dropped entry should win: title = %q", byID[a.ID].Dropped.Title)
	}
}

// Multiple dropped entries for the same task — most recent wins.
func TestActiveDroppedEditsMostRecentWins(t *testing.T) {
	a := todo.New("alpha")
	aV1 := a
	aV1.Title = "Alpha v1"
	aV2 := a
	aV2.Title = "Alpha v2"

	entries := []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: aV1},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteDropped, Dropped: aV2},
	}

	byID, _ := activeDroppedEdits(entries)
	if byID[a.ID].Dropped.Title != "Alpha v2" {
		t.Errorf("most-recent entry should win: title = %q, want \"Alpha v2\"", byID[a.ID].Dropped.Title)
	}
}

// ── reapplyDroppedEdit (integration) ────────────────────────────────────────
//
// These tests go through the real save path via repo.Save so the monotonic
// ModifiedAt clamp (StampModified) is exercised exactly as production code
// uses it. They reach the store through testStore, which opens the singleton
// if the previous test released it, and share TestMain's temp HOME. Each test
// uses a per-test log path to avoid cross-test interference.

func TestReapplyDroppedEditBumpsModifiedAt(t *testing.T) {
	store := testStore(t)

	base := time.Now().Add(-time.Hour)
	task := todo.New("bumped-modifiedat task " + t.Name())
	task.ModifiedAt = base
	saveTodos(t, store, []todo.Todo{task})

	// Log entry carries a different title and priority.
	dropped := task
	dropped.Title = "Dropped title to bump ModifiedAt"
	dropped.Priority = todo.PriorityHigh

	logPath := filepath.Join(t.TempDir(), "sync.log")
	writeLogEntries(t, logPath, []syncLogEntry{
		{At: time.Now().UTC().Format(time.RFC3339Nano), Note: syncLogNoteDropped, Dropped: dropped},
	})

	prevModifiedAt := task.ModifiedAt

	// Reapply the dropped edit.
	rc := reapplyDroppedEdit(logPath, task.ID[:8])
	if rc != 0 {
		t.Fatalf("reapplyDroppedEdit rc = %d, want 0", rc)
	}

	// Load and verify via the global store.
	all, err := loadTodosForSync(store)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var got *todo.Todo
	for i := range all {
		if all[i].ID == task.ID {
			got = &all[i]
			break
		}
	}
	if got == nil {
		t.Fatal("task not found after reapply")
	}
	if got.Title != "Dropped title to bump ModifiedAt" {
		t.Errorf("title = %q, want %q", got.Title, "Dropped title to bump ModifiedAt")
	}
	if got.Priority != todo.PriorityHigh {
		t.Errorf("priority = %v, want high", got.Priority)
	}
	// StampModified guarantees ModifiedAt is strictly later than prev.
	if !got.ModifiedAt.After(prevModifiedAt) {
		t.Errorf("ModifiedAt = %v, should be strictly after previous %v", got.ModifiedAt, prevModifiedAt)
	}
}

func TestReapplyDroppedEditWritesRecoveryMarker(t *testing.T) {
	store := testStore(t)

	task := todo.New("recovery-marker task " + t.Name())
	saveTodos(t, store, []todo.Todo{task})

	dropped := task
	dropped.Title = "Dropped version for marker test"

	logPath := filepath.Join(t.TempDir(), "sync.log")
	writeLogEntries(t, logPath, []syncLogEntry{
		{At: time.Now().UTC().Format(time.RFC3339Nano), Note: syncLogNoteDropped, Dropped: dropped},
	})

	if rc := reapplyDroppedEdit(logPath, task.ID[:8]); rc != 0 {
		t.Fatalf("reapplyDroppedEdit rc = %d, want 0", rc)
	}

	// After reapply the log must have a recovery marker …
	entries, err := parseSyncLog(logPath)
	if err != nil {
		t.Fatalf("parseSyncLog: %v", err)
	}
	hasMarker := false
	for _, e := range entries {
		if e.Note == syncLogNoteRecovered && e.Dropped.ID == task.ID {
			hasMarker = true
			break
		}
	}
	if !hasMarker {
		t.Error("recovery marker not written to log")
	}
	// … and the active dropped-edit set must be empty.
	_, order := activeDroppedEdits(entries)
	if len(order) != 0 {
		t.Errorf("active entries after reapply = %d, want 0", len(order))
	}
}

// Task deleted on another device should refuse reapply rather than resurrecting it.
func TestReapplyDroppedEditRefusesDeletedTask(t *testing.T) {
	if err := openStore(); err != nil {
		t.Fatalf("openStore: %v", err)
	}

	// ghost is in the log but was never saved to the store.
	ghost := todo.New("ghost task " + t.Name())

	logPath := filepath.Join(t.TempDir(), "sync.log")
	writeLogEntries(t, logPath, []syncLogEntry{
		{At: time.Now().UTC().Format(time.RFC3339Nano), Note: syncLogNoteDropped, Dropped: ghost},
	})

	rc := reapplyDroppedEdit(logPath, ghost.ID[:8])
	if rc == 0 {
		t.Error("reapply of a non-existent task should fail, got rc 0")
	}
}

// --recover (list mode) with no log should print the "no dropped edits" message.
func TestCLISyncRecoverListEmpty(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "sync.log") // does not exist
	out := captureStdout(t, func() {
		printDroppedEdits(logPath)
	})
	if !strings.Contains(out, "no dropped edits") {
		t.Errorf("empty log output = %q, want it to say no dropped edits", out)
	}
}

// --recover (list mode) shows active entries.
func TestCLISyncRecoverListShowsEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.log")

	a := todo.New("do laundry")
	a.Priority = todo.PriorityHigh
	b := todo.New("buy groceries")

	writeLogEntries(t, path, []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteDropped, Dropped: b},
	})

	out := captureStdout(t, func() {
		printDroppedEdits(path)
	})
	if !strings.Contains(out, "Do laundry") {
		t.Errorf("output = %q, want 'Do laundry'", out)
	}
	if !strings.Contains(out, "Buy groceries") {
		t.Errorf("output = %q, want 'Buy groceries'", out)
	}
	// High priority should be mentioned.
	if !strings.Contains(out, "high") {
		t.Errorf("output = %q, want priority 'high' shown", out)
	}
}

// --recover (list mode) hides already-recovered entries.
func TestCLISyncRecoverListHidesRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.log")

	a := todo.New("recovered task")
	b := todo.New("still pending task")

	writeLogEntries(t, path, []syncLogEntry{
		{At: "2026-06-01T10:00:00Z", Note: syncLogNoteDropped, Dropped: a},
		{At: "2026-06-01T11:00:00Z", Note: syncLogNoteDropped, Dropped: b},
		{At: "2026-06-01T12:00:00Z", Note: syncLogNoteRecovered, Dropped: a},
	})

	out := captureStdout(t, func() {
		printDroppedEdits(path)
	})
	if strings.Contains(out, "Recovered task") {
		t.Errorf("already-recovered task should be hidden, but output = %q", out)
	}
	if !strings.Contains(out, "Still pending task") {
		t.Errorf("pending task should still appear, but output = %q", out)
	}
}

// writeLogEntries is a test helper that marshals entries as JSON lines into path.
func writeLogEntries(t *testing.T, path string, entries []syncLogEntry) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatalf("write entry: %v", err)
		}
	}
}
