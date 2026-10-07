package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// TestClosePendingSubtreeClosesDescendants covers the helper behind the
// auto-close-subtasks toggle: it closes every still-pending descendant
// (transitively) and leaves already-done ones and the parent itself alone.
func TestClosePendingSubtreeClosesDescendants(t *testing.T) {
	p := makeSub("p", "parent", "", 0)
	c1 := makeSub("c1", "c1", "p", time.Second)   // pending
	c2 := makeSub("c2", "c2", "p", 2*time.Second) // already done
	c2.Status = todo.Done
	g := makeSub("g", "grand", "c1", 3*time.Second) // pending, transitive

	m := modelWithTasks(t, p, c1, c2, g)
	closed := m.closePendingSubtree("p")
	if len(closed) != 2 {
		t.Fatalf("closed = %v, want 2 (c1 and g)", closed)
	}
	if m.get("c1").Status != todo.Done {
		t.Error("pending child c1 should be closed")
	}
	if m.get("g").Status != todo.Done {
		t.Error("transitive pending grandchild g should be closed")
	}
	if m.get("p").Status != todo.Pending {
		t.Error("closePendingSubtree must not touch the parent itself")
	}
}

// TestAutoCloseSubtasksOnCascadesFromKeypress: with the toggle on, closing a
// parent (d) cascades its open subtasks closed without staging a confirm.
func TestAutoCloseSubtasksOnCascadesFromKeypress(t *testing.T) {
	p := makeSub("p", "parent", "", 0)
	c := makeSub("c", "child", "p", time.Second)

	m := modelWithTasks(t, p, c)
	m.tab = tabTasks
	m.autoCloseSubtasks = true
	m = sendKey(t, m, "d")

	if m.mode == modeConfirm {
		t.Fatal("auto-close-subtasks on: closing a parent must not prompt")
	}
	if m.get("p").Status != todo.Done {
		t.Error("parent should be closed")
	}
	if m.get("c").Status != todo.Done {
		t.Error("open subtask should cascade closed")
	}
}

// TestAutoCloseSubtasksOffPrompts: with the toggle off (default), closing a
// parent with open subtasks stages the confirm and leaves the subtask open.
func TestAutoCloseSubtasksOffPrompts(t *testing.T) {
	p := makeSub("p", "parent", "", 0)
	c := makeSub("c", "child", "p", time.Second)

	m := modelWithTasks(t, p, c)
	m.tab = tabTasks
	m.autoCloseSubtasks = false
	m = sendKey(t, m, "d")

	if m.mode != modeConfirm {
		t.Fatalf("auto-close-subtasks off: expected confirm-close-parent, got mode %v", m.mode)
	}
	if m.get("c").Status != todo.Pending {
		t.Error("subtask must stay pending until the confirm is answered")
	}
	if m.get("p").Status != todo.Pending {
		t.Error("parent must not be closed while the confirm is pending")
	}
}
