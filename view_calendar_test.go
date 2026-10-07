package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// TestRenderTimelineSubSkipsBareEntries asserts the sub-line is skipped when
// the entry has neither project nor tags (so bare entries stay 1-line), and
// that the project + tags appear in that order otherwise.
func TestRenderTimelineSubSkipsBareEntries(t *testing.T) {
	m := newTestModel()

	bare := dayActivity{title: "bare"}
	if got := m.renderTimelineSub(bare, 80, false); got != "" {
		t.Errorf("bare entry: want empty sub line, got %q", got)
	}

	withProj := dayActivity{title: "x", project: "alpha"}
	if got := m.renderTimelineSub(withProj, 80, false); !strings.Contains(got, "[alpha]") {
		t.Errorf("project-only: missing [alpha] in %q", got)
	}

	withTags := dayActivity{title: "x", tags: []string{"a", "b"}}
	if got := m.renderTimelineSub(withTags, 80, false); !strings.Contains(got, "#a") || !strings.Contains(got, "#b") {
		t.Errorf("tag-only: missing tags in %q", got)
	}

	both := dayActivity{title: "x", project: "alpha", tags: []string{"a"}}
	got := m.renderTimelineSub(both, 80, false)
	pIdx := strings.Index(got, "[alpha]")
	tIdx := strings.Index(got, "#a")
	if pIdx < 0 || tIdx < 0 || pIdx > tIdx {
		t.Errorf("project must precede tags: pIdx=%d tIdx=%d in %q", pIdx, tIdx, got)
	}
}

// TestRenderTimelineSubDropsTagsOnNarrow asserts the sub-line drops tags
// before project when the combined width would overflow innerW. This keeps
// the no-wrap contract from the renderTimelineLines panel honest.
func TestRenderTimelineSubDropsTagsOnNarrow(t *testing.T) {
	m := newTestModel()
	long := dayActivity{
		title:   "x",
		project: "shorty",
		tags:    []string{"a-very-long-tag-name-that-eats-the-width"},
	}
	got := m.renderTimelineSub(long, 16, false) // 16 - 4 indent = 12 avail
	if !strings.Contains(got, "[shorty]") {
		t.Errorf("project should survive narrow width: %q", got)
	}
	if strings.Contains(got, "a-very-long-tag-name") {
		t.Errorf("tag should be dropped when it doesn't fit: %q", got)
	}
}

// TestRenderTimelineSubShowsParent asserts a subtask activity renders a
// "↳ parent" reference even when it has no project or tags (so a done subtask's
// short title gets parent context in the day timeline), and that the parent ref
// is truncated rather than overflowing a narrow panel.
func TestRenderTimelineSubShowsParent(t *testing.T) {
	m := newTestModel()

	sub := dayActivity{title: "fix", completed: true, parentTitle: "Big parent task"}
	got := m.renderTimelineSub(sub, 80, false)
	if !strings.Contains(got, "↳ Big parent task") {
		t.Errorf("want parent reference in sub line, got %q", got)
	}

	// Parent reference must not overflow the inner width once styled
	// (indent + truncated content stays within innerW).
	narrow := dayActivity{title: "fix", parentTitle: "A parent title that is far too long to fit"}
	got = m.renderTimelineSub(narrow, 16, false)
	if w := ansi.StringWidth(got); w > 16 {
		t.Errorf("parent ref overflowed innerW=16: width=%d %q", w, got)
	}
}

// localMidnight returns midnight of today shifted by deltaDays, matching how
// due dates are stored (local midnight, date-only).
func localMidnight(deltaDays int) time.Time {
	now := time.Now()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return d.AddDate(0, 0, deltaDays)
}

// TestActivitiesForDaySurfacesDueTasks asserts an unfinished task with a future
// DueDate shows up as a "due" activity on its due day, and that a completed task
// with the same due date does not (its deadline is moot).
func TestActivitiesForDaySurfacesDueTasks(t *testing.T) {
	m := newTestModel()
	day := localMidnight(5)

	pending := mkTodo("p", "ship release", todo.Pending)
	pending.DueDate = day
	done := mkTodo("d", "already shipped", todo.Done)
	done.DueDate = day
	m.add(pending)
	m.add(done)
	m.refreshCaches()

	acts := m.activitiesForDay(day)
	if len(acts) != 1 {
		t.Fatalf("activitiesForDay = %d activities, want 1 (done task must not surface)", len(acts))
	}
	if a := acts[0]; !a.due || a.taskID != "p" {
		t.Fatalf("got %+v, want due event for task p", a)
	}

	line := ansi.Strip(m.renderTimelineEntry(acts[0], 0, 80))
	if !strings.Contains(line, "⧗ due") || !strings.Contains(line, "Ship release") {
		t.Fatalf("timeline entry = %q, want due marker + title", line)
	}
}

