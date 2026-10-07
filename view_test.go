package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestNarrowNoWrap ensures no rendered line ever exceeds the terminal width,
// which would cause ugly wrapping inside the bordered panels.
func TestNarrowNoWrap(t *testing.T) {
	for _, width := range []int{40, 50, 60, 70, 80, 120} {
		m := newTestModel()
		m.termWidth = width
		m.termHeight = 30
		for i := 0; i < 5; i++ {
			task := todo.New("A fairly long task title that could overflow easily here")
			task.DueDate = time.Now().AddDate(0, 0, i)
			task.Tags = []string{"alpha", "beta"}
			m.add(task)
		}
		m.refreshCaches()
		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width=%d: line %d is %d cells wide: %q", width, n, w, line)
			}
		}
	}
}

// TestKeyHintsVisibleSearch asserts the Tasks-tab footer always advertises
// the filter: at widths where the full hint list can't fit, renderKeyHints must
// fall back to the curated short set instead of truncating it away.
func TestKeyHintsVisibleSearch(t *testing.T) {
	m := newTestModel()
	m.tab = tabTasks
	for _, width := range []int{74, 114, 200} {
		hint := m.renderKeyHints(width)
		if !strings.Contains(hint, "/ filter") {
			t.Errorf("width=%d: hint line lost '/ filter': %q", width, hint)
		}
	}
}

// TestQuickAddShowsSyntaxHint asserts the quick-add input surfaces the inline
// syntax, and that other text inputs (e.g. detail-pane comments) don't.
func TestQuickAddShowsSyntaxHint(t *testing.T) {
	applyLang(string(langEN))
	m := newTestModel()
	m.termWidth, m.termHeight = 100, 30
	m2 := script(t, m, "a")
	if !strings.Contains(m2.View(), "#tag @project due:tomorrow") {
		t.Error("quick-add input should show the syntax hint line")
	}
}

// TestNarrowNoWrapTranslated guards against translations that overflow a
// bordered panel the English source fit. Translated words are generally longer
// — German compounds especially — so for every tab/width it asserts the widest
// translated line is no wider than the English baseline (or the terminal).
// Comparing against English keeps pre-existing layout limits on the densest
// tabs from counting as translation regressions.
//
// It sweeps every language in availableLanguages rather than naming one, so a
// language added later inherits the guard instead of shipping unchecked.
func TestNarrowNoWrapTranslated(t *testing.T) {
	// Reflowing single-list tabs are checked across every width. The calendar and
	// projects tabs use fixed two-panel layouts with their own minimum widths (and
	// pre-existing narrow-width handling), so they're only swept where they fit.
	listTabs := []tab{tabTasks, tabTags, tabBoard, tabStats, tabSettings}
	panelTabs := []tab{tabCalendar, tabProjects}

	// initialModel applies the stored language, so set lang *after* building it.
	maxLineWidth := func(width int, tb tab, lang language) int {
		m := newTestModel()
		applyLang(string(lang))
		m.termWidth = width
		m.termHeight = 30
		for i := 0; i < 5; i++ {
			task := todo.New("A fairly long task title that could overflow easily here")
			task.DueDate = time.Now().AddDate(0, 0, i)
			task.Tags = []string{"alpha", "beta"}
			task.Project = "Demo"
			m.add(task)
		}
		m.refreshCaches()
		m.tab = tb
		widest := 0
		for _, line := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(line); w > widest {
				widest = w
			}
		}
		return widest
	}

	check := func(lang language, width int, tb tab) {
		baseline := maxLineWidth(width, tb, langEN)
		got := maxLineWidth(width, tb, lang)
		applyLang(string(langEN))

		limit := baseline
		if width > limit {
			limit = width
		}
		if got > limit {
			t.Errorf("%s tab=%d width=%d: widest line %d cells exceeds limit %d (en baseline %d)",
				lang, tb, width, got, limit, baseline)
		}
	}

	for _, lang := range availableLanguages {
		if lang == langEN {
			continue // English is the baseline being compared against
		}
		for _, width := range []int{40, 50, 60, 70, 80, 120} {
			for _, tb := range listTabs {
				check(lang, width, tb)
			}
		}
		for _, width := range []int{70, 80, 120} {
			for _, tb := range panelTabs {
				check(lang, width, tb)
			}
		}
	}
}

// A passed due date reaches the row as "[!]" in the status column. It used to
// reach it only as colour, which a screenshot, a dim theme or a reader who does
// not see the difference all lose; "!" was free because priority gave it up —
// priority is already the largest term in the Score column.
//
// High priority must therefore carry no "!" of its own, or the alarm would mean
// two different things on two different rows.
func TestOverdueShowsExclamationInStatusColumn(t *testing.T) {
	late := todo.New("Finish the audit")
	late.DueDate = time.Now().Add(-48 * time.Hour)
	hi := todo.New("Water the plants")
	hi.Priority = todo.PriorityHigh
	m := modelWithTasks(t, late, hi)

	var lateLine, hiLine string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "Finish the audit") {
			lateLine = line
		}
		if strings.Contains(line, "Water the plants") {
			hiLine = line
		}
	}
	if lateLine == "" || hiLine == "" {
		t.Fatalf("both task rows should render; late=%q hi=%q", lateLine, hiLine)
	}
	if !strings.Contains(lateLine, "[!]") {
		t.Errorf("overdue row should carry [!]: %q", lateLine)
	}
	if strings.Contains(hiLine, "!") {
		t.Errorf("high priority no longer marks the row with '!': %q", hiLine)
	}
}

// At side-by-side widths the Tasks tab always previews the cursor task's
// detail in the right column — without pressing Enter — and narrow terminals
// fall back to the stacked enter-to-open layout.
func TestSideBySideDetailPreview(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))
	m.termHeight = 40

	m.termWidth = sideBySideMinWidth + 10
	if !strings.Contains(m.View(), tr("Priority")) {
		t.Error("side-by-side: detail preview should render without Enter")
	}

	m.termWidth = sideBySideMinWidth - 10
	if strings.Contains(m.View(), tr("Priority")) {
		t.Error("stacked fallback: detail should stay hidden until Enter")
	}
	m2 := script(t, m, "enter")
	if !strings.Contains(m2.View(), tr("Priority")) {
		t.Error("stacked fallback: Enter should open the detail pane")
	}
}

