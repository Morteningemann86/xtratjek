package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── Detail pages ──────────────────────────────────────────────────────────────

// detailSectionBar is the line at the top of a task's detail pane that names
// its sections, the current one lit while the pane has focus, so ←/→ says
// what it will do before it is pressed. It takes the row the panel's top
// padding would otherwise leave blank. When the names do not fit in width,
// it keeps the current section and as many neighbours as fit, with … where
// the rest are.
func (m model) detailSectionBar(width int) string {
	// Indented like the field labels, which sit after the cursor's gutter.
	width -= len([]rune(cursorGap))
	cur := detailSectionOf(m.detail.field)
	focused := m.pane == paneDetail
	labels := make([]string, len(detailSections))
	for i, s := range detailSections {
		labels[i] = tr(s.label)
	}
	const sep = " · "
	fits := func(lo, hi int) bool {
		w := 0
		for i := lo; i <= hi; i++ {
			w += len([]rune(labels[i]))
		}
		w += (hi - lo) * len([]rune(sep))
		if lo > 0 {
			w += 2 // "… "
		}
		if hi < len(labels)-1 {
			w += 2 // " …"
		}
		return w <= width
	}
	lo, hi := cur, cur
	for grew := true; grew; {
		grew = false
		if hi+1 < len(labels) && fits(lo, hi+1) {
			hi++
			grew = true
		}
		if lo > 0 && fits(lo-1, hi) {
			lo--
			grew = true
		}
	}
	var b strings.Builder
	if lo > 0 {
		b.WriteString(dimStyle.Render(ellipsis + " "))
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			b.WriteString(dimStyle.Render(sep))
		}
		if i == cur && focused {
			b.WriteString(headerStyle.Render(labels[i]))
		} else {
			b.WriteString(dimStyle.Render(labels[i]))
		}
	}
	if hi < len(labels)-1 {
		b.WriteString(dimStyle.Render(" " + ellipsis))
	}
	return cursorGap + truncateStyled(b.String(), max(width, 0))
}

