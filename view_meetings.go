package main

import (
	"fmt"
	"strings"

	"github.com/Iliorn/tjek/meeting"

	"github.com/charmbracelet/x/ansi"
)

// view_meetings.go renders the Meetings tab: a single pane that shows the
// meeting list (pane == paneList) or the open meeting's detail, including
// its suggestions once it has reached StatusReady (pane == paneDetail) — the
// same single-pane-per-mode shape Settings uses (buildSettingsContent),
// rather than the Tasks-tab list+detail split, which is wired to
// m.currentTodo()/buildDetailContent and has no Meetings equivalent.

// meetingStatusGlyph is the bracketed status mark, the same convention
// boardConfig.statusBox uses for a task's row ([ ] ready, [✓] done, …).
func meetingStatusGlyph(s meeting.Status) string {
	switch s {
	case meeting.StatusRecording:
		return "[●]"
	case meeting.StatusTranscribing, meeting.StatusSummarizing:
		return "[~]"
	case meeting.StatusReady:
		return "[!]"
	case meeting.StatusReviewed:
		return "[✓]"
	case meeting.StatusError:
		return "[✗]"
	default:
		return "[ ]"
	}
}

func trMeetingStatus(s meeting.Status) string {
	switch s {
	case meeting.StatusDraft:
		return tr("draft")
	case meeting.StatusRecording:
		return tr("recording")
	case meeting.StatusTranscribing:
		return tr("transcribing…")
	case meeting.StatusSummarizing:
		return tr("summarizing…")
	case meeting.StatusReady:
		return tr("ready for review")
	case meeting.StatusReviewed:
		return tr("reviewed")
	case meeting.StatusError:
		return tr("error")
	}
	return string(s)
}