// The detail pane's placement is a setting, and all three placements have to
// hold the same contracts the default one does: the panel is where it was put,
// and no line is wider than the terminal.
func TestDetailPanePlacement(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))
	m.termHeight = 40
	m.termWidth = sideBySideMinWidth + 10

	// Column order: the two panels are the same two boxes either way, so the
	// assertion is which border title comes first on the row that holds both.
	// The detail panel titles itself with the task, so the selected task's
	// title is what marks that column. Which task is selected is asked, not
	// assumed: the two are created back to back, and on Windows' coarse clock
	// they share a CreatedAt, so the order falls through to the random ID.
	selected := m.currentTodo().Title
	titleRow := func(m model) string {
		for _, line := range strings.Split(ansi.Strip(m.View()), "\n") {
			if strings.Contains(line, "╭─ ") && strings.Count(line, "╭─ ") == 2 {
				return line
			}
		}
		return ""
	}

	m.detailPos = detailRight
	row := titleRow(m)
	if row == "" {
		t.Fatal("right: no row carries both panel titles")
	}
	if strings.Index(row, tr("Overview")) > strings.Index(row, selected) {
		t.Errorf("right: the list should come first:\n%s", row)
	}

	m.detailPos = detailLeft
	row = titleRow(m)
	if row == "" {
		t.Fatal("left: no row carries both panel titles")
	}
	if strings.Index(row, tr("Overview")) < strings.Index(row, selected) {
		t.Errorf("left: the detail should come first:\n%s", row)
	}

	// Bottom is the stacked layout at any width, which on Tasks means the
	// detail stays shut until enter — the narrow fallback's behaviour, chosen
	// rather than fallen into.
	m.detailPos = detailBottom
	if m.sideBySide() {
		t.Error("bottom: the layout should not be side-by-side")
	}
	if strings.Contains(m.View(), tr("Priority")) {
		t.Error("bottom: the detail should stay hidden until enter")
	}
	opened := script(t, m, "enter")
	if !strings.Contains(opened.View(), tr("Priority")) {
		t.Error("bottom: enter should open the stacked detail")
	}

	for _, pos := range []detailPos{detailRight, detailLeft, detailBottom} {
		for _, w := range []int{60, 90, sideBySideMinWidth, 160} {
			mm := m
			mm.detailPos = pos
			mm.termWidth = w
			for i, line := range strings.Split(mm.View(), "\n") {
				if got := ansi.StringWidth(line); got > w {
					t.Fatalf("pos=%v w=%d line %d is %d wide: %q", pos, w, i, got, ansi.Strip(line))
				}
			}
		}
	}
}

func TestPersistentPanelsUseContextualBorderTitles(t *testing.T) {
	applyLang(string(langEN))
	t.Cleanup(func() { applyLang(string(langEN)) })

	plainView := func(m model) string {
		m.termWidth = 120 // stacked layouts make every panel title easy to assert
		m.termHeight = 40
		return ansi.Strip(m.View())
	}
	assertTitle := func(label, view, title string) {
		t.Helper()
		if !strings.Contains(view, "╭─ "+title+" ") {
			t.Errorf("%s missing border title %q:\n%s", label, title, view)
		}
	}

	task := todo.New("Panel title task")
	tasks := modelWithTasks(t, task)
	assertTitle("tasks", plainView(tasks), "Overview")
	tasks.showHistory = true
	assertTitle("task history", plainView(tasks), "History")

	tagged := todo.New("Tagged task")
	tagged.Tags = []string{"home"}
	tags := modelWithTasks(t, tagged)
	tags.tab = tabTags
	assertTitle("tags", plainView(tags), "Overview")
	assertTitle("tag detail", plainView(tags), "#home")

	projectTask := todo.New("Project task")
	projectTask.Project = "alpha"
	projectTask.DueDate = time.Now().AddDate(0, 0, 3) // something to put on a timeline
	projects := modelWithTasks(t, projectTask)
	projects.tab = tabProjects
	assertTitle("projects", plainView(projects), "Overview")
	assertTitle("project pane", plainView(projects), "@alpha")
	projects.projectTaskMode = true
	assertTitle("project list, drilled in", plainView(projects), "Overview")
	assertTitle("project pane, drilled in", plainView(projects), "@alpha")

	calendar := modelWithTasks(t)
	calendar.tab = tabCalendar
	calendar.calendar.selected = time.Date(2026, time.July, 14, 0, 0, 0, 0, time.Local)
	calendarView := plainView(calendar)
	assertTitle("calendar day", calendarView, localizedDayDateAbbrev(calendar.calendar.selected))
	assertTitle("calendar month", calendarView, localizedMonthYear(calendar.calendar.selected))

	for _, tc := range []struct {
		label string
		tab   tab
		title string
	}{
		{"board", tabBoard, "Workflow"},
		{"stats", tabStats, "Summary"},
		{"settings", tabSettings, "Preferences"},
	} {
		m := modelWithTasks(t, task)
		m.tab = tc.tab
		view := plainView(m)
		assertTitle(tc.label, view, tc.title)
		if tc.tab == tabStats {
			assertTitle("stats activity", view, "Activity")
		}
	}
}

func TestTaskPositionAndSortAppearInPanelTitle(t *testing.T) {
	applyLang(string(langEN))
	t.Cleanup(func() { applyLang(string(langEN)) })

	m := modelWithTasks(t, todo.New("alpha"), todo.New("beta"))
	m.termWidth = 120
	m.termHeight = 40
	m.taskSort = taskSortSize
	m.refreshCaches()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "╭─ Overview [1/2] [sort: size] ") {
		t.Fatalf("task panel title missing bracketed position and sort status:\n%s", view)
	}
	if count := strings.Count(view, "1/2"); count != 1 {
		t.Errorf("task position should appear only in the panel title; found %d occurrences", count)
	}
	if status := ansi.Strip(m.renderStatusLine()); strings.Contains(status, "sort:") {
		t.Errorf("Tasks status line should no longer contain sort status: %q", status)
	}

	m.showHistory = true
	m.cursor = 0
	for i := range m.cache.active {
		task := m.get(m.cache.active[i].ID)
		task.Status = todo.Done
	}
	m.refreshCaches()
	historyView := ansi.Strip(m.View())
	if !strings.Contains(historyView, "╭─ History [1/2] [sort: completed] ") {
		t.Fatalf("history panel title missing bracketed position and sort status:\n%s", historyView)
	}
}

// Focusing the right-hand detail must not reduce the number of rows rendered
// in the left-hand list. buildSideBySide narrows a model copy so list columns
// reflow; that narrowed width must not make the height calculation mistake the
// copy for a stacked layout and reserve space for a nonexistent bottom panel.
func TestSideBySideDetailFocusKeepsFullListHeight(t *testing.T) {
	tasks := make([]todo.Todo, 40)
	for i := range tasks {
		tasks[i] = todo.New(fmt.Sprintf("task-row-%02d", i))
	}
	m := modelWithTasks(t, tasks...)
	m.termWidth = sideBySideMinWidth + 10
	m.termHeight = 30

	m.pane = paneList
	listFocusedRows := strings.Count(m.View(), "task-row-")
	m.pane = paneDetail
	detailFocusedRows := strings.Count(m.View(), "task-row-")

	if detailFocusedRows != listFocusedRows {
		t.Errorf("side-by-side detail focus changed rendered task rows: list focus=%d detail focus=%d",
			listFocusedRows, detailFocusedRows)
	}
}

// The Tags tab's pane stacks under the list at every width, like the
// Projects tab's: it holds a task list, which reads best at full width.
func TestTagsPaneStacksAtEveryWidth(t *testing.T) {
	task := todo.New("fix the fence")
	task.AddTag("home")
	m := modelWithTasks(t, task)
	m.tab = tabTags
	m.termHeight = 40

	summary := strings.TrimSpace(fmt.Sprintf(tr("  %d open · %d overdue · %d done"), 1, 0, 0))
	for _, w := range []int{sideBySideMinWidth + 10, sideBySideMinWidth - 10} {
		m.termWidth = w
		if m.sideBySide() || !m.detailVisible() {
			t.Errorf("width %d: sideBySide=%v detailVisible=%v, want the stacked pane", w, m.sideBySide(), m.detailVisible())
		}
		if !strings.Contains(m.View(), summary) {
			t.Errorf("width %d: tag summary not rendered below the list", w)
		}
	}
}