func (m model) renderDetailPage1(t *todo.Todo) string {
	b := getBuilder()
	defer putBuilder(b)

	availableW := m.termWidth - 8
	isDetailFocused := m.pane == paneDetail

	// Value width = content width minus label column and cursor prefix.
	valW := availableW - detailLabelColWidth - 2
	if valW < 10 {
		valW = 10
	}

	renderField := func(label, value string, field detailField) string {
		cur := cursorGap
		isCurrent := isDetailFocused && m.detail.field == field
		if isCurrent {
			cur = cursorMark
		}
		// truncateStyled, not truncate: values are assembled by their callers
		// and one may arrive already styled, and cutting that by rune count
		// slices an escape sequence in half.
		value = truncateStyled(value, valW)
		paddedLabel := detailLabelStyle.Render(padRight(label+":", detailLabelColWidth))
		var v string
		if isCurrent {
			v = detailSelectedStyle.Render(value)
		} else {
			v = detailValueStyle.Render(value)
		}
		return cur + paddedLabel + v
	}
	// An unset field is a dim dash until the cursor reaches it, and only then
	// says what it is missing and how to fill it. Spelling out "not set" on
	// every empty row made a new task's pane mostly placeholders, with the
	// fields that do say something lost among them. The row stays either way:
	// it is where the cursor goes to set the value.
	unsetVal := func(hint string, field detailField) string {
		if isDetailFocused && m.detail.field == field {
			return hint
		}
		return dimStyle.Render(unsetMark)
	}

	startVal := unsetVal(tr("not set"), fieldStartDate)
	if !t.StartDate.IsZero() {
		startVal = formatStartDate(t.StartDate)
	}
	dueVal := unsetVal(tr("not set"), fieldDueDate)
	if !t.DueDate.IsZero() {
		dueVal = t.DueDate.Format("02-01-06")
		if t.IsOverdue() {
			dueVal += tr(" ! overdue")
		}
	}
	recurVal := unsetVal(tr("not set"), fieldRecurrence)
	if t.Recurrence != "" {
		recurVal = "↻ " + trRecurrence(t.Recurrence)
	}
	projectVal := unsetVal(tr("not set"), fieldProject)
	if t.Project != "" {
		projectVal = t.Project
	}
	notesVal := unsetVal(tr("none (press enter or 'n' to edit)"), fieldNotes)
	if t.Notes != "" {
		lines := strings.SplitN(t.Notes, "\n", 2)
		// Reserve room for the " (…)" multi-line marker (5 cells) so the
		// final value still fits within valW.
		budget := valW
		if len(lines) > 1 {
			budget -= 5
		}
		if budget < 4 {
			budget = 4
		}
		preview := truncate(lines[0], budget)
		if len(lines) > 1 {
			preview += " (…)"
		}
		notesVal = preview
	}

	// Left column: interactive fields the user navigates through.
	left := getBuilder()
	defer putBuilder(left)
	left.WriteString(renderField(tr("Start date"), startVal, fieldStartDate) + "\n")
	left.WriteString(renderField(tr("Due date"), dueVal, fieldDueDate) + "\n")
	if completedFieldVisible(t) {
		completedVal := t.CompletedAt.Format("02-01-06 15:04")
		if !(isDetailFocused && m.detail.field == fieldCompleted) {
			completedVal = checkDoneStyle.Render(completedVal)
		}
		left.WriteString(renderField(tr("Completed on"), completedVal, fieldCompleted) + "\n")
	}
	left.WriteString(renderField(tr("Recurrence"), recurVal, fieldRecurrence) + "\n")
	left.WriteString(renderField(tr("Priority"), t.Priority.Icon()+" "+trPriority(t.Priority), fieldPriority) + "\n")
	left.WriteString(renderField(tr("Size"), trSize(t.Size), fieldSize) + "\n")
	if m.boardCfg.stageFieldVisible(t) {
		// ←/→ change the value here rather than jumping section, and this is
		// the only field in the pane where they mean that — so the value wears
		// the same ‹ … › brackets the Settings rows use. The shape of the field
		// says it is a picker; a hint tacked on after it said the same thing in
		// words, in a place the eye reads as part of the value. The brackets are
		// decoration: too narrow for both and they go whole, leaving the name.
		stageVal := m.boardCfg.stageDisplay(t.Stage)
		if icon := m.boardCfg.stageIcon(t.Stage); icon != "" {
			stageVal = icon + " " + stageVal
		}
		if bracketed := "‹ " + stageVal + " ›"; len([]rune(bracketed)) <= valW {
			stageVal = bracketed
		}
		left.WriteString(renderField(tr("Stage"), stageVal, fieldStage) + "\n")
	}
	left.WriteString(renderField(tr("Project"), projectVal, fieldProject) + "\n")
	left.WriteString(renderField(tr("Description"), notesVal, fieldNotes) + "\n")

	// Right column (or continuation in single-col mode): read-only metadata.
	right := getBuilder()
	defer putBuilder(right)

	roField := func(label string, valueStyle func(string) string, value string) {
		value = truncate(value, valW)
		right.WriteString("  " + detailLabelStyle.Render(padRight(label, detailLabelColWidth)) +
			valueStyle(value) + "\n")
	}
	plainVal := func(s string) string { return detailValueStyle.Render(s) }
	timerVal := func(s string) string { return timerStyle.Render(s) }

	// Order within this block is derived-facts first, provenance last: Score
	// explains the task's position in the list, the UUID and timestamps are
	// rows nobody acts on.
	subTime := m.descendantTimeSpent(t.ID)
	if len(t.TimeEntries) > 0 || subTime > 0 {
		own := t.TotalTimeSpent()
		timeVal := trCount("%s (%d entries)", len(t.TimeEntries), formatDuration(own), len(t.TimeEntries))
		if subTime > 0 {
			// Show the rolled-up total separately so the user can see what
			// their own logged time was vs. what subtasks added.
			timeVal += fmt.Sprintf(tr("  +%s subtasks = %s"), formatDuration(subTime), formatDuration(own+subTime))
		}
		if t.IsTimerRunning() {
			timeVal += tr(" ◉ tracking")
		}
		roField(tr("Time spent:"), timerVal, timeVal)
	}

	// Score breakdown: surfaces *why* a task ranks where it does. Shown only
	// on pending tasks because Done always scores 0 and the breakdown would
	// be a row of zeros.
	if t.Status == todo.Pending {
		sc := m.rank.Components(t)
		// Same precision as before but with the ".0" noise trimmed off whole
		// components — the all-%.1f form overflowed valW at a 120-col
		// terminal and truncated the tail of the breakdown to "(…)".
		comp := func(v float64) string {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
		}
		// The percentage is the *ranked* score, the one the list column and the
		// row's position agree on. When a subtask or waiting work lifted it, the
		// components below are its own and no longer add up to it — the ↑ says
		// so, and `w` spells out where the lift came from.
		ranked := m.rankedScore(t)
		lift := ""
		if ranked > sc.Total+0.0005 {
			lift = "↑ "
		}
		breakdown := fmt.Sprintf(tr("%s  (%sD %s · P %s · M %s · S %s · A %s)"),
			m.rank.FormatPercent(ranked), lift,
			comp(sc.Urgency), comp(sc.Importance), comp(sc.Momentum), comp(sc.Size), comp(sc.Age))
		// The components are an explanation, not a value: clipped to
		// "(D 10.5 · P 10 · M 10 · S…" they explain nothing and cost a row
		// saying so. When the column cannot hold the whole account, keep the
		// percentage — which is the part the list column and the row's
		// position agree on — and leave the breakdown to `w` and `tjek why`,
		// which have the width for it.
		if len([]rune(breakdown)) > valW {
			breakdown = m.rank.FormatPercent(ranked) + lift
		}
		if rank.StartsLater(t, m.frameTime) {
			breakdown = startsLong(t.StartDate)
		}
		roField(tr("Score:"), plainVal, breakdown)
	}

	roField(tr("Created:"), plainVal, t.CreatedAt.Format("02-01-06 15:04"))
	// A task nobody has touched since creating it has nothing to say here, and
	// the duplicate timestamp reads as though something happened.
	if !t.ModifiedAt.Equal(t.CreatedAt) {
		roField(tr("Modified:"), plainVal, t.ModifiedAt.Format("02-01-06 15:04"))
	}
	roField(tr("ID:"), plainVal, shortID(t.ID))

	b.WriteString(left.String())
	b.WriteString(right.String())
	b.WriteString("\n")

	tagCur := cursorGap
	if isDetailFocused && m.detail.field == fieldTags && len(t.Tags) == 0 {
		tagCur = cursorMark
	}
	b.WriteString(tagCur + detailLabelStyle.Render(tr("Tags:")) + "\n")
	if len(t.Tags) == 0 {
		b.WriteString("  " + emptySection(isDetailFocused && m.detail.field == fieldTags, tr("No tags. Press 'a' to add one.")) + "\n")
	} else {
		for i, tag := range t.Tags {
			pfx := cursorGap
			if isDetailFocused && m.detail.field == fieldTags && i == m.detail.tagCursor {
				pfx = cursorMark
				b.WriteString(detailSelectedStyle.Render(pfx) + tagStyle.Render("⟨#"+tag+"⟩") + "\n")
			} else {
				b.WriteString(dimStyle.Render(pfx) + tagStyle.Render("⟨#"+tag+"⟩") + "\n")
			}
		}
	}

	return b.String()
}

