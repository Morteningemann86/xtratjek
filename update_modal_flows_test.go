package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Scripted flows for the detail-pane and list-tab modals.
//
// ARCHITECTURE.md's rule is that a modal interaction gets a script flow, for the
// reason these handlers make plain: each one is a confirm-then-mutate pair
// that has to push undo before it touches the store, mark the task modified
// after, and reset the mode — and none of that is visible from the type
// signature. Thirteen of them had no coverage at all, which is also thirteen
// places where "y" could quietly stop deleting anything.
//
// Each test asserts the same three things a user would notice if they broke:
// the store changed the way the prompt promised, the change is undoable, and
// the app came back to normal mode.

// openDetailOnTask puts the list cursor on a named task, enters the detail
// pane and parks it on the given field. Naming the task matters: the list is
// ordered by sequence score, so "the first row" is whatever the sequencer
// ranked highest, not the task the test happens to have built first. Setting
// the field directly rather than walking the section jumps keeps these tests
// about the modal, not about navigation (which
// TestScriptDetailTimeEntriesNavigate already covers).
func openDetailOnTask(t *testing.T, m model, taskID string, field detailField) model {
	t.Helper()
	m.followTask(taskID)
	m = openDetailOn(t, m, field)
	if got := m.currentTodo(); got == nil || got.ID != taskID {
		t.Fatalf("detail pane opened on %v, want the task under test", got)
	}
	return m
}

func openDetailOn(t *testing.T, m model, field detailField) model {
	t.Helper()
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail {
		t.Fatalf("pane = %v, want paneDetail", m.pane)
	}
	m.detail.field = field
	return m
}

func assertBackToNormal(t *testing.T, m model, what string) {
	t.Helper()
	if m.mode != modeNormal {
		t.Errorf("after %s: mode = %v, want modeNormal — the modal never closed", what, m.mode)
	}
}

// ── Comments ────────────────────────────────────────────────────────────────

func TestScriptEditComment(t *testing.T) {
	task := todo.New("Has a comment")
	task.AddComment("original text")
	m := modelWithTasks(t, task)

	m = openDetailOn(t, m, fieldComments)
	m = sendKey(t, m, "enter")
	if m.mode != modeEditComment {
		t.Fatalf("after 'enter': mode = %v, want modeEditComment", m.mode)
	}
	// The field must arrive pre-filled — an edit that starts blank is
	// indistinguishable from an add and silently discards the old text.
	if got := m.textInput.Value(); got != "original text" {
		t.Fatalf("input = %q, want the existing comment", got)
	}

	m = script(t, m, "backspace", "backspace", "backspace", "backspace", "ed", "enter")
	assertBackToNormal(t, m, "editing a comment")

	got := m.get(task.ID)
	if len(got.Comments) != 1 {
		t.Fatalf("got %d comments, want 1 — an edit must not add one", len(got.Comments))
	}
	if got.Comments[0].Text != "original ed" {
		t.Errorf("comment = %q, want the edited text", got.Comments[0].Text)
	}

	m = sendKey(t, m, "u")
	if restored := m.get(task.ID); restored.Comments[0].Text != "original text" {
		t.Errorf("after undo: comment = %q, want the original", restored.Comments[0].Text)
	}
}

func TestScriptEditCommentEscapeKeepsOriginal(t *testing.T) {
	task := todo.New("Has a comment")
	task.AddComment("keep me")
	m := modelWithTasks(t, task)

	m = openDetailOn(t, m, fieldComments)
	m = script(t, m, "enter", "xyz", "esc")
	assertBackToNormal(t, m, "escaping a comment edit")
	if got := m.get(task.ID).Comments[0].Text; got != "keep me" {
		t.Errorf("comment = %q, want it untouched after esc", got)
	}
}

