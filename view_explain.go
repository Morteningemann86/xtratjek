package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
)

// view_explain.go is the reading side of rank/explain.go: the "why this
// rank" overlay (w on a task) and the shared line building `tjek why` prints.
// Both surfaces compose the same rows so the two can never explain one score
// differently.
//
// The scoring code returns reason codes, not prose, so the sentences are
// chosen here — which is also the only place they can be, since lang.go is
// excluded from the translation scan that keeps every UI string covered.

// ── Sentences ─────────────────────────────────────────────────────────────────

// trSeqReason renders the sentence behind one factor's value: not just what the
// axis scored but what in the task produced it, which is the half the score
// column can never show.
func trSeqReason(f rank.Factor) string {
	switch f.Reason {
	case rank.ReasonNoDue:
		return tr("no due date")
	case rank.ReasonOverdue:
		if f.N == 1 {
			return tr("1 day overdue")
		}
		return trCount("%d days overdue", f.N, f.N)
	case rank.ReasonDueToday:
		return tr("due today")
	case rank.ReasonDueTomorrow:
		return tr("due tomorrow")
	case rank.ReasonDueInDays:
		return trCount("due in %d days; the ramp adds points daily", f.N, f.N)
	case rank.ReasonDueBeyondRamp:
		return fmt.Sprintf(tr("due in %d days, further out than the 7-day ramp"), f.N)
	case rank.ReasonPriority:
		return fmt.Sprintf(tr("priority is %s"), tr(f.Word))
	case rank.ReasonMomentumTask:
		return tr("you worked on this task in the last 48h")
	case rank.ReasonMomentumProj:
		return fmt.Sprintf(tr("project @%s saw activity in the last 48h"), f.Word)
	case rank.ReasonMomentumTag:
		return fmt.Sprintf(tr("tag #%s saw activity in the last 48h"), f.Word)
	case rank.ReasonMomentumCold:
		return tr("nothing here was touched in the last 48h")
	case rank.ReasonSize:
		return fmt.Sprintf(tr("size is %s"), tr(f.Word))
	case rank.ReasonAgeDays:
		if f.N == 1 {
			return tr("created 1 day ago")
		}
		return trCount("created %d days ago", f.N, f.N)
	case rank.ReasonAgeToday:
		return tr("created today")
	case rank.ReasonAgeOff:
		return tr("aging is switched off in Settings")
	}
	return ""
}

// trShiftCause names what will do the moving.
func trShiftCause(c rank.ShiftCause) string {
	if c == rank.ShiftMomentum {
		return tr("momentum runs out")
	}
	return tr("the deadline ramp steps a day closer")
}

// ── Shared rows ───────────────────────────────────────────────────────────────

// seqMathColW is the width of the arithmetic column: "10.0 × 2.0 = 20.0" laid
// out so the weighted values line up under each other and under Total.
const seqMathColW = 20

// seqFactorMath renders one row's arithmetic. Knobbed dimensions show the
// multiplication that produced their points — that is the whole transparency
// claim, so it is spelled out rather than summarised; Size and Age carry no
// bias and show only the value, in the same column.
func seqFactorMath(f rank.Factor) string {
	if !f.Knobbed {
		return padLeft(fmt.Sprintf("%.1f", f.Weighted), seqMathColW)
	}
	return padLeft(fmt.Sprintf("%.1f", f.Raw), 5) +
		fmt.Sprintf(" × %.1f = ", f.Weight) +
		padLeft(fmt.Sprintf("%.1f", f.Weighted), 6)
}

// seqRelativeWhen renders how far off a forecast moment is. Distance is the
// question ("do I care today?"); the wall clock is not.
func seqRelativeWhen(now, at time.Time) string {
	d := at.Sub(now)
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins < 1 {
			mins = 1
		}
		return fmt.Sprintf(tr("in %dm"), mins)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf(tr("in %dh"), int(d.Hours()))
	}
	return fmt.Sprintf(tr("in %dd"), int(d.Hours()/24))
}

