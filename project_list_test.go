package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// TestProjectListWindowFollowsCursor guards fd8502d1: the Projects tab renderer
// used to draw from index 0 and the offset was clamped against m.cursor (the
// Tasks-tab cursor) instead of m.projectCursor, so navigating down past the
// visible rows walked the project cursor off-screen. The rendered window must
// follow m.projectCursor like the other list tabs.
func TestProjectListWindowFollowsCursor(t *testing.T) {
	const n = 40
	var tasks []todo.Todo
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("p%02d", i) // by name: p00..p39
		tk := mkTodo(p, "task "+p, todo.Pending)
		tk.Project = p
		tasks = append(tasks, tk)
	}
	m := modelWithTasks(t, tasks...)
	m.tab = tabProjects
	m.projectOrder = groupSortName

	visible := m.projectListVisibleRows()
	if visible < 1 || visible >= n {
		t.Fatalf("need 1 <= visible < %d for a meaningful window, got %d", n, visible)
	}

	// Walk the cursor to the last project via real key handling (down = j),
	// which re-runs the offset clamp at the end of updateList each step.
	for i := 0; i < n-1; i++ {
		m = sendKey(t, m, "down")
	}
	if m.projectCursor != n-1 {
		t.Fatalf("projectCursor = %d, want %d", m.projectCursor, n-1)
	}

	out := m.renderProjectListContent(m.allProjectsForList())
	if !strings.Contains(out, "p39") {
		t.Errorf("cursor project p39 missing from rendered window:\n%s", out)
	}
	if !strings.Contains(out, "▶") {
		t.Errorf("cursor marker missing from rendered window:\n%s", out)
	}
	if strings.Contains(out, "p00") {
		t.Errorf("top project p00 should have scrolled out of the window:\n%s", out)
	}
}

// TestProjectDrillUsesTaskRenderer guards the drilled-in Projects view:
// pressing Enter on a project must switch to a task-list view that uses the
// same row renderer as the Tasks tab (checkbox, cursor marker, task title),
// with the done tasks folded into one line until h, and the timeline strip in
// the right column when an open task has a date to put on it.
func TestProjectDrillUsesTaskRenderer(t *testing.T) {
	tasks := []todo.Todo{
		mkTodo("t1", "Alpha task", todo.Pending),
		mkTodo("t2", "Beta task", todo.Pending),
		mkTodo("t3", "Gamma task", todo.Done),
	}
	for i := range tasks {
		tasks[i].Project = "myproject"
	}
	tasks[0].DueDate = startOfDay(time.Now()).AddDate(0, 0, 5)
	m := modelWithTasks(t, tasks...)
	m.tab = tabProjects

	// Drill into the project.
	m = sendKey(t, m, "enter")
	if !m.projectTaskMode {
		t.Fatal("enter on project should set projectTaskMode = true")
	}

	// buildProjectListContent should return a side-by-side layout (two columns).
	w := m.termWidth - 6
	outerH := m.termHeight - 4
	out := m.buildProjectListContent(w, outerH)

	// The task titles should be visible in the left column.
	if !strings.Contains(out, "Alpha task") {
		t.Errorf("drilled-in view missing 'Alpha task':\n%s", out)
	}
	if !strings.Contains(out, "Beta task") {
		t.Errorf("drilled-in view missing 'Beta task':\n%s", out)
	}
	if strings.Contains(out, "Gamma task") || !strings.Contains(out, "1 done") {
		t.Errorf("the done task should fold into a count until h:\n%s", out)
	}
	m = sendKey(t, m, "h")
	if out := m.buildProjectListContent(w, outerH); !strings.Contains(out, "Gamma task") {
		t.Errorf("h should list the done task:\n%s", out)
	}

	// Drawn by the task renderer: status box in the gutter, then the title.
	if !strings.Contains(out, "  [ ] Alpha task") {
		t.Errorf("drilled-in view does not draw task rows:\n%s", out)
	}

	// The dated task carries the timeline beside its row, past the divider.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[ ] Alpha task") && strings.Count(line, "│") < 3 { // two borders and the divider
			t.Errorf("the dated task's row has no timeline beside it: %q", line)
		}
	}

	// No-wrap contract: the full View() output must not exceed termWidth.
	// TestNarrowNoWrap covers this via View(); verify it here too via the
	// full-screen render so the drilled-in Projects path is explicitly swept.
	for _, line := range strings.Split(m.View(), "\n") {
		if lw := ansi.StringWidth(line); lw > m.termWidth {
			t.Errorf("View() line %d cells exceeds termWidth %d: %q", lw, m.termWidth, line)
		}
	}
}

