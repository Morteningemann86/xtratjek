package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// TestUndoPersistRoundTrip writes a delete entry to the sidecar and reads it
// back, asserting the snapshot survives the round-trip. The whole test binary
// runs against a temp $HOME (see main_test.go), so the file lands under that.
func TestUndoPersistRoundTrip(t *testing.T) {
	parent := todo.New("parent")
	child := todo.New("child")
	child.ParentID = parent.ID

	entry := undoEntry{
		desc:    undoDescDeleteTask,
		ids:     []string{parent.ID, child.ID},
		partial: []todo.Todo{parent, child},
	}
	if err := savePersistedUndoEntries([]undoEntry{entry}); err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(undoPersistPath()) })

	got, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].desc != undoDescDeleteTask {
		t.Errorf("desc = %q, want %q", got[0].desc, undoDescDeleteTask)
	}
	if len(got[0].ids) != 2 || got[0].ids[0] != parent.ID || got[0].ids[1] != child.ID {
		t.Errorf("ids = %v, want [%s %s]", got[0].ids, parent.ID, child.ID)
	}
	if len(got[0].partial) != 2 || got[0].partial[0].ID != parent.ID {
		t.Errorf("partial = %+v, want parent first", got[0].partial)
	}
}

// TestUndoPersistCapsAtFive guards the 5-entry floor the user explicitly asked
// for: when more delete entries are pushed than the cap, only the most recent
// undoPersistMaxEntries survive in the sidecar.
func TestUndoPersistCapsAtFive(t *testing.T) {
	t.Cleanup(func() { _ = os.Remove(undoPersistPath()) })
	mk := func(id string) undoEntry {
		x := todo.New("t-" + id)
		x.ID = id
		return undoEntry{
			desc:    undoDescDeleteTask,
			ids:     []string{id},
			partial: []todo.Todo{x},
		}
	}
	var stack []undoEntry
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		stack = append(stack, mk(id))
	}
	if err := savePersistedUndoEntries(stack); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != undoPersistMaxEntries {
		t.Fatalf("want %d entries kept, got %d", undoPersistMaxEntries, len(got))
	}
	// Newest entries are kept (the tail), oldest dropped.
	wantIDs := []string{"c", "d", "e", "f", "g"}
	for i, want := range wantIDs {
		if got[i].ids[0] != want {
			t.Errorf("entry %d id = %s, want %s", i, got[i].ids[0], want)
		}
	}
}

// TestUndoPersistSkipsNonDelete asserts non-delete undo kinds aren't persisted —
// they describe mutations whose meaning is bound to the live in-memory tasks
// and don't survive a restart cleanly.
func TestUndoPersistSkipsNonDelete(t *testing.T) {
	t.Cleanup(func() { _ = os.Remove(undoPersistPath()) })
	x := todo.New("x")
	stack := []undoEntry{
		{desc: "edit notes", ids: []string{x.ID}, partial: []todo.Todo{x}},
		{desc: "remove tag", ids: []string{x.ID}, partial: []todo.Todo{x}},
	}
	if err := savePersistedUndoEntries(stack); err != nil {
		t.Fatalf("save: %v", err)
	}
	// With no delete entries, the file is removed entirely.
	if _, err := os.Stat(undoPersistPath()); !os.IsNotExist(err) {
		t.Errorf("non-delete entries should leave no file, stat err = %v", err)
	}
}

// TestUndoPersistMissingFileNoError: a fresh install has no file. Load must
// return (nil, nil) so initialModel doesn't surface a spurious error.
func TestUndoPersistMissingFileNoError(t *testing.T) {
	_ = os.Remove(undoPersistPath())
	got, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Errorf("want nil entries, got %v", got)
	}
}

// TestUndoPersistSurvivesRestart simulates the real bug: pushUndo("delete
// task") in one store, build a fresh store via initialModel-style restore,
// pop, and check the partial snapshot comes back intact. This is the full
// loop the user expects: delete → quit → restart → 'u' restores.
func TestUndoPersistSurvivesRestart(t *testing.T) {
	t.Cleanup(func() { _ = os.Remove(undoPersistPath()) })

	src := &Store{}
	src.ensureTasks()
	parent := todo.New("survives-restart")
	src.add(parent)
	src.pushUndo(undoDescDeleteTask, parent.ID)
	src.remove(parent.ID)

	// Simulate restart: rebuild a fresh store from the sidecar.
	dst := &Store{}
	dst.ensureTasks()
	persisted, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst.undoStack = append(dst.undoStack, persisted...)

	entry, ok := dst.popUndo()
	if !ok {
		t.Fatal("popUndo returned no entry after restart restore")
	}
	if entry.desc != undoDescDeleteTask {
		t.Errorf("desc = %q, want %q", entry.desc, undoDescDeleteTask)
	}
	if len(entry.partial) != 1 || entry.partial[0].ID != parent.ID {
		t.Errorf("partial lost the parent task: %+v", entry.partial)
	}
}

// TestUndoPersistCorruptFileReportsError: a broken JSON file must surface as
// an error so initialModel can show it (and the user can rm the file) rather
// than silently dropping the recoverable history.
func TestUndoPersistCorruptFileReportsError(t *testing.T) {
	path := undoPersistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if _, err := loadPersistedUndoEntries(); err == nil {
		t.Errorf("want error for corrupt JSON, got nil")
	}
}

// TestRecordDeleteUndoReplacesCorruptHistory: a corrupt undo.json must not
// silently disable recording for a CLI delete — the entry lands in a fresh
// history and the function reports the delete as recoverable.
func TestRecordDeleteUndoReplacesCorruptHistory(t *testing.T) {
	path := undoPersistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	victim := todo.New("deleted while history corrupt")
	entry := undoEntry{desc: undoDescDeleteTask, ids: []string{victim.ID}, partial: []todo.Todo{victim}}
	if !recordDeleteUndo(entry) {
		t.Fatal("recordDeleteUndo = false, want true (entry should land in a fresh history)")
	}

	got, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("history still unreadable after record: %v", err)
	}
	if len(got) != 1 || len(got[0].partial) != 1 || got[0].partial[0].ID != victim.ID {
		t.Fatalf("recorded entry not found, got %+v", got)
	}
}

// TestRecordDeleteUndoAppendsToHealthyHistory: the ordinary path keeps prior
// entries and appends the new one newest-last.
func TestRecordDeleteUndoAppendsToHealthyHistory(t *testing.T) {
	t.Cleanup(func() { _ = os.Remove(undoPersistPath()) })
	_ = os.Remove(undoPersistPath())

	first := todo.New("first delete")
	if !recordDeleteUndo(undoEntry{desc: undoDescDeleteTask, ids: []string{first.ID}, partial: []todo.Todo{first}}) {
		t.Fatal("first record failed")
	}
	second := todo.New("second delete")
	if !recordDeleteUndo(undoEntry{desc: undoDescDeleteTask, ids: []string{second.ID}, partial: []todo.Todo{second}}) {
		t.Fatal("second record failed")
	}

	got, err := loadPersistedUndoEntries()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[1].partial[0].ID != second.ID {
		t.Fatalf("want 2 entries newest-last, got %+v", got)
	}
}
