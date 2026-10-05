package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Iliorn/tjek/todo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// update_keyscript_test.go drives the real Update dispatch with scripted key
// sequences — the closest thing to "a user at the keyboard" that runs without
// a terminal. These flows live in update_modes.go / update_detail.go, which
// hold the app's most convention-enforced invariants (pushUndo before mutate,
// markModified after, cursor clamps, mode resets) and historically had almost
// no coverage. Each test asserts the store/undo/save bookkeeping, not the
// rendering.

// script sends each entry through the full Update path. Multi-rune strings
// arrive as one KeyRunes msg (how Bubble Tea delivers a paste), which the
// textinput consumes the same as typed characters.
func script(t *testing.T, m model, keys ...string) model {
	t.Helper()
	for _, k := range keys {
		m = sendKey(t, m, k)
	}
	return m
}

// sendKeyCmd is sendKey but keeps the returned command, for asserting on
// tea.Quit and friends.
func sendKeyCmd(t *testing.T, m model, msg tea.KeyMsg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func TestScriptQuickAddCreatesTaskAndUndoRemoves(t *testing.T) {
	m := modelWithTasks(t)

	m = sendKey(t, m, "a")
	if m.mode != modeInput {
		t.Fatalf("after 'a': mode = %v, want modeInput", m.mode)
	}
	m = script(t, m, "fix the boiler", "enter")

	if m.mode != modeNormal {
		t.Fatalf("after enter: mode = %v, want modeNormal", m.mode)
	}
	if m.len() != 1 {
		t.Fatalf("store has %d tasks, want 1", m.len())
	}
	created := m.currentTodo()
	if created == nil || created.Title != "Fix the boiler" {
		t.Fatalf("created = %+v, want capitalized title", created)
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("mutation did not schedule a save (savePending/saveScheduled both false)")
	}
	if len(m.undoStack) != 1 {
		t.Fatalf("undoStack len = %d, want 1", len(m.undoStack))
	}

	// Undo of a creation must remove the task AND tombstone it, so the
	// pending save can't resurrect it.
	id := created.ID
	m = sendKey(t, m, "u")
	if m.len() != 0 {
		t.Errorf("after undo: store has %d tasks, want 0", m.len())
	}
	if _, ok := m.tombstones[id]; !ok {
		t.Error("after undo of a create: no tombstone recorded for the removed task")
	}
}

// TestScriptQuickAddOpensDetailOnNewTask asserts that pressing Enter in the
// add-task input opens the detail pane of the newly created task with the
// cursor/selection positioned on that task. This is the "open detail after add"
// flow from backlog item fa2261e1.
func TestScriptQuickAddOpensDetailOnNewTask(t *testing.T) {
	// Start with one existing task so we can verify the cursor moves to the
	// newly added task rather than staying on whatever row it was on.
	existing := todo.New("existing task")
	m := modelWithTasks(t, existing)

	// Cursor starts on the existing task.
	if m.pane != paneList {
		t.Fatalf("initial pane = %v, want paneList", m.pane)
	}

	m = script(t, m, "a", "brand new task", "enter")

	// The app must switch to the detail pane.
	if m.pane != paneDetail {
		t.Errorf("after add: pane = %v, want paneDetail", m.pane)
	}
	// The detail must be reset to the first field.
	if m.detail.field != fieldStartDate {
		t.Errorf("after add: detail.field = %v, want fieldStartDate", m.detail.field)
	}
	// The cursor (and currentTodo) must point at the newly added task.
	got := m.currentTodo()
	if got == nil {
		t.Fatal("currentTodo is nil after add — cursor not on any task")
	}
	if got.Title != "Brand new task" {
		t.Errorf("currentTodo.Title = %q, want %q", got.Title, "Brand new task")
	}
}

// TestScriptQuickAddOpensDetailOnFirstTask covers the edge case where the list
// was empty before the add: the newly created task must still land in the
// detail pane.
func TestScriptQuickAddOpensDetailOnFirstTask(t *testing.T) {
	m := modelWithTasks(t) // empty list

	m = script(t, m, "a", "very first task", "enter")

	if m.pane != paneDetail {
		t.Errorf("after first add: pane = %v, want paneDetail", m.pane)
	}
	got := m.currentTodo()
	if got == nil {
		t.Fatal("currentTodo is nil after add into an empty list")
	}
	if got.Title != "Very first task" {
		t.Errorf("currentTodo.Title = %q, want %q", got.Title, "Very first task")
	}
}

// TestScriptQuickAddFilteredOutStaysInList covers adding a task while a search
// filter hides it: the cursor can't follow the new task, so the app must stay
// in the list pane instead of opening the detail of whatever task the cursor
// was on.
func TestScriptQuickAddFilteredOutStaysInList(t *testing.T) {
	existing := todo.New("alpha task")
	m := modelWithTasks(t, existing)

	m = script(t, m, "/", "alpha", "enter")
	if m.searchQuery != "alpha" {
		t.Fatalf("searchQuery = %q, want alpha", m.searchQuery)
	}

	m = script(t, m, "a", "bravo task", "enter")

	if m.len() != 2 {
		t.Fatalf("store has %d tasks, want 2", m.len())
	}
	if m.pane != paneList {
		t.Errorf("after filtered add: pane = %v, want paneList", m.pane)
	}
	if got := m.currentTodo(); got == nil || got.Title != "Alpha task" {
		t.Errorf("currentTodo = %+v, want cursor to stay on the visible task", got)
	}
}

func TestScriptQuickAddParsesInlineSyntax(t *testing.T) {
	m := modelWithTasks(t)
	m = script(t, m, "a", "ship release p:high due:tomorrow #work @tjek", "enter")

	created := m.currentTodo()
	if created == nil {
		t.Fatal("no task created")
	}
	if created.Title != "Ship release" {
		t.Errorf("title = %q, want syntax tokens stripped", created.Title)
	}
	if created.Priority != todo.PriorityHigh {
		t.Errorf("priority = %v, want High", created.Priority)
	}
	if created.Project != "tjek" {
		t.Errorf("project = %q, want tjek", created.Project)
	}
	if len(created.Tags) != 1 || created.Tags[0] != "work" {
		t.Errorf("tags = %v, want [work]", created.Tags)
	}
	wantDue := startOfDay(time.Now()).AddDate(0, 0, 1)
	if !created.DueDate.Equal(wantDue) {
		t.Errorf("due = %v, want %v", created.DueDate, wantDue)
	}
}

func TestDetailDueDatePropagatesToSubtasksAndUndoRestores(t *testing.T) {
	oldDue := startOfDay(time.Now()).AddDate(0, 0, 3)
	parent := todo.New("Parent deadline")
	parent.DueDate = oldDue
	child := todo.NewSubtask("Child deadline", parent.ID)
	child.DueDate = oldDue
	grandchild := todo.NewSubtask("Grandchild deadline", child.ID)
	grandchild.DueDate = oldDue
	m := modelWithTasks(t, parent, child, grandchild)

	m.pane = paneDetail
	m.detail.field = fieldDueDate
	m.mode = modeInput
	m.textInput.SetValue("+10d")
	m = sendKey(t, m, "enter")

	want := startOfDay(time.Now()).AddDate(0, 0, 10)
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		if got := m.get(id).DueDate; !got.Equal(want) {
			t.Errorf("%s due = %v, want propagated %v", id, got, want)
		}
		if _, dirty := m.dirtyIDs[id]; !dirty {
			t.Errorf("%s not marked dirty after due propagation", id)
		}
	}

	m.performUndo()
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		if got := m.get(id).DueDate; !got.Equal(oldDue) {
			t.Errorf("undo: %s due = %v, want original %v", id, got, oldDue)
		}
	}
}