// seqShiftRank renders the predicted position, or the fact that it holds.
func seqShiftRank(e rank.Explanation, s rank.Shift) string {
	switch {
	case s.Pos == 0:
		return tr("unranked")
	case s.Pos == e.Pos:
		return fmt.Sprintf(tr("still #%d"), s.Pos)
	default:
		return fmt.Sprintf("#%d", s.Pos)
	}
}

// seqHeadline is the one-line answer: where the task sits, and the percentage
// the lists show for it.
func seqHeadline(e rank.Explanation) string {
	if e.Done {
		return tr("done: done tasks score 0 and leave the ranking")
	}
	if e.Pos == 0 {
		return fmt.Sprintf(tr("%s · not in the ranking (subtasks rank with their parent)"),
			formatPercentOf(e.Total, e.FieldMax))
	}
	return fmt.Sprintf(tr("#%d of %d by sequence · %s"), e.Pos, e.Of, formatPercentOf(e.Total, e.FieldMax))
}

// seqStartsLine says why a task with a later start date sits below work that
// scores less: the position comes from the date, not the score beside it.
func seqStartsLine(e rank.Explanation) string {
	if e.StartsOn.IsZero() {
		return ""
	}
	return fmt.Sprintf(tr("starts %s, so it ranks below the work you can start today"), e.StartsOn.Format("02-01-06"))
}

// seqScaleLine says what 100% currently costs. Normalizing against the live
// field means the mark moves when the top task is finished; naming it turns
// that from a number quietly changing under the user into a stated fact.
func seqScaleLine(e rank.Explanation) string {
	return fmt.Sprintf(tr("%s = %.1f of the %.1f points the top task scores right now"),
		formatPercentOf(e.Total, e.FieldMax), e.Total, e.FieldMax)
}

// formatPercentOf renders a score as its share of an explicit field maximum —
// the explanation carries its own, computed on the same clock and biases the
// rest of it used.
func formatPercentOf(score, max float64) string {
	return fmt.Sprintf("%d%%", rank.PercentOfField(score, max))
}

// seqNeighbourLine states the margin as the thing the user can act on: how many
// points short of the row above, how many clear of the row below.
func seqNeighbourLine(n *rank.Neighbour, above bool, width int) string {
	if n == nil {
		return ""
	}
	form := tr("%.1f points clear of #%d %s")
	if above {
		form = tr("%.1f points short of #%d %s")
	}
	// Budget the title by what the rest of the line costs, so a long title
	// truncates instead of pushing the line past the pane. Below the point
	// where a couple of letters would survive, the margin alone is the answer.
	head := fmt.Sprintf(form, n.Gap, n.Pos, "")
	budget := width - len([]rune(head)) - 4
	if budget < 4 {
		return strings.TrimSpace(head)
	}
	return fmt.Sprintf(form, n.Gap, n.Pos, "“"+truncate(n.Title, budget)+"”")
}

// explainWideEnough is the width at which the forecast block can afford its
// full heading. Below it the short form says the same thing.
const explainWideEnough = 52

// seqNoteMinW is the narrowest column a reason sentence is worth putting in.
// Under it the sentence would clip to a stub ("due in(…)"), which reads worse
// than no reason at all, so it moves to a line of its own instead.
const seqNoteMinW = 20

// ── The overlay ───────────────────────────────────────────────────────────────

// explainChromeLines is the number of fixed rows renderExplainFullscreen spends
// on its title block and footer, mirroring helpChromeLines.
const explainChromeLines = 6

// explainBodyLines builds the overlay body at the given content width. Returned
// lines are already styled; the caller clips them to the window.
// Rows budget their own columns, but the breakdown's arithmetic column has a
// floor no budget can go under — it is a number, not prose. So the overlay
// clips itself on the way out, the way the help overlay does with its key
// column, and the width contract holds at any size.
func (m model) explainBodyLines(e rank.Explanation, width int) []string {
	lines := m.explainBodyRows(e, width)
	if width > 0 {
		truncateLines(lines, width)
	}
	return lines
}