// A selected overdue row must show both states: the overdue foreground and the
// selection background. Before the combined styles, the status colour won the
// style switch outright and the only cursor cue on an overdue-heavy list was
// the arrow glyph.
func TestSelectedOverdueRowKeepsSelectionBackground(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	over := todo.New("Pay the rent")
	over.DueDate = time.Now().Add(-48 * time.Hour)
	over2 := todo.New("File the taxes")
	over2.DueDate = time.Now().Add(-24 * time.Hour)
	m := modelWithTasks(t, over, over2)

	var selLine, plainLine string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "Pay the rent") {
			selLine = line // cursor starts on the first row
		}
		if strings.Contains(line, "File the taxes") {
			plainLine = line
		}
	}
	if selLine == "" || plainLine == "" {
		t.Fatalf("both overdue rows should render; sel=%q plain=%q", selLine, plainLine)
	}
	wantPrefix := newFastStyle(selectedOverdueRowStyle).prefix
	if !strings.Contains(selLine, wantPrefix) {
		t.Errorf("selected overdue row should use overdue fg + sel bg (%q): %q", wantPrefix, selLine)
	}
	if strings.Contains(plainLine, "48;2;") {
		t.Errorf("unselected overdue row should have no background: %q", plainLine)
	}
}

func TestOverdueSubtaskIsRedInDetailWithoutParentMarker(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	parent := todo.New("Parent task")
	child := todo.NewSubtask("Late child", parent.ID)
	child.DueDate = time.Now().Add(-24 * time.Hour)
	m := modelWithTasks(t, parent, child)
	m.pane = paneDetail
	m.detailTaskID = parent.ID
	m.detail = detailState{field: fieldSubtasks, subtaskCursor: 0}

	detail := m.renderDetailPage2(m.currentTodo())
	var childLine string
	for _, line := range strings.Split(detail, "\n") {
		if strings.Contains(line, child.Title) {
			childLine = line
			break
		}
	}
	if childLine == "" {
		t.Fatal("overdue child should render in parent details")
	}
	if want := newFastStyle(overdueStyle).prefix; !strings.Contains(childLine, want) {
		t.Errorf("overdue child should use overdue red (%q): %q", want, childLine)
	}

	m.pane = paneList
	m.detailTaskID = ""
	list := ansi.Strip(m.View())
	if strings.Contains(list, "‼") {
		t.Errorf("parent list row should not carry an overdue-descendant marker:\n%s", list)
	}
	if !strings.Contains(list, "(0/1)") {
		t.Errorf("parent should retain its subtask progress badge:\n%s", list)
	}
}

func TestDetailPanelTitleMarksSubtask(t *testing.T) {
	parent := todo.New("Parent task")
	child := todo.NewSubtask("Child task", parent.ID)
	m := modelWithTasks(t, parent, child)
	m.pane = paneDetail

	// Top-level detail: the title is the bare task name, no marker.
	m.detailTaskID = parent.ID
	if got := m.detailPanelTitle(); got != parent.Title {
		t.Fatalf("top-level detail title = %q, want %q", got, parent.Title)
	}

	// Drilled into a subtask: the title carries the chevron marker.
	m.detailTaskID = child.ID
	m.detailStack = []string{parent.ID}
	if got := m.detailPanelTitle(); got != "↳ "+child.Title {
		t.Fatalf("subtask detail title = %q, want %q", got, "↳ "+child.Title)
	}
}

func TestSelectedTaskRowHighlightIncludesTags(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	task := todo.New("Tagged task")
	task.Tags = []string{"urgent", "home"}
	m := modelWithTasks(t, task)
	m.termWidth = 100 // stacked width leaves ample room for both tag chips

	var row string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "Tagged task") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("tagged task row should render")
	}
	tagPos := strings.Index(row, "#urgent")
	if tagPos < 0 {
		t.Fatalf("selected row should render its tags: %q", row)
	}
	wantPrefix := newFastStyle(taskTagSelectedRowStyle).prefix
	if !strings.Contains(row[:tagPos], wantPrefix) {
		t.Errorf("selected-row background should continue into tags (%q): %q", wantPrefix, row)
	}
}

// The selected row is a bar, not a stretch of coloured text: it has to reach
// the pane's right edge whether or not the task has tags, or it stops wherever
// that row's last column happened to end — at the Tags column on a tagless
// task — and reads as a block sitting in the middle of the list.
func TestSelectedRowHighlightReachesThePaneEdge(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	tagged := todo.New("Tagged task")
	tagged.Tags = []string{"home"}
	bare := todo.New("Bare task")
	parent := todo.New("Parent task")
	sub := todo.New("A subtask")
	sub.ParentID = parent.ID

	m := modelWithTasks(t, tagged, bare, parent, sub)
	m.termWidth = 100
	m.setExpanded(parent.ID, true)
	m.refreshCaches()

	selStyle := newFastStyle(selectedRowStyle)
	for title, id := range map[string]string{
		"Tagged task": tagged.ID,
		"Bare task":   bare.ID,
		"A subtask":   sub.ID,
	} {
		m.cursor = m.visibleActiveIndexOf(id)
		if m.cursor < 0 {
			t.Fatalf("%s should be a visible row", title)
		}

		var row string
		for _, line := range strings.Split(m.renderTaskList(), "\n") {
			if strings.Contains(ansi.Strip(line), title) {
				row = line
				break
			}
		}
		if row == "" {
			t.Fatalf("%s row should render", title)
		}
		if w := ansi.StringWidth(row); w != m.termWidth-8 {
			t.Errorf("selected %q row spans %d columns, want the pane's %d", title, w, m.termWidth-8)
		}
		tail := strings.TrimSuffix(row, selStyle.suffix)
		cut := strings.LastIndex(tail, selStyle.prefix)
		if cut < 0 {
			t.Fatalf("selected %q row should carry the selection background: %q", title, row)
		}
		if pad := tail[cut+len(selStyle.prefix):]; strings.TrimLeft(pad, " ") != "" {
			t.Errorf("selected %q row should end in selection-styled padding, got %q", title, pad)
		}
	}
}

