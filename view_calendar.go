package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ── Day activities ────────────────────────────────────────────────────────────

type dayActivity struct {
	taskID  string
	entryID string
	title   string
	project string
	tags    []string
	start   time.Time
	stop    time.Time // zero while the entry is still running
	// completed marks this as a completion event (task was marked done on
	// this day) rather than a time-tracking entry. start==stop==CompletedAt
	// and duration is 0; the timeline renders it as "✓ done at HH:MM".
	completed bool
	// due marks this as a deadline event (the task's DueDate falls on this
	// day and it isn't done). start==stop==DueDate and duration is 0; the
	// timeline renders it as "⧗ due" (orange) or "⧗ overdue" (red) when the
	// due day is already past.
	due bool
	// doneThatDay marks a time entry whose task was completed on the same
	// day. The separate "✓ done at" row is suppressed when the day has
	// tracked time, so the entry carries the tick itself — otherwise a
	// finished task showed only the timer dot, which read as still running.
	doneThatDay bool
	// parentTitle is set when the activity's task is a subtask; the timeline
	// shows it as a "↳ parent" reference because subtask titles are often too
	// terse to identify on their own.
	parentTitle string
}

func (a dayActivity) duration() time.Duration {
	if a.completed {
		return 0
	}
	if a.stop.IsZero() {
		return time.Since(a.start)
	}
	return a.stop.Sub(a.start)
}

// calDay is a local calendar day, comparable and usable as a map key. The
// calendar compares a day against every time entry on every frame, so it is
// three integers rather than a formatted string.
type calDay struct {
	year  int
	month time.Month
	day   int
}

func dayKey(t time.Time) calDay {
	// Times come back from storage in UTC; the calendar grid is laid out
	// in the user's local zone, so take the date in local time too —
	// otherwise an entry started at 01:00 local (= 23:00 UTC yesterday)
	// gets keyed to the wrong day.
	y, mo, d := t.Local().Date()
	return calDay{y, mo, d}
}

// activitiesForDay returns every time entry started on the given day plus
// every completion event (a task marked done on that day) so the calendar
// surfaces work even when the user closed the task without tracking time.
// Result is ordered by timestamp.
func (m model) activitiesForDay(day time.Time) []dayActivity {
	key := dayKey(day)
	// Read by the timeline, its title, the roll-ups and the entry cursor's
	// clamp on every key; each build walks every task and time entry. The
	// slice is shared, so it is read-only.
	if m.cache != nil && m.cache.dayActsFor == key && m.cache.dayActs != nil {
		return m.cache.dayActs
	}
	acts := m.buildActivitiesForDay(key)
	if m.cache != nil {
		m.cache.dayActsFor, m.cache.dayActs = key, acts
	}
	return acts
}

func (m model) buildActivitiesForDay(key calDay) []dayActivity {
	var acts []dayActivity
	for _, t := range m.tasks {
		parentTitle := ""
		if t.ParentID != "" {
			if p := m.get(t.ParentID); p != nil {
				parentTitle = p.Title
			}
		}
		doneToday := t.Status == todo.Done && !t.CompletedAt.IsZero() && dayKey(t.CompletedAt) == key
		for _, e := range t.TimeEntries {
			if dayKey(e.StartedAt) != key {
				continue
			}
			acts = append(acts, dayActivity{
				doneThatDay: doneToday && !e.StoppedAt.IsZero(),
				taskID:      t.ID,
				entryID:     e.ID,
				title:       t.Title,
				project:     t.Project,
				tags:        t.Tags,
				start:       e.StartedAt,
				stop:        e.StoppedAt,
				parentTitle: parentTitle,
			})
		}
		// Completion event: task marked done on this day. Only surfaces when
		// the day has no tracked time for that task — otherwise the time
		// entries already cover the work and a second "done at HH:MM" row
		// would just be noise.
		if doneToday {
			hasTracked := false
			for _, e := range t.TimeEntries {
				if dayKey(e.StartedAt) == key {
					hasTracked = true
					break
				}
			}
			if !hasTracked {
				acts = append(acts, dayActivity{
					taskID:      t.ID,
					title:       t.Title,
					project:     t.Project,
					tags:        t.Tags,
					start:       t.CompletedAt,
					stop:        t.CompletedAt,
					completed:   true,
					parentTitle: parentTitle,
				})
			}
		}
		// Deadline event: an unfinished task whose DueDate lands on this day.
		// Shown regardless of whether the day also has tracked time — the
		// deadline is different information from the work, and due dates are
		// stored at local midnight so this sorts to the top of the day.
		if !t.DueDate.IsZero() && t.Status != todo.Done && dayKey(t.DueDate) == key {
			acts = append(acts, dayActivity{
				taskID:      t.ID,
				title:       t.Title,
				project:     t.Project,
				tags:        t.Tags,
				start:       t.DueDate,
				stop:        t.DueDate,
				due:         true,
				parentTitle: parentTitle,
			})
		}
	}
	// Total order with deterministic tiebreakers — m.tasks is a map
	// (randomized iteration) and sort.Slice isn't stable, so without
	// these, entries with equal start times would reshuffle on every
	// render and appear to "jump" as the cursor moves.
	sort.Slice(acts, func(i, j int) bool {
		if !acts[i].start.Equal(acts[j].start) {
			return acts[i].start.Before(acts[j].start)
		}
		if acts[i].taskID != acts[j].taskID {
			return acts[i].taskID < acts[j].taskID
		}
		return acts[i].entryID < acts[j].entryID
	})
	if acts == nil {
		acts = []dayActivity{} // non-nil, so an empty day is memoized too
	}
	return acts
}