func TestScriptDeleteComment(t *testing.T) {
	task := todo.New("Two comments")
	task.AddComment("first")
	task.AddComment("second")
	m := modelWithTasks(t, task)

	m = openDetailOn(t, m, fieldComments)
	m.detail.commentCursor = 1
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}

	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a comment delete")
	got := m.get(task.ID)
	if len(got.Comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(got.Comments))
	}
	// The cursor pointed at the second one; deleting the wrong row is the
	// failure mode an off-by-one here produces.
	if got.Comments[0].Text != "first" {
		t.Errorf("remaining comment = %q, want %q", got.Comments[0].Text, "first")
	}

	m = sendKey(t, m, "u")
	if len(m.get(task.ID).Comments) != 2 {
		t.Error("undo did not restore the deleted comment")
	}
}

func TestScriptDeleteCommentDeclined(t *testing.T) {
	task := todo.New("One comment")
	task.AddComment("survivor")
	m := modelWithTasks(t, task)

	m = openDetailOn(t, m, fieldComments)
	m = script(t, m, "x", "n")
	assertBackToNormal(t, m, "declining a comment delete")
	if len(m.get(task.ID).Comments) != 1 {
		t.Error("declining the prompt deleted the comment anyway")
	}
}

// ── Dependencies ────────────────────────────────────────────────────────────

func TestScriptAddDependencyThroughSearch(t *testing.T) {
	blocked := todo.New("Paint the wall")
	blocker := todo.New("Plaster the wall")
	m := modelWithTasks(t, blocked, blocker)

	m = openDetailOnTask(t, m, blocked.ID, fieldDependencies)
	m = sendKey(t, m, "a")
	if m.mode != modeSearchDep {
		t.Fatalf("after 'a': mode = %v, want modeSearchDep", m.mode)
	}
	m = script(t, m, "Plaster", "enter")
	assertBackToNormal(t, m, "picking a dependency")

	got := m.get(blocked.ID)
	if len(got.Dependencies) != 1 {
		t.Fatalf("got %d dependencies, want 1", len(got.Dependencies))
	}
	if got.Dependencies[0] != blocker.ID {
		t.Errorf("dependency = %q, want the plaster task", got.Dependencies[0])
	}
}

func TestScriptDependencySearchEscapes(t *testing.T) {
	m := modelWithTasks(t, todo.New("Paint the wall"), todo.New("Plaster the wall"))
	m = openDetailOn(t, m, fieldDependencies)
	m = script(t, m, "a", "Plas", "esc")
	assertBackToNormal(t, m, "escaping the dependency search")
	if len(m.currentTodo().Dependencies) != 0 {
		t.Error("escaping the search still added a dependency")
	}
}

func TestScriptDeleteDependency(t *testing.T) {
	blocker := todo.New("Plaster the wall")
	blocked := todo.New("Paint the wall")
	blocked.AddDependency(blocker.ID)
	m := modelWithTasks(t, blocked, blocker)

	m = openDetailOnTask(t, m, blocked.ID, fieldDependencies)
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a dependency delete")
	if len(m.get(blocked.ID).Dependencies) != 0 {
		t.Error("the dependency was not removed")
	}
	// The other task must be untouched: removing an edge is not removing a
	// task, and the blocker has no idea it was depended on.
	if m.get(blocker.ID) == nil {
		t.Error("removing a dependency deleted the blocking task")
	}
	m = sendKey(t, m, "u")
	if len(m.get(blocked.ID).Dependencies) != 1 {
		t.Error("undo did not restore the dependency")
	}
}

// ── Subtasks ────────────────────────────────────────────────────────────────

func TestScriptDeleteSubtask(t *testing.T) {
	parent := todo.New("Build a shed")
	sub := todo.NewSubtask("Pour the slab", parent.ID)
	m := modelWithTasks(t, parent, sub)

	m = openDetailOn(t, m, fieldSubtasks)
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a subtask delete")

	if m.get(sub.ID) != nil {
		t.Error("the subtask survived the delete")
	}
	if m.get(parent.ID) == nil {
		t.Fatal("deleting a subtask removed its parent")
	}
	// The store's subtaskOf index has to lose the edge too, or the parent
	// keeps rendering a progress count for a task that no longer exists.
	if m.subtaskCount(parent.ID) != 0 {
		t.Errorf("parent still reports %d subtasks", m.subtaskCount(parent.ID))
	}

	m = sendKey(t, m, "u")
	if m.get(sub.ID) == nil {
		t.Error("undo did not restore the subtask")
	}
}