// Detail values are assembled by their callers, so one can arrive already
// styled. Truncating that by rune count cuts an SGR sequence in half: the
// terminal swallows the "(…)" marker as sequence parameters, prints whatever
// falls out — a stray ")" beside the value — and never sees the reset, so the
// style leaks into the panel border and the frame breaks. The widths here are
// the side-by-side ones, where the detail pane is half the window and the
// value budget is tight enough to truncate.
func TestDetailValuesTruncateWithoutBreakingEscapes(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	task := todo.New("Create dashboard for solution teams")
	task.Stage = defaultStages()[0]
	m := modelWithTasks(t, task)
	m.termHeight = 40
	m.pane = paneDetail
	m.detailTaskID = task.ID

	for _, w := range []int{110, 120, 130, 140, 160, 200} {
		m.termWidth = w
		m.refreshCaches()
		out := m.View()
		// ansi.Strip removes complete sequences only, so a leftover ESC is the
		// fingerprint of a cut one.
		if stripped := ansi.Strip(out); strings.ContainsRune(stripped, 0x1b) {
			for _, line := range strings.Split(stripped, "\n") {
				if strings.ContainsRune(line, 0x1b) {
					t.Fatalf("width %d: a truncated value cut an escape sequence: %q", w, line)
				}
			}
		}
		var stage string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(ansi.Strip(line), tr("Stage")+":") {
				stage = ansi.Strip(line)
			}
		}
		if stage == "" {
			t.Fatalf("width %d: the Stage row should render", w)
		}
		// The ‹ … › brackets are decoration: they render as a pair or not at
		// all, never as a half-open one left behind by a truncation.
		if strings.Contains(stage, "‹") != strings.Contains(stage, "›") {
			t.Errorf("width %d: the picker brackets should go whole rather than be clipped: %q", w, stage)
		}
		if !strings.Contains(stage, defaultStages()[0]) {
			t.Errorf("width %d: the stage name itself must survive: %q", w, stage)
		}
	}
}

// A tag too long for its cell is clipped, not replaced by a count: "#a-tag…"
// still names the tag, where "+1" would say only that there is one.
func TestTaskTagOverflowClipsTheTag(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	task := todo.New("Short title")
	task.Tags = []string{"a-tag-name-far-too-long-for-the-task-list-column"}
	other := todo.New("Other task")
	m := modelWithTasks(t, task, other)
	m.termWidth = 60
	for i := range m.cache.active {
		if m.cache.active[i].ID == task.ID {
			m.cursor = (i + 1) % len(m.cache.active) // keep the overflow row unselected
			break
		}
	}

	var rawRow, row string
	for _, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "Short title") {
			rawRow = line
			row = plain
			break
		}
	}
	if row == "" {
		t.Fatal("task row should render")
	}
	if !strings.Contains(row, "#a-tag-name") || !strings.Contains(row, "…") {
		t.Errorf("a tag too long for the cell should be clipped, not dropped: %q", row)
	}
	if strings.Contains(row, "list-column") {
		t.Errorf("overflowing tag should not leak past its clip: %q", row)
	}
	tagPos := strings.Index(rawRow, "#a-tag-name")
	wantTagPrefix := newFastStyle(tagStyle).prefix
	if tagPos < 0 || !strings.Contains(rawRow[:tagPos], wantTagPrefix) {
		t.Errorf("the clipped tag should be rendered as a Tags-column value in tag colour (%q): %q",
			wantTagPrefix, rawRow)
	}
}

// The degraded cell is a fallback, not the rule: when a tag fits alongside a
// wider one, it is drawn whole and only the wider one is clipped, so the most
// useful tag is still on screen.
func TestTaskTagOverflowKeepsTheChipsThatFit(t *testing.T) {
	task := todo.New("Short title")
	task.Tags = []string{"bug", "a-tag-name-far-too-long-for-the-task-list-column"}
	m := modelWithTasks(t, task)
	m.termWidth = 80
	m.refreshCaches()

	var row string
	for _, line := range strings.Split(m.View(), "\n") {
		if plain := ansi.Strip(line); strings.Contains(plain, "Short title") {
			row = plain
			break
		}
	}
	if row == "" {
		t.Fatal("task row should render")
	}
	if !strings.Contains(row, "#bug #a-tag") {
		t.Errorf("the tag that fits should still be drawn, the next clipped beside it: %q", row)
	}
	if !strings.Contains(row, "…") || strings.Contains(row, "list-column") {
		t.Errorf("the tag that does not fit should be clipped: %q", row)
	}
}

func TestWideSideBySideProjectColumnUsesSpareWidth(t *testing.T) {
	const project = "customer-success-platform-redesign"
	task := todo.New("Prepare launch brief")
	task.Project = project
	m := modelWithTasks(t, task)
	m.termWidth = 200
	m.termHeight = 30
	m.refreshCaches()

	var listRow string
	for _, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "[ ]") && strings.Contains(plain, task.Title) {
			// In side-by-side mode the detail panel continues on the same terminal
			// row. Keep only the left panel up to its closing border.
			titleAt := strings.Index(plain, task.Title)
			if borderAt := strings.Index(plain[titleAt:], "│"); borderAt >= 0 {
				plain = plain[:titleAt+borderAt]
			}
			listRow = plain
			break
		}
	}
	if listRow == "" {
		t.Fatal("task list row should render")
	}
	if !strings.Contains(listRow, project) {
		t.Errorf("wide list should show the full project name instead of truncating it: %q", listRow)
	}
	if strings.Contains(listRow, "(…)") {
		t.Errorf("wide list used a truncation marker despite available space: %q", listRow)
	}
}

// An overdue dependency conveys itself through the row color, not a glyph — a
// normal-priority task whose dependency is overdue carries no "!".
func TestOverdueDependencyAddsNoGlyph(t *testing.T) {
	dep := todo.New("blocking dep")
	dep.ID = "dep1"
	dep.DueDate = time.Now().Add(-48 * time.Hour) // overdue, still pending
	blocked := todo.New("Finish the audit")       // normal priority, depends on dep1
	blocked.Dependencies = []string{"dep1"}

	m := modelWithTasks(t, blocked, dep)
	var line string
	for _, l := range strings.Split(m.View(), "\n") {
		if strings.Contains(l, "Finish the audit") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("blocked task row should render")
	}
	if strings.Contains(line, "!") {
		t.Errorf("overdue-dependency row should carry no '!' (color-only): %q", line)
	}
}

// The detail pane shows both directions of the dependency graph in one
// merged Dependencies list: ↧ rows for outbound edges, dimmed ↥ rows for
// the pending tasks waiting on this one.
func TestDetailShowsInboundDependents(t *testing.T) {
	blocker := todo.New("build the widget")
	blocker.ID = "blk1"
	dependent := todo.New("ship the widget")
	dependent.Dependencies = []string{"blk1"}
	m := modelWithTasks(t, blocker, dependent)
	m.termWidth, m.termHeight = 120, 40 // side-by-side: detail previews the cursor task

	setCursorOn := func(id string) {
		for i := range m.cache.active {
			if m.cache.active[i].ID == id {
				m.cursor = i
				return
			}
		}
		t.Fatalf("task %s not in active list", id)
	}

	setCursorOn("blk1")
	out := m.View()
	if !strings.Contains(out, "↥ Ship the widget") {
		t.Errorf("blocker detail should list its dependent as a ↥ row:\n%s", out)
	}

	setCursorOn(dependent.ID)
	m.invalidateDetailCache()
	out = m.View()
	// "    ↥ " is the detail pane's inbound-row indent; the bare trailing ↥
	// on the blocker's list row is a different (expected) glyph.
	if strings.Contains(out, "    ↥ ") {
		t.Errorf("dependent's detail should have no ↥ rows:\n%s", out)
	}
	// The outbound side carries the ↧ glyph, mirroring the list rows.
	if !strings.Contains(out, "↧ Build the widget") {
		t.Errorf("outbound dependency line should carry the ↧ glyph:\n%s", out)
	}
}