// trackedPerDay sums tracked time per day for entries started in [from, to].
func (m model) trackedPerDay(from, to time.Time) map[calDay]time.Duration {
	end := to.AddDate(0, 0, 1)
	totals := make(map[calDay]time.Duration)
	for _, t := range m.tasks {
		for _, e := range t.TimeEntries {
			if e.StartedAt.Before(from) || !e.StartedAt.Before(end) {
				continue
			}
			totals[dayKey(e.StartedAt)] += e.Duration()
		}
	}
	return totals
}

// dueDaysInRange marks days in [from, to] that have at least one unfinished
// task due, so the month grid can hint upcoming deadlines without the user
// landing on each day.
func (m model) dueDaysInRange(from, to time.Time) map[calDay]bool {
	end := to.AddDate(0, 0, 1)
	days := make(map[calDay]bool)
	for _, t := range m.tasks {
		if t.DueDate.IsZero() || t.Status == todo.Done {
			continue
		}
		if t.DueDate.Before(from) || !t.DueDate.Before(end) {
			continue
		}
		days[dayKey(t.DueDate)] = true
	}
	return days
}

// ── Calendar tab layout ───────────────────────────────────────────────────────

func (m model) buildCalendarContent(w, outerH int) string {
	innerH := panelContentHeight(outerH)
	// The month grid is a fixed 22-column block, so on a narrow window the two
	// panes would join into lines wider than the terminal. Below the threshold,
	// drop the
	// grid and give the timeline the whole width: ← / → and [ / ] still move
	// the day and month, so nothing becomes unreachable, and the month title
	// moves onto the timeline's border so you can still see where you are.
	if w < calSideBySideMinWidth {
		return m.buildCalendarNarrow(w, innerH)
	}
	tlW := w - calPanelWidth - 4
	if tlW < minInnerWidth {
		tlW = minInnerWidth
	}

	calLines := m.renderMonthCalendarLines()
	tlLines := m.renderTimelineLines(tlW-2, innerH)

	fitLines := func(lines []string, h, contentW int) []string {
		if len(lines) > h {
			lines = lines[:h]
		}
		for len(lines) < h {
			lines = append(lines, "")
		}
		truncateLines(lines, contentW)
		return lines
	}
	// The month panel is as tall as what it shows — the grid and the day's
	// and month's totals — rather than stretched to the window: the empty
	// rows under it would frame nothing. The timeline keeps the full height,
	// since it scrolls through the day.
	for len(calLines) > 0 && strings.TrimSpace(ansi.Strip(calLines[len(calLines)-1])) == "" {
		calLines = calLines[:len(calLines)-1]
	}
	// What the grid only marks, by name, in the rows the panel has left: a
	// blank, the heading, and a row at least, less the blank at the foot.
	if rows := innerH - len(calLines) - 3; rows >= 1 {
		calLines = append(calLines, m.renderComingUpLines(calPanelWidth-2, rows)...)
	}
	calLines = fitLines(calLines, min(len(calLines)+1, innerH), calPanelWidth-2)
	tlLines = fitLines(tlLines, innerH, tlW-2)

	// Accent border on the pane that owns keystrokes — same contract as the
	// Tasks side-by-side layout.
	calStyle, tlStyle := listPanelFocusedStyle, listPanelStyle
	if m.calendar.focusTimeline {
		calStyle, tlStyle = listPanelStyle, listPanelFocusedStyle
	}
	calPanel := calStyle.Width(calPanelWidth).Render(strings.Join(calLines, "\n"))
	tlPanel := tlStyle.Width(tlW).Render(strings.Join(tlLines, "\n"))
	tlPanel = withBorderTitle(tlPanel, m.calendarTimelineTitle(localizedDayDateAbbrev(m.calendar.selected), tlW), tlW, m.calendar.focusTimeline)
	calPanel = withBorderTitle(calPanel, localizedMonthYear(m.calendar.selected), calPanelWidth, !m.calendar.focusTimeline)
	return lipgloss.JoinHorizontal(lipgloss.Top, tlPanel, calPanel)
}