func TestScriptDeleteCascadesAndUndoRestoresSubtree(t *testing.T) {
	parent := todo.New("Parent job")
	sub := todo.NewSubtask("Child step", parent.ID)
	m := modelWithTasks(t, parent, sub)

	// Cursor sits on the only top-level task. Delete must stage a confirm
	// that mentions the cascade, then tombstone parent + child on 'y'.
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}
	m = sendKey(t, m, "y")
	if m.len() != 0 {
		t.Fatalf("after confirm: %d tasks left, want 0 (cascade)", m.len())
	}
	if len(m.tombstones) != 2 {
		t.Fatalf("tombstones = %d, want 2 (parent + subtask)", len(m.tombstones))
	}

	// Undo must restore both, clear their tombstones, and rebuild the
	// subtaskOf index (the de72a1a bug class: parent restored but its
	// subtask bucket wiped, children unreachable until restart).
	m = sendKey(t, m, "u")
	if m.len() != 2 {
		t.Fatalf("after undo: %d tasks, want 2", m.len())
	}
	if _, dead := m.tombstones[parent.ID]; dead {
		t.Error("after undo: parent still tombstoned — pending save would re-delete it")
	}
	if _, dead := m.tombstones[sub.ID]; dead {
		t.Error("after undo: subtask still tombstoned")
	}
	if got := m.subtaskCount(parent.ID); got != 1 {
		t.Errorf("after undo: subtaskCount = %d, want 1 (index rebuilt)", got)
	}
	// The restore must count as the latest write or the next sync would
	// re-apply the deletion (tombstone carries a newer DeletedAt).
	if restored := m.get(parent.ID); restored != nil && time.Since(restored.ModifiedAt) > time.Minute {
		t.Errorf("restored parent ModifiedAt = %v — undo must stamp now so it wins the merge", restored.ModifiedAt)
	}
}

func TestScriptRenameTaskViaR(t *testing.T) {
	m := modelWithTasks(t, todo.New("Old name"))
	target := m.currentTodo()

	m = sendKey(t, m, "r")
	if m.mode != modeEditTitle {
		t.Fatalf("after 'r': mode = %v, want modeEditTitle", m.mode)
	}
	// The editor pre-fills the current title; typed runes append.
	m = script(t, m, " v2", "enter")
	if got := m.get(target.ID); got.Title != "Old name v2" {
		t.Errorf("title = %q, want %q", got.Title, "Old name v2")
	}
	if _, dirty := m.dirtyIDs[target.ID]; !dirty {
		t.Error("renamed task not in dirtyIDs — rename would never persist")
	}
	if len(m.undoStack) != 1 {
		t.Errorf("undoStack len = %d, want 1", len(m.undoStack))
	}
}

// D sets a due date from the list, using the detail pane's prompt and parser.
func TestScriptSetDueDateFromList(t *testing.T) {
	m := modelWithTasks(t, todo.New("Reschedule me"))
	target := m.currentTodo()

	m = sendKey(t, m, "D")
	if m.mode != modeEditDue {
		t.Fatalf("after 'D': mode = %v, want modeEditDue", m.mode)
	}
	m = script(t, m, "+3d", "enter")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want back to normal after enter", m.mode)
	}
	got := m.get(target.ID)
	want := startOfDay(time.Now().AddDate(0, 0, 3))
	if !startOfDay(got.DueDate).Equal(want) {
		t.Errorf("due date = %v, want %v", got.DueDate, want)
	}
	if _, dirty := m.dirtyIDs[target.ID]; !dirty {
		t.Error("rescheduled task not in dirtyIDs — the date would never persist")
	}

	// Undo puts the task back to having no due date at all.
	m.performUndo()
	if got := m.get(target.ID); !got.DueDate.IsZero() {
		t.Errorf("after undo: due date = %v, want zero", got.DueDate)
	}
}

// An empty prompt clears the date; an unparseable one keeps the task as it was
// and leaves the user in the prompt to fix the typo.
func TestScriptSetDueDateClearsAndRejects(t *testing.T) {
	task := todo.New("Has a date")
	task.DueDate = time.Now().AddDate(0, 0, 2)
	m := modelWithTasks(t, task)

	m = sendKey(t, m, "D")
	m = script(t, m, "not a date", "enter")
	if m.mode != modeEditDue {
		t.Errorf("mode = %v, want to stay in the prompt after a bad date", m.mode)
	}
	if m.get(task.ID).DueDate.IsZero() {
		t.Error("a rejected date cleared the existing one")
	}
	if m.err == "" {
		t.Error("a rejected date said nothing")
	}

	// esc, then a second pass that submits an empty value.
	m = sendKey(t, m, "esc")
	m = sendKey(t, m, "D")
	// The prompt pre-fills the current date, so clear it first.
	for range "02-01-06" {
		m = sendKey(t, m, "backspace")
	}
	m = sendKey(t, m, "enter")
	if got := m.get(task.ID); !got.DueDate.IsZero() {
		t.Errorf("due date = %v, want cleared", got.DueDate)
	}
}

func TestScriptAddCommentOnDetailComments(t *testing.T) {
	m := modelWithTasks(t, todo.New("Task with comments"))
	target := m.currentTodo()

	// Detail pane → jump sections to comments (tags → subtasks → deps →
	// time entries → comments).
	m = script(t, m, "enter", "right", "right", "right", "right", "right")
	if m.pane != paneDetail || m.detail.field != fieldComments {
		t.Fatalf("pane=%v field=%v, want detail fieldComments", m.pane, m.detail.field)
	}
	m = script(t, m, "a", "looks good", "enter")

	got := m.get(target.ID)
	if len(got.Comments) != 1 || got.Comments[0].Text != "looks good" {
		t.Fatalf("comments = %+v, want one 'looks good'", got.Comments)
	}
	if m.detail.commentCursor != 0 {
		t.Errorf("commentCursor = %d, want 0 (on the new comment)", m.detail.commentCursor)
	}
	if len(m.undoStack) != 1 {
		t.Errorf("undoStack len = %d, want 1", len(m.undoStack))
	}
}

func TestScriptAddSubtaskFromDetail(t *testing.T) {
	m := modelWithTasks(t, todo.New("Parent task"))
	target := m.currentTodo()

	m = script(t, m, "enter", "right", "right") // sections: tags → subtasks
	if m.mode != modeNormal || m.detail.field != fieldSubtasks {
		t.Fatalf("field = %v, want fieldSubtasks", m.detail.field)
	}
	m = script(t, m, "a", "child step", "enter")

	if got := m.subtaskCount(target.ID); got != 1 {
		t.Fatalf("subtaskCount = %d, want 1", got)
	}
	subID := m.subtaskIDs(target.ID)[0]
	sub := m.get(subID)
	if sub.Title != "Child step" || sub.ParentID != target.ID {
		t.Errorf("subtask = %+v, want capitalized title under parent", sub)
	}
	if _, dirty := m.dirtyIDs[subID]; !dirty {
		t.Error("new subtask not in dirtyIDs")
	}
}