func (m model) renderDetailPage2(t *todo.Todo) string {
	b := getBuilder()
	defer putBuilder(b)

	availableW := m.termWidth - 8
	isDetailFocused := m.pane == paneDetail

	// Each row is "  [?] <title>" — 2 cursor cells + 4 bracket+space cells = 6.
	itemW := availableW - 6
	if itemW < 4 {
		itemW = 4
	}

	// Subtasks, then dependencies (+ inbound Blocks), one stacked flow.
	// Separate builders keep the section boundaries obvious.
	subB := getBuilder()
	defer putBuilder(subB)
	depB := getBuilder()
	defer putBuilder(depB)

	subtaskCur := cursorGap
	if isDetailFocused && m.detail.field == fieldSubtasks && m.subtaskCount(t.ID) == 0 {
		subtaskCur = cursorMark
	}
	subB.WriteString(subtaskCur + detailLabelStyle.Render(tr("Subtasks:")) + "\n")
	if m.subtaskCount(t.ID) == 0 {
		subB.WriteString("  " + emptySection(isDetailFocused && m.detail.field == fieldSubtasks, tr("No subtasks. Press 'a' to add one.")) + "\n")
	} else {
		for i, subID := range m.subtaskIDs(t.ID) {
			sub := m.findTodoByID(subID)
			pfx := cursorGap
			isSubSelected := isDetailFocused && m.detail.field == fieldSubtasks && i == m.detail.subtaskCursor
			if isSubSelected {
				pfx = cursorMark
			}
			if sub == nil {
				subB.WriteString(dimStyle.Render(fmt.Sprintf(tr("%s[?] unknown subtask"), pfx)) + "\n")
				continue
			}
			title := truncate(sub.Title, itemW)
			if sub.Status == todo.Done {
				if isSubSelected {
					subB.WriteString(detailSelectedStyle.Render(pfx+"[") + checkDoneStyle.Render("✓") + detailSelectedStyle.Render("] "+title) + "\n")
				} else {
					subB.WriteString(dimStyle.Render(pfx+"[") + checkDoneStyle.Render("✓") + dimStyle.Render("] "+title) + "\n")
				}
			} else {
				line := fmt.Sprintf("%s[ ] %s", pfx, title)
				switch {
				case sub.IsOverdue():
					subB.WriteString(overdueStyle.Render(line) + "\n")
				case isSubSelected:
					subB.WriteString(detailSelectedStyle.Render(line) + "\n")
				default:
					subB.WriteString(detailValueStyle.Render(line) + "\n")
				}
			}
		}
	}

	inbound := dependentsOf(m.allTodos(), t.ID)
	depCur := cursorGap
	if isDetailFocused && m.detail.field == fieldDependencies && len(t.Dependencies) == 0 && len(inbound) == 0 {
		depCur = cursorMark
	}
	depB.WriteString(depCur + detailLabelStyle.Render(tr("Dependencies:")) + "\n")
	if len(t.Dependencies) == 0 {
		if len(inbound) == 0 {
			depB.WriteString("  " + emptySection(isDetailFocused && m.detail.field == fieldDependencies, tr("No dependencies. Press 'a' to add one.")) + "\n")
		}
	} else {
		for i, depID := range t.Dependencies {
			dep := m.findTodoByID(depID)
			pfx := cursorGap
			isDepSelected := isDetailFocused && m.detail.field == fieldDependencies && i == m.detail.depCursor
			if isDepSelected {
				pfx = cursorMark
			}
			if dep == nil {
				depB.WriteString(dimStyle.Render(fmt.Sprintf(tr("%s[?] unknown task"), pfx)) + "\n")
				continue
			}
			status := "[ ]"
			if dep.Status == todo.Done {
				status = "[✓]"
			}
			warn := ""
			if dep.IsOverdue() {
				warn = " !"
			}
			// ↧ mirrors the list-row "waiting on this" glyph, and the ↥ on
			// the Blocks lines below — outbound vs inbound at a glance.
			line := fmt.Sprintf("%s%s ↧ %s%s", pfx, status, truncate(dep.Title, itemW-len(warn)-2), warn)
			switch {
			case dep.IsOverdue():
				depB.WriteString(overdueStyle.Render(line) + "\n")
			case isDepSelected:
				depB.WriteString(detailSelectedStyle.Render(line) + "\n")
			default:
				depB.WriteString(detailValueStyle.Render(line) + "\n")
			}
		}
	}
	// Inbound edges continue the same list: dimmed ↥ rows are the pending
	// tasks waiting on this one, aligned so ↥ sits under ↧. Selectable for
	// enter-to-jump, but the edge itself is editable only from the other task.
	for i, d := range inbound {
		sel := isDetailFocused && m.detail.field == fieldDependencies &&
			len(t.Dependencies)+i == m.detail.depCursor
		pfx := cursorGap
		if sel {
			pfx = cursorMark
		}
		line := pfx + "    ↥ " + truncate(d.Title, itemW-2)
		if sel {
			depB.WriteString(detailSelectedStyle.Render(line) + "\n")
		} else {
			depB.WriteString(dimStyle.Render(line) + "\n")
		}
	}

	b.WriteString(subB.String())
	b.WriteString("\n")
	b.WriteString(depB.String())

	return b.String()
}