// Without an open task that has a date, a timeline has nothing ahead of it to
// draw: the drilled-in list takes the whole width, and the pane under the
// project list shows the project's tasks instead of an empty chart.
func TestProjectWithoutDatesSkipsTheTimeline(t *testing.T) {
	a := mkTodo("t1", "Undated task", todo.Pending)
	a.Project = "plain"
	m := modelWithTasks(t, a)
	m.tab = tabProjects
	m.termWidth, m.termHeight = 120, 30
	w, outerH := m.termWidth-6, m.termHeight-4

	undatedRowHasStrip := func(out string) bool {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "[ ] Undated task") {
				return strings.Count(line, "│") > 2 // the pane's two borders, plus the timeline's divider
			}
		}
		t.Fatalf("no task row for the undated task:\n%s", out)
		return false
	}
	if out := m.buildProjectListContent(w, outerH); undatedRowHasStrip(out) {
		t.Errorf("the pane should list the tasks without an empty timeline:\n%s", out)
	}
	m = sendKey(t, m, "enter")
	if out := m.buildProjectListContent(w, outerH); undatedRowHasStrip(out) {
		t.Errorf("drilled in, the pane should not carry an empty timeline:\n%s", out)
	}
}

// TestProjectDrillCursorScrolls guards that the drilled-in task list scrolls
// with the cursor inside the pane under the project list, so the cursor's task
// stays on screen as the user walks down past the pane's height.
func TestProjectDrillCursorScrolls(t *testing.T) {
	// A terminal small enough that the pane cannot show all 30 tasks.
	const n = 30
	var tasks []todo.Todo
	for i := 0; i < n; i++ {
		tk := mkTodo(fmt.Sprintf("t%02d", i), fmt.Sprintf("Task %02d", i), todo.Pending)
		tk.Project = "bigproject"
		tasks = append(tasks, tk)
	}
	m := modelWithTasks(t, tasks...)
	m.tab = tabProjects
	m.termHeight = 20

	m = sendKey(t, m, "enter")
	if !m.projectTaskMode {
		t.Fatal("enter on project should set projectTaskMode = true")
	}
	for i := 0; i < n-1; i++ {
		m = sendKey(t, m, "down")
	}
	if m.cursor != n-1 {
		t.Fatalf("cursor = %d, want %d after navigating to last task", m.cursor, n-1)
	}
	out := ansi.Strip(m.View())
	if last := fmt.Sprintf("[ ] Task %02d", n-1); !strings.Contains(out, last) {
		t.Errorf("cursor task %q missing from the pane after scrolling:\n%s", last, out)
	}
	if strings.Contains(out, "[ ] Task 00") {
		t.Errorf("the first task should have scrolled out of the pane:\n%s", out)
	}
}

// TestProjectDrillEnterOpensDetail guards that pressing Enter on a task inside
// the drilled-in project view opens the detail pane, and Esc backs out of it.
func TestProjectDrillEnterOpensDetail(t *testing.T) {
	tk := mkTodo("t1", "Detail target", todo.Pending)
	tk.Project = "proj"
	m := modelWithTasks(t, tk)
	m.tab = tabProjects

	// Drill into the project, then press Enter on the task.
	m = sendKey(t, m, "enter") // drill in
	if !m.projectTaskMode {
		t.Fatal("first enter should drill into project")
	}
	m = sendKey(t, m, "enter") // open task detail
	if m.pane != paneDetail {
		t.Errorf("second enter should open detail pane, got pane = %v", m.pane)
	}

	// Esc exits the detail pane back to the task list.
	m = sendKey(t, m, "esc")
	if m.pane != paneList {
		t.Errorf("esc should return to list pane, got pane = %v", m.pane)
	}
	if !m.projectTaskMode {
		t.Errorf("esc from detail should stay in projectTaskMode")
	}

	// Another Esc backs out of the drill to the project list.
	m = sendKey(t, m, "esc")
	if m.projectTaskMode {
		t.Errorf("second esc should exit projectTaskMode")
	}
}