func (m model) explainBodyRows(e rank.Explanation, width int) []string {
	if width < 8 {
		width = 8
	}
	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add("  " + selectedStyle.Render(truncate(e.Title, width-2)))
	add("  " + helpStyle.Render(truncate(seqHeadline(e), width-2)))
	if !e.Done && e.FieldMax > 0 {
		add("  " + dimStyle.Render(truncate(seqScaleLine(e), width-2)))
	}
	if e.Boosted {
		add("  " + helpStyle.Render(truncate(fmt.Sprintf(
			tr("ranked on %.1f, lifted by a subtask or by work waiting on it"), e.Ranked), width-2)))
	}
	if s := seqStartsLine(e); s != "" {
		add("  " + helpStyle.Render(truncate(s, width-2)))
	}
	if m.taskSort != taskSortSequence {
		add("  " + dimStyle.Render(truncate(tr("the list is on another sort right now; this is the Sequence ranking"), width-2)))
	}
	if e.Done {
		return lines
	}
	add("")

	nameW := 0
	for _, f := range e.Factors {
		if n := len([]rune(tr(f.Name))); n > nameW {
			nameW = n
		}
	}
	nameW += 2
	// The reason is a sentence: given a column too narrow to read one in, it
	// moves to its own indented line rather than being clipped to a stub. The
	// numbers keep their column either way — they are what has to line up.
	noteW := width - 4 - nameW - seqMathColW - 3
	inlineNotes := noteW >= seqNoteMinW
	for _, f := range e.Factors {
		row := "  " + detailLabelStyle.Render(padRight(tr(f.Name), nameW)) +
			normalStyle.Render(seqFactorMath(f))
		if inlineNotes {
			add(row + helpStyle.Render("   "+truncate(trSeqReason(f), noteW)))
			continue
		}
		add(row)
		add("      " + helpStyle.Render(truncate(trSeqReason(f), width-6)))
	}
	add("  " + strings.Repeat(" ", nameW+seqMathColW-6) + dimStyle.Render("──────"))
	add("  " + detailLabelStyle.Render(padRight(tr("Total"), nameW)) +
		activeCountStyle.Render(padLeft(fmt.Sprintf("%.1f", e.Total), seqMathColW)))

	if e.Above != nil || e.Below != nil {
		add("")
		if s := seqNeighbourLine(e.Above, true, width-4); s != "" {
			add("  " + normalStyle.Render(s))
		}
		if s := seqNeighbourLine(e.Below, false, width-4); s != "" {
			add("  " + normalStyle.Render(s))
		}
	}

	add("")
	if len(e.Shifts) == 0 {
		add("  " + detailLabelStyle.Render(tr("Moves on its own:")))
		add("  " + helpStyle.Render(truncate(tr("nothing in the next three days; this position is stable"), width-2)))
		return lines
	}
	heading := tr("Moves on its own, with no edit from you:")
	if width < explainWideEnough {
		heading = tr("Moves on its own:")
	}
	add("  " + detailLabelStyle.Render(heading))
	causeW := width - 4 - 8 - 14 - 10
	for _, s := range e.Shifts {
		when := padRight(seqRelativeWhen(e.Now, s.At), 8)
		score := padLeft(fmt.Sprintf("%.1f", s.Total), 6) + padLeft(fmt.Sprintf("(%+.1f)", s.Delta), 8)
		rank := seqShiftRank(e, s)
		row := "  " + helpStyle.Render(when) + normalStyle.Render(score) + "  "
		if causeW >= seqNoteMinW {
			add(row + selectedStyle.Render(padRight(rank, 10)) +
				helpStyle.Render(truncate(trShiftCause(s.Cause), causeW)))
			continue
		}
		add(row + selectedStyle.Render(rank))
		add("      " + helpStyle.Render(truncate(trShiftCause(s.Cause), width-6)))
	}
	return lines
}

