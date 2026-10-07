package main

import (
	"strings"
)

// view_chat.go renders the Chat tab: a scrollable conversation with the
// input textarea pinned at the bottom of the same pane. Unlike every other
// tab — a list (paneList), or Settings/Meetings' single scrolling document
// — Chat's pane is always "focused" for typing (modeChatInput), so there is
// no separate list/detail split and no cursor row to track; the thing kept
// in view is the bottom of the conversation, not a selected line.

// chatInputHeight is how many rows m.chatInput reserves at the bottom of
// the pane — rendered as its own fixed block (see buildChatContent), never
// run through fitSettingsPane's trailing-blank-line trimming: a textarea's
// blank rows are real rows of input space, not throwaway spacing the way a
// document's trailing blank line is.
const chatInputHeight = 3

func chatRoleLabel(role string) string {
	if role == "user" {
		return tr("You")
	}
	return tr("Assistant")
}

// renderChatMessages renders the conversation plus a pending action line,
// if any — not the input box, which is a live widget (textarea.View()),
// appended separately by buildChatContent. A missing API key's notice goes
// last, next to the input, so it stays in view however long the history.
func (m model) renderChatMessages(w int) string {
	notice := m.chatMissingKeyNotice()
	if len(m.chatMessages) == 0 && m.chatPendingAction == nil && !m.chatLoading && notice == "" && !m.chatConfirmReset {
		return normalStyle.Render(tr("  Ask about your tasks, projects, or meetings."))
	}
	var lines []string
	for _, msg := range m.chatMessages {
		lines = append(lines, titleStyle.Render("  "+chatRoleLabel(msg.Role)+":"))
		for _, ln := range wrapPlain(msg.Content, w-2) {
			lines = append(lines, "    "+ln)
		}
		lines = append(lines, "")
	}
	switch {
	case m.chatConfirmReset:
		lines = append(lines, confirmStyle.Render("  "+tr("Clear the whole conversation? This can't be undone")+" — y/n"))
	case m.chatPendingAction != nil:
		lines = append(lines, confirmStyle.Render("  "+m.chatPendingActionLabel+" — y/n"))
	case m.chatLoading:
		lines = append(lines, dimStyle.Render("  "+tr("thinking…")))
	}
	if notice != "" {
		for _, ln := range wrapPlain(notice, w-2) {
			lines = append(lines, overdueStyle.Render("  "+ln))
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// renderChatList is renderListContent's Chat case — kept for consistency
// with every other tab's renderXList, the same reason renderMeetingsList
// is (view_meetings.go), even though buildChatContent (the pane actually
// drawn) builds its own content directly.
func (m model) renderChatList() string {
	return m.renderChatMessages(m.termWidth - 8)
}

// chatSelectedLine is the conversation line fitSettingsPane should keep
// visible: the last one, minus however many chatScrollOffset has scrolled
// back — split the exact same way fitSettingsPane splits content
// internally, so the index lines up with what it actually counts.
func chatSelectedLine(content string, scrollOffset int) int {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	sel := len(lines) - 1 - scrollOffset
	if sel < 0 {
		return 0
	}
	return sel
}

// buildChatContent is the Chat tab's single pane: the conversation,
// scrolled to stay near its latest message, with the input textarea always
// occupying the last chatInputHeight rows — a fixed-height block appended
// after fitSettingsPane has already sized the scrollable history to what's
// left, the same chrome-budget split renderEditMeetingTextFullscreen uses
// between its heading and its own textarea.
func (m model) buildChatContent(w, outerH int) string {
	innerH := panelContentHeight(outerH)
	historyH := innerH - chatInputHeight
	if historyH < 1 {
		historyH = 1
	}
	content := m.renderChatMessages(w - 2)
	historyLines := fitSettingsPane(content, historyH, w-2, chatSelectedLine(content, m.chatScrollOffset))

	ta := m.chatInput
	ta.SetWidth(w - 2)
	ta.SetHeight(chatInputHeight)
	inputLines := strings.Split(ta.View(), "\n")
	for len(inputLines) < chatInputHeight {
		inputLines = append(inputLines, "")
	}
	inputLines = inputLines[:chatInputHeight]
	truncateLines(inputLines, w-2)

	lines := append(historyLines, inputLines...)
	panel := listPanelFocusedStyle.Width(w).Render(strings.Join(lines, "\n"))
	return withBorderTitle(panel, m.listPanelTitle(), w, true)
}
