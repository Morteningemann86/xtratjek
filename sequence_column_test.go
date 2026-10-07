package main

import (
	"database/sql"
	"math"
	"testing"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}

// assertSequenceColumn reads the persisted sequence column for taskID and
// reports any drift from the live in-memory formula at the current rank.Biases.
// Tolerance matches approxEq (1e-3) — float comparisons must not be exact.
func assertSequenceColumn(t *testing.T, h *sql.DB, rk rank.Ranker, taskID string, live *todo.Todo, label string) {
	t.Helper()
	var col float64
	if err := h.QueryRow(`SELECT sequence FROM todos WHERE id=?`, taskID).Scan(&col); err != nil {
		t.Fatalf("%s: query sequence: %v", label, err)
	}
	want := rk.Score(live)
	if !approxEq(col, want) {
		t.Errorf("%s: column=%v, formula=%v, drift=%v", label, col, want, want-col)
	}
}

// TestSequenceColumnTracksFormulaAfterSave is the baseline: a freshly-saved
// row's `sequence` column must equal the score the live formula produces.
func TestSequenceColumnTracksFormulaAfterSave(t *testing.T) {
	h := openTestDB(t)
	balanced := rank.Ranker{Biases: rank.Biases{Deadline: rank.Balanced, Priority: rank.Balanced, Momentum: rank.Balanced, Aging: true}}

	task := todo.New("ranked task")
	task.Priority = todo.PriorityHigh
	task.Size = todo.SizeSmall
	saveTodos(t, h, []todo.Todo{task})

	assertSequenceColumn(t, h, balanced, task.ID, &task, "after save")
}

// TestSequenceColumnDriftsWithoutResync documents the bug: when the rank.Biases
// change between writes, the column reflects the *old* weights until
// something resaves the row. This test pins the drift in place so any
// "optimization" that removes resyncSequenceColumn fails CI.
func TestSequenceColumnDriftsWithoutResync(t *testing.T) {
	h := openTestDB(t)
	task := todo.New("drift demo")
	task.Priority = todo.PriorityHigh
	task.Size = todo.SizeSmall
	saveTodos(t, h, []todo.Todo{task})

	var before float64
	if err := h.QueryRow(`SELECT sequence FROM todos WHERE id=?`, task.ID).Scan(&before); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Move every bias to Intense (×2). The in-memory formula now reports a
	// score ~roughly double; the column should *not* have changed because
	// nothing resaved this row.
	intense := rank.Ranker{Biases: rank.Biases{Deadline: rank.Intense, Priority: rank.Intense, Momentum: rank.Intense, Aging: true}}
	live := intense.Score(&task)

	var after float64
	if err := h.QueryRow(`SELECT sequence FROM todos WHERE id=?`, task.ID).Scan(&after); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !approxEq(before, after) {
		t.Errorf("column changed without a save (before=%v, after=%v) — somebody resaved silently", before, after)
	}
	if approxEq(after, live) {
		t.Errorf("expected drift between stale column (%v) and live formula (%v)", after, live)
	}
}

// TestSequenceColumnAfterResync proves the fix: resyncSequenceColumn must
// rewrite the column for every live row so it matches the formula under the
// current rank.Biases. This is the test the resync implementation has to satisfy.
func TestSequenceColumnAfterResync(t *testing.T) {
	h := openTestDB(t)
	a := todo.New("alpha")
	a.Priority = todo.PriorityHigh
	a.Size = todo.SizeSmall
	b := todo.New("beta")
	b.Priority = todo.PriorityLow
	b.Size = todo.SizeLarge
	saveTodos(t, h, []todo.Todo{a, b})

	intense := rank.Ranker{Biases: rank.Biases{Deadline: rank.Intense, Priority: rank.Intense, Momentum: rank.Intense, Aging: true}}
	if err := resyncSequenceColumn(h, intense.Score); err != nil {
		t.Fatalf("resync: %v", err)
	}

	assertSequenceColumn(t, h, intense, a.ID, &a, "alpha after resync")
	assertSequenceColumn(t, h, intense, b.ID, &b, "beta after resync")
}