// TestProjectDrillDetailShowsTaskNotGantt guards the regression where pressing
// Enter on a task inside the drilled-in view (pane == paneDetail) rendered an
// empty right column instead of the task's detail. The right column must show
// detail content (task title) and must NOT show the Gantt timeline header.
func TestProjectDrillDetailShowsTaskNotGantt(t *testing.T) {
	tk := mkTodo("t1", "Detail target", todo.Pending)
	tk.Project = "proj"
	m := modelWithTasks(t, tk)
	m.tab = tabProjects

	// Drill into the project, then open the task detail.
	m = sendKey(t, m, "enter") // drill in
	if !m.projectTaskMode {
		t.Fatal("first enter should drill into project")
	}
	m = sendKey(t, m, "enter") // open task detail
	if m.pane != paneDetail {
		t.Fatal("second enter should open detail pane")
	}

	// The detail panel is titled with the task and holds its fields.
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "╭─ Detail target") || !strings.Contains(out, tr("Priority")) {
		t.Errorf("the task's detail should be on screen:\n%s", out)
	}
}

// TestProjectDrillTimelineIsAStripNotASecondList pins the drilled-in Projects
// layout: the right column draws bars only, windowed to the same rows the task
// list beside it is showing, so each task's bar sits on the line with its
// title. Rendering the labelled chart there made the two columns list the same
// tasks in the same order — and spent half of the right column on the repeat,
// leaving a chart floored at minChartWidth.
func TestProjectDrillTimelineIsAStripNotASecondList(t *testing.T) {
	titles := []string{"Alpha task", "Beta task", "Undated task"}
	tasks := make([]todo.Todo, 0, len(titles))
	base := newTestModel().frameTime
	for i, title := range titles {
		td := mkTodo(fmt.Sprintf("t%d", i), title, todo.Pending)
		td.Project = "apollo"
		if title != "Undated task" {
			td.StartDate = base.AddDate(0, 0, -3+i)
			td.DueDate = base.AddDate(0, 0, 4+i*3)
		}
		tasks = append(tasks, td)
	}
	m := modelWithTasks(t, tasks...)
	m.tab = tabProjects
	m = sendKey(t, m, "enter")
	if !m.projectTaskMode {
		t.Fatal("enter on project should set projectTaskMode = true")
	}

	out := m.buildProjectListContent(m.termWidth-6, m.termHeight-4)
	// Task rows read "] Title"; the project list above names one in Next up.
	for _, title := range titles {
		if n := strings.Count(out, "] "+title); n != 1 {
			t.Errorf("%q has %d task rows in the drilled-in view, want 1 (the timeline must not repeat the list):\n%s", title, n, out)
		}
	}

	lineWith := func(title string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "] "+title) {
				return line
			}
		}
		t.Fatalf("no line contains %q:\n%s", title, out)
		return ""
	}
	for _, title := range []string{"Alpha task", "Beta task"} {
		if !strings.Contains(lineWith(title), "█") {
			t.Errorf("%q: bar is not on the same line as the title:\n%s", title, lineWith(title))
		}
	}
	if strings.Contains(lineWith("Undated task"), "█") {
		t.Errorf("undated task drew a bar:\n%s", lineWith("Undated task"))
	}

	for _, line := range strings.Split(m.View(), "\n") {
		if lw := ansi.StringWidth(line); lw > m.termWidth {
			t.Errorf("View() line %d cells exceeds termWidth %d: %q", lw, m.termWidth, line)
		}
	}
}