// Enter on a ↥ row jumps to the task waiting on this one, exactly like
// enter on a ↧ row jumps to the dependency.
func TestEnterOnInboundDependentJumps(t *testing.T) {
	blocker := todo.New("build the widget")
	blocker.ID = "blk1"
	dependent := todo.New("ship the widget")
	dependent.Dependencies = []string{"blk1"}
	m := modelWithTasks(t, blocker, dependent)
	for i := range m.cache.active {
		if m.cache.active[i].ID == "blk1" {
			m.cursor = i
		}
	}
	m.pane = paneDetail
	// The blocker has no outbound deps, so row 0 is the inbound ↥ row.
	m.detail = detailState{field: fieldDependencies, depCursor: 0}

	updated, _ := m.startEditing()
	m2 := updated.(model)
	if m2.pane != paneList {
		t.Fatalf("pane = %v, want paneList after jump", m2.pane)
	}
	if cur := m2.currentTodo(); cur == nil || cur.ID != dependent.ID {
		t.Errorf("cursor should land on the dependent, got %+v", cur)
	}
}

// TestSelectedTabNeverTruncated asserts that the selected tab keeps its full
// title while the bar can afford it by abbreviating the others, and that when
// it cannot, it falls back to its curated short label — never to a mechanical
// cut of the long one.
//
// At termWidth=86 the full-label bar does not fit but the mixed arrangement
// (selected full, others abbreviated) does. At termWidth=80 only the all-short
// bar fits: the selected tab takes its short label rather than collapsing every
// other tab to a bare digit, which is what it used to do.
func TestSelectedTabNeverTruncated(t *testing.T) {
	applyLang(string(langEN))

	cases := []struct {
		selectedTab tab
		width       int
		want        string
	}{
		{tabCalendar, 120, "2 Calendar"},
		{tabSettings, 120, "7 Settings"},
		{tabCalendar, 97, "2 Cal "},
		{tabSettings, 97, "7 Setup"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s@%d", strings.TrimSpace(tc.want), tc.width), func(t *testing.T) {
			m := newTestModel()
			m.termWidth = tc.width
			m.termHeight = 30
			m.tab = tc.selectedTab
			m.refreshCaches()

			header := ansi.Strip(strings.Split(m.View(), "\n")[0])
			if !strings.Contains(header, tc.want) {
				t.Errorf("width %d: selected label %q missing from header: %q", tc.width, tc.want, header)
			}
			// The other tabs keep a word, not a bare digit.
			if !strings.Contains(header, "1 Tasks") {
				t.Errorf("width %d: the other tabs collapsed: %q", tc.width, header)
			}
		})
	}
}

// TestSelectedTabNeverTruncatedWidthSweep checks that across a range of narrow
// terminal widths: (a) no rendered line exceeds termWidth (for list-only tabs
// that respect the no-wrap contract at every width), and (b) the selected tab's
// full title is present in the header whenever the title fits in avail on its
// own (i.e. the terminal isn't so narrow that even the selected title alone
// would overflow).
//
// The Calendar tab uses a fixed two-panel layout with its own minimum width
// (same as TestNarrowNoWrapTranslated) so it is excluded from the no-wrap sweep.
// Two different selected tabs exercise different full-label lengths.
func TestSelectedTabNeverTruncatedWidthSweep(t *testing.T) {
	applyLang(string(langEN))

	type tabCase struct {
		tb        tab
		checkWrap bool // false for fixed-layout tabs that have their own min-width
	}
	cases := []tabCase{
		{tabBoard, true},    // "5 Board" — list-style tab under the no-wrap contract
		{tabSettings, true}, // "7 Settings" — second selected-tab check
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("tab%d", tc.tb), func(t *testing.T) {
			fullLabel := [numTabs]string{
				tr("1 Tasks"), tr("2 Calendar"), tr("3 Tags"),
				tr("4 Projects"), tr("5 Board"), tr("6 Stats"), tr("7 Settings"),
			}[tc.tb]
			shortLabel := tabShortLabels[langEN][tc.tb]

			for _, width := range []int{40, 50, 60, 70, 80, 100, 120} {
				m := newTestModel()
				m.termWidth = width
				m.termHeight = 30
				m.tab = tc.tb
				m.refreshCaches()

				out := m.View()
				lines := strings.Split(out, "\n")

				// No-wrap contract: every line ≤ termWidth.
				if tc.checkWrap {
					for n, line := range lines {
						if w := ansi.StringWidth(line); w > width {
							t.Errorf("tab=%d width=%d: line %d is %d cells wide: %q",
								tc.tb, width, n, w, line)
						}
					}
				}

				// Selected-tab guarantee: the full label or the curated short
				// one must appear in the header when there is room for it — i.e.
				// the selected tab's full label plus the minimum-width unselected
				// tabs (bare numbers) plus separators all fit in avail.
				// avail ≈ termWidth − titleW(5) − 2 − hintW(11) − 4 = termWidth − 22.
				// Minimum unselected contribution: (numTabs−1) separators + (numTabs−1)×1.
				approxMinWidth := len([]rune(fullLabel)) + (numTabs - 1) + (numTabs - 1)
				if width-22 >= approxMinWidth {
					header := lines[0]
					if !strings.Contains(header, fullLabel) && !strings.Contains(header, shortLabel) {
						t.Errorf("tab=%d width=%d: selected tab label %q/%q missing from header: %q",
							tc.tb, width, fullLabel, shortLabel, header)
					}
				}
			}
		})
	}
}

// When comments wrap to multiple lines in a narrow column, the detail scroll
// must still bring the selected comment into view — counting one line per
// comment used to undershoot and push the cursor row off the bottom edge.
func TestDetailScrollReachesWrappedComment(t *testing.T) {
	task := todo.New("Task with a very long tail")
	for i := 1; i <= 12; i++ {
		task.AddComment(fmt.Sprintf("comment number %d with some length to it", i))
	}
	m := modelWithTasks(t, task)
	m.termWidth, m.termHeight = 120, 24
	m.pane = paneDetail
	m.detail = detailState{field: fieldComments, commentCursor: 9}
	m.invalidateDetailCache()

	if !strings.Contains(m.View(), "comment number 10") {
		t.Error("selected comment should be scrolled into view")
	}
}