func TestEnterOpensSelectedSubtaskDetail(t *testing.T) {
	parent := todo.New("Parent task")
	child := todo.NewSubtask("Child task", parent.ID)
	grandchild := todo.NewSubtask("Grandchild task", child.ID)
	m := modelWithTasks(t, parent, child, grandchild)
	m.pane = paneDetail
	m.detailTaskID = parent.ID
	m.detail = detailState{field: fieldSubtasks, subtaskCursor: 0}
	m.pushFocus(stateDetailPane)

	m = script(t, m, "enter")
	if cur := m.currentTodo(); cur == nil || cur.ID != child.ID {
		t.Fatalf("Enter should open child details, current task = %+v", cur)
	}
	if m.mode != modeNormal || m.detail.field != fieldStartDate {
		t.Fatalf("mode=%v field=%v, want normal detail at start date", m.mode, m.detail.field)
	}

	// The explicit detail target also allows traversal below the one-level
	// Tasks-list expansion.
	m.detail = detailState{field: fieldSubtasks, subtaskCursor: 0}
	m = script(t, m, "enter")
	if cur := m.currentTodo(); cur == nil || cur.ID != grandchild.ID {
		t.Fatalf("Enter should open nested child details, current task = %+v", cur)
	}

	// Esc backs out one level per press — grandchild → child → parent → list —
	// instead of collapsing straight back to the list.
	m = script(t, m, "esc")
	if m.pane != paneDetail || m.detailTaskID != child.ID {
		t.Fatalf("first Esc should return to child detail: pane=%v target=%q", m.pane, m.detailTaskID)
	}
	if m.detail.field != fieldSubtasks {
		t.Fatalf("returning up a level should land on the Subtasks section, field=%v", m.detail.field)
	}
	m = script(t, m, "esc")
	if m.pane != paneDetail || m.detailTaskID != parent.ID {
		t.Fatalf("second Esc should return to parent detail: pane=%v target=%q", m.pane, m.detailTaskID)
	}
	m = script(t, m, "esc")
	if m.pane != paneList || m.detailTaskID != "" {
		t.Fatalf("final Esc should return to list and clear detail target: pane=%v target=%q", m.pane, m.detailTaskID)
	}
	if cur := m.currentTodo(); cur == nil || cur.ID != parent.ID {
		t.Fatalf("list cursor should remain on parent after closing nested details, got %+v", cur)
	}
}

func TestDetailSigilsQuickAddTagAndProjectFromAnyField(t *testing.T) {
	task := todo.New("Target")
	m := modelWithTasks(t, task)
	m.pane = paneDetail
	m.detailTaskID = task.ID
	m.detail = detailState{field: fieldComments}
	m.pushFocus(stateDetailPane)

	m = sendKey(t, m, "#")
	if m.mode != modeSearchTag {
		t.Fatalf("after '#': mode = %v, want modeSearchTag", m.mode)
	}
	if m.detail.field != fieldComments {
		t.Fatalf("'#' moved detail cursor to %v, want fieldComments", m.detail.field)
	}
	m = script(t, m, "urgent", "enter")
	if got := m.get(task.ID).Tags; len(got) != 1 || got[0] != "urgent" {
		t.Fatalf("tags after quick add = %v, want [urgent]", got)
	}

	m.detail.field = fieldStartDate
	m = sendKey(t, m, "@")
	if m.mode != modeSearchProject {
		t.Fatalf("after '@': mode = %v, want modeSearchProject", m.mode)
	}
	if m.detail.field != fieldStartDate {
		t.Fatalf("'@' moved detail cursor to %v, want fieldStartDate", m.detail.field)
	}
	m = script(t, m, "launch", "enter")
	if got := m.get(task.ID).Project; got != "launch" {
		t.Fatalf("project after quick add = %q, want %q", got, "launch")
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("quick detail mutations did not schedule a save")
	}
}

func TestRRenamesSelectedSubtask(t *testing.T) {
	parent := todo.New("Parent task")
	child := todo.NewSubtask("Old child title", parent.ID)
	m := modelWithTasks(t, parent, child)
	m.pane = paneDetail
	m.detailTaskID = parent.ID
	m.detail = detailState{field: fieldSubtasks, subtaskCursor: 0}

	m = script(t, m, "r")
	if m.mode != modeEditSubtask || m.textInput.Value() != child.Title {
		t.Fatalf("r should start subtask rename: mode=%v value=%q", m.mode, m.textInput.Value())
	}
	m.textInput.SetValue("Renamed child")
	m = script(t, m, "enter")
	if got := m.get(child.ID).Title; got != "Renamed child" {
		t.Errorf("renamed subtask title = %q, want %q", got, "Renamed child")
	}
}

func TestScriptManualTimeEntryEndsNow(t *testing.T) {
	m := modelWithTasks(t, todo.New("Tracked work"))
	target := m.currentTodo()

	m = script(t, m, "T", "45m", "enter")

	got := m.get(target.ID)
	if len(got.TimeEntries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.TimeEntries))
	}
	e := got.TimeEntries[0]
	if e.IsRunning() {
		t.Fatal("manual entry is running — must be closed")
	}
	if d := e.StoppedAt.Sub(e.StartedAt); d != 45*time.Minute {
		t.Errorf("duration = %v, want 45m", d)
	}
	// "I just spent 45m on this": the entry must end now, not start now.
	if drift := time.Since(e.StoppedAt); drift < 0 || drift > 5*time.Second {
		t.Errorf("entry ends %v from now, want ≈now", drift)
	}
}

func TestScriptSearchFiltersAndEscRestores(t *testing.T) {
	m := modelWithTasks(t, todo.New("Alpha work"), todo.New("Beta work"))

	m = script(t, m, "/", "alpha", "enter")
	if m.searchQuery != "alpha" {
		t.Fatalf("searchQuery = %q, want alpha", m.searchQuery)
	}
	// The filtered rebuild is lazy — View calls ensureCache before reading.
	m.ensureCache()
	if got := m.visibleActiveLen(); got != 1 {
		t.Errorf("filtered list len = %d, want 1", got)
	}

	// Re-enter search and cancel: the filter must clear entirely.
	m = script(t, m, "/", "esc")
	if m.searchQuery != "" {
		t.Errorf("after esc: searchQuery = %q, want empty", m.searchQuery)
	}
	m.ensureCache()
	if got := m.visibleActiveLen(); got != 2 {
		t.Errorf("after esc: list len = %d, want 2", got)
	}
}

func TestScriptTimerKeepsSingleRunningInvariant(t *testing.T) {
	m := modelWithTasks(t, todo.New("First"), todo.New("Second"))

	m = sendKey(t, m, "t")
	first := m.currentTodo()
	if !first.IsTimerRunning() {
		t.Fatal("first task's timer not running after 't'")
	}
	m = script(t, m, "down", "t")
	second := m.currentTodo()
	if second.ID == first.ID {
		t.Fatal("cursor did not move — test setup wrong")
	}
	if !second.IsTimerRunning() {
		t.Error("second task's timer not running")
	}
	if m.get(first.ID).IsTimerRunning() {
		t.Error("first task still running — single-timer invariant broken")
	}
	if len(m.runningTimers) != 1 {
		t.Errorf("runningTimers = %d, want 1", len(m.runningTimers))
	}
}

func TestScriptCtrlCQuitsFromAnyMode(t *testing.T) {
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}

	assertQuits := func(t *testing.T, m model, where string) {
		t.Helper()
		_, cmd := sendKeyCmd(t, m, ctrlC)
		if cmd == nil {
			t.Fatalf("%s: ctrl+c returned nil cmd, want tea.Quit", where)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: ctrl+c cmd = %T, want tea.QuitMsg", where, cmd())
		}
	}

	m := modelWithTasks(t, todo.New("Something"))
	assertQuits(t, m, "list pane")
	assertQuits(t, sendKey(t, m, "enter"), "detail pane")
	assertQuits(t, sendKey(t, m, "a"), "quick-add modal")
	assertQuits(t, sendKey(t, m, "x"), "confirm-delete modal")
}

func TestScriptEscCancelsModalWithoutMutation(t *testing.T) {
	m := modelWithTasks(t, todo.New("Existing"))

	m = script(t, m, "a", "abandoned draft", "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v, want modeNormal", m.mode)
	}
	if m.len() != 1 {
		t.Errorf("store len = %d, want 1 (nothing created)", m.len())
	}
	if len(m.undoStack) != 0 {
		t.Errorf("undoStack len = %d, want 0 (nothing to undo)", len(m.undoStack))
	}
	if m.savePending || m.saveScheduled {
		t.Error("cancelled modal scheduled a save")
	}
}

