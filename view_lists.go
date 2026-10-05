package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/rank"
	"github.com/Iliorn/tjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ── Tags list ─────────────────────────────────────────────────────────────────

func (m model) renderTagList() string {
	tags := m.getFilteredTagsForTab()

	if len(tags) == 0 {
		switch {
		case m.tagTabSearchQuery != "":
			return normalStyle.Render(tr("  No tags match your filter."))
		case len(m.cache.tagGroups) > 0:
			return m.nothingOpenNote(tr("  Every tag is finished."))
		}
		return strings.Join([]string{
			normalStyle.Render(tr("  No tags yet. Add tags to tasks in the detail view.")),
			dimStyle.Render(tr("  Tags group related tasks; this tab shows what is open in each.")),
		}, "\n")
	}

	return m.renderGroupRows(groupRows{
		nameHdr:    tr("Tag"),
		keys:       tags,
		sums:       m.cache.tagGroups,
		labelStyle: tagStyle,
		cursor:     m.tagTabCursor,
		start:      m.listOffset,
		count:      m.tagListVisibleRows(),
		label: func(key string) string {
			if key == untaggedKey {
				return tr("(untagged)")
			}
			return "#" + key
		},
		editing: func(key, lead, label string) (string, bool) {
			if m.mode != modeEditTag || m.editingTagName != key {
				return "", false
			}
			return tagSelectedStyle.Render(lead+label+" ") + m.textInput.View(), true
		},
	})
}

// nothingOpenNote is what a Tags or Projects list says when hiding the
// finished groups has left it empty — which reads as an empty store unless it
// says where the rest went.
func (m model) nothingOpenNote(headline string) string {
	return normalStyle.Render(headline) + "\n" +
		dimStyle.Render(fmt.Sprintf(tr("  %s shows the finished ones."), effectiveKey("history", "h")))
}

// groupRows is one Tags or Projects list to draw.
type groupRows struct {
	nameHdr    string
	keys       []string
	sums       map[string]*groupSummary
	label      func(key string) string
	labelStyle lipgloss.Style
	// cursor is the selected row; start and count window the list.
	cursor, start, count int
	// editing draws the row being renamed in place, when it is this one.
	editing func(key, lead, label string) (string, bool)
}

// renderGroupRows draws a Tags or Projects list: the group, how much is open
// in it (and late, when anything anywhere is), when it last moved, and the task
// to do next in it. The next-up title takes whatever width is left, which is
// most of it, and drops out whole on a window too narrow to say anything with.
// A finished group — shown only after h — is drawn dim throughout.
func (m model) renderGroupRows(g groupRows) string {
	b := getBuilder()
	defer putBuilder(b)

	gap := strings.Repeat(" ", listColGap)
	openHdr, lateHdr, lastHdr, nextHdr := tr("Open"), tr("Overdue"), tr("Last"), tr("Next up")
	labelMax, openW, lateW, lastW := 0, runeLen(openHdr), runeLen(lateHdr), runeLen(lastHdr)
	anyLate := false
	for _, key := range g.keys {
		s := g.sums[key]
		labelMax = max(labelMax, runeLen(g.label(key)))
		openW = max(openW, len(strconv.Itoa(s.open)))
		lastW = max(lastW, runeLen(formatSince(s.last, m.frameTime)))
		anyLate = anyLate || s.overdue > 0
	}
	nameW := contentFitWidth(m.termWidth, labelMax, listColGap, runeLen(g.nameHdr)+listColGap)
	avail := m.termWidth - 8
	used := len(cursorGap) + nameW + openW + listColGap + lastW + listColGap
	if anyLate {
		used += lateW + listColGap
	}
	nextW := avail - used
	showNext := nextW >= runeLen(nextHdr)

	header := cursorGap + padRight(g.nameHdr, nameW) + padLeft(openHdr, openW) + gap
	if anyLate {
		header += padLeft(lateHdr, lateW) + gap
	}
	header += padLeft(lastHdr, lastW) + gap
	if showNext {
		header += nextHdr
	}
	b.WriteString(headerStyle.Render(padRight(header, avail)) + "\n")

	end := min(g.start+g.count, len(g.keys))
	for i := max(g.start, 0); i < end; i++ {
		key := g.keys[i]
		s := g.sums[key]
		lead := cursorGap
		if i == g.cursor {
			lead = cursorMark
		}
		label := g.label(key)
		if row, ok := g.editing(key, lead, label); ok {
			b.WriteString(row + "\n")
			continue
		}
		name := padRight(truncate(label, nameW-1), nameW)
		open, late, next := strconv.Itoa(s.open), "─", s.nextTitle
		if s.overdue > 0 {
			late = strconv.Itoa(s.overdue)
		}
		if s.finished() {
			open, next = "─", ""
		}
		openCell := padLeft(open, openW) + gap
		lateCell := ""
		if anyLate {
			lateCell = padLeft(late, lateW) + gap
		}
		lastCell := padLeft(formatSince(s.last, m.frameTime), lastW) + gap
		nextCell := ""
		if showNext {
			nextCell = truncate(next, nextW)
		}

		switch {
		case i == g.cursor:
			b.WriteString(selectedStyle.Render(padRight(lead+name+openCell+lateCell+lastCell+nextCell, avail)) + "\n")
		case s.finished():
			b.WriteString(doneCountStyle.Render(lead+name+openCell+lateCell+lastCell) + "\n")
		default:
			lateStyle := dimStyle
			if s.overdue > 0 {
				lateStyle = overdueCountStyle
			}
			b.WriteString(g.labelStyle.Render(lead+name) +
				activeCountStyle.Render(openCell) +
				lateStyle.Render(lateCell) +
				dimStyle.Render(lastCell) +
				normalStyle.Render(nextCell) + "\n")
		}
	}
	return b.String()
}

func runeLen(s string) int { return len([]rune(s)) }

// renderGroupTaskRows draws rows from..from+count of a group's task list
// (groupTaskList) with the Tasks tab's own row renderer, so a task reads the
// same wherever you meet it. An open subtask listed under its open parent is
// drawn as the Tasks tab draws an unfolded one. sel is the drill cursor, or -1
// when the list is only a preview. The Project column is left out where every
// row would repeat the same name.
func (m model) renderGroupTaskRows(tasks []todo.Todo, from, count, sel int, showProject bool) []string {
	b := getBuilder()
	defer putBuilder(b)

	cols, nested := m.groupTaskCols(tasks, showProject)
	pos := ""
	if sel >= 0 {
		pos = listPosLabel(sel, len(tasks))
	}
	renderListHeaderTitled(b, m.termWidth, false, cols, pos, tr("Tasks"))

	siblings := make(map[string]int)
	for i := range tasks {
		if nested[i] {
			siblings[tasks[i].ParentID]++
		}
	}
	seen := make(map[string]int)
	end := min(from+count, len(tasks))
	for i := range tasks[:end] {
		t := tasks[i]
		if nested[i] {
			idx := seen[t.ParentID]
			seen[t.ParentID]++
			if i >= from {
				b.WriteString(m.renderSubtaskLine(&t, idx, siblings[t.ParentID], cols, i, sel, sel >= 0))
			}
			continue
		}
		if i >= from {
			b.WriteString(m.renderTaskLineWithSet(&t, i, sel, sel >= 0, m.cache.overdueSet, cols))
		}
	}
	return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
}

// groupTaskCols sizes the columns of a group's task list to the tasks it holds
// at the model's width, and reports which rows are nested subtasks.
func (m model) groupTaskCols(tasks []todo.Todo, showProject bool) (listCols, []bool) {
	nested := m.groupNestedRows(tasks)
	contentMax, tagsMax, projectMax := 0, 0, 0
	hasDue := false
	for i := range tasks {
		// The width each row draws: a task row's whole label (badges
		// included), a subtask row's title behind its indent.
		if nested[i] {
			w := runeLen(tasks[i].Title) + subtaskIndentW
			if tasks[i].IsTimerRunning() {
				w += 2 // the ⧗ in front of the title
			}
			contentMax = max(contentMax, w)
		} else {
			contentMax = max(contentMax, taskRowLabelWidth(m.taskRowLabel(&tasks[i])))
		}
		tagsMax = max(tagsMax, rowTagsWidth(tasks[i].Tags))
		hasDue = hasDue || !tasks[i].DueDate.IsZero()
		if showProject {
			projectMax = max(projectMax, runeLen(tasks[i].Project))
		}
	}
	return taskListCols(m.termWidth, false, contentMax, tagsMax, hasDue, dueColMax(tasks, m.frameTime), projectMax), nested
}

// groupFoldNote is the line the done tasks of a group fold into while
// finished work is hidden, or "" when there is nothing folded.
func (m model) groupFoldNote(s *groupSummary) string {
	if m.showFinishedGroups || s == nil || s.done == 0 {
		return ""
	}
	return dimStyle.Render(trCount("  ✓ %d done · %s shows them", s.done, s.done, effectiveKey("history", "h")))
}