// ── Tags and projects on the detail pane ────────────────────────────────────

func TestScriptRemoveTagFromTask(t *testing.T) {
	task := todo.New("Tagged task")
	task.AddTag("home")
	task.AddTag("urgent")
	other := todo.New("Also tagged")
	other.AddTag("home")
	m := modelWithTasks(t, task, other)

	// By ID, for the reason the project test below spells out: with equal
	// CreatedAt these two score identically and the cursor lands on whichever
	// UUID sorted first.
	m = openDetailOnTask(t, m, task.ID, fieldTags)
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a tag removal")

	got := m.get(task.ID)
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Errorf("tags = %v, want just [urgent]", got.Tags)
	}
	// This is the per-task removal, not the global one — the other task
	// keeps its copy of the tag.
	if len(m.get(other.ID).Tags) != 1 {
		t.Error("removing a tag from one task removed it from another")
	}
}

func TestScriptRemoveProjectFromTask(t *testing.T) {
	task := todo.New("Grouped task")
	task.SetProject("kitchen")
	other := todo.New("Also grouped")
	other.SetProject("kitchen")
	m := modelWithTasks(t, task, other)

	// By ID, not by cursor position: two tasks built back to back score
	// identically, and on a clock as coarse as Windows' they also share a
	// CreatedAt — so the sequence sort falls through to the ID tie-break and
	// the row under the cursor is whichever UUID sorted first.
	m = openDetailOnTask(t, m, task.ID, fieldProject)
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a project removal")

	if got := m.get(task.ID).Project; got != "" {
		t.Errorf("project = %q, want it cleared", got)
	}
	if got := m.get(other.ID).Project; got != "kitchen" {
		t.Errorf("the other task's project changed to %q", got)
	}
	m = sendKey(t, m, "u")
	if got := m.get(task.ID).Project; got != "kitchen" {
		t.Errorf("after undo: project = %q, want it restored", got)
	}
}

// ── Global tag delete, from the Tags tab ────────────────────────────────────

func TestScriptDeleteTagGlobally(t *testing.T) {
	first := todo.New("One")
	first.AddTag("obsolete")
	first.AddTag("keep")
	second := todo.New("Two")
	second.AddTag("obsolete")
	m := modelWithTasks(t, first, second)

	// Tab 3 is Tags; the digit shortcuts jump straight there.
	m = sendKey(t, m, "3")
	if m.tab != tabTags {
		t.Fatalf("tab = %v, want tabTags", m.tab)
	}
	m.tagTabCursor = tagRowIndex(t, m, "obsolete")
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	// The prompt has to say ALL — this is the one delete on the Tags tab
	// that reaches tasks the user cannot see from here.
	if !strings.Contains(m.confirmMsg, "ALL") {
		t.Errorf("confirm message %q should make the global scope obvious", m.confirmMsg)
	}

	m = sendKey(t, m, "y")
	assertBackToNormal(t, m, "confirming a global tag delete")

	for _, task := range []todo.Todo{first, second} {
		for _, tag := range m.get(task.ID).Tags {
			if tag == "obsolete" {
				t.Errorf("task %q kept the deleted tag", m.get(task.ID).Title)
			}
		}
	}
	if got := m.get(first.ID).Tags; len(got) != 1 || got[0] != "keep" {
		t.Errorf("first task's tags = %v, want just [keep]", got)
	}

	m = sendKey(t, m, "u")
	if len(m.get(second.ID).Tags) != 1 {
		t.Error("undo did not restore the globally deleted tag")
	}
}

// ── The runaway-timer prompt ────────────────────────────────────────────────