// buildCalendarNarrow is the single-pane calendar for windows too small to
// hold the month grid beside the timeline. The timeline takes the full width
// with no floor, so it clips to the window instead of overflowing it.
func (m model) buildCalendarNarrow(w, innerH int) string {
	if w < 0 {
		w = 0 // borders only; a floor here would push the box past the window
	}
	lines := m.renderTimelineLines(w-2, innerH)
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	truncateLines(lines, w-2)

	panel := listPanelFocusedStyle.Width(w).Render(strings.Join(lines, "\n"))
	title := localizedDayDateAbbrev(m.calendar.selected) + " · " + localizedMonthYear(m.calendar.selected)
	return withBorderTitle(panel, m.calendarTimelineTitle(title, w), w, true)
}

// ── Month calendar (right panel) ──────────────────────────────────────────────

func (m model) renderMonthCalendarLines() []string {
	sel := m.calendar.selected
	monthStart := time.Date(sel.Year(), sel.Month(), 1, 0, 0, 0, 0, sel.Location())
	monthEnd := monthStart.AddDate(0, 1, -1)
	totals := m.trackedPerDay(monthStart, monthEnd)
	dueDays := m.dueDaysInRange(monthStart, monthEnd)

	var maxDay, monthTotal time.Duration
	for _, d := range totals {
		monthTotal += d
		if d > maxDay {
			maxDay = d
		}
	}

	today := startOfDay(m.frameTime)
	innerW := calPanelWidth - 2

	var lines []string
	lines = append(lines, dimStyle.Render(localizedWeekdayHeader()))

	// Monday-first offset of the 1st, matching the stats heatmap convention.
	day := monthStart.AddDate(0, 0, -((int(monthStart.Weekday()) + 6) % 7))
	for day.Before(monthEnd) || day.Equal(monthEnd) {
		cells := make([]string, 0, 7)
		for dow := 0; dow < 7; dow++ {
			if day.Month() != sel.Month() {
				cells = append(cells, "  ")
			} else {
				cell := fmt.Sprintf("%2d", day.Day())
				tracked := totals[dayKey(day)]
				switch {
				case day.Equal(sel):
					cell = calSelectedDayStyle.Render(cell)
				case tracked > 0:
					idx := len(calGradient) - 1
					if maxDay > 0 {
						idx = int(float64(tracked) / float64(maxDay) * float64(len(calGradient)-1))
						if idx >= len(calGradient) {
							idx = len(calGradient) - 1
						}
					}
					cell = calGradient[idx].Bold(true).Render(cell)
				case day.Equal(today):
					cell = calTodayStyle.Render(cell)
				case dueDays[dayKey(day)]:
					// Upcoming/unmet deadline: orange + underline, distinct
					// from today (orange bold) and overdue red.
					cell = depOverdueStyle.Underline(true).Render(cell)
				default:
					cell = normalStyle.Render(cell)
				}
				cells = append(cells, cell)
			}
			day = day.AddDate(0, 0, 1)
		}
		lines = append(lines, strings.Join(cells, " "))
	}

	lines = append(lines, m.renderDayRollupLines(innerW)...)

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(tr("Month "))+timerStyle.Render(formatDuration(monthTotal)))
	return lines
}

// comingUpDays is how far ahead of the selected day the month panel lists
// deadlines: a week, the span the grid's due marks are read across.
const comingUpDays = 7