// Inside a project, subtasks fold as they do on the Tasks tab: → shows all of
// a task's subtasks, whether or not they carry the project, and ← on one of
// them folds the parent and puts the cursor back on it. The rows are sized to
// what they draw, so neither the (0/2) badge nor a subtask title is clipped.
func TestScriptProjectDrillFoldsSubtasks(t *testing.T) {
	parent := todo.New("Plan the trip")
	parent.Project = "house"
	inProject := todo.NewSubtask("Book flights", parent.ID)
	inProject.Project = "house"
	noProject := todo.NewSubtask("Find a hotel near the station", parent.ID)
	fence := todo.New("Fix the fence")
	fence.Project = "house"

	m := modelWithTasks(t, parent, inProject, noProject, fence)
	m.termWidth, m.termHeight = 110, 30
	m.switchTab(tabProjects)
	m = sendKey(t, m, "enter")
	rows := func() []string {
		tasks, _ := m.drillTaskList()
		var out []string
		for _, task := range tasks {
			out = append(out, task.Title)
		}
		return out
	}
	if got := rows(); len(got) != 2 {
		t.Fatalf("folded rows = %v, want the parent and the fence", got)
	}
	if cur := m.currentTodo(); cur == nil || cur.ID != parent.ID {
		t.Fatalf("cursor not on the parent to start with")
	}

	m = sendKey(t, m, "right")
	if got := rows(); len(got) != 4 {
		t.Fatalf("unfolded rows = %v, want both subtasks under the parent", got)
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{"- [ ] Plan the trip (0/2)", "Book flights", "Find a hotel near the station"} {
		if !strings.Contains(out, want) {
			t.Errorf("unfolded view lacks %q:\n%s", want, out)
		}
	}

	m = sendKey(t, m, "down")
	m = sendKey(t, m, "left")
	if got := rows(); len(got) != 2 {
		t.Errorf("← on a subtask left rows = %v, want the parent folded", got)
	}
	if cur := m.currentTodo(); cur == nil || cur.ID != parent.ID {
		t.Errorf("← on a subtask should return the cursor to its parent")
	}
}

// The timeline is an extra beside the list, not a reason to lose its columns:
// on a window where the strip would cost the task rows Score, Due or Size, the
// list keeps the width; on a wide one the strip is back.
func TestProjectTimelineGivesWayToTheListsColumns(t *testing.T) {
	td := mkTodo("t1", "Dated task", todo.Pending)
	td.Project = "apollo"
	td.DueDate = newTestModel().frameTime.AddDate(0, 0, 5)
	m := modelWithTasks(t, td)
	m.tab = tabProjects

	rowOf := func(width int) string {
		m.termWidth, m.termHeight = width, 30
		out := m.buildProjectListContent(m.termWidth-6, m.termHeight-4)
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "[ ] Dated task") {
				return ansi.Strip(line)
			}
		}
		t.Fatalf("width %d: no task row:\n%s", width, out)
		return ""
	}
	narrow := rowOf(84)
	if strings.Count(narrow, "│") > 2 {
		t.Errorf("at 84 columns the strip should give way to the list: %q", narrow)
	}
	if !strings.Contains(narrow, "100%") || !strings.Contains(narrow, "5d") {
		t.Errorf("at 84 columns the row should keep its Score and Due: %q", narrow)
	}
	if wide := rowOf(140); strings.Count(wide, "│") < 3 {
		t.Errorf("at 140 columns the timeline should be beside the row: %q", wide)
	}
}

// An edge of the timeline that falls on today says so once, not as a date
// beside a "today:" label repeating it.
func TestProjectTimelineNamesATodayEdgeOnce(t *testing.T) {
	m := newTestModel()
	today := m.frameTime
	axis := ansi.Strip(m.renderGanttAxis(today, today.AddDate(0, 0, 10), today, 40, 0))
	if strings.Count(axis, today.Format("02-01")) != 0 || !strings.Contains(axis, tr("today")) {
		t.Errorf("the left edge is today, so it should read %q, not the date: %q", tr("today"), axis)
	}
	if strings.Count(axis, tr("today")) != 1 {
		t.Errorf("today should be named once: %q", axis)
	}
}