func idleModel(t *testing.T) (model, todo.Todo) {
	t.Helper()
	task := todo.New("Left running overnight")
	m := modelWithTasks(t, task)
	live := m.get(task.ID)
	live.StartTimer()
	// Backdate the open entry so the prompt reports a real elapsed time.
	live.TimeEntries[0].StartedAt = time.Now().Add(-9 * time.Hour)
	m.openIdlePrompt(live)
	if m.mode != modeIdlePrompt {
		t.Fatalf("mode = %v, want modeIdlePrompt", m.mode)
	}
	return m, task
}

func TestScriptIdlePromptKeepsTheTimer(t *testing.T) {
	m, task := idleModel(t)
	m = sendKey(t, m, "k")
	assertBackToNormal(t, m, "keeping a runaway timer")
	if !m.get(task.ID).IsTimerRunning() {
		t.Error("[k]eep stopped the timer it was supposed to keep")
	}
}

func TestScriptIdlePromptStopsTheTimer(t *testing.T) {
	m, task := idleModel(t)
	m = sendKey(t, m, "s")
	assertBackToNormal(t, m, "stopping a runaway timer")

	got := m.get(task.ID)
	if got.IsTimerRunning() {
		t.Error("[s]top left the timer running")
	}
	// Stopping keeps the tracked time — that is what distinguishes it from
	// [d]iscard, and confusing the two loses a day's work.
	if len(got.TimeEntries) != 1 {
		t.Fatalf("got %d entries, want the stopped one kept", len(got.TimeEntries))
	}
	m = sendKey(t, m, "u")
	if !m.get(task.ID).IsTimerRunning() {
		t.Error("undo did not restart the timer")
	}
}

func TestScriptIdlePromptDiscardsTheEntry(t *testing.T) {
	m, task := idleModel(t)
	m = sendKey(t, m, "d")
	assertBackToNormal(t, m, "discarding a runaway timer")

	got := m.get(task.ID)
	if len(got.TimeEntries) != 0 {
		t.Errorf("got %d entries, want the runaway one discarded", len(got.TimeEntries))
	}
	if got.IsTimerRunning() {
		t.Error("the timer is still running after a discard")
	}
	m = sendKey(t, m, "u")
	if len(m.get(task.ID).TimeEntries) != 1 {
		t.Error("undo did not restore the discarded entry")
	}
}

func TestScriptIdlePromptEscapeIsKeep(t *testing.T) {
	m, task := idleModel(t)
	// esc is the reflex for "get this off my screen"; it must be the
	// non-destructive option, not a silent discard.
	m = sendKey(t, m, "esc")
	assertBackToNormal(t, m, "escaping the idle prompt")
	if !m.get(task.ID).IsTimerRunning() {
		t.Error("esc stopped the timer — it must behave like [k]eep")
	}
}

// tagRowIndex finds a tag's row on the Tags tab. The list is sorted and can
// carry a virtual "(untagged)" row, so a literal index would pin the test to
// an ordering it does not care about.
func tagRowIndex(t *testing.T, m model, tag string) int {
	t.Helper()
	for i, got := range m.getFilteredTagsForTab() {
		if got == tag {
			return i
		}
	}
	t.Fatalf("tag %q is not on the Tags tab; rows are %v", tag, m.getFilteredTagsForTab())
	return 0
}

// Quick-add with only tokens has nothing to call the task: the field stays
// open with a hint, and nothing is stored.
func TestScriptQuickAddNeedsATitle(t *testing.T) {
	m := modelWithTasks(t)
	m = sendKey(t, m, "a")
	m = script(t, m, "#work", "enter")
	if n := len(m.allTodos()); n != 0 {
		t.Fatalf("stored %d task(s) from a title-less quick-add", n)
	}
	if m.mode != modeInput || !strings.Contains(m.err, "title") {
		t.Errorf("mode = %v, toast = %q; want the field still open and a hint", m.mode, m.err)
	}
	m = script(t, m, " home", "enter") // a title may follow the tokens
	if tasks := m.allTodos(); len(tasks) != 1 || tasks[0].Title != "Home" || !slices.Contains(tasks[0].Tags, "work") {
		t.Errorf("finishing the title stored %+v", tasks)
	}
}