// dueWithParent reports whether t is a subtask due the same day as its open
// parent. A subtask takes its parent's deadline unless given its own, so a
// list of deadlines would otherwise print a parent's date once per step; the
// parent's row stands for them, and a subtask with a date of its own still
// gets a row.
func dueWithParent(tasks map[string]*todo.Todo, t *todo.Todo) bool {
	if t.ParentID == "" {
		return false
	}
	p := tasks[t.ParentID]
	return p != nil && !p.Deleted && p.Status != todo.Done && !p.DueDate.IsZero() &&
		startOfDay(p.DueDate).Equal(startOfDay(t.DueDate))
}

// renderComingUpLines lists the open tasks due in the week from the selected
// day, soonest first, under the month grid: the names behind its due marks,
// moving with the cursor so the week ahead can be read from any day. At most
// maxRows rows, the last saying how many more there are.
func (m model) renderComingUpLines(w, maxRows int) []string {
	from := startOfDay(m.calendar.selected)
	until := from.AddDate(0, 0, comingUpDays)
	var due []*todo.Todo
	for _, t := range m.tasks {
		if t.Deleted || t.Status == todo.Done || t.DueDate.IsZero() {
			continue
		}
		if !t.DueDate.Before(from) && t.DueDate.Before(until) && !dueWithParent(m.tasks, t) {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].DueDate.Equal(due[j].DueDate) {
			return due[i].DueDate.Before(due[j].DueDate)
		}
		if due[i].Priority != due[j].Priority {
			return due[i].Priority > due[j].Priority
		}
		return due[i].ID < due[j].ID
	})

	lines := []string{"", dimStyle.Render(tr("Coming up"))}
	if len(due) == 0 {
		return append(lines, dimStyle.Render(" "+tr("nothing due")))
	}
	shown := due
	if len(due) > maxRows {
		shown = due[:max(maxRows-1, 0)]
	}
	for _, t := range shown {
		day := padRight(localizedWeekdayShort(t.DueDate.Weekday()), 3) + fmt.Sprintf(" %02d  ", t.DueDate.Day())
		style := normalStyle
		if t.IsOverdueAt(m.frameTime) {
			style = overdueStyle
		}
		lines = append(lines, dimStyle.Render(day)+style.Render(truncate(t.Title, w-len([]rune(day)))))
	}
	if more := len(due) - len(shown); more > 0 {
		lines = append(lines, dimStyle.Render(strings.TrimSpace(fmt.Sprintf(tr("  … and %d more"), more))))
	}
	return lines
}

// renderDayRollupLines builds the per-project / per-tag breakdown for the
// selected day, shown under the month grid.
func (m model) renderDayRollupLines(innerW int) []string {
	acts := m.activitiesForDay(m.calendar.selected)
	if len(acts) == 0 {
		return nil
	}

	projTotals := make(map[string]time.Duration)
	tagTotals := make(map[string]time.Duration)
	var dayTotal time.Duration
	for _, a := range acts {
		d := a.duration()
		dayTotal += d
		if a.project != "" {
			projTotals[a.project] += d
		}
		for _, tag := range a.tags {
			tagTotals["#"+tag] += d
		}
	}

	type rollup struct {
		name string
		d    time.Duration
	}
	top := func(totals map[string]time.Duration, limit int) []rollup {
		rs := make([]rollup, 0, len(totals))
		for name, d := range totals {
			// A task that is only due or done today lends its tags and
			// project to the day without any time; a "0m" row says nothing.
			if d > 0 {
				rs = append(rs, rollup{name, d})
			}
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].d != rs[j].d {
				return rs[i].d > rs[j].d
			}
			return rs[i].name < rs[j].name
		})
		if len(rs) > limit {
			rs = rs[:limit]
		}
		return rs
	}

	nameW := innerW - 8 // 1 indent + 7 for the right-aligned duration
	lines := []string{"", dimStyle.Render(tr("Day ")) + timerStyle.Render(formatDuration(dayTotal))}
	for _, r := range top(projTotals, 3) {
		lines = append(lines, " "+projLabelStyle.Render(padRight(truncate(r.name, nameW), nameW))+
			timerStyle.Render(fmt.Sprintf("%7s", formatDurationCompact(r.d))))
	}
	for _, r := range top(tagTotals, 3) {
		lines = append(lines, " "+tagStyle.Render(padRight(truncate(r.name, nameW), nameW))+
			timerStyle.Render(fmt.Sprintf("%7s", formatDurationCompact(r.d))))
	}
	return lines
}

// ── Activity timeline (right panel) ───────────────────────────────────────────