// groupPaneHead is the top of the pane under a Tags or Projects list: the
// group's counts, and a small bar of how much of it is done.
func (m model) groupPaneHead(s *groupSummary, availW int) []string {
	if s == nil {
		return nil
	}
	lines := []string{normalStyle.Render(truncate("  "+trCount("%d open", s.open, s.open)+" · "+trCount("%d overdue", s.overdue, s.overdue)+" · "+trCount("%d done", s.done, s.done), availW))}
	if total := s.open + s.done; total > 0 {
		pct := float64(s.done) / float64(total)
		lines = append(lines, "  "+renderProgressBar(pct, groupBarWidth)+normalStyle.Render(fmt.Sprintf(" %3d%%", int(pct*100))))
	}
	return lines
}

// progressEighths maps a sub-cell fill (1–7 eighths) to its block element, so
// a bar moves in eighths of a cell rather than whole cells.
var progressEighths = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// renderProgressBar draws pct (0–1) as a barW-cell bar in tagProgressGradient,
// the unfilled rest as the faint track. Cells sharing a gradient step render
// as one run.
func renderProgressBar(pct float64, barW int) string {
	var b strings.Builder
	grad := len(tagProgressGradient)
	gradAt := func(pos float64) lipgloss.Style {
		return tagProgressGradient[min(int(pos*float64(grad-1)), grad-1)]
	}
	eighths := min(int(math.Round(pct*float64(barW)*8)), barW*8)
	filled, partial := eighths/8, eighths%8
	run, runIdx := 0, -1
	for j := 0; j < filled; j++ {
		pos := 0.0
		if filled > 1 {
			pos = float64(j) / float64(filled-1)
		}
		idx := min(int(pos*float64(grad-1)), grad-1)
		if idx != runIdx && run > 0 {
			b.WriteString(tagProgressGradient[runIdx].Render(strings.Repeat("█", run)))
			run = 0
		}
		runIdx = idx
		run++
	}
	if run > 0 {
		b.WriteString(tagProgressGradient[runIdx].Render(strings.Repeat("█", run)))
	}
	empty := barW - filled
	if partial > 0 && filled < barW {
		// The partial glyph's unfilled part is its background, so it takes
		// the track's tint and the fill runs straight into it.
		b.WriteString(gradAt(float64(filled) / float64(barW)).Inherit(barTrackStyle).Render(progressEighths[partial]))
		empty--
	}
	if empty > 0 {
		b.WriteString(barTrackStyle.Render(strings.Repeat(barTrack, empty)))
	}
	return b.String()
}

// ── Stats ─────────────────────────────────────────────────────────────────────

// statsScopedTodos returns the task set the Stats tab aggregates: everything
// when no search is active, otherwise the top-level tasks matching the query
// (same compileSearch grammar as the Tasks list — #tag, @project, free text).
// Subtasks are dropped from the filtered form; every stats bucket reads
// top-level rows anyway, and the active `/query` chip already tells the user
// the page is scoped.
func (m model) statsScopedTodos() []*todo.Todo {
	all := m.allTodos()
	if m.searchQuery == "" {
		return all
	}
	match := compileSearch(m.searchQuery)
	scoped := make([]*todo.Todo, 0, len(all))
	for _, t := range all {
		if t.ParentID == "" && match(*t) {
			scoped = append(scoped, t)
		}
	}
	return scoped
}

// statsLabelCell is a Stats label padded to the label column, clipped so at
// least one space is left before the value: a translated label longer than
// the column would otherwise run straight into its number.
func statsLabelCell(label string) string {
	return padRight(truncate(label, statsLabelWidth-1), statsLabelWidth)
}