// TestRenderTimelineEntryOverdue asserts a due event whose day is already past
// renders as "⧗ overdue" rather than "⧗ due".
func TestRenderTimelineEntryOverdue(t *testing.T) {
	m := newTestModel()
	past := localMidnight(-3)
	a := dayActivity{title: "file taxes", start: past, stop: past, due: true}

	line := ansi.Strip(m.renderTimelineEntry(a, 0, 80))
	if !strings.Contains(line, "⧗ overdue") {
		t.Fatalf("timeline entry = %q, want overdue marker", line)
	}
}

// TestTimelineTicksTrackedTaskCompletedThatDay asserts a task that was timed
// and then closed on the same day shows a ✓ on its entry. The separate
// "done at" row is suppressed when the day has tracked time, so without this
// the entry kept the timer dot and read as still running.
func TestTimelineTicksTrackedTaskCompletedThatDay(t *testing.T) {
	m := newTestModel()
	day := localMidnight(-1)

	tracked := mkTodo("t", "write report", todo.Done)
	tracked.TimeEntries = []todo.TimeEntry{{ID: "e1", StartedAt: day.Add(9 * time.Hour), StoppedAt: day.Add(10 * time.Hour)}}
	tracked.CompletedAt = day.Add(10 * time.Hour)
	m.add(tracked)
	m.refreshCaches()

	acts := m.activitiesForDay(day)
	if len(acts) != 1 || !acts[0].doneThatDay {
		t.Fatalf("activitiesForDay = %+v, want one entry marked doneThatDay", acts)
	}
	line := ansi.Strip(m.renderTimelineEntry(acts[0], 0, 80))
	if !strings.Contains(line, "✓") || strings.Contains(line, "●") {
		t.Fatalf("timeline entry = %q, want ✓ instead of the timer dot", line)
	}
}

// Under the month grid, the open tasks due in the week from the selected day,
// soonest first, cut to the rows the panel has with a count of the rest.
func TestCalendarComingUp(t *testing.T) {
	today := startOfDay(time.Now())
	due := func(title string, days int) todo.Todo {
		td := todo.New(title)
		td.DueDate = today.AddDate(0, 0, days)
		return td
	}
	finished := due("Finished", 1)
	finished.Status = todo.Done
	m := modelWithTasks(t, due("Third", 5), due("First", 0), due("Second", 2), due("Next month", 30), due("Yesterday", -1), finished)
	m.switchTab(tabCalendar)

	got := ansi.Strip(strings.Join(m.renderComingUpLines(20, 10), "\n"))
	for _, want := range []string{"First", "Second", "Third"} {
		if !strings.Contains(got, want) {
			t.Errorf("coming up lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"Next month", "Yesterday", "Finished"} {
		if strings.Contains(got, not) {
			t.Errorf("coming up lists %q, outside the week or done:\n%s", not, got)
		}
	}
	if strings.Index(got, "First") > strings.Index(got, "Second") || strings.Index(got, "Second") > strings.Index(got, "Third") {
		t.Errorf("not soonest first:\n%s", got)
	}

	short := ansi.Strip(strings.Join(m.renderComingUpLines(20, 2), "\n"))
	if !strings.Contains(short, "First") || !strings.Contains(short, "2 more") || strings.Contains(short, "Third") {
		t.Errorf("two rows should be the first task and a count of the rest:\n%s", short)
	}
	for _, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > 20 {
			t.Errorf("line %q is %d wide, over the panel's 20", line, w)
		}
	}
}

// A subtask due the same day as its parent is the parent's deadline again;
// listing it once per step crowds the week out. A subtask with its own date
// keeps its row.
func TestCalendarComingUpListsAParentsDeadlineOnce(t *testing.T) {
	today := startOfDay(time.Now())
	parent := todo.New("Quarterly report")
	parent.DueDate = today.AddDate(0, 0, 3)
	same := todo.New("Draft text")
	same.ParentID = parent.ID
	same.DueDate = parent.DueDate
	own := todo.New("Collect numbers")
	own.ParentID = parent.ID
	own.DueDate = today.AddDate(0, 0, 1)
	m := modelWithTasks(t, parent, same, own)
	m.switchTab(tabCalendar)

	got := ansi.Strip(strings.Join(m.renderComingUpLines(30, 10), "\n"))
	if !strings.Contains(got, "Quarterly report") || !strings.Contains(got, "Collect numbers") {
		t.Errorf("the parent and the subtask with its own date should be listed:\n%s", got)
	}
	if strings.Contains(got, "Draft text") {
		t.Errorf("a subtask due with its parent should not repeat the deadline:\n%s", got)
	}
}