// renderMeetingsList is renderListContent's Meetings case — kept for
// consistency with every other tab's renderXList, even though
// buildMeetingsContent (the pane actually drawn) builds its own content
// directly rather than calling this.
func (m model) renderMeetingsList() string {
	if len(m.meetings) == 0 {
		return strings.Join([]string{
			normalStyle.Render(tr("  No meetings yet.")),
			dimStyle.Render(fmt.Sprintf(tr("  Press %s to add one."), effectiveKey("add", "a"))),
		}, "\n")
	}
	var lines []string
	for i, mt := range m.meetings {
		lines = append(lines, m.renderMeetingRow(mt, i == m.cursor))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderMeetingRow(mt *meeting.Meeting, selected bool) string {
	glyph := meetingStatusGlyph(mt.Status)
	date := mt.Date.Format("02 Jan")
	title := mt.Title
	line := fmt.Sprintf("%s %-10s  %s", glyph, date, title)
	if selected {
		return selectedRowStyle.Render(" " + line + " ")
	}
	style := normalStyle
	switch mt.Status {
	case meeting.StatusReady:
		style = overdueStyle // the same "needs your attention" red the Due cells use
	case meeting.StatusError:
		style = overdueStyle
	}
	return style.Render(line)
}

// buildMeetingsContent is the Meetings tab's single pane: the list, or the
// open meeting's detail — see the file header for why this doesn't share
// Tasks' side-by-side builder.
func (m model) buildMeetingsContent(w, outerH int) string {
	innerH := panelContentHeight(outerH)
	var content string
	var selectedLine int
	if m.pane == paneDetail {
		content, selectedLine = m.renderMeetingDetail(w - 2)
	} else {
		content = m.renderMeetingsList()
		selectedLine = m.cursor
	}
	lines := fitSettingsPane(content, innerH, w-2, selectedLine)
	style := listPanelFocusedStyle
	if m.pane == paneDetail {
		style = detailPanelFocusedStyle
	}
	panel := style.Width(w).Render(strings.Join(lines, "\n"))
	return withBorderTitle(panel, m.listPanelTitle(), w, true)
}

// renderMeetingDetail builds the open meeting's detail document: metadata,
// transcript, summary, and — once ready — the suggestion review list.
// Returns the content and which line the cursor is on, the same two values
// renderSettingsSection returns, for the same reason: fitSettingsPane scrolls
// the pane to keep that line in view.
func (m model) renderMeetingDetail(w int) (string, int) {
	mt := m.meetingByID(m.openMeetingID)
	if mt == nil {
		return dimStyle.Render(tr("  Meeting not found.")), 0
	}
	var b strings.Builder
	selectedLine := 0
	line := 0
	writeln := func(s string) {
		b.WriteString(s)
		b.WriteByte('\n')
		line++
	}

	writeln(fmt.Sprintf("%s %s", meetingStatusGlyph(mt.Status), trMeetingStatus(mt.Status)))
	if mt.ErrorMsg != "" {
		writeln(overdueStyle.Render("  " + mt.ErrorMsg))
	}
	writeln("")
	if len(mt.Attendees) > 0 {
		writeln(dimStyle.Render(tr("Attendees: ")) + strings.Join(mt.Attendees, ", "))
	}
	writeln(dimStyle.Render(tr("Date: ")) + mt.Date.Format("2006-01-02 15:04"))
	if mt.AudioPath != "" {
		writeln(dimStyle.Render(tr("Recording: ")) + mt.AudioPath)
	}
	writeln("")

	if mt.Summary != "" {
		writeln(titleStyle.Render(tr("Summary")))
		for _, ln := range wrapPlain(mt.Summary, w) {
			writeln(ln)
		}
		writeln("")
	}

	if len(m.meetingSuggestions) > 0 {
		writeln(titleStyle.Render(tr("Action items")))
		for i, s := range m.meetingSuggestions {
			focused := i == m.meetingReviewCursor
			if focused {
				selectedLine = line
			}
			for _, ln := range m.renderSuggestionRow(s, focused, w) {
				writeln(ln)
			}
		}
		writeln("")
	}

	if mt.Transcript != "" {
		writeln(titleStyle.Render(tr("Transcript")))
		for _, ln := range wrapPlain(mt.Transcript, w) {
			writeln(ln)
		}
	} else {
		writeln(dimStyle.Render(tr("No transcript yet.")))
	}

	return strings.TrimRight(b.String(), "\n"), selectedLine
}

// renderSuggestionRow draws one action-item review row: a checkbox-like mark
// for its resolution, the title, and its project/tags/priority/due as a dim
// trailer — two lines so the trailer never crowds the title off a narrow pane.
func (m model) renderSuggestionRow(s meeting.Suggestion, focused bool, w int) []string {
	mark := "[ ]"
	switch {
	case s.Resolved && s.Accepted:
		mark = "[✓]"
	case s.Resolved && !s.Accepted:
		mark = "[✗]"
	}
	title := fmt.Sprintf("  %s %s", mark, s.Title)
	var trailerParts []string
	if s.SuggestedProject != "" {
		trailerParts = append(trailerParts, "@"+s.SuggestedProject)
	}
	for _, tag := range s.SuggestedTags {
		trailerParts = append(trailerParts, "#"+tag)
	}
	trailerParts = append(trailerParts, "p:"+s.SuggestedPriority)
	if s.SuggestedDue != "" {
		trailerParts = append(trailerParts, "due:"+s.SuggestedDue)
	}
	trailer := "      " + strings.Join(trailerParts, " ")

	if focused {
		return []string{selectedRowStyle.Render(ansi.Truncate(title, w, "…")), selectedStyle.Render(ansi.Truncate(trailer, w, "…"))}
	}
	return []string{normalStyle.Render(title), dimStyle.Render(trailer)}
}

// wrapPlain word-wraps s to width w, a minimal wrapper for the transcript and
// summary — the one place in this view that shows free-form prose rather
// than a single-line field, so it is the one place that needs one.
func wrapPlain(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(paragraph)
		line := ""
		for _, word := range words {
			cand := word
			if line != "" {
				cand = line + " " + word
			}
			if ansi.StringWidth(cand) > w && line != "" {
				out = append(out, line)
				line = word
				continue
			}
			line = cand
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