func (m model) renderDetailPage3(t *todo.Todo) string {
	b := getBuilder()
	defer putBuilder(b)

	innerW := m.termWidth - 10
	if innerW < minInnerWidth {
		innerW = minInnerWidth
	}
	isDetailFocused := m.pane == paneDetail

	// ── Time Entries ─────────────────────────────────────────────────────────
	entryCur := cursorGap
	if isDetailFocused && m.detail.field == fieldTimeEntries && len(t.TimeEntries) == 0 {
		entryCur = cursorMark
	}
	b.WriteString(entryCur + detailLabelStyle.Render(tr("Time entries:")) + "\n")
	if len(t.TimeEntries) == 0 {
		b.WriteString("  " + emptySection(isDetailFocused && m.detail.field == fieldTimeEntries, tr("No time entries. Press 'T' to add one.")) + "\n")
	} else {
		// Per-entry format: "HH:MM–HH:MM (duration)" mirroring the calendar
		// timeline's range style so the two surfaces look consistent.
		// A running entry (no StoppedAt) shows "HH:MM–now" and is not
		// editable/deletable — guard in updateDetail handles that.
		entryLineW := innerW - 4 // 2 cursor + 2 padding
		if entryLineW < 10 {
			entryLineW = 10
		}
		for i, e := range t.TimeEntries {
			isSelected := isDetailFocused && m.detail.field == fieldTimeEntries && i == m.detail.timeEntryCursor
			pfx := cursorGap
			if isSelected {
				pfx = cursorMark
			}
			endStr := e.StoppedAt.Format("15:04")
			running := e.IsRunning()
			if running {
				endStr = tr("now")
			}
			rangeStr := e.StartedAt.Format("15:04") + "–" + endStr
			durStr := formatDuration(e.Duration())
			// Date prefix: show "DD-MM " only when the entry is not from today,
			// so at-a-glance reading of today's entries stays terse.
			datePrefix := ""
			today := startOfDay(time.Now())
			if startOfDay(e.StartedAt) != today {
				datePrefix = e.StartedAt.Format("02-01 ")
			}
			line := pfx + datePrefix + rangeStr + "  " + durStr
			if running {
				line += tr(" ◉")
			}
			line = truncate(line, entryLineW)
			if isSelected {
				b.WriteString(detailSelectedStyle.Render(line) + "\n")
			} else {
				b.WriteString(detailValueStyle.Render(line) + "\n")
			}
		}
	}
	b.WriteString("\n")

	// ── Comments ─────────────────────────────────────────────────────────────
	commentCur := cursorGap
	if isDetailFocused && m.detail.field == fieldComments && len(t.Comments) == 0 {
		commentCur = cursorMark
	}
	b.WriteString(commentCur + detailLabelStyle.Render(tr("Comments:")) + "\n")
	if len(t.Comments) == 0 {
		b.WriteString("  " + emptySection(isDetailFocused && m.detail.field == fieldComments, tr("No comments yet. Press 'a' to add one.")) + "\n")
	} else {
		available := innerW - commentPrefixLen
		if available < 10 {
			available = 10
		}
		for i, c := range t.Comments {
			isSelected := isDetailFocused && m.detail.field == fieldComments && i == m.detail.commentCursor
			pfx := cursorGap
			if isSelected {
				pfx = cursorMark
			}
			header := fmt.Sprintf("%s[%s] ", pfx, c.CreatedAt.Format("02-01-06 15:04"))
			wrapped := wrapText(c.Text, available)
			indent := strings.Repeat(" ", len([]rune(header)))
			for j, line := range wrapped {
				var fullLine string
				if j == 0 {
					fullLine = header + line
				} else {
					fullLine = indent + line
				}
				if isSelected {
					b.WriteString(detailSelectedStyle.Render(fullLine) + "\n")
				} else {
					b.WriteString(detailValueStyle.Render(fullLine) + "\n")
				}
			}
		}
	}
	return b.String()
}