// renderExplainFullscreen draws the overlay. It is full-screen chrome of its
// own, like the help overlay, and clips itself to the window.
func (m model) renderExplainFullscreen() string {
	t := m.get(m.explainTaskID)
	if t == nil {
		t = m.currentTodo()
	}
	e := m.rank.Explain(t, m.allTodos())

	b := getBuilder()
	defer putBuilder(b)

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  "+tr("Why this rank")) + "\n")
	b.WriteString("\n")

	width := m.termWidth - 2
	body := m.explainBodyLines(e, width)
	// Clip to what the window can hold rather than scrolling: the body is a
	// fixed handful of rows, and a scroll offset would be one more thing to
	// clamp.
	if vh := m.termHeight - explainChromeLines; vh >= 0 && len(body) > vh {
		body = body[:vh]
	}
	for _, line := range body {
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  "+tr("esc or w to close  ·  tune the weights in Settings → Sequencer")) + "\n")

	lines := strings.Split(b.String(), "\n")
	if m.termWidth > 0 {
		truncateLines(lines, m.termWidth)
	}
	target := m.termHeight - 1
	if target < 0 {
		target = 0
	}
	for len(lines) < target {
		lines = append(lines, "")
	}
	if len(lines) > target {
		lines = lines[:target]
	}
	return strings.Join(lines, "\n")
}

// ── Plain text (CLI) ──────────────────────────────────────────────────────────

// explainPlainLines is the unstyled form `tjek why` prints. Same rows, same
// order, no escape sequences — the CLI is a pipe target as often as a terminal.
func explainPlainLines(e rank.Explanation) []string {
	var lines []string
	lines = append(lines, e.Title, "  "+seqHeadline(e))
	if !e.Done && e.FieldMax > 0 {
		lines = append(lines, "  "+seqScaleLine(e))
	}
	if e.Boosted {
		lines = append(lines, "  "+fmt.Sprintf(tr("ranked on %.1f, lifted by a subtask or by work waiting on it"), e.Ranked))
	}
	if s := seqStartsLine(e); s != "" {
		lines = append(lines, "  "+s)
	}
	if e.Done {
		return lines
	}
	lines = append(lines, "")
	nameW := 0
	for _, f := range e.Factors {
		if n := len([]rune(tr(f.Name))); n > nameW {
			nameW = n
		}
	}
	nameW += 2
	for _, f := range e.Factors {
		lines = append(lines, "  "+padRight(tr(f.Name), nameW)+seqFactorMath(f)+"   "+trSeqReason(f))
	}
	lines = append(lines,
		"  "+strings.Repeat(" ", nameW+seqMathColW-6)+"──────",
		"  "+padRight(tr("Total"), nameW)+padLeft(fmt.Sprintf("%.1f", e.Total), seqMathColW))

	if e.Above != nil || e.Below != nil {
		lines = append(lines, "")
		if s := seqNeighbourLine(e.Above, true, 80); s != "" {
			lines = append(lines, "  "+s)
		}
		if s := seqNeighbourLine(e.Below, false, 80); s != "" {
			lines = append(lines, "  "+s)
		}
	}

	lines = append(lines, "")
	if len(e.Shifts) == 0 {
		lines = append(lines, "  "+tr("Moves on its own:"),
			"  "+tr("nothing in the next three days; this position is stable"))
		return lines
	}
	lines = append(lines, "  "+tr("Moves on its own, with no edit from you:"))
	for _, s := range e.Shifts {
		lines = append(lines, "  "+padRight(seqRelativeWhen(e.Now, s.At), 8)+
			padLeft(fmt.Sprintf("%.1f", s.Total), 6)+padLeft(fmt.Sprintf("(%+.1f)", s.Delta), 8)+
			"  "+padRight(seqShiftRank(e, s), 10)+trShiftCause(s.Cause))
	}
	return lines
}