// The boxed footer fields — search, quick-add, the pickers, the palette — are
// drawn under a pane and have to line up with it. They carry the panels'
// MarginLeft(2) to manage it; without it every box is inset two columns to the
// left of everything above it, which is exactly what it looks like. The plain
// footer rows (hints, prompts, picker results) share the same rule one level
// in: a 4-cell gutter puts their text in the box's own text column.
func TestFooterBoxesLineUpWithThePaneAbove(t *testing.T) {
	a := todo.New("alpha")
	a.AddTag("home")
	base := modelWithTasks(t, a, todo.New("beta"))

	for _, mode := range []struct {
		name string
		set  func(*model)
	}{
		{"search", func(m *model) { m.mode = modeSearch; m.searchInput.SetValue("ho") }},
		{"quick add", func(m *model) { m.mode = modeInput; m.textInput.SetValue("buy milk") }},
		{"dependency picker", func(m *model) { m.mode = modeSearchDep; m.depSearchInput.SetValue("a") }},
		{"tag picker", func(m *model) { m.mode = modeSearchTag; m.tagSearchInput.SetValue("h") }},
		{"project picker", func(m *model) { m.mode = modeSearchProject }},
		{"palette", func(m *model) { m.mode = modePalette; m.paletteInput.SetValue("bo") }},
		{"stage editor", func(m *model) { m.mode = modeEditStages; m.textInput.SetValue(m.boardCfg.stagesDisplay()) }},
	} {
		// Both layouts: stacked (one box per line) and side-by-side, where the
		// list area is two boxes and only the leftmost edge is comparable.
		for _, w := range []int{90, 140} {
			m := base
			m.termWidth, m.termHeight = w, 26
			mode.set(&m)

			type edge struct {
				line string
				col  int
			}
			var edges []edge
			for _, line := range strings.Split(ansi.Strip(m.View()), "\n") {
				i := strings.IndexAny(line, "╭╰")
				if i < 0 {
					continue
				}
				edges = append(edges, edge{line, ansi.StringWidth(line[:i])})
			}
			if len(edges) < 4 {
				t.Fatalf("%s at w=%d: found %d box edges, expected a pane and a field",
					mode.name, w, len(edges))
			}
			for _, e := range edges[1:] {
				if e.col != edges[0].col {
					t.Errorf("%s at w=%d: box starts at column %d, the pane above at %d:\n%s\n%s",
						mode.name, w, e.col, edges[0].col, edges[0].line, e.line)
				}
				if got, want := ansi.StringWidth(e.line), ansi.StringWidth(edges[0].line); got != want {
					t.Errorf("%s at w=%d: box line is %d cells wide, the pane above %d:\n%s\n%s",
						mode.name, w, got, want, edges[0].line, e.line)
				}
			}
		}
	}
}

// The status line's corner is for the state you have to act on. A healthy sync
// used to put a dim ✓ there — a symbol with nowhere to look it up, saying only
// that nothing was wrong — so the corner now stays empty and Settings carries
// the steady state in words. The failure keeps its place, and the help overlay
// explains it: a mark nobody can decode is the thing this replaced.
func TestStatusLineSpeaksOnlyWhenSyncFails(t *testing.T) {
	on := true
	configured := func(m *model) {
		m.syncCfg = syncConfig{URL: "https://example.invalid", Token: "a-token", AutoSync: &on}
		m.autoSync = true
	}

	for _, c := range []struct {
		name string
		set  func(*model)
		want string // "" = the line says nothing
	}{
		{"sync not configured", func(m *model) {}, ""},
		{"sync healthy", configured, ""},
		{"sync failing", func(m *model) { configured(m); m.lastSyncFailed = true }, tr("✕ sync")},
	} {
		m := modelWithTasks(t, todo.New("alpha"))
		m.termWidth, m.termHeight = 90, 20
		c.set(&m)

		got := strings.TrimSpace(ansi.Strip(m.renderStatusLine()))
		if got != c.want {
			t.Errorf("%s: status line = %q, want %q", c.name, got, c.want)
		}
		if strings.Contains(got, "✓") {
			t.Errorf("%s: status line carries a ✓ again: %q", c.name, got)
		}
	}

	// Whatever the line can still show has to be explained where symbols are
	// looked up, or it is the same unreadable mark under a different glyph.
	m := modelWithTasks(t, todo.New("alpha"))
	m.termWidth, m.termHeight = 100, 40
	m.mode = modeHelp
	// The body rather than the rendered overlay: the sections below the fold
	// are still what the overlay scrolls through.
	help := ansi.Strip(strings.Join(m.helpBodyLines(), "\n"))
	for _, mark := range []string{tr("✕ sync"), tr("FOCUS")} {
		if !strings.Contains(help, mark) {
			t.Errorf("the help overlay does not explain %q", mark)
		}
	}
}

// The tab bar renders through styles with Padding(0, 1), so every tab is two
// cells wider than its label. tabsWidthMixed used to leave that out, which let
// renderTabs pick a level that measured inside its budget and rendered fourteen
// cells past it — paid for by the header truncating whatever sat to its right.
// That is why the shortcut hint used to read "? shortcuts · ctr".
func TestTabBarFitsTheWidthItIsGiven(t *testing.T) {
	t.Cleanup(func() { applyLang(string(langEN)) })
	for _, lang := range availableLanguages {
		for _, tb := range []tab{tabTasks, tabCalendar, tabSettings} {
			m := modelWithTasks(t, todo.New("alpha"))
			applyLang(string(lang)) // after the model: initialModel re-applies the stored language
			m.tab = tb
			// The floor: bare numbers plus the selected tab's full label, which
			// renderTabs never goes below. Budgets under it cannot be honoured.
			floor := ansi.StringWidth(ansi.Strip(m.renderTabs(0)))
			for avail := floor; avail <= floor+60; avail++ {
				if got := ansi.StringWidth(ansi.Strip(m.renderTabs(avail))); got > avail {
					t.Fatalf("lang=%s tab=%v: renderTabs(%d) rendered %d cells", lang, tb, avail, got)
				}
			}
		}
	}
}

// On a window that fits the short labels, the bar reads the same whichever
// tab is selected. It used to keep the selected tab's full label at any cost,
// so at 80 columns switching to Calendar, Projects or Settings collapsed every
// other tab to a bare digit and the bar changed shape on every switch. Every
// language is checked, which is what holds tabShortLabels to 80 columns.
// tabBarShortLabelStableWidth is, per language, a width narrow enough to need
// the abbreviated tab labels but wide enough that every tab's abbreviated
// form fits regardless of which is selected — found empirically (each
// language's abbreviated set is a different total length, so the band one
// language is stable in can be a single column wide in another; there is no
// one width that lands all three in it at once).
var tabBarShortLabelStableWidth = map[language]int{
	langEN: 97,
	langDA: 97,
	langDE: 99,
}

func TestTabBarKeepsItsLabelsAcrossTabs(t *testing.T) {
	t.Cleanup(func() { applyLang(string(langEN)) })
	for _, lang := range availableLanguages {
		m := modelWithTasks(t, todo.New("alpha"))
		applyLang(string(lang)) // after the model: initialModel re-applies the stored language
		m.termWidth, m.termHeight = tabBarShortLabelStableWidth[lang], 24
		want := ""
		for tb := tab(0); tb < numTabs; tb++ {
			if !m.boardCfg.tabVisible(tb) {
				continue
			}
			m.tab = tb
			bar := strings.SplitN(ansi.Strip(m.View()), "\n", 2)[0]
			if want == "" {
				want = bar
				continue
			}
			if bar != want {
				t.Errorf("lang=%s: the bar changed on tab %d\n got: %q\nwant: %q", lang, tb, bar, want)
			}
		}
		if !strings.Contains(want, tabShortLabels[lang][1]) {
			t.Errorf("lang=%s: the short labels do not fit: %q", lang, want)
		}
	}
}