// ── Gantt ─────────────────────────────────────────────────────────────────────

// ganttPlacement is how much of the timeline one task can claim: a span when it
// has both dates, a single marked moment when it has one date or only a record
// of having happened, and nothing when the task carries no moment at all.
type ganttPlacement int

const (
	ganttNothing ganttPlacement = iota
	ganttPoint
	ganttSpan
)

// ganttMoment is where a task without a start→due span still belongs on the
// timeline, and whether it belongs anywhere. A task is not absent from a
// project's history just because nobody dated it: a due date on its own is a
// deadline, a start on its own is a beginning, a completed task happened when
// it was completed, and tracked time is a record of work at a moment. Only a
// pending task that has never been dated, worked on or finished has no place
// to be drawn, and that is the one case the chart leaves blank.
func ganttMoment(t todo.Todo) (time.Time, bool) {
	switch {
	case !t.DueDate.IsZero():
		return t.DueDate, true
	case !t.StartDate.IsZero():
		return t.StartDate, true
	case t.Status == todo.Done && !t.CompletedAt.IsZero():
		return t.CompletedAt, true
	}
	var latest time.Time
	for _, e := range t.TimeEntries {
		if !e.DeletedAt.IsZero() {
			continue
		}
		when := e.StoppedAt
		if when.IsZero() {
			when = e.StartedAt
		}
		if when.After(latest) {
			latest = when
		}
	}
	return latest, !latest.IsZero()
}