func (m model) renderStatsList() string {
	b := getBuilder()
	defer putBuilder(b)

	now := m.frameTime
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	weekAhead := today.AddDate(0, 0, 7)
	weekAgo := today.AddDate(0, 0, -7)
	twoWeeksAgo := today.AddDate(0, 0, -14)
	monthAgo := today.AddDate(0, -1, 0)
	twoMonthsAgo := today.AddDate(0, -2, 0)

	var activeTasks, overdueTasks, dueToday, dueThisWeek int
	var doneToday, doneThisWeek, doneThisMonth, doneLastWeek, donePrevMonth int
	var createdThisWeek, createdThisMonth int
	var highPri, medPri, lowPri int
	var timeToDone []time.Duration
	var activeAges []time.Duration
	var oldestAge time.Duration
	oldestTitle := ""
	// Cycle time (start date→completed) of every completed task that had a
	// start date, plus the count of pending tasks, both bucketed by size
	// (index = int(todo.Size)). A task can be created well before it's started,
	// so we measure from StartDate, not CreatedAt; tasks with no start date
	// don't contribute. Feed the "Cycle time by size" and "Projected backlog
	// clear" blocks below.
	var cycleBySize [3][]time.Duration
	var pendingBySize [3]int

	scope := m.statsScopedTodos()
	for _, t := range scope {
		if t.ParentID != "" {
			continue
		}
		if !t.CreatedAt.Before(weekAgo) {
			createdThisWeek++
		}
		if !t.CreatedAt.Before(monthAgo) {
			createdThisMonth++
		}
		if t.Status == todo.Done {
			if !t.CompletedAt.IsZero() {
				if i := int(t.Size); i >= 0 && i < len(cycleBySize) && !t.StartDate.IsZero() {
					// Skip anomalies where completion predates the start date.
					if d := t.CompletedAt.Sub(t.StartDate); d >= 0 {
						cycleBySize[i] = append(cycleBySize[i], d)
					}
				}
				if !t.CompletedAt.Before(today) {
					doneToday++
				}
				if !t.CompletedAt.Before(weekAgo) {
					doneThisWeek++
				}
				if !t.CompletedAt.Before(twoWeeksAgo) && t.CompletedAt.Before(weekAgo) {
					doneLastWeek++
				}
				if !t.CompletedAt.Before(monthAgo) {
					doneThisMonth++
					timeToDone = append(timeToDone, t.CompletedAt.Sub(t.CreatedAt))
				}
				if !t.CompletedAt.Before(twoMonthsAgo) && t.CompletedAt.Before(monthAgo) {
					donePrevMonth++
				}
			}
		} else {
			activeTasks++
			if i := int(t.Size); i >= 0 && i < len(pendingBySize) {
				pendingBySize[i]++
			}
			age := now.Sub(t.CreatedAt)
			activeAges = append(activeAges, age)
			if age > oldestAge {
				oldestAge = age
				oldestTitle = t.Title
			}
			switch {
			case t.IsOverdue():
				overdueTasks++
			case !t.DueDate.IsZero() && t.DueDate.Before(tomorrow):
				dueToday++
			case !t.DueDate.IsZero() && t.DueDate.Before(weekAhead):
				dueThisWeek++
			}
			switch t.Priority {
			case todo.PriorityHigh:
				highPri++
			case todo.PriorityMedium:
				medPri++
			default:
				lowPri++
			}
		}
	}

	// Median cycle time per size, computed once (medianDuration sorts in place)
	// and shared by the cycle-time and projection blocks.
	var medBySize [3]time.Duration
	var haveMed [3]bool
	for i := range cycleBySize {
		if len(cycleBySize[i]) > 0 {
			medBySize[i] = medianDuration(cycleBySize[i])
			haveMed[i] = true
		}
	}

	availW := m.termWidth - 8
	gradLen := len(statsGradient)

	// Lay sections out in up to three columns when there's room, so the page
	// stays short enough to fit a not-very-tall screen. minColW is only where
	// the search starts: the sections are built as thunks over colW/valW, and
	// the layout below takes the most columns whose lines all fit.
	const gap = 4
	const minColW = 37
	maxCols := (availW + gap) / (minColW + gap)
	if maxCols < 1 {
		maxCols = 1
	}
	if maxCols > 3 {
		maxCols = 3
	}
	var cols, colW, valW int
	setCols := func(n int) {
		cols, colW, valW = n, availW, statsValueWidth
		if n > 1 {
			colW = (availW - (n-1)*gap) / n
			valW = 5
		}
	}

	// stat writes one row sized to colW into sb (bar only if it fits).
	stat := func(sb *strings.Builder, label string, value, total int, showBar bool) {
		labelStr := statsLabelCell("  " + label)
		valStr := fmt.Sprintf("%d", value)
		barW := colW - statsLabelWidth - valW - 6
		if barW > statsBarWidth {
			barW = statsBarWidth
		}
		if !showBar || total <= 0 || barW < 6 {
			sb.WriteString(detailLabelStyle.Render(labelStr) + normalStyle.Render(valStr) + "\n")
			return
		}
		pct := float64(value) / float64(total)
		filled := int(pct * float64(barW))
		if filled > barW {
			filled = barW
		}
		var bar strings.Builder
		bar.Grow(barW * 4)
		for j := 0; j < filled; j++ {
			pos := 0.0
			if filled > 1 {
				pos = float64(j) / float64(filled-1)
			}
			gradIdx := int(pos * float64(gradLen-1))
			if gradIdx >= gradLen {
				gradIdx = gradLen - 1
			}
			bar.WriteString(statsGradient[gradIdx].Render("█"))
		}
		// The track shares one style for its whole run, so it costs one Render
		// call rather than one per cell (ARCHITECTURE.md, "Group same-style
		// runs").
		if empty := barW - filled; empty > 0 {
			bar.WriteString(barTrackStyle.Render(strings.Repeat(barTrack, empty)))
		}
		// Rounded, not truncated: 6 of 9 is 67%, and truncating each share of
		// a breakdown makes its rows add up to less than 100.
		sb.WriteString(detailLabelStyle.Render(labelStr) + normalStyle.Render(padRight(valStr, valW)) +
			bar.String() + dimStyle.Render(fmt.Sprintf(" %3.0f%%", pct*100)) + "\n")
	}

	section := func(build func(*strings.Builder)) func() string {
		return func() string {
			var sb strings.Builder
			build(&sb)
			return strings.TrimRight(sb.String(), "\n")
		}
	}

	workload := section(func(sb *strings.Builder) {
		sb.WriteString(statsHeaderStyle.Render(tr("  Workload")) + "\n")
		if overdueTasks > 0 {
			sb.WriteString(detailLabelStyle.Render(statsLabelCell("  "+tr("Overdue"))) +
				overdueCountStyle.Render(fmt.Sprintf("%d", overdueTasks)) + "\n")
		} else {
			stat(sb, tr("Overdue"), 0, 0, false)
		}
		stat(sb, tr("Due today"), dueToday, 0, false)
		stat(sb, tr("Due this week"), dueThisWeek, 0, false)
		stat(sb, tr("Active total"), activeTasks, 0, false)
		// How often a completed task sat in the engine's top-5 at close —
		// the feedback loop for tuning the sequence biases. Hidden until
		// rank-stamped completions exist.
		if hits, rated := rank.HitStats(scope, rank.HitWindow); rated > 0 {
			stat(sb, tr("Seq hit (top-5)"), hits, rated, true)
		}
	})

	// flowSection renders a created/completed/net-backlog block with a trend
	// comparison against the previous equal-length period.
	flowSection := func(title, vsLabel string, created, completed, prevCompleted int) func() string {
		return section(func(sb *strings.Builder) {
			sb.WriteString(statsHeaderStyle.Render("  "+title) + "\n")
			stat(sb, tr("Created"), created, 0, false)
			stat(sb, tr("Completed"), completed, 0, false)
			net := created - completed
			netLabel := detailLabelStyle.Render(statsLabelCell(tr("  Net backlog")))
			switch {
			case net > 0:
				sb.WriteString(netLabel + overdueCountStyle.Render(fmt.Sprintf(tr("+%d ▲ growing"), net)) + "\n")
			case net < 0:
				sb.WriteString(netLabel + activeCountStyle.Render(fmt.Sprintf(tr("%d ▼ shrinking"), net)) + "\n")
			default:
				sb.WriteString(netLabel + dimStyle.Render(tr("±0 → steady")) + "\n")
			}
			trendArrow := "→"
			if completed > prevCompleted {
				trendArrow = "↑"
			} else if completed < prevCompleted {
				trendArrow = "↓"
			}
			sb.WriteString(detailLabelStyle.Render(statsLabelCell("  "+vsLabel)) +
				normalStyle.Render(fmt.Sprintf(tr("%d done vs %d  %s"), completed, prevCompleted, trendArrow)) + "\n")
		})
	}

	flow := flowSection(tr("Flow (last 7 days)"), tr("vs last week"), createdThisWeek, doneThisWeek, doneLastWeek)
	flow30 := flowSection(tr("Flow (last 30 days)"), tr("vs prior 30d"), createdThisMonth, doneThisMonth, donePrevMonth)

	throughput := section(func(sb *strings.Builder) {
		sb.WriteString(statsHeaderStyle.Render(tr("  Throughput")) + "\n")
		ttdLabel := detailLabelStyle.Render(statsLabelCell(tr("  Time to done (30d)")))
		if len(timeToDone) > 0 {
			sb.WriteString(ttdLabel + normalStyle.Render(tr("median ")+formatDaysCompact(medianDuration(timeToDone))) + "\n")
		} else {
			sb.WriteString(ttdLabel + dimStyle.Render(tr("none yet")) + "\n")
		}
		if len(activeAges) > 0 {
			sb.WriteString(detailLabelStyle.Render(statsLabelCell(tr("  Median active age"))) +
				normalStyle.Render(formatDaysCompact(medianDuration(activeAges))) + "\n")
			// The title takes what the column has left after its age. In a
			// multi-column layout it asks for at least statsOldestMinW, which
			// overflows a column too narrow to say which task it is — and an
			// overflowing line is what sends the layout down a column.
			age := " (" + formatDaysCompact(oldestAge) + ")"
			oldestW := colW - statsLabelWidth - len([]rune(age))
			if want := min(len([]rune(oldestTitle)), statsOldestMinW); cols > 1 && oldestW < want {
				oldestW = want
			}
			sb.WriteString(detailLabelStyle.Render(statsLabelCell(tr("  Oldest active"))) +
				normalStyle.Render(truncate(oldestTitle, oldestW)) +
				dimStyle.Render(age) + "\n")
		}
	})

	priority := func() string { return "" }
	if activeTasks > 0 {
		priority = section(func(sb *strings.Builder) {
			sb.WriteString(statsHeaderStyle.Render(tr("  Active by priority")) + "\n")
			stat(sb, tr("↑ High"), highPri, activeTasks, true)
			stat(sb, tr("→ Medium"), medPri, activeTasks, true)
			stat(sb, tr("↓ Low"), lowPri, activeTasks, true)
		})
	}

	velocity := section(func(sb *strings.Builder) {
		sb.WriteString(statsHeaderStyle.Render(tr("  Completion velocity")) + "\n")
		stat(sb, tr("Today"), doneToday, 0, false)
		stat(sb, tr("This week"), doneThisWeek, 0, false)
		stat(sb, tr("This month"), doneThisMonth, 0, false)
		if doneThisWeek > 0 {
			sb.WriteString(detailLabelStyle.Render(statsLabelCell(tr("  Avg (7d)"))) +
				normalStyle.Render(fmt.Sprintf(tr("%.1f tasks/day"), float64(doneThisWeek)/7.0)) + "\n")
		}
	})

	// Size rows share one order (Small, Medium, Large) across both size blocks.
	sizeRows := []struct {
		label string
		idx   int
	}{
		{tr("Small"), int(todo.SizeSmall)},
		{tr("Medium"), int(todo.SizeMedium)},
		{tr("Large"), int(todo.SizeLarge)},
	}

	// Median calendar time from start date to completion, per size — the "how
	// long does a task of this size actually take once started" cue that feeds
	// the estimate. Only started+completed tasks contribute.
	cycleTime := section(func(sb *strings.Builder) {
		sb.WriteString(statsHeaderStyle.Render(tr("  Cycle time by size")) + "\n")
		for _, s := range sizeRows {
			label := detailLabelStyle.Render(statsLabelCell("  " + s.label))
			if haveMed[s.idx] {
				sb.WriteString(label + normalStyle.Render(formatDaysCompact(medBySize[s.idx])) +
					dimStyle.Render(fmt.Sprintf(" (n=%d)", len(cycleBySize[s.idx]))) + "\n")
			} else {
				sb.WriteString(label + dimStyle.Render(tr("none yet")) + "\n")
			}
		}
	})

	// Rough ETA to clear the pending backlog: median cycle time × pending count
	// per size, summed. Serial estimate (assumes one task finished after the
	// next), so it's an upper-bound feel, not a schedule.
	projection := section(func(sb *strings.Builder) {
		sb.WriteString(statsHeaderStyle.Render(tr("  Projected backlog clear")) + "\n")
		var total time.Duration
		haveTotal := false
		for _, s := range sizeRows {
			n := pendingBySize[s.idx]
			if n == 0 {
				continue
			}
			label := detailLabelStyle.Render(statsLabelCell("  " + s.label))
			if !haveMed[s.idx] {
				sb.WriteString(label + dimStyle.Render(fmt.Sprintf(tr("%d pending, no pace"), n)) + "\n")
				continue
			}
			sub := time.Duration(n) * medBySize[s.idx]
			total += sub
			haveTotal = true
			sb.WriteString(label + normalStyle.Render(fmt.Sprintf("%d×%s=%s",
				n, formatDaysCompact(medBySize[s.idx]), formatDaysCompact(sub))) + "\n")
		}
		totalLabel := detailLabelStyle.Render(statsLabelCell(tr("  Projected clear")))
		if haveTotal {
			sb.WriteString(totalLabel + normalStyle.Render("~"+formatDaysCompact(total)) + "\n")
		} else {
			sb.WriteString(totalLabel + dimStyle.Render(tr("none yet")) + "\n")
		}
	})

	for n := maxCols; n > 1; n-- {
		setCols(n)
		var columns [][]string
		if n == 3 {
			// Keep the two Flow windows together in the middle column.
			columns = [][]string{
				stackSections(workload(), throughput(), cycleTime()),
				stackSections(flow(), flow30(), projection()),
				stackSections(priority(), velocity()),
			}
		} else {
			columns = [][]string{
				stackSections(workload(), flow(), flow30(), cycleTime()),
				stackSections(throughput(), priority(), velocity(), projection()),
			}
		}
		if linesFit(colW, columns...) {
			b.WriteString(zipColumns(colW, gap, columns...))
			return b.String()
		}
	}
	setCols(1)
	first := true
	for _, sec := range []func() string{workload, flow, flow30, throughput, cycleTime, priority, velocity, projection} {
		s := sec()
		if strings.TrimSpace(s) == "" {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		b.WriteString(s + "\n")
		first = false
	}

	return b.String()
}

// linesFit reports whether every line of every column is at most w cells wide.
func linesFit(w int, columns ...[]string) bool {
	for _, col := range columns {
		for _, line := range col {
			if ansi.StringWidth(line) > w {
				return false
			}
		}
	}
	return true
}

// stackSections concatenates non-empty section blocks into a line slice with a
// blank line between each.
func stackSections(sections ...string) []string {
	var lines []string
	for _, s := range sections {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(s, "\n")...)
	}
	return lines
}