func TestScriptToggleDoneStopsTimerAndMarksDone(t *testing.T) {
	m := modelWithTasks(t, todo.New("Close me"))
	target := m.currentTodo()

	// Start its timer, then close it: the TUI must not leave a dangling
	// open time entry on a done task.
	m = script(t, m, "t", "d")
	got := m.get(target.ID)
	if got.Status != todo.Done {
		t.Fatalf("status = %v, want Done", got.Status)
	}
	if got.IsTimerRunning() {
		t.Error("timer still running on a done task")
	}
	if got.CompletedAt.IsZero() {
		t.Error("CompletedAt not stamped")
	}
}

// ── Time-entries section in the detail pane ───────────────────────────────────

func navToTimeEntries(t *testing.T, m model) model {
	t.Helper()
	// enter → detail pane; → steps fields, tags, subtasks, dependencies,
	// time entries.
	return script(t, m, "enter", "right", "right", "right", "right")
}

func TestScriptDetailTimeEntriesNavigate(t *testing.T) {
	task := todo.New("Tracked task")
	now := time.Now()
	task.AddTimeEntry(now.Add(-2*time.Hour), now.Add(-time.Hour))
	task.AddTimeEntry(now.Add(-30*time.Minute), now.Add(-10*time.Minute))
	m := modelWithTasks(t, task)

	m = navToTimeEntries(t, m)
	if m.pane != paneDetail {
		t.Fatalf("pane = %v, want paneDetail", m.pane)
	}
	if m.detail.field != fieldTimeEntries {
		t.Fatalf("field = %v, want fieldTimeEntries", m.detail.field)
	}
	if m.detail.timeEntryCursor != 0 {
		t.Fatalf("timeEntryCursor = %d, want 0", m.detail.timeEntryCursor)
	}

	// Navigate down to the second entry.
	m = sendKey(t, m, "down")
	if m.detail.timeEntryCursor != 1 {
		t.Fatalf("timeEntryCursor after down = %d, want 1", m.detail.timeEntryCursor)
	}
	// Navigate back up.
	m = sendKey(t, m, "up")
	if m.detail.timeEntryCursor != 0 {
		t.Fatalf("timeEntryCursor after up = %d, want 0", m.detail.timeEntryCursor)
	}
}

func TestScriptDetailTimeEntryEdit(t *testing.T) {
	task := todo.New("Editable entry")
	now := time.Now()
	task.AddTimeEntry(now.Add(-2*time.Hour), now.Add(-time.Hour))
	m := modelWithTasks(t, task)
	target := m.get(task.ID)
	entryID := target.TimeEntries[0].ID

	m = navToTimeEntries(t, m)
	if m.detail.field != fieldTimeEntries {
		t.Fatalf("field = %v, want fieldTimeEntries", m.detail.field)
	}

	// 'r' must open modeEditTimeEntry with the entry pre-filled.
	m = sendKey(t, m, "r")
	if m.mode != modeEditTimeEntry {
		t.Fatalf("after 'r': mode = %v, want modeEditTimeEntry", m.mode)
	}
	if m.pendingEntryID != entryID {
		t.Fatalf("pendingEntryID = %q, want %q", m.pendingEntryID, entryID)
	}

	// Submit a new time range.
	m = script(t, m, "esc") // cancel the input
	if m.mode != modeNormal {
		t.Fatalf("after esc: mode = %v, want modeNormal", m.mode)
	}

	// Now open and confirm an edit.
	m = sendKey(t, m, "r")
	m.textInput.SetValue("08:00-09:30")
	m = sendKey(t, m, "enter")

	got := m.get(task.ID)
	if len(got.TimeEntries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.TimeEntries))
	}
	d := got.TimeEntries[0].StoppedAt.Sub(got.TimeEntries[0].StartedAt)
	if d != 90*time.Minute {
		t.Errorf("edited duration = %v, want 90m", d)
	}
	if len(m.undoStack) == 0 {
		t.Error("edit must push an undo entry")
	}
}

func TestScriptDetailTimeEntryDeleteAndUndoStack(t *testing.T) {
	task := todo.New("Deletable entry")
	now := time.Now()
	task.AddTimeEntry(now.Add(-time.Hour), now)
	m := modelWithTasks(t, task)
	target := m.get(task.ID)
	if len(target.TimeEntries) != 1 {
		t.Fatalf("setup: %d entries, want 1", len(target.TimeEntries))
	}

	m = navToTimeEntries(t, m)
	if m.detail.field != fieldTimeEntries {
		t.Fatalf("field = %v, want fieldTimeEntries", m.detail.field)
	}

	// 'x' must open a confirm prompt.
	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("after 'x': mode = %v, want modeConfirm", m.mode)
	}

	// Confirm deletion with 'y'.
	m = sendKey(t, m, "y")
	if m.mode != modeNormal {
		t.Fatalf("after 'y': mode = %v, want modeNormal", m.mode)
	}
	got := m.get(task.ID)
	if len(got.TimeEntries) != 0 {
		t.Fatalf("after delete: %d entries, want 0", len(got.TimeEntries))
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("deletion did not schedule a save")
	}
	// Deletion must push an undo entry so it can be reversed.
	if len(m.undoStack) == 0 {
		t.Fatal("delete must push an undo entry")
	}
	// The undo entry must name the parent task's ID so a partial restore
	// knows which task to snapshot and mark dirty.
	top := m.undoStack[len(m.undoStack)-1]
	if len(top.ids) == 0 || top.ids[0] != task.ID {
		t.Errorf("undo entry ids = %v, want [%s]", top.ids, task.ID)
	}
	// The undo snapshot must capture the pre-delete state (1 entry).
	if len(top.partial) == 0 {
		t.Error("undo entry must carry a partial snapshot")
	} else if len(top.partial[0].TimeEntries) != 1 {
		t.Errorf("undo snapshot has %d entries, want 1 (pre-delete state)",
			len(top.partial[0].TimeEntries))
	}
	// Cursor must clamp: with 0 entries left, timeEntryCursor must be 0.
	if m.detail.timeEntryCursor != 0 {
		t.Errorf("timeEntryCursor after delete = %d, want 0 (clamped)", m.detail.timeEntryCursor)
	}

	// Undo must restore the deleted entry.
	m = sendKey(t, m, "u")
	restored := m.get(task.ID)
	if restored == nil || len(restored.TimeEntries) != 1 {
		t.Fatalf("after undo: entry not restored (task = %+v)", restored)
	}
}

func TestScriptDetailTimeEntryRunningEntryGuard(t *testing.T) {
	// A running timer entry must be protected from edit/delete in the detail
	// pane — the user must stop the timer first, just like the calendar.
	task := todo.New("Running timer")
	m := modelWithTasks(t, task)

	// Start the timer via 't' in list pane.
	m = sendKey(t, m, "t")
	if !m.get(task.ID).IsTimerRunning() {
		t.Fatal("timer not running after 't'")
	}

	m = navToTimeEntries(t, m)
	if m.detail.field != fieldTimeEntries {
		t.Fatalf("field = %v, want fieldTimeEntries", m.detail.field)
	}

	// 'r' on running entry must flash info, not open edit mode.
	initialStack := len(m.undoStack)
	m = sendKey(t, m, "r")
	if m.mode != modeNormal {
		t.Errorf("after 'r' on running entry: mode = %v, want modeNormal (guarded)", m.mode)
	}
	if len(m.undoStack) != initialStack {
		t.Error("guard must not push undo")
	}

	// 'x' on running entry must flash info, not open confirm.
	m = sendKey(t, m, "x")
	if m.mode != modeNormal {
		t.Errorf("after 'x' on running entry: mode = %v, want modeNormal (guarded)", m.mode)
	}
}