// ganttPlacementOf reports how a task is drawn and, for a point, when.
func ganttPlacementOf(t todo.Todo) (ganttPlacement, time.Time) {
	if !t.StartDate.IsZero() && !t.DueDate.IsZero() {
		return ganttSpan, time.Time{}
	}
	if at, ok := ganttMoment(t); ok {
		return ganttPoint, at
	}
	return ganttNothing, time.Time{}
}

// ganttDateWindow is the range every Gantt surface scales against: the earliest
// start and the latest due in the set, with a fallback window when the tasks
// carry no dates at all. Marked moments widen it too — a marker clamped onto an
// edge would put a task on a date it does not have. Shared by the labelled
// chart and the drilled-in strip so the two can never scale the same project
// differently.
func ganttDateWindow(tasks []todo.Todo, today time.Time) (minDate, maxDate time.Time, totalDays float64) {
	fold := func(d time.Time) {
		if d.IsZero() {
			return
		}
		if minDate.IsZero() || d.Before(minDate) {
			minDate = d
		}
		if maxDate.IsZero() || d.After(maxDate) {
			maxDate = d
		}
	}
	for _, t := range tasks {
		if !t.StartDate.IsZero() && (minDate.IsZero() || t.StartDate.Before(minDate)) {
			minDate = t.StartDate
		}
		if !t.DueDate.IsZero() && (maxDate.IsZero() || t.DueDate.After(maxDate)) {
			maxDate = t.DueDate
		}
		if kind, at := ganttPlacementOf(t); kind == ganttPoint {
			fold(at)
		}
	}
	if minDate.IsZero() {
		minDate = today.AddDate(0, 0, -7)
	}
	if maxDate.IsZero() {
		maxDate = today.AddDate(0, 1, 0)
	}
	// Today is on every timeline: the chart is read against it, and a window
	// that ended before it hid how late the last dates already were.
	if today.Before(minDate) {
		minDate = startOfDay(today)
	}
	if today.After(maxDate) {
		maxDate = today
	}
	if !maxDate.After(minDate) {
		maxDate = minDate.AddDate(0, 0, 14)
	}
	totalDays = maxDate.Sub(minDate).Hours() / 24
	if totalDays < 1 {
		totalDays = 1
	}
	return minDate, maxDate, totalDays
}

// ganttColumn maps a date onto a column of a chartW-wide chart.
func ganttColumn(d, minDate time.Time, totalDays float64, chartW int) int {
	return int(math.Round(d.Sub(minDate).Hours() / 24 * float64(chartW) / totalDays))
}

// Cell colours in the Gantt buffers: negative codes are the fixed styles,
// 99 is a completed bar, 200+ indexes ganttOverdueGradient, and everything
// else indexes ganttGradient.
const (
	ganttCellEmpty = -1
	ganttCellToday = -2
	ganttCellGuide = -3
	ganttCellDone  = 99
)

// ganttMarkerRune is the glyph for a task drawn as a single moment rather than
// a span. Shape carries priority — a diamond for high, a dot for the rest — so
// a chart of markers still says which ones mattered.
func ganttMarkerRune(t todo.Todo) rune {
	if t.Priority == todo.PriorityHigh {
		return '◆'
	}
	return '•'
}

