package main

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// undo_property_test.go is a property test for the undo subsystem: every
// mutation reachable from the keyboard, followed by 'u', must restore the
// store to its exact prior content. Undo correctness is enforced per call
// site (each handler chooses what pushUndo captures), so this hunts the gap
// class directly instead of enumerating handlers: run randomized scripted
// ops through the real Update dispatch and compare content digests around
// each op+undo pair. The seed is fixed — a failure reproduces exactly, and
// the failing op name + iteration are in the message.

// undoDigest fingerprints the store's task content, excluding task-level
// ModifiedAt: touchRestored deliberately stamps restored tasks with "now" so
// an undo wins the last-writer-wins sync merge — that field is expected to
// differ after undo. Everything else must round-trip bit-exact.
func undoDigest(m model) [32]byte {
	ts := m.allTodos()
	for i := range ts {
		ts[i].ModifiedAt = time.Time{}
	}
	return tasksync.StoreDigest(todoValues(ts))
}

// normalize escs the model back to the list pane in normal mode, cancelling
// any modal an op left open (a confirm prompt an op staged but a guard
// declined, a detail pane an op navigated into). Ops and the 'u' undo key
// both assume they start from list-normal.
func normalize(t *testing.T, m model) model {
	t.Helper()
	for i := 0; i < 6 && (m.mode != modeNormal || m.pane != paneList); i++ {
		m = sendKey(t, m, "esc")
	}
	if m.mode != modeNormal || m.pane != paneList {
		t.Fatalf("could not normalize model: mode=%v pane=%v", m.mode, m.pane)
	}
	return m
}

func TestUndoRestoresStoreContentProperty(t *testing.T) {
	// A few fixed seeds, each a subtest: deterministic reproduction (the
	// failing seed is in the subtest name) with wider op-sequence coverage
	// than any single seed.
	for _, seed := range []int64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runUndoProperty(t, seed)
		})
	}
}

func runUndoProperty(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	serial := 0

	parent := todo.New("Seed parent")
	sub := todo.NewSubtask("Seed child", parent.ID)
	tagged := todo.New("Seed tagged")
	tagged.AddTag("seed")
	tagged.Project = "seedproj"
	tagged.DueDate = startOfDay(time.Now()).AddDate(0, 0, 3)
	m := modelWithTasks(t, parent, sub, tagged)

	ops := []struct {
		name string
		run  func(m model) model
	}{
		{"quick add", func(m model) model {
			serial++
			return script(t, m, "a", fmt.Sprintf("generated task %d", serial), "enter")
		}},
		{"delete cascade", func(m model) model {
			return script(t, m, "x", "y")
		}},
		{"rename", func(m model) model {
			return script(t, m, "r", " renamed", "enter")
		}},
		{"toggle done", func(m model) model {
			return sendKey(t, m, "d")
		}},
		{"toggle timer", func(m model) model {
			return sendKey(t, m, "t")
		}},
		{"manual time entry", func(m model) model {
			return script(t, m, "T", "30m", "enter")
		}},
		{"add comment", func(m model) model {
			serial++
			return script(t, m, "enter", "right", "right", "a",
				fmt.Sprintf("comment %d", serial), "enter")
		}},
		{"add subtask", func(m model) model {
			serial++
			return script(t, m, "enter", "right", "a",
				fmt.Sprintf("subtask %d", serial), "enter")
		}},
		{"add comment", func(m model) model {
			// Detail page 1, field cycling reaches comments via repeated
			// down; detailAdd routes on the focused field. Navigate there
			// blind — if the field isn't reachable the op degrades to a
			// no-op and the undo-guard skips it.
			serial++
			return script(t, m, "enter", "down", "down", "down", "a",
				fmt.Sprintf("comment %d", serial), "enter")
		}},
		{"move cursor", func(m model) model {
			return sendKey(t, m, "down") // not undoable; digest must not move either
		}},
	}

	for i := 0; i < 400; i++ {
		op := ops[rng.Intn(len(ops))]
		if m.len() == 0 {
			op = ops[0] // store emptied by deletes: only adding makes progress
		}
		before := undoDigest(m)
		undoDepth := len(m.undoStack)

		m = op.run(m)
		m = normalize(t, m)

		if len(m.undoStack) == undoDepth {
			// Guarded no-op (nothing under cursor, field not reachable, …):
			// nothing was pushed, so the content must be unchanged too.
			if got := undoDigest(m); got != before {
				t.Fatalf("iter %d: op %q mutated the store without pushing undo", i, op.name)
			}
			continue
		}
		m = sendKey(t, m, "u")
		if got := undoDigest(m); got != before {
			t.Fatalf("iter %d: op %q + undo did not restore store content", i, op.name)
		}
	}
}