// zipColumns places line slices side by side, each padded (ANSI-aware) to colW
// with a gap between. Every column but the last is truncated to colW so a long
// line can never bleed into its neighbour.
func zipColumns(colW, gap int, columns ...[]string) string {
	widths := make([]int, len(columns))
	for i := range widths {
		widths[i] = colW
	}
	return zipColumnsW(widths, gap, columns...)
}

// zipColumnsW is zipColumns with a width per column, for layouts whose columns
// are sized to what they hold rather than to an equal share.
func zipColumnsW(widths []int, gap int, columns ...[]string) string {
	maxLen := 0
	for _, col := range columns {
		if len(col) > maxLen {
			maxLen = len(col)
		}
	}
	var b strings.Builder
	pad := strings.Repeat(" ", gap)
	for i := 0; i < maxLen; i++ {
		for c, col := range columns {
			line := ""
			if i < len(col) {
				line = col[i]
			}
			if c == len(columns)-1 {
				b.WriteString(strings.TrimRight(line, " "))
				continue
			}
			w := 0
			if c < len(widths) {
				w = widths[c]
			}
			line = ansi.Truncate(line, w, "")
			if lw := ansi.StringWidth(line); lw < w {
				line += strings.Repeat(" ", w-lw)
			}
			b.WriteString(line + pad)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// medianDuration returns the median of ds, sorting in place.
func medianDuration(ds []time.Duration) time.Duration {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	mid := len(ds) / 2
	if len(ds)%2 == 0 {
		return (ds[mid-1] + ds[mid]) / 2
	}
	return ds[mid]
}

// formatDaysCompact is a tight form of formatDays ("~12d") for inline columns.
func formatDaysCompact(d time.Duration) string {
	days := d.Hours() / 24
	switch {
	case days < 1:
		return "<1d"
	case days < 10:
		return fmt.Sprintf("%.1fd", days)
	default:
		return fmt.Sprintf("%.0fd", days)
	}
}

// ── Task lists ────────────────────────────────────────────────────────────────

func (m model) renderTaskList() string {
	active := m.activeTodos()
	if len(active) == 0 {
		if m.searchQuery != "" {
			return normalStyle.Render(tr("  No tasks match the filter."))
		}
		if m.focusFilter {
			return normalStyle.Render(tr("  No tasks due today or overdue. Nice!"))
		}
		// First-run guidance: show the quick-add syntax (English keywords stay
		// literal — they're parsing tokens, not display strings) plus a pointer to
		// the full help. Width-clip the example so it honours the no-wrap contract.
		availW := m.termWidth - 8
		return strings.Join([]string{
			normalStyle.Render(tr("  No tasks yet. Press 'a' to add one.")),
			"",
			dimStyle.Render(truncate(tr("  Try:  ")+tr("Buy milk #shopping due:friday p:high @home"), availW)),
			dimStyle.Render(tr("  Press ? for all keyboard shortcuts.")),
		}, "\n")
	}

	b := getBuilder()
	defer putBuilder(b)

	overdueSet := m.cache.overdueSet

	// Column widths (widest row content + widest tag cell) are derived from the
	// active set and cached by refreshTaskColMetrics, so the frame doesn't
	// rescan every task — see cache.go.
	cols := taskListCols(m.termWidth, false, m.cache.activeColContentMax, m.cache.activeColTagsMax, m.cache.activeColHasDue, dueColMax(m.cache.active, m.frameTime), m.cache.activeColProjectMax)
	total := m.visibleActiveLen()
	// Cursor/total and sort status are shown in the Overview border title.
	renderListHeader(b, m.termWidth, false, cols, "")

	maxVisible := m.taskListRows()
	startIdx := m.listOffset
	if startIdx > total {
		startIdx = 0
	}
	endIdx := startIdx + maxVisible
	if endIdx > total {
		endIdx = total
	}
	// Materialize only the rows we draw, not the whole flattened list.
	window := m.visibleActiveWindow(startIdx, endIdx)

	for i := startIdx; i < endIdx; i++ {
		t := &window[i-startIdx]
		if t.ParentID == "" {
			b.WriteString(m.renderTaskLineWithSet(t, i, m.cursor, true, overdueSet, cols))
			continue
		}
		siblings := m.subtaskIDs(t.ParentID)
		subIdx := 0
		for j, id := range siblings {
			if id == t.ID {
				subIdx = j
				break
			}
		}
		b.WriteString(m.renderSubtaskLine(t, subIdx, len(siblings), cols, i, m.cursor, true))
	}
	m.renderClosedTodayBlock(b, maxVisible-(endIdx-startIdx))
	return b.String()
}

// renderClosedTodayBlock fills unused rows at the bottom of the list pane with
// what the user closed today. The pane is a fixed-height box, so on any list
// shorter than the terminal those rows were drawn as blanks — a third of the
// screen on a normal day's list.
//
// It is a read-out, not a list: the rows carry no cursor and no keys, so
// nothing above them changes, and the block only ever occupies space the active
// list is not using. free is how many rows are going spare; the block declines
// to draw at all below the three it takes to be worth reading (a separating
// blank, its label, and one task). The set itself comes from the cache
// (refreshClosedToday) rather than from a scan of cache.done, which carries the
// user's history sort and is not ordered by completion time at all under
// historySortAlpha.
func (m model) renderClosedTodayBlock(b *strings.Builder, free int) {
	if free < 3 || m.showHistory {
		return
	}
	done := m.cache.closedToday
	n := len(done)
	if n == 0 {
		return
	}
	shown := n
	if max := free - 2; shown > max {
		shown = max
	}
	avail := m.termWidth - 12
	if avail < 8 {
		avail = 8
	}
	b.WriteString("\n")
	label := fmt.Sprintf(tr("  Closed today (%d)"), n)
	b.WriteString(dimStyle.Render(label) + "\n")
	for i := 0; i < shown; i++ {
		b.WriteString(dimStyle.Render("   ") +
			fastCheckDone.render("✓") +
			fastDim.render(" "+truncate(done[i].Title, avail)) + "\n")
	}
}

func (m model) renderHistoryList() string {
	completed := m.completedTodos()
	if len(completed) == 0 {
		if m.searchQuery != "" {
			return normalStyle.Render(tr("  No completed tasks match the filter."))
		}
		return normalStyle.Render(tr("  No completed tasks yet."))
	}

	b := getBuilder()
	defer putBuilder(b)

	contentMax := 0
	tagsMax := 0
	hasDue := false
	for i := range completed {
		if w := len([]rune(completed[i].Title)); w > contentMax {
			contentMax = w
		}
		if tw := rowTagsWidth(completed[i].Tags); tw > tagsMax {
			tagsMax = tw
		}
		if !completed[i].DueDate.IsZero() {
			hasDue = true
		}
	}
	// dueMax (0) is ignored for history — it forces its fixed 12-wide date column.
	cols := taskListCols(m.termWidth, true, contentMax, tagsMax, hasDue, 0, 0)
	// Cursor/total and sort status are shown in the History border title.
	renderListHeader(b, m.termWidth, true, cols, "")

	maxVisible := m.taskListRows()
	startIdx := m.listOffset
	endIdx := startIdx + maxVisible
	if endIdx > len(completed) {
		endIdx = len(completed)
	}
	if startIdx > len(completed) {
		startIdx = 0
	}

	for i := startIdx; i < endIdx; i++ {
		b.WriteString(m.renderHistoryLine(completed[i], i, m.cursor, true, cols))
	}
	return b.String()
}

func (m model) renderHistoryLine(t todo.Todo, index, cursor int, active bool, cols listCols) string {
	titleW := cols.titleW
	selected := index == cursor && active
	dueVal := ""
	if !t.DueDate.IsZero() {
		dueVal = t.DueDate.Format("02-01-06")
	}
	completedVal := ""
	if !t.CompletedAt.IsZero() {
		completedVal = t.CompletedAt.Format("02-01-06")
	}
	titleCol := padRight(truncate(t.Title, titleW-listColGap), titleW)
	// History rows get the same two-tone treatment as the active list: the
	// title at full strength, the dates dim. A completed task is a record, and
	// the record's subject is its title, not the day it closed.
	rowStyle, metaStyle := fastNormal, fastDim
	if selected {
		rowStyle, metaStyle = fastSelectedRow, fastSelectedDim
	}
	var r rowBuf
	r.add(rowStyle, cursorGap+"[")
	r.add(fastCheckDone, "✓")
	r.add(rowStyle, "] ")
	r.add(rowStyle, titleCol)
	if cols.showDue {
		r.add(metaStyle, padRight(dueVal, cols.dueW))
	}
	if cols.showLast {
		r.add(metaStyle, padRight(completedVal, 12))
	}

	contentW := m.termWidth - 8
	tagsStr, tagsDrawnW := m.renderRowTags(&t, contentW-r.w, selected)
	line := r.String()
	if selected {
		return line + tagsStr + selectedRowTail(fastSelectedRow, r.w+tagsDrawnW, contentW) + "\n"
	}
	return line + tagsStr + "\n"
}

// subtaskIndentW is how much narrower a subtask row's title is than its
// parent's: the indent and tree connector drawn in front of it.
const subtaskIndentW = 4

func (m *model) renderSubtaskLine(sub *todo.Todo, subIndex, subTotal int, cols listCols, flatIndex, cursor int, active bool) string {
	connector := "├"
	if subIndex == subTotal-1 {
		connector = "└"
	}
	titleW := cols.titleW - subtaskIndentW
	if titleW < 10 {
		titleW = 10
	}
	title := truncate(sub.Title, titleW)
	if sub.IsTimerRunning() {
		title = "⧗ " + title
	}
	cursorStr := cursorGap
	selected := flatIndex == cursor && active
	check := "[ ]"
	if sub.Status == todo.Done {
		check = "[✓]"
	} else if len(sub.TimeEntries) > 0 {
		check = "[>]"
	}
	body := "   " + connector + " " + check + " " + title

	if selected {
		return fastSelectedRow.render(cursorStr+body) +
			selectedRowTail(fastSelectedRow, len([]rune(cursorStr+body)), m.termWidth-8) + "\n"
	}
	if sub.Status == todo.Done {
		// Keep ✓ in checkDoneStyle so the done marker stays legible
		// against the surrounding dim row.
		return fastDim.render(cursorStr+"   "+connector+" [") +
			fastCheckDone.render("✓") +
			fastDim.render("] "+title) + "\n"
	}
	return fastDim.render(cursorStr+body) + "\n"
}

// taskRowLabel splits a row's title cell into the three pieces fitTaskRowLabel
// lays out: the prefix (answers "can I pick this up?" at the left edge), the
// title text, and the badges (blocker/blocked arrows, recurrence, subtask
// progress). They are kept apart so a narrow column clips the title, which has
// slack in it, and never the badges, which change the decision.
//
// refreshTaskColMetrics sizes the title column from this same function, so the
// width it reserves and the width the row draws cannot drift. Priority has no
// glyph: it is already the largest term in the Score column.
func (m *model) taskRowLabel(t *todo.Todo) (prefix, text, badges string) {
	var p strings.Builder
	if t.IsTimerRunning() {
		p.WriteString("⧗ ")
	}
	// One arrow, not two: a task in the middle of a chain is both, and the
	// half that decides whether you can start it is the one worth a cell.
	switch {
	case m.cache.blockedSet[t.ID]:
		p.WriteString("↧ ") // waiting on an unfinished dependency — sorts last
	case m.cache.blockerSet[t.ID]:
		p.WriteString("↥ ") // others depend on this — clearing it unblocks them
	}
	prefix = p.String()
	text = t.Title
	var b strings.Builder
	if t.IsRecurring() {
		b.WriteString(" ↻")
	}
	if subDone, subTotal := m.subtaskProgress(t.ID); subTotal > 0 {
		fmt.Fprintf(&b, " (%d/%d)", subDone, subTotal)
	}
	return prefix, text, b.String()
}

// taskRowLabelWidth is the display width taskRowLabel's three pieces draw
// together — what the title column has to hold for the row to render whole.
func taskRowLabelWidth(prefix, text, badges string) int {
	return len([]rune(prefix)) + len([]rune(text)) + len([]rune(badges))
}

// fitTaskRowLabel lays the three pieces into avail cells, clipping the title
// text and keeping the badges. When even prefix+badges overrun the column
// there is nothing left to protect, so the whole label is clipped as one
// string rather than drawn past the column's edge.
func fitTaskRowLabel(prefix, text, badges string, avail int) string {
	if avail <= 0 {
		return ""
	}
	fixed := len([]rune(prefix)) + len([]rune(badges))
	if fixed < avail {
		return prefix + truncate(text, avail-fixed) + badges
	}
	return truncate(prefix+text+badges, avail)
}

// rowBuf accumulates a task-list row as styled runs while tracking the plain
// display width it has emitted. Two things make it worth a type: the width has
// to be counted on the unstyled text (an SGR sequence is a dozen runes, and the
// tags cell and the selected-row tail are both positioned from this count), and
// consecutive runs that share a style are coalesced into one render call, so a
// row still costs a handful of escape sequences rather than one per column.
type rowBuf struct {
	out  strings.Builder
	run  strings.Builder
	cur  fastStyle
	open bool
	w    int
}

// add appends s in style st. Styles are compared by their cached SGR prefix and
// suffix rather than by the struct: fastStyle carries a lipgloss.Style, and it
// is the escape sequences, not the struct identity, that decide whether two
// runs can share one render call.
func (r *rowBuf) add(st fastStyle, s string) {
	if s == "" {
		return
	}
	if r.open && st.prefix == r.cur.prefix && st.suffix == r.cur.suffix {
		r.run.WriteString(s)
	} else {
		r.flush()
		r.cur, r.open = st, true
		r.run.WriteString(s)
	}
	r.w += len([]rune(s))
}

func (r *rowBuf) flush() {
	if !r.open || r.run.Len() == 0 {
		r.run.Reset()
		return
	}
	r.out.WriteString(r.cur.render(r.run.String()))
	r.run.Reset()
}

func (r *rowBuf) String() string {
	r.flush()
	r.open = false
	return r.out.String()
}

// rowPalette is the pair of styles one task row is painted with. status carries
// whatever the row's state is saying — normal, overdue, blocked by something
// overdue, timer running — and meta is the dim tone the secondary columns are
// drawn in. Both already carry the selection background when the row is the
// one under the cursor, so a selected overdue row shows the selection and the
// status at once instead of one masking the other.
//
// Splitting the row across two styles is the whole point: painting every cell
// in the status colour meant the score, the size and the project name shouted
// as loudly as the title, and an overdue row coloured its project name red —
// a cell that has nothing to do with being overdue.
type rowPalette struct {
	status fastStyle
	meta   fastStyle
}

func taskRowPalette(t *todo.Todo, hasOverdueDep, selected bool) rowPalette {
	switch {
	case t.IsTimerRunning() && selected:
		return rowPalette{fastSelectedTimer, fastSelectedDim}
	case t.IsTimerRunning():
		return rowPalette{fastTimer, fastDim}
	case t.IsOverdue() && selected:
		return rowPalette{fastSelectedOverdue, fastSelectedDim}
	case t.IsOverdue():
		return rowPalette{fastOverdue, fastDim}
	case hasOverdueDep && selected:
		return rowPalette{fastSelectedDepOverdue, fastSelectedDim}
	case hasOverdueDep:
		return rowPalette{fastDepOverdue, fastDim}
	case selected:
		return rowPalette{fastSelectedRow, fastSelectedDim}
	default:
		return rowPalette{fastNormal, fastDim}
	}
}

func (m *model) renderTaskLineWithSet(t *todo.Todo, index, cursor int, active bool, overdueSet map[string]bool, cols listCols) string {
	titleW := cols.titleW
	selected := index == cursor && active
	// The status box holds one fact: where this task stands (statusBox).
	// Blocked is not in it: the sort puts blocked work at the bottom, and the
	// ↧ before the title says why it is there.
	checkbox := m.boardCfg.statusBox(t)
	// The gutter holds the fold sign: + for a task whose subtasks are hidden,
	// - for one showing them. Task rows carry no cursor mark (see cursorMark):
	// the gutter is the one place the sign has room, set apart from the box.
	foldIcon := " "
	if m.subtaskCount(t.ID) > 0 {
		if m.expandedTasks[t.ID] {
			foldIcon = "-"
		} else {
			foldIcon = "+"
		}
	}
	// hasOverdueDep drives the row colour (see taskRowPalette), not a glyph.
	hasOverdueDep := t.HasOverdueDependencyFast(overdueSet)
	pal := taskRowPalette(t, hasOverdueDep, selected)

	dueVal := ""
	if !t.DueDate.IsZero() {
		dueVal = formatDueShort(t.DueDate, m.frameTime)
	}
	// The due cell is the one piece of metadata the status colour is actually
	// about, so it keeps the status tone when the task is late and drops to the
	// dim tone otherwise. That way a red cell in the Due column means the date
	// is the problem, instead of being one more cell in a uniformly red row.
	dueStyle := pal.meta
	if t.IsOverdue() || hasOverdueDep {
		dueStyle = pal.status
	}

	prefix, text, badges := m.taskRowLabel(t)
	// Reserve one trailing space inside the column so a clipped title never
	// butts up against the Score column that follows.
	label := fitTaskRowLabel(prefix, text, badges, titleW-listColGap)

	var r rowBuf
	r.add(pal.status, foldIcon+" "+checkbox+" ")
	r.add(pal.status, padRight(label, titleW))
	if cols.showLast {
		// Score reads as a percent of the current field (rank/score.go): "82%"
		// says how close to the top this is, where a bare "24.4" only said "a
		// number". Right-aligned in the field so every score ends in the same
		// column and the % signs line up; the field's trailing listColGap is
		// the gap to Due.
		score := m.rank.FormatPercent(m.rankedScore(t))
		if rank.StartsLater(t, m.frameTime) {
			score = startsCell(t.StartDate, m.frameTime)
		}
		r.add(pal.meta, padRight(padLeft(truncate(score, cols.lastW-listColGap), cols.lastW-listColGap), cols.lastW))
	}
	if cols.showDue {
		// Right-aligned for the same reason: "2d" and "20-09-27" share a right
		// edge, so the gap to Size is the same on every row.
		r.add(dueStyle, padRight(padLeft(dueVal, cols.dueW-listColGap), cols.dueW))
	}
	if cols.showSize {
		// One letter at the column's left edge, under its header; the column
		// carries its own trailing gap, the same way every other column does.
		r.add(pal.meta, padRight(strings.ToLower(t.Size.Letter()), cols.sizeW))
	}
	if cols.showProject {
		// Truncate at projectW-listColGap so the column always leaves its full
		// gap before the tags, clipped name or not.
		r.add(pal.meta, padRight(truncate(t.Project, cols.projectW-listColGap), cols.projectW))
	}

	contentW := m.termWidth - 8
	tagsStr, tagsDrawnW := m.renderRowTags(t, contentW-r.w, selected)
	line := r.String()
	if selected {
		return line + tagsStr + selectedRowTail(pal.status, r.w+tagsDrawnW, contentW) + "\n"
	}
	return line + tagsStr + "\n"
}

// ── Projects ──────────────────────────────────────────────────────────────────

func (m model) renderProjectListContent(projects []string) string {
	if len(projects) == 0 {
		switch {
		case m.searchQuery != "":
			return normalStyle.Render(tr("  No projects match the filter."))
		case len(m.cache.projectGroups) > 0:
			return m.nothingOpenNote(tr("  Every project is finished."))
		}
		return normalStyle.Render(tr("  No projects yet. Add a project to a task first."))
	}
	return m.renderGroupRows(groupRows{
		nameHdr:    tr("Project"),
		keys:       projects,
		sums:       m.cache.projectGroups,
		labelStyle: normalStyle,
		cursor:     m.projectCursor,
		start:      m.listOffset,
		count:      m.projectListVisibleRows(),
		label:      func(key string) string { return key },
		editing: func(key, lead, _ string) (string, bool) {
			if m.mode != modeEditProjectInline || key != m.editingProjectName {
				return "", false
			}
			return normalStyle.Render(lead) + m.textInput.View(), true
		},
	})
}

// renderDrillTaskList renders a drill-in task list as the list panel (a tag's
// or project's list beside or above an opened task),
// windowed to the rows the clamp keeps the cursor in, with the done tasks'
// fold line under the last row when there is room for it. The Project column
// is shown where the rows can differ in it. visible is drillTaskVisibleRows
// read on the full model: m.termWidth is already narrowed to the column's
// share by the caller, and a narrowed copy answers height questions for a
// layout that is not the one on screen.
func (m model) renderDrillTaskList(tasks []todo.Todo, s *groupSummary, showProject bool, visible int) []string {
	fold := m.groupFoldNote(s)
	if len(tasks) == 0 {
		lines := []string{dimStyle.Render(tr("  Nothing open here."))}
		if fold != "" {
			lines = append(lines, fold)
		}
		return lines
	}
	start := min(m.listOffset, len(tasks))
	lines := m.renderGroupTaskRows(tasks, start, visible, m.cursor, showProject)
	if fold != "" && start+visible >= len(tasks) && len(tasks)-start < visible {
		lines = append(lines, fold)
	}
	return lines
}

// ── Settings list ─────────────────────────────────────────────────────────────

// Settings is one pane of grouped rows, laid out in two columns when the
// terminal is wide enough. The groups say which control belongs to what
// ("Listen" and "Server token" are the sync server's own). Titles are held in English and
// passed through tr() at render time — a package var would freeze them before
// applyLang runs.
type settingsGroup struct {
	title string
	rows  []int
	// preview draws the top-N ranking under this group's rows. It is the whole
	// account the bias knobs give of themselves, so it sits with them rather
	// than at the foot of the pane where a knob change would scroll it away.
	preview bool
}

var settingsGroups = []settingsGroup{
	{title: "Appearance", rows: []int{
		settingTheme,
		settingLanguage,
		settingDetailPos,
	}},
	{title: "General", rows: []int{
		settingAutoCloseParent,
		settingAutoCloseSubtasks,
		settingSubtaskTags,
		settingShowBoard,
		settingStages,
		settingReminder,
		settingReminderTime,
	}},
	{title: "Sequencer", preview: true, rows: []int{
		settingBiasDeadline,
		settingBiasPriority,
		settingBiasMomentum,
		settingAging,
	}},
	{title: "Sync", rows: []int{
		settingSyncAuto,
		settingSyncBoard,
		settingSyncServer,
		settingSyncToken,
		settingSyncNow,
	}},
	{title: "Server", rows: []int{
		settingServerOn,
		settingServerListen,
		settingServerToken,
	}},
	{title: "Export", rows: []int{
		settingExportFolder,
		settingImportFile,
	}},
	{title: "About", rows: []int{
		settingVersion,
		settingCheckUpdate,
	}},
	{title: "AI & Meetings", rows: []int{
		settingAIProvider,
		settingAnthropicKey,
		settingOpenAIKey,
		settingGeminiKey,
		settingFFmpegInput,
	}},
}

// The pane is drawn as two columns of groups: the first settingsColumnSplit
// groups on the left, the rest on the right. The split is a group boundary, so
// the column-major reading order is exactly settingsNavOrder's order — up/down
// walks the left column, then continues at the top of the right one, and no
// navigation code has to know about columns at all.
//
// Below settingsTwoColMinWidth a column would be too narrow for a label and
// its value, so the pane falls back to the single column.
const (
	settingsColumnSplit    = 3
	settingsTwoColMinWidth = 96
	settingsColGap         = 3
)

// settingsSelectable reports whether the cursor may land on a row. Version is
// a fact, not a control: enter on it did nothing, so stopping there was a dead
// step in the middle of the list.
func settingsSelectable(id int) bool { return id != settingVersion }

// settingsRowVisible hides the rows that configure something this machine is
// not doing. The bind address and the server token describe an endpoint that
// only exists while the server runs, so on a client — which is most installs —
// they were two rows of setup for a thing the user had already said no to.
//
// Turning the server on is what brings them back, and that stays reachable
// without them: the Enabled row asks for the token itself (serverStartFlow)
// when there is none, rather than sending the user to a row it just hid.
func (m model) settingsRowVisible(id int) bool {
	switch id {
	case settingServerListen, settingServerToken:
		return m.inprocServer != nil || m.serverExternal
	case settingReminderTime:
		return m.reminderOn
	}
	return true
}

// settingsEditsText marks the rows whose enter opens a text editor. They render
// with a trailing mark so an editable value can be told apart from a ‹ cycled ›
// one without pressing anything.
func settingsEditsText(id int) bool {
	switch id {
	case settingStages, settingSyncServer, settingSyncToken, settingServerListen, settingServerToken,
		settingExportFolder, settingImportFile,
		settingAnthropicKey, settingOpenAIKey, settingGeminiKey, settingFFmpegInput:
		return true
	}
	return false
}

// settingsEditMark is the affordance on a row that opens an editor.
const settingsEditMark = " ⏎"

// settingsNavOrder returns the linear up/down traversal order: the groups in
// the order they are drawn, each top→bottom. Rows the cursor cannot land on —
// unselectable or hidden by the current state — are left out here, so every
// caller inherits the skip.
func (m model) settingsNavOrder() []int {
	out := make([]int, 0, numSettingsRows)
	for _, g := range settingsGroups {
		for _, id := range g.rows {
			if settingsSelectable(id) && m.settingsRowVisible(id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// visibleGroupRows is the group's rows minus the ones the current state hides.
// It returns the group's own slice when nothing is hidden, which is the common
// case and the one worth not allocating for.
func (m model) visibleGroupRows(g settingsGroup) []int {
	hidden := false
	for _, id := range g.rows {
		if !m.settingsRowVisible(id) {
			hidden = true
			break
		}
	}
	if !hidden {
		return g.rows
	}
	out := make([]int, 0, len(g.rows))
	for _, id := range g.rows {
		if m.settingsRowVisible(id) {
			out = append(out, id)
		}
	}
	return out
}

// settingsCursorStep advances the settings cursor by delta along the visual
// traversal order, clamping at the ends so up at the top / down at the bottom
// are no-ops.
func (m model) settingsCursorStep(cur, delta int) int {
	order := m.settingsNavOrder()
	idx := 0
	for i, id := range order {
		if id == cur {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	} else if idx >= len(order) {
		idx = len(order) - 1
	}
	return order[idx]
}

// renderSettingsSection builds the unboxed Settings content and, with it, the
// line the cursor row is drawn on (-1 when the cursor is on nothing visible).
// The pane scrolls by that number, and it comes back from the renderer rather
// than from a parallel line-counting function: headings, group separators and
// the bias preview all sit between the rows, and a second place counting them
// is a second place to get them wrong. w is the content width available (no
// outer borders); the pane builder applies the final per-line width contract.
func (m model) renderSettingsSection(w int) (string, int) {
	if w < 8 {
		w = 8
	}
	labels := map[int]string{
		settingBiasDeadline:      tr("Deadline pressure"),
		settingBiasPriority:      tr("Priority focus"),
		settingBiasMomentum:      tr("Momentum bias"),
		settingAging:             tr("Aging increases score"),
		settingAutoCloseParent:   tr("Auto-close parent"),
		settingAutoCloseSubtasks: tr("Auto-close subtasks"),
		settingShowBoard:         tr("Kanban board"),
		settingTheme:             tr("Theme"),
		settingLanguage:          tr("Language"),
		settingDetailPos:         tr("Detail pane"),
		settingStages:            tr("Board columns"),
		settingSyncAuto:          tr("Automatic"),
		settingSyncBoard:         tr("Share board columns"),
		settingSyncServer:        tr("Sync server"),
		settingSyncToken:         tr("Sync token"),
		settingSyncNow:           tr("Sync now"),
		settingServerOn:          tr("Enabled"),
		settingServerListen:      tr("Listen"),
		settingServerToken:       tr("Server token"),
		settingVersion:           tr("Version"),
		settingCheckUpdate:       tr("Check for updates"),
		settingReminder:          tr("Daily reminder"),
		settingReminderTime:      tr("Reminder time"),
		settingExportFolder:      tr("Auto-export folder"),
		settingImportFile:        tr("Import from file"),
		settingSubtaskTags:       tr("Subtasks copy tags"),
		settingAIProvider:        tr("AI provider"),
		settingAnthropicKey:      tr("Anthropic API key"),
		settingOpenAIKey:         tr("OpenAI API key"),
		settingGeminiKey:         tr("Gemini API key"),
		settingFFmpegInput:       tr("Microphone override"),
	}
	agingVal := tr("Off")
	if m.rank.Biases.Aging {
		agingVal = tr("On")
	}
	autoCloseVal := tr("Off")
	if m.autoCloseParent {
		autoCloseVal = tr("On")
	}
	showBoardVal := tr("Off")
	if m.boardCfg.shown {
		showBoardVal = tr("On")
	}
	autoCloseSubsVal := tr("Off")
	if m.autoCloseSubtasks {
		autoCloseSubsVal = tr("On")
	}
	reminderVal := tr("Off")
	if m.reminderOn {
		reminderVal = tr("On")
	}
	subtaskTagsVal := tr("Off")
	if m.subtaskTags {
		subtaskTagsVal = tr("On")
	}
	syncAutoVal := "‹ " + tr("needs server") + " ›"
	if m.syncCfg.ready() {
		if m.autoSync {
			syncAutoVal = "‹ " + tr("On") + " ›"
		} else {
			syncAutoVal = "‹ " + tr("Off") + " ›"
		}
	}
	syncBoardVal := tr("Off")
	if m.boardCfg.sync {
		syncBoardVal = tr("On")
	}
	syncServerVal := tr("not set")
	if m.syncCfg.URL != "" {
		syncServerVal = m.syncCfg.URL
	}
	syncTokenVal := tr("not set")
	if m.syncCfg.Token != "" {
		syncTokenVal = "•••• " + tr("set")
	}
	// Off is the answer for every machine that is not the hub. The token is
	// only needed to turn this on, and toggleServer says so when they try.
	serverState := tr("Off")
	switch {
	case m.inprocServer != nil:
		serverState = tr("On")
	case m.serverExternal:
		serverState = tr("external")
	}
	serverTokenVal := tr("not set")
	if m.syncCfg.ServerToken != "" {
		serverTokenVal = "•••• " + tr("set")
		// Flagged where it can be fixed: the row that edits it. The reason
		// itself lives in `tjek doctor`; here there is only room to say that
		// it is worth replacing and which key does that.
		if weakSyncToken(m.syncCfg.ServerToken) != "" {
			serverTokenVal = "•••• " + tr("weak token; ctrl+g on this row generates a strong one")
		}
	}
	keyVal := func(k string) string {
		if k == "" {
			return tr("not set")
		}
		return "•••• " + tr("set")
	}
	ffmpegInputVal := tr("platform default")
	if m.ffmpegInput != "" {
		ffmpegInputVal = m.ffmpegInput
	}
	values := map[int]string{
		settingBiasDeadline:      biasPickerValue(m.rank.Biases.Deadline),
		settingBiasPriority:      biasPickerValue(m.rank.Biases.Priority),
		settingBiasMomentum:      biasPickerValue(m.rank.Biases.Momentum),
		settingAging:             "‹ " + agingVal + " ›",
		settingAutoCloseParent:   "‹ " + autoCloseVal + " ›",
		settingAutoCloseSubtasks: "‹ " + autoCloseSubsVal + " ›",
		settingShowBoard:         "‹ " + showBoardVal + " ›",
		settingTheme:             "‹ " + m.themeName + " ›",
		settingLanguage:          "‹ " + activeLang.displayName() + " ›",
		settingDetailPos:         "‹ " + trDetailPos(m.detailPos) + " ›",
		settingStages:            m.boardCfg.stagesDisplay(),
		settingSyncAuto:          syncAutoVal,
		settingSyncBoard:         "‹ " + syncBoardVal + " ›",
		settingSyncServer:        syncServerVal,
		settingSyncToken:         syncTokenVal,
		settingSyncNow:           tr("press enter to sync"),
		settingServerOn:          "‹ " + serverState + " ›",
		settingServerListen:      m.syncCfg.listenAddr(),
		settingServerToken:       serverTokenVal,
		settingVersion:           appVersion,
		settingCheckUpdate:       tr("press enter to check"),
		settingReminder:          "‹ " + reminderVal + " ›",
		settingReminderTime:      "‹ " + formatReminder(m.reminderAt) + " ›",
		settingExportFolder:      exportFolderDisplay(m.exportFolder),
		settingImportFile:        tr("choose a file"),
		settingSubtaskTags:       "‹ " + subtaskTagsVal + " ›",
		settingAIProvider:        "‹ " + aiprovider.DisplayName(m.aiProvider) + " ›",
		settingAnthropicKey:      keyVal(m.aiKeys.Anthropic),
		settingOpenAIKey:         keyVal(m.aiKeys.OpenAI),
		settingGeminiKey:         keyVal(m.aiKeys.Gemini),
		settingFFmpegInput:       ffmpegInputVal,
	}

	// One label column across every group, so the values line up down the
	// whole pane rather than stepping in and out at each heading.
	labelW := 0
	for _, g := range settingsGroups {
		for _, id := range m.visibleGroupRows(g) {
			if n := len([]rune(labels[id])); n > labelW {
				labelW = n
			}
		}
	}
	labelW += 2

	// renderRow draws a row within maxW cells. The value is clipped before the
	// edit mark is added, so a long value ("Board columns" on a narrow pane)
	// loses its tail and keeps the ⏎ that says enter edits it.
	renderRow := func(id, maxW int) string {
		cursor := cursorGap
		labelStyle := normalStyle
		if id == m.settingsCursor {
			cursor = selectedStyle.Render(cursorMark)
			labelStyle = selectedStyle
		}
		valueW := maxW - len([]rune(cursorGap)) - labelW
		value := values[id]
		if settingsEditsText(id) {
			value = truncate(value, valueW-len([]rune(settingsEditMark))) + settingsEditMark
		} else {
			value = truncate(value, valueW)
		}
		return cursor + labelStyle.Render(padRight(labels[id], labelW)) + helpStyle.Render(value)
	}

	// renderColumn draws one column's worth of groups and reports the line the
	// cursor row landed on (-1 when the cursor is not in this column). colW is
	// the column's own width, so the bias preview sizes its titles to the
	// column it sits in rather than to the whole pane. wide holds the same
	// lines with the rows clipped to wideW instead, for a row with nothing
	// beside it (joinSettingsColumns).
	renderColumn := func(groups []settingsGroup, colW, wideW int) (lines, wide []string, selected int) {
		selected = -1
		for _, g := range groups {
			rows := m.visibleGroupRows(g)
			if len(rows) == 0 {
				continue
			}
			if len(lines) > 0 {
				lines = append(lines, "")
				wide = append(wide, "")
			}
			lines = append(lines, cursorGap+headerStyle.Render(tr(g.title)))
			wide = append(wide, lines[len(lines)-1])
			for _, id := range rows {
				if id == m.settingsCursor {
					selected = len(lines)
				}
				lines = append(lines, renderRow(id, colW))
				wide = append(wide, renderRow(id, wideW))
			}
			// Live preview: the top-N tasks ranked with the current knob values is
			// the whole account the pane gives of a bias change — a prose tagline
			// for the mix said less than the five rows that actually move.
			if g.preview {
				if preview := m.renderSettingsTopPreview(m.rank.Biases, m.rank.Heat, m.frameTime, colW); preview != "" {
					previewLines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
					lines = append(lines, previewLines...)
					wide = append(wide, previewLines...)
				}
			}
		}
		return lines, wide, selected
	}

	var lines []string
	var selected int
	if w >= settingsTwoColMinWidth {
		colW := (w - settingsColGap) / 2
		left, leftWide, leftSel := renderColumn(settingsGroups[:settingsColumnSplit], colW, w)
		right, _, rightSel := renderColumn(settingsGroups[settingsColumnSplit:], colW, colW)
		lines, selected = joinSettingsColumns(left, leftWide, leftSel, right, rightSel, colW)
	} else {
		lines, _, selected = renderColumn(settingsGroups, w, w)
	}

	if m.updateStatus != "" {
		lines = append(lines, "", "  "+activeCountStyle.Render(m.updateStatus))
	}
	if m.syncStatus != "" {
		// Wrapped, not truncated. This line is where a sync failure explains
		// itself, and the explanation is the tail: cutting it at a fixed 60
		// columns once left a user reading "Last sync failed: server returned
		// 500 Internal Server Error" for a week while the sentence naming the
		// stale server sat just past the cut.
		lines = append(lines, "")
		for _, line := range clampLines(wrapText(m.syncStatus, w-4), syncStatusMaxLines) {
			lines = append(lines, "  "+helpStyle.Render(line))
		}
	}
	return strings.Join(lines, "\n") + "\n", selected
}

// joinSettingsColumns lays the two columns of groups side by side inside the
// one pane. The columns are top-aligned, so a row's line number in the joined
// block is its index in its own column — which is exactly what the pane's
// scroll (fitSettingsPane) is given, with no second coordinate system to keep
// in step. Lines are padded to the column width with ansi.StringWidth, not
// len(): every row carries styling, and byte length would indent the right
// column by the width of the escape sequences.
//
// leftWide is left with its rows clipped to the whole pane rather than to
// colW; it is used where the right column has nothing.
func joinSettingsColumns(left, leftWide []string, leftSel int, right []string, rightSel, colW int) ([]string, int) {
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if r == "" {
			// Nothing to the right of this line, so a value wider than its
			// column share may run on into the empty lane — which is how
			// "Board columns" keeps showing its whole list of columns.
			if i < len(leftWide) {
				l = leftWide[i]
			}
			out = append(out, l)
			continue
		}
		// With a row to the right, the left line is clipped to its share
		// instead: the right column has to start at the same x on every line
		// or it stops reading as a column, and a long value shoving its
		// neighbour sideways is worse than an ellipsis.
		l = ansi.Truncate(l, colW, ellipsis)
		pad := colW + settingsColGap - ansi.StringWidth(l)
		if pad < settingsColGap {
			pad = settingsColGap
		}
		out = append(out, l+strings.Repeat(" ", pad)+r)
	}
	selected := leftSel
	if selected < 0 {
		selected = rightSel
	}
	return out, selected
}

// renderSettingsList preserves a plain, unboxed rendering for focused unit
// tests and other callers. View uses buildSettingsContent to put the same
// content in its pane.
func (m model) renderSettingsList() string {
	content, _ := m.renderSettingsSection(m.termWidth - 8)
	return content
}

// fitSettingsPane keeps the selected row visible when a narrow/short terminal
// cannot show the whole pane, then pads the pane to its assigned height.
func fitSettingsPane(content string, height, width, selectedLine int) []string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	start := 0
	if selectedLine >= height {
		start = selectedLine - height + 1
	}
	if start+height > len(lines) {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	lines = append([]string(nil), lines[start:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	truncateLines(lines, width)
	return lines
}

// buildSettingsContent renders the settings rows into the tab's single pane.
func (m model) buildSettingsContent(w, outerH int) string {
	content, selected := m.renderSettingsSection(w - 2)
	// panelContentHeight, not outerH-2: the panel's chrome is two borders and
	// the padding row above the first line. Counting only the borders left the
	// pane one row taller than it drew, so the last row — and, when the cursor
	// was on it, the row the scroll had just been asked to reveal — fell off
	// the bottom.
	lines := fitSettingsPane(content, panelContentHeight(outerH), w-2, selected)
	panel := listPanelFocusedStyle.Width(w).Render(strings.Join(lines, "\n"))
	return withBorderTitle(panel, m.listPanelTitle(), w, true)
}

// settingsPreviewN is the number of ranked rows shown in the bias-knob preview.
const settingsPreviewN = 5

// renderSettingsTopPreview returns a small block showing the top N pending
// tasks ranked by the supplied biases/heat (pure — no global mutation). On
// empty task sets it returns an empty string so the caller can skip it.
// maxW is the column width available (content, no outer borders).
func (m model) renderSettingsTopPreview(b rank.Biases, heat rank.Heat, now time.Time, maxW int) string {
	all := m.allTodos()
	rows := rank.TopWith(all, b, heat, now)
	if len(rows) == 0 {
		return ""
	}
	if len(rows) > settingsPreviewN {
		rows = rows[:settingsPreviewN]
	}

	var sb strings.Builder

	hdr := tr("Top 5 with these weights:")
	sb.WriteString("\n  " + dimStyle.Render(hdr) + "\n")

	// Row format: "  NN  SS.S  <title>"
	// "  " (2) + rank (2) + "  " (2) + score (4, e.g. "12.3") + "  " (2) = 12 chars before title.
	const rowPrefixW = 12
	titleMax := maxW - rowPrefixW
	if titleMax < 8 {
		titleMax = 8
	}

	// The preview ranks with knob values that are not live yet, so its
	// percentages are relative to its own field — the live 100% mark belongs
	// to a different set of weights.
	previewMax := 0.0
	for i := range rows {
		if s := rank.ComponentsAt(now, &rows[i], b, heat).Total; s > previewMax {
			previewMax = s
		}
	}
	for i, t := range rows {
		score := rank.ComponentsAt(now, &t, b, heat).Total
		pos := fmt.Sprintf("%2d", i+1)
		scoreStr := fmt.Sprintf("%4s", strconv.Itoa(rank.PercentOfField(score, previewMax))+"%")
		title := truncate(t.Title, titleMax)
		line := fmt.Sprintf("  %s  %s  %s", pos, scoreStr, title)
		sb.WriteString(dimStyle.Render(line) + "\n")
	}
	return sb.String()
}

// biasPickerValue formats a bias for the Settings picker the same way the
// theme/language pickers do: title-cased value between thin chevrons.
func biasPickerValue(b rank.Level) string {
	s := tr(b.String())
	if s == "" {
		return "‹ - ›"
	}
	// CapitalizeTitle rather than slicing the first byte: a translated word can
	// start with a multi-byte rune, and s[:1] would cut it in half.
	return "‹ " + todo.CapitalizeTitle(s) + " ›"
}