// TestScriptBoardMoveCardAcrossStages drives the Board tab through the real
// Update path: L advances the selected card a stage at a time, into Done as a
// full close (rank stamp included); H out of Done stages the reopen confirm,
// and y lands the card back in its stored stage.
func TestScriptBoardMoveCardAcrossStages(t *testing.T) {
	seed := todo.New("board rider")
	m := modelWithTasks(t, seed)
	m = script(t, m, "5")
	if m.tab != tabBoard {
		t.Fatalf("after '5': tab = %v, want tabBoard", m.tab)
	}

	m = script(t, m, "L")
	got := m.get(seed.ID)
	if got.Stage != "In progress" {
		t.Fatalf("after first L: stage = %q, want In progress", got.Stage)
	}
	if len(m.undoStack) != 1 {
		t.Fatalf("stage move pushed %d undo entries, want 1", len(m.undoStack))
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("stage move did not schedule a save")
	}
	if m.board.col != 1 {
		t.Errorf("board focus did not follow the card: col = %d, want 1", m.board.col)
	}

	m = script(t, m, "L") // → Review
	if got = m.get(seed.ID); got.Stage != "Review" {
		t.Fatalf("after second L: stage = %q, want Review", got.Stage)
	}

	m = script(t, m, "L") // Review → Done: the shared close path
	got = m.get(seed.ID)
	if got.Status != todo.Done {
		t.Fatalf("after third L: status = %v, want Done", got.Status)
	}
	if got.SeqRankAtDone == 0 {
		t.Error("board close did not capture the sequence rank")
	}
	if m.board.col != m.boardCfg.doneColumn() {
		t.Errorf("focus should follow into the Done column, col = %d", m.board.col)
	}

	m = script(t, m, "H")
	if m.mode != modeConfirm {
		t.Fatalf("H on a Done card should stage the reopen confirm, mode = %v", m.mode)
	}
	m = script(t, m, "y")
	got = m.get(seed.ID)
	if got.Status != todo.Pending {
		t.Fatalf("after confirm: status = %v, want Pending", got.Status)
	}
	if got.Stage != "Review" {
		t.Errorf("reopened card should keep its stored stage, got %q", got.Stage)
	}
	if m.board.col != m.boardCfg.stageIndex("Review") {
		t.Errorf("board cursor should follow the reopened card, col = %d", m.board.col)
	}
}

// TestScriptBoardDoneKeySharesCloseSemantics pins 'd' on the board to the
// Tasks-tab close path: a pending parent with open subtasks stages the
// confirm instead of silently closing.
func TestScriptBoardDoneKeySharesCloseSemantics(t *testing.T) {
	parent := todo.New("parent with open work")
	sub := todo.New("open subtask")
	sub.ParentID = parent.ID
	m := modelWithTasks(t, parent, sub)

	m = script(t, m, "5", "d")
	if m.mode != modeConfirm {
		t.Fatalf("d on a parent with open subtasks: mode = %v, want modeConfirm", m.mode)
	}
	m = script(t, m, "n")
	if got := m.get(parent.ID); got.Status != todo.Pending {
		t.Errorf("declined close still toggled the parent: %v", got.Status)
	}
}

// tab walks the tab bar forward; shift+tab must walk it back, from both panes.
// Before shift+tab existed, reaching the previous tab meant cycling all the way
// around — the registry now advertises it, so dispatch has to honour it.
func TestScriptShiftTabWalksTabsBackwards(t *testing.T) {
	m := modelWithTasks(t, todo.New("write the report"))

	m = sendKey(t, m, "shift+tab")
	if m.tab != tabMeetings {
		t.Fatalf("shift+tab from Tasks: tab = %v, want tabMeetings (wrap backwards)", m.tab)
	}
	m = sendKey(t, m, "tab")
	if m.tab != tabTasks {
		t.Fatalf("tab from Meetings: tab = %v, want tabTasks", m.tab)
	}

	// …and from inside the detail pane, where only forward tab used to work.
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail {
		t.Fatalf("enter on a task: pane = %v, want paneDetail", m.pane)
	}
	m = sendKey(t, m, "shift+tab")
	if m.tab != tabMeetings {
		t.Fatalf("shift+tab from the detail pane: tab = %v, want tabMeetings", m.tab)
	}
}

// The help advertises the digit shortcuts as global navigation. They only ever
// worked in the list pane, so pressing 5 with a task open did nothing at all.
func TestScriptNumberKeysSwitchTabsFromDetailPane(t *testing.T) {
	m := modelWithTasks(t, todo.New("write the report"))
	m = sendKey(t, m, "enter")
	if m.pane != paneDetail {
		t.Fatalf("enter on a task: pane = %v, want paneDetail", m.pane)
	}
	for key, want := range map[string]tab{
		"5": tabBoard,
		"2": tabCalendar,
		"7": tabSettings,
		"1": tabTasks,
	} {
		m = sendKey(t, m, key)
		if m.tab != want {
			t.Errorf("%q from the detail pane: tab = %v, want %v", key, m.tab, want)
		}
	}
}

// restoreStages registers a cleanup that puts the persisted stage list back
// the way it was — the Settings editor writes settings.json, which
// initialModel reads back in every later test.
func restoreStages(t *testing.T) {
	t.Helper()
	before, _ := loadSettings()
	prev, prevIcons := before.Stages, before.StageIcons
	t.Cleanup(func() {
		if s, err := loadSettings(); err == nil {
			s.Stages, s.StageIcons = prev, prevIcons
			_ = saveSettings(s)
		}
	})
}

// settingsCursorTo walks the Settings cursor to a row with real key presses.
// The traversal order is Preferences then Sequencer and the ends clamp, so it
// tries both directions and gives up rather than looping forever.
func settingsCursorTo(t *testing.T, m model, row int) model {
	t.Helper()
	for _, key := range []string{"up", "down"} {
		for i := 0; i < numSettingsRows; i++ {
			if m.settingsCursor == row {
				return m
			}
			m = sendKey(t, m, key)
		}
	}
	if m.settingsCursor != row {
		t.Fatalf("could not reach settings row %d, stuck on %d", row, m.settingsCursor)
	}
	return m
}

// Editing the board columns in Settings: enter opens the inline editor, enter
// again applies the list, persists it, and carries the cards of a renamed
// column across so the board keeps its layout. Undo puts the cards back.
func TestScriptEditBoardColumnsFromSettings(t *testing.T) {
	card := todo.New("ship the release")
	card.Stage = "Review"
	m := modelWithTasks(t, card)
	// Put the file back afterwards, or the edited list leaks into every model
	// a later test builds.
	restoreStages(t)
	m.boardCfg.setStages([]string{"Backlog", "In progress", "Review", "Done"})
	m.markCacheDirty()

	m = sendKey(t, m, "7")
	m = settingsCursorTo(t, m, settingStages)
	m = sendKey(t, m, "enter")
	if m.mode != modeEditStages {
		t.Fatalf("enter on the Board columns row: mode = %v, want modeEditStages", m.mode)
	}
	if got := m.textInput.Value(); got != "Backlog, In progress, Review, Done" {
		t.Fatalf("editor pre-fill = %q, want the current list", got)
	}

	m.textInput.SetValue("Backlog, In progress, QA, Done")
	m = sendKey(t, m, "enter")

	if m.mode != modeNormal {
		t.Fatalf("after applying: mode = %v, want modeNormal", m.mode)
	}
	if want := []string{"Backlog", "In progress", "QA", "Done"}; !reflect.DeepEqual(m.boardCfg.stages, want) {
		t.Fatalf("stages = %v, want %v", m.boardCfg.stages, want)
	}
	if got := m.get(card.ID); got == nil || got.Stage != "QA" {
		t.Fatalf("renamed column: card stage = %v, want QA (cards follow the rename)", got)
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("moving cards between stages did not schedule a save")
	}

	// The edit is its own inverse: renaming the column back carries the same
	// cards back with it. (It is deliberately not on the undo stack — see
	// applyStageEdit.)
	m = settingsCursorTo(t, m, settingStages)
	m = sendKey(t, m, "enter")
	m.textInput.SetValue("Backlog, In progress, Review, Done")
	m = sendKey(t, m, "enter")
	if got := m.get(card.ID); got == nil || got.Stage != "Review" {
		t.Fatalf("after renaming the column back: card stage = %v, want Review", got)
	}
}