// timelineSummary is the "3 entries · 1h 20m" line for the selected day. It
// rides in the agenda pane's border title (see calendarTimelineTitle) rather
// than inside the pane: as a right-aligned first line plus its blank spacer it
// cost two of the agenda's rows on every render, and the border had the room.
func (m model) timelineSummary() string {
	acts := m.activitiesForDay(m.calendar.selected)
	var total time.Duration
	for _, a := range acts {
		total += a.duration()
	}
	if len(acts) == 1 {
		return tr("1 entry · ") + formatDuration(total)
	}
	return trCount("%d entries · %s", len(acts), len(acts), formatDuration(total))
}

// calendarTimelineTitle appends the day's summary in brackets to the agenda
// pane's border title. withBorderTitle truncates at boxW-4, which would eat the
// date before the summary, so the bracket is dropped whole when it doesn't fit
// — the day is the part you can't navigate without.
func (m model) calendarTimelineTitle(base string, boxW int) string {
	title := base + " [" + m.timelineSummary() + "]"
	if len([]rune(title)) > boxW-4 {
		return base
	}
	return title
}

func (m model) renderTimelineLines(innerW, innerH int) []string {
	acts := m.activitiesForDay(m.calendar.selected)

	var lines []string
	if len(acts) == 0 {
		lines = append(lines, dimStyle.Render(tr("  No activity on this day.")))
		lines = append(lines, dimStyle.Render(tr("  Press t on a task (tab 1) to start tracking.")))
		return lines
	}

	// Per-entry height is 1 line for bare entries and 2 lines for entries
	// with project/tags. Precompute heights so pagination is honest.
	heights := make([]int, len(acts))
	for i := range acts {
		heights[i] = 1
		if acts[i].project != "" || len(acts[i].tags) > 0 || acts[i].parentTitle != "" {
			heights[i] = 2
		}
	}

	bodyH := innerH
	if bodyH < 1 {
		bodyH = 1
	}

	// Pick a [start, end) window centered on the cursor that fits in bodyH,
	// counting each entry's height + 1 connector between entries. Reserves
	// one line for a leading/trailing "⋮" marker when entries are clipped.
	fit := func(reserveTop, reserveBot int) (int, int) {
		budget := bodyH - reserveTop - reserveBot
		if budget < 1 {
			budget = 1
		}
		cur := m.calendar.entryCursor
		if cur < 0 {
			cur = 0
		} else if cur >= len(acts) {
			cur = len(acts) - 1
		}
		used := heights[cur]
		start, end := cur, cur+1
		for {
			grew := false
			if end < len(acts) {
				extra := heights[end] + 1 // entry + connector above it
				if used+extra <= budget {
					used += extra
					end++
					grew = true
				}
			}
			if start > 0 {
				extra := heights[start-1] + 1
				if used+extra <= budget {
					used += extra
					start--
					grew = true
				}
			}
			if !grew {
				break
			}
		}
		return start, end
	}

	start, end := fit(0, 0)
	clippedTop := start > 0
	clippedBot := end < len(acts)
	if clippedTop || clippedBot {
		// Re-fit reserving room for the ⋮ markers we're about to emit.
		top, bot := 0, 0
		if clippedTop {
			top = 1
		}
		if clippedBot {
			bot = 1
		}
		start, end = fit(top, bot)
		clippedTop = start > 0
		clippedBot = end < len(acts)
	}

	if clippedTop {
		lines = append(lines, dimStyle.Render("  ⋮"))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderTimelineEntry(acts[i], i, innerW))
		hasNext := i < end-1 || clippedBot
		if sub := m.renderTimelineSub(acts[i], innerW, hasNext); sub != "" {
			lines = append(lines, sub)
		}
		if i < end-1 {
			lines = append(lines, dimStyle.Render("  │"))
		}
	}
	if clippedBot {
		lines = append(lines, dimStyle.Render("  ⋮"))
	}
	return lines
}

