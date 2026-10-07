package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// openFileDBPair opens two independent handles (separate connection pools,
// like two processes) onto one on-disk database with the schema applied —
// the shape of the hub host, where `tjek serve` and CLI invocations write
// the same file.
func openFileDBPair(t *testing.T) (h1, h2 *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.db")
	h1, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("open h1: %v", err)
	}
	t.Cleanup(func() { h1.Close() })
	h2, err = openStoreAt(path)
	if err != nil {
		t.Fatalf("open h2: %v", err)
	}
	t.Cleanup(func() { h2.Close() })
	return h1, h2
}

// TestMergeSurvivesConcurrentLocalWrite reproduces the hub clobber: a CLI
// write on the serve host lands between the merge's load and its save. The
// old three-step path overwrote the task row with the merged snapshot and
// tombstoned the just-added comment as "vanished" — a deletion that would
// then have propagated to every device. The transactional merge must instead
// detect the stale snapshot, retry, and keep BOTH the incoming edit and the
// concurrent comment.
func TestMergeSurvivesConcurrentLocalWrite(t *testing.T) {
	h1, h2 := openFileDBPair(t)

	base := time.Now().Add(-time.Hour)
	x := todo.New("shared task")
	x.ModifiedAt = base
	saveTodos(t, h1, []todo.Todo{x})

	// The incoming client push: a title edit that must win LWW on scalars.
	// Stamped ahead of the hook's AddComment (which bumps ModifiedAt to now) —
	// otherwise the hub's comment write is the later writer and correctly
	// keeps its own title, collapsing the retry into a no-op merge.
	incoming := x
	incoming.Title = "Renamed by client"
	incoming.ModifiedAt = time.Now().Add(time.Hour)

	// Between load and save, "another process" (h2) comments on the task.
	fired := false
	mergeStoreTestHook = func() {
		if fired {
			return // retry pass: the concurrent write already happened
		}
		fired = true
		cur, err := loadTodosForSync(h2)
		if err != nil {
			t.Fatalf("hook load: %v", err)
		}
		if len(cur) != 1 {
			t.Fatalf("hook: want 1 task, got %d", len(cur))
		}
		cur[0].AddComment("added mid-merge by hub CLI")
		saveTodos(t, h2, []todo.Todo{cur[0]})
	}
	t.Cleanup(func() { mergeStoreTestHook = nil })

	merged, changed, err := mergeIntoStore(h1, []todo.Todo{incoming}, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("mergeIntoStore: %v", err)
	}
	if !changed {
		t.Fatal("merge reported no change despite an incoming edit")
	}
	if !fired {
		t.Fatal("test hook never ran — hook plumbing broken")
	}
	if len(merged) != 1 {
		t.Fatalf("want 1 merged task, got %d", len(merged))
	}

	final, err := loadTodosForSync(h1)
	if err != nil {
		t.Fatalf("load final: %v", err)
	}
	if len(final) != 1 {
		t.Fatalf("want 1 task in store, got %d", len(final))
	}
	got := final[0]
	if got.Title != "Renamed by client" {
		t.Errorf("client edit lost: title = %q", got.Title)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("concurrent comment clobbered: %d comments, want 1", len(got.Comments))
	}
	if !got.Comments[0].DeletedAt.IsZero() {
		t.Errorf("concurrent comment was tombstoned (DeletedAt=%v)", got.Comments[0].DeletedAt)
	}
}

// TestMergeIntoStoreNoOpDoesNotWrite: pushing a set the store already contains
// must report changed=false — the guard that keeps the fs watcher and SSE
// broadcast from feeding back on idle periodic syncs.
func TestMergeIntoStoreNoOpDoesNotWrite(t *testing.T) {
	h := openTestDB(t)
	x := todo.New("stable")
	saveTodos(t, h, []todo.Todo{x})

	loaded, err := loadTodosForSync(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, changed, err := mergeIntoStore(h, loaded, rank.DefaultBiases())
	if err != nil {
		t.Fatalf("mergeIntoStore: %v", err)
	}
	if changed {
		t.Error("no-op merge reported changed=true")
	}
}

// TestChangedTasksWritesOnlyTouchedRows: the merge write set must be the tasks
// the merge actually changed — reordered slices are not changes, new and
// edited tasks are.
func TestChangedTasksWritesOnlyTouchedRows(t *testing.T) {
	a := todo.New("untouched")
	a.Tags = []string{"x", "y"}
	b := todo.New("edited")
	c := todo.New("new on the other side")

	current := []todo.Todo{a, b}

	bEdited := b
	bEdited.Title = "Edited elsewhere"
	// a comes back with its tags reordered — canonically identical.
	aReordered := a
	aReordered.Tags = []string{"y", "x"}
	merged := []todo.Todo{aReordered, bEdited, c}

	dirty := changedTasks(current, merged)
	got := map[string]bool{}
	for _, d := range dirty {
		got[d.ID] = true
	}
	if len(dirty) != 2 || !got[b.ID] || !got[c.ID] {
		t.Errorf("want exactly {edited, new}, got %d: %v", len(dirty), got)
	}
	if got[a.ID] {
		t.Error("reordered-but-identical task treated as changed")
	}
}