// esc leaves the stage editor without touching the list — the same escape
// hatch every other inline Settings editor gives.
func TestScriptEditBoardColumnsEscapeKeepsList(t *testing.T) {
	m := modelWithTasks(t, todo.New("ship the release"))
	restoreStages(t)
	m.boardCfg.setStages([]string{"Backlog", "In progress", "Review", "Done"})
	m.markCacheDirty()
	m = sendKey(t, m, "7")
	m = settingsCursorTo(t, m, settingStages)
	m = sendKey(t, m, "enter")
	m.textInput.SetValue("Nope")
	m = sendKey(t, m, "esc")

	if m.mode != modeNormal {
		t.Fatalf("esc: mode = %v, want modeNormal", m.mode)
	}
	if want := []string{"Backlog", "In progress", "Review", "Done"}; !reflect.DeepEqual(m.boardCfg.stages, want) {
		t.Errorf("stages = %v, want the list unchanged %v", m.boardCfg.stages, want)
	}
}

// Quick-add completion, driven the way a user would: type '#', see the
// existing tags, tab to splice one in, enter to file the task. The tag on the
// created task is what proves the completion reached the parser, not just the
// footer.
func TestScriptQuickAddCompletesTag(t *testing.T) {
	existing := todo.New("Fix boiler")
	existing.AddTag("home")
	existing.AddTag("house-stuff")
	m := modelWithTasks(t, existing)

	m = sendKey(t, m, "a")
	m = script(t, m, "buy milk #")
	sigil, matches := m.completionMatches()
	if sigil != "#" || len(matches) == 0 {
		t.Fatalf("after typing '#': sigil %q, matches %v — want the existing tags offered", sigil, matches)
	}

	m = sendKey(t, m, "tab")
	if got, want := m.textInput.Value(), "buy milk #"+matches[0]+" "; got != want {
		t.Fatalf("after tab: input = %q, want %q", got, want)
	}
	m = sendKey(t, m, "enter")

	created := m.currentTodo()
	if created == nil || created.Title != "Buy milk" {
		t.Fatalf("created = %+v, want the completed task", created)
	}
	if len(created.Tags) != 1 || created.Tags[0] != matches[0] {
		t.Errorf("tags = %v, want the completed tag %q", created.Tags, matches[0])
	}
}

// ↑/↓ pick a different chip before tab accepts it, and both keys keep their
// normal meaning once the completion row is gone.
func TestScriptQuickAddPicksSuggestionWithArrows(t *testing.T) {
	existing := todo.New("Fix boiler")
	existing.AddTag("home")
	existing.AddTag("house-stuff")
	m := modelWithTasks(t, existing)

	m = sendKey(t, m, "a")
	m = script(t, m, "buy milk #h")
	_, matches := m.completionMatches()
	if len(matches) < 2 {
		t.Fatalf("matches = %v, want at least two to pick between", matches)
	}
	m = sendKey(t, m, "down")
	if m.suggestCursor != 1 {
		t.Fatalf("after ↓: suggestCursor = %d, want 1", m.suggestCursor)
	}
	m = sendKey(t, m, "tab")
	if got, want := m.textInput.Value(), "buy milk #"+matches[1]+" "; got != want {
		t.Errorf("after ↓ then tab: input = %q, want the second chip %q", got, want)
	}
	// The completed token ends the row, so the highlight is back at the top.
	if m.suggestCursor != 0 {
		t.Errorf("suggestCursor = %d, want it reset after accepting", m.suggestCursor)
	}
}

// Typing past the token retires the completion row, and tab must then do
// nothing rather than splice a stale suggestion into the middle of the title.
func TestScriptQuickAddTabIsInertWithoutSuggestions(t *testing.T) {
	m := modelWithTasks(t, todo.New("Fix boiler"))
	m = sendKey(t, m, "a")
	m = script(t, m, "buy milk")
	if _, matches := m.completionMatches(); len(matches) != 0 {
		t.Fatalf("plain title offered completions: %v", matches)
	}
	m = sendKey(t, m, "tab")
	if got := m.textInput.Value(); got != "buy milk" {
		t.Errorf("tab with no completion row changed the input to %q", got)
	}
}

// The Tags tab used to be read-only: enter left for the Tasks tab and the task
// list under the cursor was a picture. Now enter drills into it and the
// row-level keys act on the task you can see highlighted.
func TestScriptTagDrillActsOnTasks(t *testing.T) {
	a := todo.New("Alpha")
	a.AddTag("home")
	a.Priority = todo.PriorityHigh // ranks first
	b := todo.New("Beta")
	b.AddTag("home")
	m := modelWithTasks(t, a, b)
	m.tab = tabTags
	// With finished work shown the completed task stays in the list, so the
	// cursor has somewhere to follow it to.
	m.showFinishedGroups = true

	m = sendKey(t, m, "enter")
	if !m.tagTaskMode {
		t.Fatal("enter on a tag did not drill into its tasks")
	}
	if got := m.currentTodo(); got == nil || got.Title != "Alpha" {
		t.Fatalf("cursor landed on %v, want the first task", got)
	}

	// d completes the task under the cursor, and the cursor stays on it even
	// though completing re-sorts the list (done sinks to the bottom).
	m = sendKey(t, m, "d")
	if got := m.get(a.ID); got == nil || got.Status != todo.Done {
		t.Fatalf("d in the drill did not complete the task: %v", got)
	}
	if got := m.currentTodo(); got == nil || got.ID != a.ID {
		t.Errorf("cursor jumped to %v after the re-sort, want it to follow the task", got)
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("completing from the drill did not schedule a save")
	}

	// p cycles priority on the same row.
	m = sendKey(t, m, "p")
	if got := m.get(a.ID); got.Priority == todo.PriorityHigh {
		t.Error("p in the drill did not change the priority")
	}

	// esc backs out one level, to the tag list.
	m = sendKey(t, m, "esc")
	if m.tagTaskMode {
		t.Error("esc did not leave the drill")
	}
	if m.tab != tabTags {
		t.Errorf("esc left the tab entirely: %v", m.tab)
	}
}

// enter twice walks tag → task → detail, and the pane shows that task rather
// than the tag summary it was showing a keystroke earlier.
func TestScriptTagDrillOpensTaskDetail(t *testing.T) {
	a := todo.New("Alpha")
	a.AddTag("home")
	m := modelWithTasks(t, a)
	m.tab = tabTags

	m = script(t, m, "enter", "enter")
	if m.pane != paneDetail || m.detailTaskID != a.ID {
		t.Fatalf("enter in the drill: pane %v, detailTaskID %q, want the task's detail", m.pane, m.detailTaskID)
	}
	if got := m.detailPanelTitle(); got != "Alpha" {
		t.Errorf("detail panel title = %q, want the task title", got)
	}
	// The tag summary always carries an "N active · N done · N overdue" line;
	// the task detail never does.
	if body := m.buildDetailContent(); strings.Contains(body, "active ·") {
		t.Error("the detail pane is still rendering the tag summary")
	}
	// esc unwinds one level at a time: detail → drill → tag list.
	m = sendKey(t, m, "esc")
	if m.pane == paneDetail || !m.tagTaskMode {
		t.Fatalf("first esc: pane %v, drill %v — want back in the drill", m.pane, m.tagTaskMode)
	}
	m = sendKey(t, m, "esc")
	if m.tagTaskMode {
		t.Error("second esc did not return to the tag list")
	}
}