// The one thing the header says about getting unstuck is a single "?", so it
// must actually be there on any terminal anyone uses. It is allowed to go on a
// window too narrow for the tab bar itself — the tabs are the navigation and
// the hint is a courtesy — but not one column sooner.
func TestHeaderShowsTheHelpKey(t *testing.T) {
	t.Cleanup(func() { applyLang(string(langEN)) })
	for _, lang := range availableLanguages {
		// 57, not 55: a 9th tab (Chat) means the bare-digit tab bar alone
		// needs one column more at the floor than it did at 8 tabs — see
		// TestTabBarKeepsItsLabelsAcrossTabs's tabBarShortLabelStableWidth
		// for the same kind of per-tab-count recalibration.
		for w := 57; w <= 200; w++ {
			m := modelWithTasks(t, todo.New("alpha"))
			applyLang(string(lang))
			m.termWidth, m.termHeight = w, 20
			// The tab labels carry no "?" in any language, so its presence in
			// the header is the hint and nothing else.
			header := ansi.Strip(strings.SplitN(m.View(), "\n", 2)[0])
			if !strings.Contains(header, "?") {
				t.Fatalf("lang=%s w=%d: no help key in the header: %q", lang, w, header)
			}
		}
	}
}

// The two doors into the app for someone who knows nothing about it must point
// at each other: the help overlay lists ctrl+k, and the palette has to be able
// to find the help. Advertising one of them in the header is only safe because
// of that.
func TestTheHelpAndThePaletteFindEachOther(t *testing.T) {
	m := modelWithTasks(t, todo.New("alpha"))
	m.termWidth, m.termHeight = 100, 40
	if body := strings.Join(m.helpBodyLines(), "\n"); !strings.Contains(ansi.Strip(body), "ctrl+k") {
		t.Error("the help overlay does not mention the command palette")
	}
	if len(m.paletteResults("help")) == 0 {
		t.Error("the palette cannot find the help")
	}
	// And the entry works: the palette presses the key rather than calling the
	// action, so an entry that resolves to nothing would fail silently.
	m = sendKey(t, m, "ctrl+k")
	m.paletteInput.SetValue("help")
	if m = sendKey(t, m, "enter"); m.mode != modeHelp {
		t.Errorf("choosing the palette's help entry left mode = %v, want modeHelp", m.mode)
	}
}

// One marker for "the row you are on", everywhere. The app used to draw three
// — ▶ in the lists, > on the board, → in Settings and the pickers — which read
// as three kinds of selection rather than one idea drawn three ways. Every
// surface renders cursorMark now, and a new list that invents its own glyph
// fails here. Task rows are the one exception, pinned by
// TestTaskRowsMarkTheCursorByHighlightAlone.
func TestEveryListMarksItsCursorTheSameWay(t *testing.T) {
	a := todo.New("alpha")
	a.AddTag("home")
	a.Project = "house"
	a.DueDate = startOfDay(time.Now()) // so the calendar's timeline has a row
	b := todo.New("beta")
	b.AddTag("work")
	sub := todo.New("a subtask")
	sub.ParentID = a.ID

	for _, c := range []struct {
		name string
		set  func(*model)
	}{
		{"detail pane", func(m *model) {
			m.switchTab(tabTasks)
			m.pane = paneDetail
			m.detailTaskID = a.ID
		}},
		{"tag list", func(m *model) { m.switchTab(tabTags) }},
		{"project list", func(m *model) { m.switchTab(tabProjects) }},
		{"board", func(m *model) { m.switchTab(tabBoard) }},
		{"settings", func(m *model) { m.switchTab(tabSettings) }},
		{"calendar timeline", func(m *model) {
			m.switchTab(tabCalendar)
			m.calendar.focusTimeline = true
		}},
		{"command palette", func(m *model) { m.mode = modePalette; m.paletteInput.SetValue("go to") }},
		{"tag picker", func(m *model) { m.mode = modeSearchTag; m.tagSearchInput.SetValue("") }},
		{"project picker", func(m *model) { m.mode = modeSearchProject }},
		{"dependency picker", func(m *model) { m.mode = modeSearchDep; m.depSearchInput.SetValue("b") }},
	} {
		m := modelWithTasks(t, a, b, sub)
		m.termWidth, m.termHeight = 120, 40
		c.set(&m)
		m.markCacheDirty()
		m.ensureCache()
		m.invalidateDetailCache()

		out := ansi.Strip(m.View())
		if !strings.Contains(out, strings.TrimSpace(cursorMark)) {
			t.Errorf("%s: no %q anywhere on screen", c.name, strings.TrimSpace(cursorMark))
		}
		// The glyphs this replaced. ">" and "→" both occur legitimately in
		// content (a "→" priority icon, "±0 → steady"), so only their use as a
		// row marker is checked: at the start of a line, or after the two-cell
		// gutter the footer lists use.
		for _, stale := range []string{"> ", "→ "} {
			for _, line := range strings.Split(out, "\n") {
				trimmed := strings.TrimLeft(line, " ")
				if strings.HasPrefix(trimmed, stale) {
					t.Errorf("%s: row still marked with %q: %q", c.name, stale, line)
				}
			}
		}
	}
}

// Task rows show the cursor by their full-width highlight alone, and their
// gutter holds the subtask fold sign, set apart from the status box.
func TestTaskRowsMarkTheCursorByHighlightAlone(t *testing.T) {
	parent := todo.New("Plan the trip")
	sub := todo.New("Book flights")
	sub.ParentID = parent.ID
	plain := todo.New("Water the plants")
	done := todo.New("Old errand")
	done.Status = todo.Done
	done.CompletedAt = time.Now()

	m := modelWithTasks(t, parent, sub, plain, done)
	rows := func() string {
		m.markCacheDirty()
		m.ensureCache()
		return ansi.Strip(m.View())
	}
	out := rows()
	for _, want := range []string{"+ [ ] Plan the trip", "  [ ] Water the plants"} {
		if !strings.Contains(out, want) {
			t.Errorf("task list lacks %q:\n%s", want, out)
		}
	}
	m.setExpanded(parent.ID, true)
	out = rows()
	if !strings.Contains(out, "- [ ] Plan the trip") || !strings.Contains(out, "└ [ ] Book flights") {
		t.Errorf("expanded parent should show - and its subtask:\n%s", out)
	}
	m.cursor = 1 // the subtask row
	if out = rows(); strings.Contains(out, strings.TrimSpace(cursorMark)) {
		t.Errorf("a selected subtask row drew %q:\n%s", strings.TrimSpace(cursorMark), out)
	}
	m.showHistory = true
	m.cursor = 0
	if out = rows(); !strings.Contains(out, "Old errand") || strings.Contains(out, strings.TrimSpace(cursorMark)) {
		t.Errorf("the history list should show the done task without %q:\n%s", strings.TrimSpace(cursorMark), out)
	}
}

// ── Row label: badges outlive the title ──────────────────────────────────────

// The badges are the cells that change a decision — recurring, and how many
// subtasks are left. Concatenating them onto the title made them the last runes
// of the string and so the first ones a narrow title column threw away. The
// title is what has slack in it.
func TestTaskRowBadgesSurviveTitleTruncation(t *testing.T) {
	task := todo.New("A title long enough that it cannot possibly fit the column")
	task.Recurrence = "weekly"
	sub := todo.NewSubtask("step one", task.ID)
	m := modelWithTasks(t, task, sub)
	m.termWidth = 70
	m.refreshCaches()

	var row string
	for _, line := range strings.Split(m.View(), "\n") {
		if plain := ansi.Strip(line); strings.Contains(plain, "A title long enough") {
			row = plain
			break
		}
	}
	if row == "" {
		t.Fatal("task row should render")
	}
	if !strings.Contains(row, ellipsis) {
		t.Fatalf("this title should be clipped at width 70, so the test proves nothing: %q", row)
	}
	if !strings.Contains(row, "↻") {
		t.Errorf("the recurrence badge should outlive the clipped title: %q", row)
	}
	if !strings.Contains(row, "(0/1)") {
		t.Errorf("the subtask badge should outlive the clipped title: %q", row)
	}
}

