package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Behaviour that nothing reached: the calendar's time-entry delete, the
// detail-pane subtask toggle, and the single-timer rule. All three carry
// semantics that only show up when they are wrong — a stranded running timer,
// a recurrence that does not respawn, an ancestor that never auto-closes.

func TestConfirmDeleteTimeEntryFromCalendarClampsTheCursor(t *testing.T) {
	task := todo.New("Tracked twice")
	day := time.Now()
	task.AddTimeEntry(day.Add(-3*time.Hour), day.Add(-2*time.Hour))
	task.AddTimeEntry(day.Add(-90*time.Minute), day.Add(-time.Hour))
	m := modelWithTasks(t, task)

	live := m.get(task.ID)
	m.calendar.selected = day
	m.calendar.focusTimeline = true
	m.calendar.entryCursor = 1
	m.pendingEntryTaskID = task.ID
	m.pendingEntryID = live.TimeEntries[1].ID

	m.confirmDeleteTimeEntry()

	if got := len(m.get(task.ID).TimeEntries); got != 1 {
		t.Fatalf("got %d entries, want 1", got)
	}
	// The cursor sat on the row that just disappeared. Left where it was it
	// would select nothing, and the next keystroke would silently no-op.
	if m.calendar.entryCursor != 0 {
		t.Errorf("entryCursor = %d, want it pulled back to 0", m.calendar.entryCursor)
	}
}

func TestConfirmDeleteTimeEntryLeavesTheTimelineWhenTheDayEmpties(t *testing.T) {
	task := todo.New("Only entry of the day")
	day := time.Now()
	task.AddTimeEntry(day.Add(-2*time.Hour), day.Add(-time.Hour))
	m := modelWithTasks(t, task)

	live := m.get(task.ID)
	m.calendar.selected = day
	m.calendar.focusTimeline = true
	m.pendingEntryTaskID = task.ID
	m.pendingEntryID = live.TimeEntries[0].ID

	m.confirmDeleteTimeEntry()

	// With nothing left to point at, focus has to go back to the month grid
	// — staying in the timeline strands the user in an empty pane.
	if m.calendar.focusTimeline {
		t.Error("focus stayed in an empty timeline")
	}
	if m.calendar.entryCursor != 0 {
		t.Errorf("entryCursor = %d, want 0", m.calendar.entryCursor)
	}
}

func TestConfirmDeleteTimeEntryIsUndoable(t *testing.T) {
	task := todo.New("Undo the delete")
	day := time.Now()
	task.AddTimeEntry(day.Add(-2*time.Hour), day.Add(-time.Hour))
	m := modelWithTasks(t, task)

	live := m.get(task.ID)
	m.calendar.selected = day
	m.pendingEntryTaskID = task.ID
	m.pendingEntryID = live.TimeEntries[0].ID
	m.confirmDeleteTimeEntry()

	if len(m.get(task.ID).TimeEntries) != 0 {
		t.Fatal("the entry was not deleted")
	}
	m.performUndo()
	if got := len(m.get(task.ID).TimeEntries); got != 1 {
		t.Errorf("after undo: %d entries, want the deleted one back", got)
	}
}

func TestToggleSubtaskStopsItsRunningTimer(t *testing.T) {
	parent := todo.New("Parent")
	sub := todo.NewSubtask("Child", parent.ID)
	m := modelWithTasks(t, parent, sub)
	m.get(sub.ID).StartTimer()

	m.toggleSubtask(parent.ID, 0)

	got := m.get(sub.ID)
	if got.Status != todo.Done {
		t.Fatalf("status = %v, want Done", got.Status)
	}
	// Closing a task with the clock running would otherwise leave an entry
	// with no stop time, which the runaway-timer guard later reports as a
	// session left open for however long the task has been done.
	if got.IsTimerRunning() {
		t.Error("closing the subtask left its timer running")
	}
	if _, running := m.runningTimers[sub.ID]; running {
		t.Error("the store's runningTimers index still holds the closed subtask")
	}
}

func TestToggleSubtaskRespawnsARecurrence(t *testing.T) {
	parent := todo.New("Parent")
	sub := todo.NewSubtask("Water the plants", parent.ID)
	sub.SetRecurrence("weekly")
	m := modelWithTasks(t, parent, sub)

	touched := m.toggleSubtask(parent.ID, 0)

	// A recurring subtask must produce its successor, and the caller has to
	// be told about it — the returned IDs are what markModified persists,
	// so a missing ID means the new task exists only in memory.
	if len(touched) < 2 {
		t.Fatalf("toggleSubtask returned %v, want the closed subtask and its successor", touched)
	}
	var successors int
	for _, id := range touched {
		if id == sub.ID {
			continue
		}
		if next := m.get(id); next != nil && next.Title == "Water the plants" && next.Status == todo.Pending {
			successors++
		}
	}
	if successors != 1 {
		t.Errorf("found %d pending successors, want 1", successors)
	}
}

func TestToggleSubtaskIgnoresAnOutOfRangeCursor(t *testing.T) {
	parent := todo.New("Parent")
	m := modelWithTasks(t, parent)
	// A parent with no subtasks, addressed by a cursor left over from a
	// longer list. It must no-op rather than index past the end.
	if got := m.toggleSubtask(parent.ID, 3); got != nil {
		t.Errorf("toggleSubtask returned %v, want nil", got)
	}
}

func TestStopOtherRunningTimersEnforcesOneTrackedTask(t *testing.T) {
	a, b, c := todo.New("A"), todo.New("B"), todo.New("C")
	a.StartTimer()
	b.StartTimer()
	todos := []todo.Todo{a, b, c}

	stopped := stopOtherRunningTimers(todos, c.ID)

	if len(stopped) != 2 {
		t.Fatalf("stopped %d timers, want 2", len(stopped))
	}
	for i := range todos {
		if todos[i].IsTimerRunning() {
			t.Errorf("%s is still running", todos[i].Title)
		}
	}
	// The returned pointers must alias the slice, not copies — callers mark
	// exactly these tasks modified, and a copy means the stop never
	// persists.
	for _, p := range stopped {
		if p.ID != todos[0].ID && p.ID != todos[1].ID {
			t.Errorf("unexpected task %q in the stopped set", p.ID)
		}
	}
}

func TestStopOtherRunningTimersSparesTheException(t *testing.T) {
	a, b := todo.New("Keep running"), todo.New("Stop me")
	a.StartTimer()
	b.StartTimer()
	todos := []todo.Todo{a, b}

	stopped := stopOtherRunningTimers(todos, a.ID)

	if len(stopped) != 1 || stopped[0].ID != b.ID {
		t.Fatalf("stopped = %v, want just the second task", stopped)
	}
	if !todos[0].IsTimerRunning() {
		t.Error("the excepted task's timer was stopped")
	}
}
