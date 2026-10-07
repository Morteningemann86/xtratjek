package main

import (
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
	tea "github.com/charmbracelet/bubbletea"
)

// Enter is an accept on confirm prompts, matching y (they're already explicit
// opt-ins). Exercised through the real Update dispatch on a delete confirm.
func TestEnterConfirmsDelete(t *testing.T) {
	task := todo.New("delete me")
	m := newTagModel(task)
	m.tab = tabTasks
	m.mode = modeConfirm
	m.confirmOnYes = (*model).confirmDeleteTask
	m.pendingDeleteID = task.ID

	next := sendKey(t, m, "enter")

	if next.mode != modeNormal {
		t.Errorf("enter should resolve the confirm; mode = %v", next.mode)
	}
	if next.get(task.ID) != nil {
		t.Error("enter should confirm the delete, but the task is still present")
	}
}

// esc still cancels — Enter accepting must not remove the negative path.
func TestEscStillCancelsDelete(t *testing.T) {
	task := todo.New("keep me")
	m := newTagModel(task)
	m.tab = tabTasks
	m.mode = modeConfirm
	m.confirmOnYes = (*model).confirmDeleteTask
	m.pendingDeleteID = task.ID

	next := sendKey(t, m, "esc")

	if next.mode != modeNormal {
		t.Errorf("esc should close the confirm; mode = %v", next.mode)
	}
	if next.get(task.ID) == nil {
		t.Error("esc should cancel the delete, but the task was removed")
	}
}

// The generic modeConfirm handler runs the staged action on accept, skips it on
// cancel, and treats a nil action as a safe no-op. Backlog abea964b.
func TestModeConfirmRunsStagedAction(t *testing.T) {
	m := newTagModel(todo.New("x"))

	ran := false
	m.mode = modeConfirm
	m.confirmOnYes = func(m *model) tea.Cmd { ran = true; return nil }
	next := sendKey(t, m, "y")
	if !ran {
		t.Error("y should run the staged confirmOnYes action")
	}
	if next.mode != modeNormal || next.confirmOnYes != nil {
		t.Errorf("accept should close the prompt and clear the action; mode=%v onYesNil=%v",
			next.mode, next.confirmOnYes == nil)
	}

	ran = false
	m.mode = modeConfirm
	m.confirmOnYes = func(m *model) tea.Cmd { ran = true; return nil }
	if next = sendKey(t, m, "n"); ran {
		t.Error("n should cancel without running the action")
	}

	m.mode = modeConfirm
	m.confirmOnYes = nil
	if next = sendKey(t, m, "enter"); next.mode != modeNormal {
		t.Errorf("a nil action should still close the prompt; mode=%v", next.mode)
	}
}