// fillGanttBar paints one task into the cell/colour buffers and reports how it
// was drawn: a span (both dates), a single marker at the moment ganttMoment
// found, or nothing at all. The buffers are cleared here, so a caller can reuse
// one pair across every row.
func fillGanttBar(t todo.Todo, minDate time.Time, totalDays float64, chartW, todayPos int, barRunes []rune, barColors []int) (ganttPlacement, time.Time) {
	for j := range barRunes {
		barRunes[j] = ' '
		barColors[j] = ganttCellEmpty
	}
	kind, at := ganttPlacementOf(t)
	if kind == ganttSpan {
		gradLen := len(ganttGradient)
		ovrdLen := len(ganttOverdueGradient)
		startPos := ganttColumn(t.StartDate, minDate, totalDays, chartW)
		endPos := ganttColumn(t.DueDate, minDate, totalDays, chartW)
		if startPos < 0 {
			startPos = 0
		}
		if endPos > chartW {
			endPos = chartW
		}
		barLen := endPos - startPos
		if barLen < 1 {
			barLen = 1
		}
		isOverdue := t.IsOverdue()
		isDone := t.Status == todo.Done
		for j := startPos; j < endPos && j < chartW; j++ {
			barRunes[j] = '█'
			var pos float64
			if barLen > 1 {
				pos = float64(j-startPos) / float64(barLen-1)
			}
			gradIdx := int(pos * float64(gradLen-1))
			if gradIdx >= gradLen {
				gradIdx = gradLen - 1
			}
			switch {
			case isDone:
				barColors[j] = ganttCellDone
			case isOverdue:
				idx := int(pos * float64(ovrdLen-1))
				if idx >= ovrdLen {
					idx = ovrdLen - 1
				}
				barColors[j] = 200 + idx
			default:
				barColors[j] = gradIdx
			}
		}
	}
	if todayPos >= 0 && todayPos < chartW {
		barRunes[todayPos] = '│'
		barColors[todayPos] = ganttCellToday
	}
	// The marker is written after the today rule, so it wins its own row: the
	// rule is repeated on every other row and stays readable, while a marker
	// hidden under it would drop the row's only piece of information.
	if kind == ganttPoint {
		pos := ganttColumn(at, minDate, totalDays, chartW)
		if pos < 0 {
			pos = 0
		}
		if pos >= chartW {
			pos = chartW - 1
		}
		if pos >= 0 {
			barRunes[pos] = ganttMarkerRune(t)
			switch {
			case t.Status == todo.Done:
				barColors[pos] = ganttCellDone
			case t.IsOverdue():
				barColors[pos] = 200 + len(ganttOverdueGradient) - 1
			default:
				barColors[pos] = len(ganttGradient) / 2
			}
		}
	}
	return kind, at
}

// writeGanttBar emits a buffered row, coalescing consecutive cells that share a
// colour into one .Render call — a per-cell render would spend a dozen runes of
// escape sequence on every column.
func writeGanttBar(b *strings.Builder, barRunes []rune, barColors []int) {
	chartW := len(barRunes)
	j := 0
	for j < chartW {
		colorIdx := barColors[j]
		start := j
		for j < chartW && barColors[j] == colorIdx {
			j++
		}
		group := string(barRunes[start:j])
		switch {
		case colorIdx == ganttCellEmpty:
			b.WriteString(group)
		case colorIdx == ganttCellToday:
			b.WriteString(ganttTodayStyle.Render(group))
		case colorIdx == ganttCellGuide:
			b.WriteString(dimStyle.Render(group))
		case colorIdx == ganttCellDone:
			b.WriteString(ganttDoneStyle.Render(group))
		case colorIdx >= 200:
			b.WriteString(ganttOverdueGradient[colorIdx-200].Render(group))
		default:
			b.WriteString(ganttGradient[colorIdx].Render(group))
		}
	}
}

