package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// storage_sync_test.go covers the slice-2 storage changes: child deletions
// become tombstones (not hard deletes), the sync load surfaces tombstones the
// live load hides, and timestamps keep sub-second precision.

func TestSaveTombstonesVanishedChild(t *testing.T) {
	h := openTestDB(t)

	task := todo.New("parent")
	task.AddComment("keep me")
	task.AddComment("delete me")
	saveTodos(t, h, []todo.Todo{task})

	live, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(live) != 1 || len(live[0].Comments) != 2 {
		t.Fatalf("setup expected 1 task with 2 comments, got %+v", live)
	}

	// Mirror DeleteComment: drop one comment from the live slice and re-save.
	reduced := live[0]
	delID := reduced.Comments[1].ID
	reduced.Comments = reduced.Comments[:1]
	saveTodos(t, h, []todo.Todo{reduced})

	// Live load hides the deleted comment.
	live2, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(live2[0].Comments) != 1 {
		t.Fatalf("live load should show 1 comment, got %d", len(live2[0].Comments))
	}

	// Sync load retains it as a tombstone so the deletion can propagate.
	sync, err := loadTodosForSync(h)
	if err != nil {
		t.Fatalf("sync load: %v", err)
	}
	var found *todo.Comment
	for i := range sync[0].Comments {
		if sync[0].Comments[i].ID == delID {
			found = &sync[0].Comments[i]
		}
	}
	if found == nil {
		t.Fatalf("deleted comment missing from sync load")
	}
	if found.DeletedAt.IsZero() {
		t.Errorf("deleted comment should carry a tombstone timestamp")
	}
}

func TestLoadForSyncIncludesDeletedTask(t *testing.T) {
	h := openTestDB(t)

	a := todo.New("alive")
	b := todo.New("doomed")
	saveTodos(t, h, []todo.Todo{a, b})
	saveTodos(t, h, nil, b.ID) // tombstone b

	live, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("live load: %v", err)
	}
	if len(live) != 1 || live[0].ID != a.ID {
		t.Fatalf("live load should exclude the deleted task, got %d", len(live))
	}

	sync, err := loadTodosForSync(h)
	if err != nil {
		t.Fatalf("sync load: %v", err)
	}
	if len(sync) != 2 {
		t.Fatalf("sync load should include the tombstone, got %d", len(sync))
	}
	byID := map[string]todo.Todo{}
	for _, x := range sync {
		byID[x.ID] = x
	}
	if !byID[b.ID].Deleted || byID[b.ID].DeletedAt.IsZero() {
		t.Errorf("tombstoned task should be Deleted with DeletedAt set: %+v", byID[b.ID])
	}
}

func TestNanoTimestampPreserved(t *testing.T) {
	h := openTestDB(t)

	task := todo.New("precise")
	task.ModifiedAt = time.Date(2026, 6, 1, 12, 0, 0, 123456789, time.UTC)
	saveTodos(t, h, []todo.Todo{task})

	got, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if n := got[0].ModifiedAt.Nanosecond(); n != 123456789 {
		t.Errorf("sub-second precision lost: Nanosecond() = %d, want 123456789", n)
	}
}
