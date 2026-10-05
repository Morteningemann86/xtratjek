package main

import (
	"fmt"
	"strings"
	"time"

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

// recordingIndicator is the "a recording is in progress" cue the status line
// shows regardless of which tab is open: recording keeps running in the
// background after navigating away or switching tabs (handleMeetingsToggleRecord
// doesn't stop it), so the status line is the one place it's visible from
// everywhere, the same way the running-task timer is.
func (m model) recordingIndicator() string {
	if m.recorder == nil {
		return ""
	}
	mt := m.meetingByID(m.recordingMeetingID)
	if mt == nil {
		return ""
	}
	elapsed := time.Since(m.recordStart)
	mm := int(elapsed.Minutes())
	ss := int(elapsed.Seconds()) % 60
	return overdueStyle.Render(fmt.Sprintf("● %s%s %02d:%02d", tr("Recording: "), truncate(mt.Title, 20), mm, ss))
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

	// Three sections, always present and in this order: what I wrote, what
	// was said, what the AI made of it. Each is its own field on Meeting
	// (Notes/Transcript/Summary) and its own in-app editor (n/T,
	// modeEditMeetingText) where editable, so none of the three can
	// overwrite another the way one shared field used to risk — see
	// meeting.Meeting's field comments.
	writeln(titleStyle.Render(tr("My notes")) + dimStyle.Render("  ("+effectiveKey("notes", "n")+")"))
	if mt.Notes != "" {
		for _, ln := range wrapPlain(mt.Notes, w) {
			writeln(ln)
		}
	} else {
		writeln(dimStyle.Render(tr("No notes yet.")))
	}
	writeln("")

	writeln(titleStyle.Render(tr("Transcript")) + dimStyle.Render("  ("+effectiveKey("edittranscript", "T")+")"))
	if mt.Transcript != "" {
		for _, ln := range wrapPlain(mt.Transcript, w) {
			writeln(ln)
		}
	} else {
		writeln(dimStyle.Render(tr("No transcript yet.")))
	}
	writeln("")

	writeln(titleStyle.Render(tr("Summary")))
	if mt.Summary != "" {
		for _, ln := range wrapPlain(mt.Summary, w) {
			writeln(ln)
		}
	} else {
		writeln(dimStyle.Render(tr("No summary yet.")))
	}

	if len(m.meetingSuggestions) > 0 {
		writeln("")
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

// meetingEditChromeLines is the fixed chrome renderEditMeetingTextFullscreen
// wraps the textarea in: a blank line, the heading, a blank line, a blank
// line, and the save/cancel hint — everything but the textarea itself.
const meetingEditChromeLines = 5

// renderEditMeetingTextFullscreen draws the in-app Notes/Transcript editor —
// full-screen chrome of its own, like the help and explain overlays, so
// editing a meeting's own text never leaves tjek the way the old $EDITOR
// round trip did.
func (m model) renderEditMeetingTextFullscreen() string {
	mt := m.meetingByID(m.meetingEditID)
	title := tr("My notes")
	if m.meetingEditField == "transcript" {
		title = tr("Transcript")
	}
	heading := title
	if mt != nil {
		heading = fmt.Sprintf("%s — %s", title, mt.Title)
	}

	b := getBuilder()
	defer putBuilder(b)

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  "+heading) + "\n")
	b.WriteString("\n")

	width := m.termWidth - 4
	if width < 1 {
		width = 1
	}
	height := m.termHeight - meetingEditChromeLines
	if height < 1 {
		height = 1
	}
	ta := m.meetingTextarea
	ta.SetWidth(width)
	ta.SetHeight(height)
	for _, ln := range strings.Split(ta.View(), "\n") {
		b.WriteString("  " + ln + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  "+tr("ctrl+s to save  ·  esc to cancel")) + "\n")

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