// renderGanttStrip is the timeline as it is drawn beside a project's task list
// (projectPaneRows): bars only, one row per task, windowed to the same rows
// the list is showing so row N on the left is row N on the right; sel is the
// cursor's row, or -1. It drops the labelled
// chart's title column and its start→due suffix on purpose — the column to its
// left already names every task in the same order and carries the due date, so
// at the drill's ~40-cell column the labels spent half the pane restating the
// list and left a 10-cell stub of a chart. The axis is one line rather than two
// (dates and today marker share it) because the list header beside it is one
// line, and the rows have to start level.
func (m model) renderGanttStrip(tasks []todo.Todo, chartW, from, count, sel int) []string {
	if len(tasks) == 0 || chartW < 1 {
		return []string{dimStyle.Render(tr("  No tasks in this project."))}
	}
	today := m.frameTime
	minDate, maxDate, totalDays := ganttDateWindow(tasks, today)
	todayPos := ganttColumn(today, minDate, totalDays, chartW)
	if todayPos < 0 || todayPos >= chartW {
		todayPos = -1
	}

	lines := []string{m.renderGanttAxis(minDate, maxDate, today, chartW, todayPos)}

	bufs := getGanttBuffers(chartW)
	defer putGanttBuffers(bufs)
	barRunes := bufs.bar[:chartW]
	barColors := bufs.color[:chartW]

	// Same clamp as the list's, so the two windows start on
	// the same task even when the offset is stale.
	if from < 0 || from > len(tasks) {
		from = 0
	}
	b := getBuilder()
	defer putBuilder(b)
	for i := from; i < from+count && i < len(tasks); i++ {
		fillGanttBar(tasks[i], minDate, totalDays, chartW, todayPos, barRunes, barColors)
		// The selected row gets a dotted rule through its empty cells: with no
		// labels on this side, that is what carries the eye from the highlighted
		// title on the left across to its bar.
		if i == sel {
			for j := range barRunes {
				if barColors[j] == ganttCellEmpty {
					barRunes[j] = '·'
					barColors[j] = ganttCellGuide
				}
			}
		}
		b.Reset()
		writeGanttBar(b, barRunes, barColors)
		lines = append(lines, b.String())
	}
	return lines
}

// renderGanttAxis draws the strip's single ruler line: the window's end dates
// at the edges and the today marker where it falls, over a dim rule. The label
// is dropped to a single tick when the span between the dates cannot hold it,
// so a narrow column loses the word and never the position.
func (m model) renderGanttAxis(minDate, maxDate, today time.Time, chartW, todayPos int) string {
	rule := make([]rune, chartW)
	marks := make([]int, chartW)
	for i := range rule {
		rule[i] = '─'
		marks[i] = ganttCellGuide
	}
	write := func(pos int, s string, mark int) {
		for i, ch := range []rune(s) {
			if pos+i < 0 || pos+i >= chartW {
				continue
			}
			rule[pos+i] = ch
			marks[pos+i] = mark
		}
	}

	// An edge that falls on today says "today" in the today colour instead of
	// its date, rather than printing the date twice ("29-09─today:29-09").
	leftDate, leftMark := minDate.Format("02-01"), ganttCellEmpty
	rightDate, rightMark := maxDate.Format("02-01"), ganttCellEmpty
	todayOnEdge := false
	if startOfDay(minDate).Equal(startOfDay(today)) {
		leftDate, leftMark, todayOnEdge = tr("today"), ganttCellToday, true
	} else if startOfDay(maxDate).Equal(startOfDay(today)) {
		rightDate, rightMark, todayOnEdge = tr("today"), ganttCellToday, true
	}
	lo, hi := 0, chartW // the span the today label may use
	edges := chartW >= len([]rune(leftDate))+len([]rune(rightDate))+4
	if edges {
		write(0, leftDate, leftMark)
		write(chartW-len([]rune(rightDate)), rightDate, rightMark)
		lo, hi = len([]rune(leftDate))+1, chartW-len([]rune(rightDate))-1
	}

	if todayPos >= 0 && !(edges && todayOnEdge) {
		todayLabel := tr("today:") + today.Format("02-01")
		labelLen := len([]rune(todayLabel))
		switch {
		case hi-lo >= labelLen:
			insertPos := todayPos - labelLen/2
			if insertPos < lo {
				insertPos = lo
			}
			if insertPos+labelLen > hi {
				insertPos = hi - labelLen
			}
			write(insertPos, todayLabel, ganttCellToday)
		default:
			write(todayPos, "┬", ganttCellToday)
		}
	}

	b := getBuilder()
	defer putBuilder(b)
	j := 0
	for j < chartW {
		mark := marks[j]
		start := j
		for j < chartW && marks[j] == mark {
			j++
		}
		group := string(rule[start:j])
		switch mark {
		case ganttCellToday:
			b.WriteString(ganttTodayStyle.Render(group))
		case ganttCellEmpty:
			b.WriteString(headerStyle.Render(group))
		default:
			b.WriteString(dimStyle.Render(group))
		}
	}
	return b.String()
}

// emptySection is the line under a section heading that has nothing in it: a
// dim dash, or — once the cursor is on the heading — the hint naming the key
// that adds the first entry. See unsetVal in renderDetailPage1.
func emptySection(current bool, hint string) string {
	if current {
		return detailValueStyle.Render(hint)
	}
	return dimStyle.Render(unsetMark)
}