// 'a' on a tag row opens quick-add already carrying the tag, so capturing into
// the tag you are looking at costs one key.
func TestScriptTagAddSeedsTheTag(t *testing.T) {
	a := todo.New("Alpha")
	a.AddTag("home")
	m := modelWithTasks(t, a)
	m.tab = tabTags

	m = sendKey(t, m, "a")
	if m.mode != modeInput {
		t.Fatalf("'a' on the Tags tab: mode = %v, want modeInput", m.mode)
	}
	if got := m.textInput.Value(); got != "#home " {
		t.Fatalf("quick-add seed = %q, want %q", got, "#home ")
	}
	m = script(t, m, "paint the shed", "enter")

	var created *todo.Todo
	for _, task := range m.tasks {
		if task.Title == "Paint the shed" {
			created = task
		}
	}
	if created == nil {
		t.Fatal("the seeded quick-add did not create a task")
	}
	if len(created.Tags) != 1 || created.Tags[0] != "home" {
		t.Errorf("tags = %v, want the seeded tag", created.Tags)
	}
}

// f keeps the old enter behaviour — the tag as a filter on the Tasks tab.
func TestScriptTagFilterJumpsToTasks(t *testing.T) {
	a := todo.New("Alpha")
	a.AddTag("home")
	m := modelWithTasks(t, a)
	m.tab = tabTags

	m = sendKey(t, m, "f")
	if m.tab != tabTasks {
		t.Fatalf("f on a tag: tab = %v, want tabTasks", m.tab)
	}
	if m.searchQuery != "#home" {
		t.Errorf("searchQuery = %q, want the tag filter", m.searchQuery)
	}
}

// The Projects drill could only ever open a detail; the task keys were dead
// there. They now match the Tags drill.
func TestScriptProjectDrillActsOnTasks(t *testing.T) {
	a := todo.New("Alpha")
	a.Project = "House"
	m := modelWithTasks(t, a)
	m.tab = tabProjects

	m = sendKey(t, m, "enter")
	if !m.projectTaskMode {
		t.Fatal("enter did not drill into the project")
	}
	m = sendKey(t, m, "d")
	if got := m.get(a.ID); got == nil || got.Status != todo.Done {
		t.Fatalf("d in the project drill did not complete the task: %v", got)
	}
}

// x on a project row was advertised in the help but wired to nothing. It now
// clears the project off its tasks, leaving the tasks themselves alone.
func TestScriptProjectDeleteClearsGrouping(t *testing.T) {
	a := todo.New("Alpha")
	a.Project = "House"
	m := modelWithTasks(t, a)
	m.tab = tabProjects

	m = sendKey(t, m, "x")
	if m.mode != modeConfirm {
		t.Fatalf("x on a project row: mode = %v, want a confirm prompt", m.mode)
	}
	m = sendKey(t, m, "y")
	if got := m.get(a.ID); got == nil {
		t.Fatal("deleting the project deleted the task")
	} else if got.Project != "" {
		t.Errorf("project = %q, want it cleared", got.Project)
	}
	m = sendKey(t, m, "u")
	if got := m.get(a.ID); got == nil || got.Project != "House" {
		t.Errorf("after undo: project = %v, want it restored", got)
	}
}

// Completing a task in the Projects drill must leave the cursor on it, and the
// next key must still reach it. (The resolution path reads the project list
// through its accessor rather than the raw cache field refreshCaches
// invalidates; updateList's own tail happens to re-warm that cache, so this
// asserts the behaviour, not the ordering.)
func TestScriptProjectDrillKeepsCursorAfterMutation(t *testing.T) {
	// Stamp CreatedAt explicitly. The drill list sorts by start date, then
	// CreatedAt, then ID — and two tasks created in the same clock tick fall
	// through to the ID, which is a random UUID. Linux's nanosecond clock
	// separates them; Windows' is coarse enough that it sometimes doesn't, so
	// "the second task I made" is only a stable position if the test says so.
	base := time.Now().Add(-time.Hour)
	first := todo.New("Alpha")
	first.Project = "House"
	first.CreatedAt = base
	second := todo.New("Beta")
	second.Project = "House"
	second.CreatedAt = base.Add(time.Minute)
	second.Priority = todo.PriorityLow // ranks below Alpha
	m := modelWithTasks(t, first, second)
	m.tab = tabProjects
	m.showFinishedGroups = true // the completed task stays listed

	m = script(t, m, "enter", "down")
	target := m.currentTodo()
	if target == nil || target.Title != "Beta" {
		t.Fatalf("cursor on %v, want Beta", target)
	}

	m = sendKey(t, m, "d")
	if got := m.get(second.ID); got == nil || got.Status != todo.Done {
		t.Fatalf("d did not complete the task under the cursor: %v", got)
	}
	if got := m.currentTodo(); got == nil || got.ID != second.ID {
		t.Fatalf("cursor moved to %v after the mutation, want it to stay on Beta", got)
	}
	// …and the next key still lands on that task rather than on nothing.
	m = sendKey(t, m, "p")
	if got := m.get(second.ID); got.Priority == todo.PriorityLow {
		t.Error("the follow-up key did not reach the task under the cursor")
	}
}

// While finished work is hidden, a task completed inside a group folds away
// like one completed on the Tasks tab, and the cursor lands on an open task.
// The group itself stays put even when that was its last open task: it is the
// one the cursor is inside, and it leaves the list only once you back out.
func TestScriptDrillFoldsCompletedTaskAndKeepsTheGroup(t *testing.T) {
	a := todo.New("Alpha")
	a.Project = "Garden"
	a.Priority = todo.PriorityHigh
	b := todo.New("Beta")
	b.Project = "Garden"
	other := todo.New("Other")
	other.Project = "Attic"
	other.Priority = todo.PriorityHigh
	other.DueDate = time.Now().Add(-48 * time.Hour) // Attic sorts first by nothing but its name
	m := modelWithTasks(t, a, b, other)
	m.tab = tabProjects
	m.projectOrder = groupSortName

	m = script(t, m, "down", "enter") // Garden
	if got := m.currentTodo(); got == nil || got.ID != a.ID {
		t.Fatalf("cursor on %v, want Alpha", got)
	}
	m = sendKey(t, m, "d")
	if got := m.currentTodo(); got == nil || got.ID != b.ID {
		t.Fatalf("after completing Alpha the cursor is on %v, want Beta", got)
	}
	m = sendKey(t, m, "d") // Garden's last open task
	projects := m.allProjectsForList()
	if m.projectCursor >= len(projects) || projects[m.projectCursor] != "Garden" {
		t.Fatalf("drilled into %v, want to stay inside Garden", projects)
	}
	m = sendKey(t, m, "u") // undo reopens Beta; still Garden
	if projects := m.allProjectsForList(); projects[m.projectCursor] != "Garden" {
		t.Fatalf("after undo the drill is on %q, want Garden", projects[m.projectCursor])
	}
	m = script(t, m, "d", "esc")
	if got := m.allProjectsForList(); len(got) != 1 || got[0] != "Attic" {
		t.Errorf("after backing out, projects = %v, want the finished Garden hidden", got)
	}
	m = sendKey(t, m, "h")
	if got := m.allProjectsForList(); len(got) != 2 {
		t.Errorf("h should show the finished project again, got %v", got)
	}
}

// s re-orders the list and h hides or shows the finished groups, and neither
// moves the cursor off the group it was on.
func TestScriptGroupListKeysKeepTheSelection(t *testing.T) {
	busy1 := todo.New("one")
	busy1.AddTag("zeta")
	busy2 := todo.New("two")
	busy2.AddTag("zeta")
	quiet := todo.New("three")
	quiet.AddTag("alpha")
	done := todo.New("four")
	done.AddTag("beta")
	done.Status = todo.Done
	m := modelWithTasks(t, busy1, busy2, quiet, done)
	m.tab = tabTags

	if tags := m.getFilteredTagsForTab(); len(tags) != 2 || tags[0] != "zeta" {
		t.Fatalf("by open work: %v, want zeta (2 open) first and beta hidden", tags)
	}
	m = sendKey(t, m, "down") // alpha
	for _, key := range []string{"s", "s", "h", "s", "h"} {
		m = sendKey(t, m, key)
		if tags := m.getFilteredTagsForTab(); tags[m.tagTabCursor] != "alpha" {
			t.Fatalf("after %q the cursor is on %q, want alpha (list %v)", key, tags[m.tagTabCursor], tags)
		}
	}
}