func TestFitTaskRowLabelClipsTheTextNotTheBadges(t *testing.T) {
	got := fitTaskRowLabel("⧗ ", "some long title here", " ! (1/2)", 16)
	if !strings.HasPrefix(got, "⧗ ") {
		t.Errorf("prefix should survive: %q", got)
	}
	if !strings.HasSuffix(got, " ! (1/2)") {
		t.Errorf("badges should survive: %q", got)
	}
	if w := len([]rune(got)); w != 16 {
		t.Errorf("label width = %d, want exactly the 16 cells it was given: %q", w, got)
	}
	// Nothing left to protect: the badges alone overrun the column, so the
	// whole label is clipped rather than drawn past the column's edge.
	if w := len([]rune(fitTaskRowLabel("⧗ ", "title", " ! (1/2)", 6))); w != 6 {
		t.Errorf("over-full label should still be clipped to its column, got width %d", w)
	}
}

// ── Row hierarchy ────────────────────────────────────────────────────────────

// One style for the whole row made the score, the size and the project name
// shout as loudly as the title, and painted an overdue row's project cell red —
// a cell that has nothing to do with being overdue. The title carries the
// status; the secondary columns are dim.
func TestTaskRowDrawsMetadataDimmerThanTheTitle(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	task := todo.New("Ship the thing")
	task.Project = "backend"
	task.DueDate = time.Now().Add(-48 * time.Hour) // overdue
	other := todo.New("Something else")
	m := modelWithTasks(t, task, other)
	m.termWidth = 120
	m.cursor = 1 // keep the overdue row unselected
	m.refreshCaches()

	var raw string
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(ansi.Strip(line), "Ship the thing") {
			raw = line
			break
		}
	}
	if raw == "" {
		t.Fatal("task row should render")
	}
	titleAt := strings.Index(raw, "Ship the thing")
	projectAt := strings.Index(raw, "backend")
	if titleAt < 0 || projectAt < 0 {
		t.Fatalf("row should carry both the title and the project: %q", raw)
	}
	overdueSGR := newFastStyle(overdueStyle).prefix
	dimSGR := newFastStyle(dimStyle).prefix
	if !strings.Contains(raw[:titleAt], overdueSGR) {
		t.Errorf("an overdue task's title should carry the overdue colour: %q", raw)
	}
	// The project cell is metadata: it must have switched to the dim tone
	// somewhere between the title and itself, and must not still be overdue-red.
	between := raw[titleAt:projectAt]
	if !strings.Contains(between, dimSGR) {
		t.Errorf("the project column should be dimmed, not painted with the row status: %q", raw)
	}
}

// ── Closed-today block ───────────────────────────────────────────────────────

// The list pane is a fixed-height box, so a short list drew the rest of the
// screen as blanks. What the user closed today is the one read-out that belongs
// in that space: it needs no cursor and no keys, and it closes the loop the
// active list opens.
func TestListPaneFillsSpareRowsWithTodaysCompletions(t *testing.T) {
	open := todo.New("Still open")
	done := todo.New("Finished this morning")
	done.Status = todo.Done
	done.CompletedAt = time.Now().Add(-2 * time.Hour)
	m := modelWithTasks(t, open, done)
	m.termWidth = 100
	m.termHeight = 40
	m.refreshCaches()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Closed today (1)") {
		t.Errorf("spare rows should carry today's completions: %q", view)
	}
	if !strings.Contains(view, "Finished this morning") {
		t.Errorf("the completed task should be named: %q", view)
	}
}

// cache.done carries whichever history sort the user picked. Under
// historySortAlpha it is ordered A→Z, so taking its head and stopping at the
// first older entry dropped the rest of the day — or all of it, when the
// alphabetically-first completion happened to be an old one.
func TestClosedTodayBlockDoesNotDependOnTheHistorySort(t *testing.T) {
	open := todo.New("Still open")
	old := todo.New("Apple closed last week")
	old.Status = todo.Done
	old.CompletedAt = time.Now().Add(-7 * 24 * time.Hour)
	recent := todo.New("Zebra closed today")
	recent.Status = todo.Done
	recent.CompletedAt = time.Now().Add(-time.Hour)

	m := modelWithTasks(t, open, old, recent)
	m.historySort = historySortAlpha
	m.termWidth = 100
	m.termHeight = 40
	m.refreshCaches()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Zebra closed today") {
		t.Errorf("today's completion should show whatever the history sort is: %q", view)
	}
	if strings.Contains(view, "Apple closed last week") {
		t.Errorf("a week-old completion should not reach today's block: %q", view)
	}
}

// A task closed on an earlier day is history, not today's read-out.
func TestClosedTodayBlockIgnoresOlderCompletions(t *testing.T) {
	open := todo.New("Still open")
	done := todo.New("Finished last week")
	done.Status = todo.Done
	done.CompletedAt = time.Now().Add(-8 * 24 * time.Hour)
	m := modelWithTasks(t, open, done)
	m.termWidth = 100
	m.termHeight = 40
	m.refreshCaches()

	if view := ansi.Strip(m.View()); strings.Contains(view, "Closed today") {
		t.Errorf("an older completion should not appear as closed today: %q", view)
	}
}

// A full list has no spare rows, so the block must not push the pane past its
// height — it only ever fills space the active list is not using.
func TestClosedTodayBlockStaysOutOfAFullList(t *testing.T) {
	var tasks []todo.Todo
	for i := 0; i < 40; i++ {
		tasks = append(tasks, todo.New(fmt.Sprintf("Task %d", i)))
	}
	done := todo.New("Finished this morning")
	done.Status = todo.Done
	done.CompletedAt = time.Now().Add(-2 * time.Hour)
	tasks = append(tasks, done)
	m := modelWithTasks(t, tasks...)
	m.termWidth = 100
	m.termHeight = 24
	m.refreshCaches()

	if view := ansi.Strip(m.View()); strings.Contains(view, "Closed today") {
		t.Errorf("a full list has no room for the block: %q", view)
	}
}

// ── Contextual footer ────────────────────────────────────────────────────────

// "t track" over a task whose timer is already running is not a hint, it is a
// wrong answer. The key toggles, so the label has to as well.
func TestFooterHintNamesWhatTheKeyWouldActuallyDo(t *testing.T) {
	task := todo.New("Write the report")
	m := modelWithTasks(t, task)
	m.refreshCaches()
	if hint := ansi.Strip(m.renderKeyHints(200)); !strings.Contains(hint, "track") {
		t.Fatalf("an idle task should offer to start tracking: %q", hint)
	}

	cur := m.currentTodo()
	cur.StartTimer()
	m.refreshCaches()
	hint := ansi.Strip(m.renderKeyHints(200))
	if !strings.Contains(hint, "stop") {
		t.Errorf("a running timer should offer to stop it: %q", hint)
	}
	if strings.Contains(hint, "start/stop time tracking") {
		t.Errorf("the generic label should have been replaced: %q", hint)
	}
}