func (m model) renderTimelineEntry(a dayActivity, index, innerW int) string {
	focused := m.calendar.focusTimeline && index == m.calendar.entryCursor
	cur := "  "
	if focused {
		cur = cursorMark
	}

	running := a.stop.IsZero() && !a.completed
	endStr := tr(" now ")
	switch {
	case a.completed:
		// Completion event collapses the range to "done at HH:MM"; duration is 0.
		endStr = a.start.Format("15:04")
	case !running:
		endStr = a.stop.Format("15:04")
	}
	rangeStr := a.start.Format("15:04") + "–" + endStr
	if a.completed {
		rangeStr = tr("✓ done at ") + a.start.Format("15:04")
	}
	if a.due {
		rangeStr = tr("⧗ due")
		if a.start.Before(startOfDay(m.frameTime)) {
			rangeStr = tr("⧗ overdue")
		}
	}
	durStr := formatDuration(a.duration())

	// Fixed right-hand block (range + duration); title/project/tags share the rest.
	// Rune count, not len(): the range separator is a multi-byte en dash.
	leftW := innerW - len([]rune(rangeStr)) - 16
	if leftW < 8 {
		leftW = 8
	}

	title := truncate(a.title, leftW)
	used := len([]rune(title))
	left := normalStyle.Render(title)
	if focused {
		left = selectedStyle.Render(title)
	}
	if pad := leftW - used; pad > 0 {
		left += strings.Repeat(" ", pad)
	}

	durStyled := timerStyle.Render(padRight(durStr, 8))
	switch {
	case running:
		durStyled = calTodayStyle.Render(padRight(durStr+" ◉", 8))
	case a.completed, a.due:
		// No duration for a completion or deadline event; pad to keep column
		// alignment honest across mixed rows.
		durStyled = dimStyle.Render(strings.Repeat(" ", 8))
	}

	dot := timerStyle.Render("●")
	switch {
	case a.completed, a.doneThatDay:
		dot = checkDoneStyle.Render("✓")
	case a.due:
		dot = depOverdueStyle.Render("◆")
		if a.start.Before(startOfDay(m.frameTime)) {
			dot = overdueStyle.Render("◆")
		}
	}
	return cur + dot + " " + left + " " +
		dimStyle.Render(rangeStr) + "  " + durStyled
}

// renderTimelineSub renders the second line of a calendar entry: project (if
// set) followed by the entry's tags. Returns "" when the entry has neither —
// in which case the caller skips the line so bare entries stay 1-line.
//
// Drops tags first, then project, if the combined plain width would overflow
// innerW. Width is computed on the plain text and the result is styled at the
// end, since truncating a styled string would slice into ANSI escapes.
//
// hasNext extends the vertical "│" connector into this sub-line's gutter so
// the column doesn't visually break between an entry's body and the next
// entry. When false (last visible entry, no clipped bot) the gutter is blank.
func (m model) renderTimelineSub(a dayActivity, innerW int, hasNext bool) string {
	if a.project == "" && len(a.tags) == 0 && a.parentTitle == "" {
		return ""
	}
	const indentW = 4 // visual cells: "  │ " or "    "
	indent := "    "
	if hasNext {
		indent = "  " + dimStyle.Render("│") + " "
	}
	avail := innerW - indentW
	if avail < 4 {
		return ""
	}

	// Parent reference takes priority: a subtask's own title is often too terse
	// to place on its own, so it's shown (truncated to fit) ahead of any
	// project/tags, which then lay out in whatever width remains.
	parentPlain := ""
	if a.parentTitle != "" {
		parentPlain = "↳ " + a.parentTitle
		if len([]rune(parentPlain)) > avail {
			parentPlain = truncate(parentPlain, avail)
		}
	}
	parentW := len([]rune(parentPlain))
	rest := avail - parentW
	if parentW > 0 {
		rest-- // separating space before project/tags
	}
	if rest < 0 {
		rest = 0
	}

	projPlain := ""
	if a.project != "" {
		projPlain = "[" + a.project + "]"
	}
	tagsPlainW := 0
	for _, tag := range a.tags {
		// "⟨#tag⟩ " = 4 cells of decoration + tag width.
		tagsPlainW += len([]rune(tag)) + 4
	}
	projW := len([]rune(projPlain))

	showProj := projW > 0
	showTags := len(a.tags) > 0
	sep := 0
	if showProj && showTags {
		sep = 1
	}
	if showProj && showTags && projW+sep+tagsPlainW > rest {
		showTags = false
	}
	if showProj && projW > rest {
		showProj = false
	}
	if parentW == 0 && !showProj && !showTags {
		return ""
	}

	var b strings.Builder
	b.WriteString(indent)
	if parentW > 0 {
		b.WriteString(dimStyle.Render(parentPlain))
	}
	if parentW > 0 && (showProj || showTags) {
		b.WriteString(" ")
	}
	if showProj {
		b.WriteString(projLabelStyle.Render(projPlain))
	}
	if showProj && showTags {
		b.WriteString(" ")
	}
	if showTags {
		b.WriteString(renderTagsPart(a.tags))
	}
	return b.String()
}