// f on a project row opens the Tasks tab filtered to that project, as f on a
// tag row does for the tag.
func TestScriptProjectFilterJumpsToTasks(t *testing.T) {
	in := todo.New("inside")
	in.Project = "House"
	out := todo.New("outside")
	m := modelWithTasks(t, in, out)
	m.tab = tabProjects

	m = sendKey(t, m, "f")
	if m.tab != tabTasks || m.searchQuery != "@House" {
		t.Fatalf("tab %v query %q, want the Tasks tab filtered to @House", m.tab, m.searchQuery)
	}
	m.ensureCache()
	if n := m.visibleActiveLen(); n != 1 {
		t.Errorf("filtered list has %d tasks, want 1", n)
	}
	m = sendKey(t, m, "4")
	if got := m.allProjectsForList(); len(got) != 1 || got[0] != "House" {
		t.Errorf("the @House query should still find House on the Projects tab, got %v", got)
	}
}

// Enter picks a card up, the arrows carry it, and enter or esc puts it down.
// Nothing is stored while it is held — carrying it over Done completes
// nothing — and the trip is one undo step however far it went.
func TestScriptBoardCarryCard(t *testing.T) {
	seed := todo.New("carried card")
	m := modelWithTasks(t, seed)
	m = script(t, m, "5", "enter")
	if m.mode != modeBoardCarry {
		t.Fatalf("enter on a card: mode = %v, want modeBoardCarry", m.mode)
	}

	m = script(t, m, "right", "right", "right") // over Done
	if got := m.get(seed.ID); got.Stage != "" || got.Status != todo.Pending {
		t.Fatalf("carrying changed the task before it was put down: stage %q status %v", got.Stage, got.Status)
	}
	if !strings.Contains(ansi.Strip(m.View()), "✓ Carried card") {
		t.Errorf("a card held over Done should preview the done look:\n%s", ansi.Strip(m.View()))
	}
	if len(m.undoStack) != 0 {
		t.Fatalf("carrying pushed %d undo entries, want 0", len(m.undoStack))
	}

	m = script(t, m, "left", "esc") // back to Review, put down
	if m.mode != modeNormal {
		t.Fatalf("esc should put the card down, mode = %v", m.mode)
	}
	if got := m.get(seed.ID); got.Stage != "Review" || got.Status != todo.Pending {
		t.Fatalf("after dropping on Review: stage %q status %v", got.Stage, got.Status)
	}
	if len(m.undoStack) != 1 {
		t.Fatalf("the trip pushed %d undo entries, want 1", len(m.undoStack))
	}
	if !m.savePending && !m.saveScheduled {
		t.Error("putting the card down did not schedule a save")
	}
	if sel := m.boardSelectedTask(); m.board.col != 2 || sel == nil || sel.ID != seed.ID {
		t.Errorf("focus should follow the card: col %d, selected %v", m.board.col, sel)
	}

	m = script(t, m, "enter", "enter") // pick up and put straight back
	if len(m.undoStack) != 1 || m.mode != modeNormal {
		t.Fatalf("a round trip to the same column should change nothing: %d undo, mode %v", len(m.undoStack), m.mode)
	}

	m = script(t, m, "enter", "right", "enter") // into Done: the shared close path
	if got := m.get(seed.ID); got.Status != todo.Done || got.SeqRankAtDone == 0 {
		t.Fatalf("dropping in Done should close the task: status %v rank %d", got.Status, got.SeqRankAtDone)
	}

	m = script(t, m, "enter")
	if m.mode != modeConfirm {
		t.Fatalf("enter on a done card should ask to reopen it, mode = %v", m.mode)
	}
}

// Icons go in through the same editor: "[R] Review" marks the column, the
// task list's box shows the mark, and it survives a restart. An emoji keeps
// the field open with the text as typed, so the one bad icon can be fixed.
func TestScriptBoardColumnIconsFromSettings(t *testing.T) {
	card := todo.New("Ship the release")
	card.Stage = "Review"
	m := modelWithTasks(t, card)
	restoreStages(t)
	m.boardCfg.setStages([]string{"Backlog", "In progress", "Review", "Done"})
	m.markCacheDirty()

	m = sendKey(t, m, "7")
	m = settingsCursorTo(t, m, settingStages)
	m = sendKey(t, m, "enter")
	m.textInput.SetValue("Backlog, [>] In progress, [🚀] Review, Done")
	m = sendKey(t, m, "enter")
	if m.mode != modeEditStages || !strings.Contains(m.err, "🚀") {
		t.Fatalf("a wide icon should keep the editor open and name it: mode %v, flash %q", m.mode, m.err)
	}
	if m.textInput.Value() != "Backlog, [>] In progress, [🚀] Review, Done" {
		t.Errorf("the text should stay as typed, got %q", m.textInput.Value())
	}

	m.textInput.SetValue("Backlog, [>] In progress, [R] Review, Done")
	m = sendKey(t, m, "enter")
	if m.mode != modeNormal {
		t.Fatalf("after applying: mode = %v", m.mode)
	}
	if got := m.boardCfg.statusBox(m.get(card.ID)); got != "[R]" {
		t.Errorf("the card's box = %q, want its column's [R]", got)
	}
	m = sendKey(t, m, "1")
	if row := ansi.Strip(m.View()); !strings.Contains(row, "[R] Ship the release") {
		t.Errorf("the task list should show the column icon:\n%s", row)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.StageIcons["review"] != "R" || s.StageIcons["in progress"] != ">" {
		t.Errorf("settings.json kept icons %v", s.StageIcons)
	}
	if again := boardConfigFromSettings(s); again.columnIcon(2) != "R" {
		t.Errorf("icons read back from settings.json = %v", again.icons)
	}
}

// → walks every section of the detail pane in document order, one press each,
// and the bar at the top of the pane lights the one the cursor is in.
func TestScriptDetailSectionsInOrder(t *testing.T) {
	m := modelWithTasks(t, todo.New("Sectioned task"))
	m.termWidth, m.termHeight = 160, 40
	m = script(t, m, "enter")
	for i, s := range detailSections {
		if i > 0 {
			m = script(t, m, "right")
		}
		if got := detailSectionOf(m.detail.field); got != i {
			t.Fatalf("after %d presses of →: in section %d, want %d (%s)", i, got, i, s.label)
		}
		bar := m.detailSectionBar(200)
		if lit := headerStyle.Render(tr(s.label)); !strings.Contains(bar, lit) {
			t.Errorf("section %s: the bar does not light it: %q", s.label, bar)
		}
	}
	m = script(t, m, "right")
	if got := detailSectionOf(m.detail.field); got != len(detailSections)-1 {
		t.Errorf("→ past the last section should stay on it, got section %d", got)
	}
	for i := len(detailSections) - 2; i >= 0; i-- {
		m = script(t, m, "left")
		if got := detailSectionOf(m.detail.field); got != i {
			t.Fatalf("← back: in section %d, want %d", got, i)
		}
	}
}

// A narrow pane keeps the current section in the bar and shows that there is
// more with an ellipsis, inside the width it is given.
func TestDetailSectionBarFitsANarrowPane(t *testing.T) {
	m := modelWithTasks(t, todo.New("Narrow"))
	m = script(t, m, "enter", "right", "right", "right")
	for _, w := range []int{60, 30, 20, 12} {
		bar := ansi.Strip(m.detailSectionBar(w))
		if got := ansi.StringWidth(bar); got > w {
			t.Errorf("width %d: bar is %d wide: %q", w, got, bar)
		}
		if w >= 20 && !strings.Contains(bar, tr("Dependencies")) {
			t.Errorf("width %d: the current section should stay in the bar: %q", w, bar)
		}
	}
}
