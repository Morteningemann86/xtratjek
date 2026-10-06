package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Iliorn/tjek/todo"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// pickerWindowStart computes the scroll offset for the detail-pane search
// pickers (dep / tag / project). The pickers render a fixed `max`-row viewport
// and keep no persistent offset state; the window is derived purely from the
// cursor each frame so no offset field is needed.
//
// When there are results below the visible window the caller must reserve the
// last slot for a "… N more below" indicator, which means the cursor must sit
// at most at slot max−2 (not max−1). This function handles that by pulling
// start forward when needed so the cursor never lands on the indicator slot.
// Similarly, when start > 0 the first slot becomes a "… N more above"
// indicator, so the cursor must sit at slot ≥ 1; start is adjusted backward
// when needed.
//
// The caller renders exactly max lines and the cursor is always on a result
// row, never on an indicator row.
func pickerWindowStart(cursor, total, max int) (start int, hasAbove, hasBelow bool) {
	if max < 1 {
		max = 1
	}
	if total <= 0 {
		return 0, false, false
	}
	// First pass: anchor cursor at the bottom of the window.
	start = cursor - (max - 1)
	if start < 0 {
		start = 0
	}
	maxStart := total - max
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}

	hasAbove = start > 0
	hasBelow = start+max < total

	// Second pass: if the cursor would land on an indicator slot, shift start.
	//
	// hasBelow reserves the last slot (index max−1) for the below-indicator.
	// If cursor == start+max−1 (last slot), pull start forward by 1 so the
	// cursor moves to slot max−2, and recompute.
	if hasBelow && cursor == start+max-1 {
		start++
		if start > maxStart {
			start = maxStart
		}
		hasAbove = start > 0
		hasBelow = start+max < total
	}

	// hasAbove reserves the first slot (index 0) for the above-indicator.
	// If cursor == start (first slot), pull start backward by 1 so the cursor
	// moves to slot 1, and recompute.
	if hasAbove && cursor == start {
		start--
		if start < 0 {
			start = 0
		}
		hasAbove = start > 0
		hasBelow = start+max < total
		// A backward shift may again cause cursor == start+max−1 (hasBelow
		// conflict) only if max==1, which is prevented by the guard above.
	}

	return start, hasAbove, hasBelow
}

// truncateLines ANSI-aware-truncates every line to maxW display cells so
// over-long lines can never wrap inside a bordered panel.
//
// The cut carries the ellipsis marker. Cutting silently is what produced
// detail-pane rows like "No dependencies. Press 'a' to add o" — a sentence that
// stops mid-word and gives the reader no way to tell a clipped line from one
// that simply ends there.
func truncateLines(lines []string, maxW int) {
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, maxW, ellipsis)
	}
}

// panelContentHeight returns the rows available to pane content after the top
// and bottom borders and the shared blank row below the border title.
func panelContentHeight(outerH int) int {
	h := outerH - 3
	if h < 1 {
		return 1
	}
	return h
}

// withBorderTitle rewrites the top border line of a lipgloss-rendered
// rounded-border box to embed the title text, producing the standard TUI look:
//
//	╭─ Title ──────────────────╮
//	│ content …               │
//	╰──────────────────────────╯
//
// boxW is the Width() argument that was passed to the panel's .Render call
// (the content width, excluding borders and padding). focused controls which
// border color is used (accent when true, dim when false). title is plain text;
// it is ANSI-truncated with "…" so the box corners always survive.
// If the box is too narrow to embed any title the function returns rendered
// unchanged.
func withBorderTitle(rendered, title string, boxW int, focused bool) string {
	if rendered == "" {
		return rendered
	}

	// style.Width(w) with RoundedBorder produces a top line:
	//   ╭ + w dashes + ╮   (total box width w+2, excluding the 2-space margin)
	//
	// With an embedded title we replace the dash run:
	//   ╭─ <title> <fill>╮
	//   3(╭─ ) + T(title) + 1( ) + F(fill) + 1(╮) = T+F+5 = w+2
	//   → F = w - T - 3
	//
	// Require at least 1 fill dash (F≥1) → max title = w - 4 where w = boxW.
	maxTitle := boxW - 4
	if maxTitle <= 0 {
		return rendered // box too narrow for any title
	}
	title = ansi.Truncate(title, maxTitle, "…")
	titleW := ansi.StringWidth(title)
	fillW := boxW - titleW - 3
	if fillW < 1 {
		fillW = 1
	}

	borderFg := currentTheme.dim
	if focused {
		borderFg = currentTheme.accent
	}
	borderSty := lipgloss.NewStyle().Foreground(borderFg)
	titleSty := lipgloss.NewStyle().Bold(true).Foreground(currentTheme.accent)

	margin := "  " // MarginLeft(2) from detailPanelStyle / listPanelStyle
	topLine := margin +
		borderSty.Render("╭─ ") +
		titleSty.Render(title) +
		borderSty.Render(" "+strings.Repeat("─", fillW)+"╮")

	// Replace only the first line of rendered (everything up to the first \n).
	idx := strings.IndexByte(rendered, '\n')
	if idx < 0 {
		return rendered // no newline — shouldn't happen for a bordered box
	}
	return topLine + rendered[idx:]
}

// detailPanelTitle returns a short label for the detail panel's border title
// given the current tab and selected item.
func (m model) detailPanelTitle() string {
	switch m.tab {
	case tabTags:
		if m.pane == paneDetail {
			if t := m.currentTodo(); t != nil {
				return t.Title
			}
		}
		tags := m.getFilteredTagsForTab()
		if m.tagTabCursor < len(tags) {
			tag := tags[m.tagTabCursor]
			if tag == untaggedKey {
				return tr("(untagged)")
			}
			return "#" + tag
		}
		return tr("Tag")
	case tabStats:
		return m.statsPanelTitle()
	default:
		if t := m.currentTodo(); t != nil {
			// A drilled-in subtask is not a top-level task; prefix a chevron
			// so the border makes clear you're inside a subtask, not viewing
			// the parent. withBorderTitle truncates the title, not the marker.
			if len(m.detailStack) > 0 {
				return "↳ " + t.Title
			}
			return t.Title
		}
		return tr("Detail")
	}
}

// listPanelTitle names the primary content box without merely repeating the
// selected tab. Context-sensitive variants make the border explain what is in
// the pane (active work versus history, for example).
func (m model) listPanelTitle() string {
	switch m.tab {
	case tabTasks:
		title := tr("Overview")
		if m.showHistory {
			title = tr("History")
		}

		total := m.visibleActiveLen()
		if m.showHistory {
			total = len(m.completedTodos())
		}
		if pos := listPosLabel(m.cursor, total); pos != "" {
			title += " [" + pos + "]"
		}
		return title + " [" + tr("sort:") + " " + m.sortLabel() + "]"
	case tabTags:
		// Same place the Tasks tab says it: on the box whose order it names,
		// not in the status line above the tab where it read as a stray label.
		hidden := hiddenFinishedGroups(m.cache.tagGroups, m.showFinishedGroups, tagFilterMatch(strings.ToLower(m.tagTabSearchQuery)))
		return groupListTitle(m.tagOrder, hidden)
	case tabBoard:
		if m.mode == modeBoardCard {
			return tr("Card")
		}
		title := tr("Workflow")
		if cols := m.boardColumns(); len(cols) > 0 {
			start, count, _ := boardWindow(len(cols), m.board.colOffset, m.termWidth-8)
			if count > 0 && count < len(cols) {
				// Which slice of the board this is, and that ←/→ reaches the
				// rest. Without it a scrolled board just looks like a board
				// that lost its columns.
				title += fmt.Sprintf("  ‹ %d–%d/%d ›", start+1, start+count, len(cols))
			}
		}
		return title
	case tabStats:
		return tr("Summary")
	case tabSettings:
		return tr("Preferences")
	case tabMeetings:
		if m.pane == paneDetail {
			if mt := m.meetingByID(m.openMeetingID); mt != nil {
				return tr("Meeting") + " · " + truncate(mt.Title, 30)
			}
		}
		return tr("Meetings")
	case tabChat:
		return tr("Chat")
	}
	return tr("Overview")
}

// projectListTitle is groupListTitle for the Projects list.
func (m model) projectListTitle() string {
	hidden := hiddenFinishedGroups(m.cache.projectGroups, m.showFinishedGroups, projectFilterMatch(m.searchQuery))
	return groupListTitle(m.projectOrder, hidden)
}

func projectTasksTitle(project string) string {
	if project == "" {
		return tr("Overview")
	}
	return tr("Overview") + " · @" + project
}

// ── Top-level View ────────────────────────────────────────────────────────────

func (m model) View() string {
	defer m.crashGuard("view", nil)
	// One trace line per frame, pairing this render with the Update that
	// produced it (see trace.go). Nil channel = tracing off = one branch.
	if traceCh != nil {
		t0 := time.Now()
		defer func() { traceFrame(lastUpdateKind, lastUpdate, time.Since(t0)) }()
	}
	m.ensureCache()
	if m.mode == modeHelp {
		return m.renderHelpFullscreen()
	}
	if m.mode == modeExplain {
		return m.renderExplainFullscreen()
	}
	if m.mode == modeEditMeetingText {
		return m.renderEditMeetingTextFullscreen()
	}

	out := getBuilder()
	defer putBuilder(out)

	w := m.termWidth - 6

	// ── HEADER ───────────────────────────────────────────────────────────
	// One bare "?" is everything the header says about getting unstuck. It is
	// the universal key for it, the overlay it opens lists ctrl+k among the
	// shortcuts, and the palette can find the overlay back — so one character
	// reaches the whole app, and the columns a sentence would cost go to the
	// tab labels instead.
	shortcutHint := helpStyle.Render("?")
	title := titleStyle.Render("tjek")
	// Right margin of the line, then the blank columns the hint is held off the
	// tab bar by. The bar is budgeted against both, so a bar that exactly fills
	// its budget still leaves the gap standing rather than costing the hint.
	budget := m.termWidth - 4
	avail := budget - ansi.StringWidth(title) - 2 - ansi.StringWidth(shortcutHint) - headerHintGap
	tabsStr := title + "  " + m.renderTabs(avail)
	padW := budget - ansi.StringWidth(tabsStr) - ansi.StringWidth(shortcutHint)
	if padW < headerHintGap {
		// A window too narrow for both. The tabs are the navigation and the
		// hint is a courtesy, so the hint goes — whole, rather than as the
		// fragment the line's truncate would otherwise leave standing.
		shortcutHint, padW = "", 1
	}
	out.WriteString(ansi.Truncate(tabsStr+strings.Repeat(" ", padW)+shortcutHint, m.termWidth-2, "") + "\n")
	// One fixed status line, so filters and toasts never reflow the list below (see renderStatusLine).
	out.WriteString(m.renderStatusLine() + "\n")

	// ── FOOTER ───────────────────────────────────────────────────────────
	footerContent := m.buildFooterContent(w)
	footerLines := 0
	if footerContent != "" {
		footerLines = strings.Count(footerContent, "\n") + 1
	}

	// ── DETAIL (with caching) ────────────────────────────────────────────
	detailContent, detailLineCount := m.buildStackedDetail(w)

	// ── LIST ─────────────────────────────────────────────────────────────
	target := m.termHeight
	availableForList := m.listPanelOuterH(detailLineCount, footerLines)
	listContent := m.buildListContent(w, availableForList)
	listSplit := strings.Split(listContent, "\n")
	for len(listSplit) > 0 && strings.TrimSpace(listSplit[len(listSplit)-1]) == "" {
		listSplit = listSplit[:len(listSplit)-1]
	}

	// ── ASSEMBLE ─────────────────────────────────────────────────────────
	// Remove from second-to-last so the bottom border is always preserved.
	for len(listSplit) > availableForList {
		n := len(listSplit)
		listSplit = append(listSplit[:n-2], listSplit[n-1:]...)
	}
	for len(listSplit) < availableForList {
		listSplit = append(listSplit, "")
	}
	for _, line := range listSplit {
		out.WriteString(line + "\n")
	}
	if detailContent != "" {
		out.WriteString(detailContent + "\n")
	}
	if footerContent != "" {
		out.WriteString(footerContent)
	}
	result := out.String()
	resultLines := strings.Split(result, "\n")
	for len(resultLines) < target {
		resultLines = append(resultLines, "")
	}
	if len(resultLines) > target {
		resultLines = resultLines[:target]
	}

	for i, line := range resultLines {
		resultLines[i] = " " + line
	}
	return strings.Join(resultLines, "\n")

}

// buildStackedDetail renders the detail panel View stacks under the list, and
// the rows it takes; ("", 0) when the tab and mode show none.
func (m model) buildStackedDetail(w int) (string, int) {
	var detailContent string
	detailLineCount := 0
	showDetail := m.mode == modeNormal
	// For tabs that open the detail on enter / close on esc, the detail
	// panel is hidden until the user explicitly opens it. In side-by-side
	// mode the Tasks detail renders inside buildListContent's right column
	// instead of as a stacked panel.
	// A task opened from a tag's or project's list follows the Tasks tab.
	switch {
	case m.tab == tabTasks || m.drillDetailOpen():
		showDetail = showDetail && m.pane == paneDetail && !m.sideBySide()
	case m.tab == tabProjects:
		// The project's pane is drawn with the project list; a task opened
		// from it is drillDetailOpen's, above.
		showDetail = showDetail && m.pane == paneDetail && !m.projectTaskMode
	}

	if showDetail {
		switch {
		case m.tab == tabSettings, m.tab == tabBoard:
			detailContent = "" // settings and board tabs have no detail pane
		case m.tab == tabStats && !m.statsChartShown():
			detailContent = ""
		case m.tab == tabTags || m.tab == tabStats:
			detailContent = m.buildDetailContent()
		default:
			detailContent = m.getCachedDetailContent()
		}

		if detailContent != "" {
			// The stacked detail only exists while it owns keystrokes on the
			// enter-to-open tabs; the always-on previews (Tags/Stats) never do
			// — except a drilled-into tag, whose task list the cursor is in.
			focused := m.pane == paneDetail || (m.tab == tabTags && m.tagTaskMode)
			dst := detailPanelStyle
			if focused {
				dst = detailPanelFocusedStyle
			}
			// Clip to the panel's inner width first (w covers the two padding
			// columns). lipgloss cannot break a long unbroken token, so an
			// unclipped line pushes the whole box past the terminal edge on a
			// narrow window — every other pane clips for the same reason.
			detailBody := strings.Split(m.applyDetailScroll(detailContent), "\n")
			if n := m.stackedTaskDetailLines(); n > 0 {
				// A stacked task detail fills the share splitStack gave it,
				// so the list above ends where its rows do.
				detailBody = strings.Split(m.applyDetailScrollN(detailContent, n-detailBorderLines), "\n")
				for len(detailBody) < n-detailBorderLines {
					detailBody = append(detailBody, "")
				}
			}
			if m.tab == tabTags && !m.drillDetailOpen() {
				// The stacked tag pane takes its share of the height whole, so
				// the list above it is only as tall as its rows (tagStackRows).
				_, paneLines := m.tagStackRows()
				detailBody = strings.Split(m.applyDetailScrollN(detailContent, paneLines), "\n")
				for len(detailBody) < paneLines {
					detailBody = append(detailBody, "")
				}
			}
			if m.stackedTaskDetailLines() > 0 && m.currentTodo() != nil {
				// The section bar takes the panel's blank top row.
				dst = dst.PaddingTop(0)
				detailBody = append([]string{m.detailSectionBar(w - 2)}, detailBody...)
			}
			truncateLines(detailBody, w-2)
			detailContent = dst.Width(w).Render(strings.Join(detailBody, "\n"))
			detailContent = withBorderTitle(detailContent, m.detailPanelTitle(), w, focused)
			detailSplit := strings.Split(detailContent, "\n")
			for len(detailSplit) > 0 && strings.TrimSpace(detailSplit[len(detailSplit)-1]) == "" {
				detailSplit = detailSplit[:len(detailSplit)-1]
			}
			detailContent = strings.Join(detailSplit, "\n")
			detailLineCount = len(detailSplit)
		}
	}
	return detailContent, detailLineCount
}

// listPanelOuterH is the height View gives the list panel, border included,
// once the header, the stacked detail (detailLines rows) and the footer
// (footerLines rows) have theirs.
func (m model) listPanelOuterH(detailLines, footerLines int) int {
	li := computeLayout(layoutInput{
		termW:       m.termWidth,
		termH:       m.termHeight,
		mode:        m.mode,
		tab:         m.tab,
		detailLines: detailLines,
	})
	return max(m.termHeight-li.headerH-detailLines-footerLines, minListHeight)
}

// ── Status line ────────────────────────────────────────────────────────────────

// renderStatusLine builds the single fixed header status line under the tab
// bar: filter chips on the left, a recording indicator (while a meeting
// recording is in progress) and the sync-health glyph on the right. The
// Tasks-tab sort label lives beside its cursor/total counter in the Overview
// or History panel title. A toast (m.err) overlays the whole line for its
// lifetime instead of claiming its own row, so filters and toasts coming and
// going never reflow the list below.
func (m model) renderStatusLine() string {
	width := m.termWidth - 2
	if width < 1 {
		width = 1
	}
	if m.err != "" {
		style := toastErrorStyle
		switch m.errKind {
		case toastSuccess:
			style = toastSuccessStyle
		case toastInfo:
			style = toastInfoStyle
		}
		return ansi.Truncate(style.Render(m.err), width, "")
	}

	var chips []string
	if m.focusFilter {
		chips = append(chips, focusChipStyle.Render(tr("FOCUS")))
	}
	if m.searchQuery != "" {
		label := m.searchQuery
		if label == untaggedKey {
			label = tr("(untagged)")
		}
		chips = append(chips, searchChipStyle.Render("/"+label))
	}
	if m.tab == tabTags && m.tagTabSearchQuery != "" {
		chips = append(chips, searchChipStyle.Render("/"+m.tagTabSearchQuery))
	}
	left := strings.Join(chips, " ")

	var right []string
	if ri := m.recordingIndicator(); ri != "" {
		right = append(right, ri)
	}
	if g := m.syncGlyph(); g != "" {
		right = append(right, g)
	}

	return statusLineJoin(left, strings.Join(right, "  "), width)
}

// statusLineJoin left-aligns left, right-aligns right, and fills the gap so the
// result is exactly width display cells. When both can't fit, the left chips
// win (the filter you just toggled is the more urgent cue) and the line is
// truncated.
func statusLineJoin(left, right string, width int) string {
	if right == "" {
		return ansi.Truncate(left, width, "")
	}
	lw := ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	if lw+1+rw > width {
		return ansi.Truncate(left, width, "")
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

// sortLabel names the ordering currently applied to the visible task list.
// History mode has its own two sorts.
func (m model) sortLabel() string {
	if m.showHistory {
		if m.historySort == historySortAlpha {
			return tr("alpha")
		}
		return tr("completed")
	}
	switch m.taskSort {
	case taskSortDueDate:
		return tr("due")
	case taskSortSize:
		return tr("size")
	default:
		return tr("score")
	}
}

// groupListTitle names a Tags or Projects list box: its order, and how many
// finished groups it is leaving out, so a short list reads as filtered rather
// than as everything there is.
func groupListTitle(order groupSort, hidden int) string {
	title := tr("Overview") + " [" + tr("sort:") + " " + order.label() + "]"
	if hidden > 0 {
		title += " " + fmt.Sprintf(tr("[+%d finished]"), hidden)
	}
	return title
}

// syncGlyph reports background-sync health for the status line: a red mark
// after a failure, and nothing otherwise.
//
// Healthy sync says nothing on purpose: the steady state is shown in words in
// Settings. A failure means this device is drifting from the others, the one
// sync fact a user must not have to go looking for.
func (m model) syncGlyph() string {
	if !m.autoSync || !m.lastSyncFailed {
		return ""
	}
	return syncFailStyle.Render(tr("✕ sync"))
}

// ── Detail scroll ────────────────────────────────────────────────────────────

func (m model) applyDetailScroll(content string) string {
	maxVisible := m.termHeight*detailMaxHeightPct/100 - 2
	if maxVisible < 3 {
		maxVisible = 3
	}
	return m.applyDetailScrollN(content, maxVisible)
}

// applyDetailScrollN is applyDetailScroll with an explicit viewport height —
// the side-by-side detail column scrolls within the full list height rather
// than the stacked panel's percentage cap.
func (m model) applyDetailScrollN(content string, maxVisible int) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= maxVisible {
		return strings.Join(lines, "\n")
	}

	cursorLine := m.estimateDetailCursorLine()
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}

	// The stored offset is where the pane was; this moves it only as far as the
	// cursor demands, against the lines actually rendered rather than the
	// model's estimate of them.
	scrollStart := detailScrollWindow(m.detail.scroll, cursorLine, maxVisible, len(lines))
	// Within a margin of the top there is nothing to gain by hiding the first
	// rows behind a marker that costs one of them.
	if scrollStart <= detailScrollMargin {
		scrollStart = 0
	}
	end := scrollStart + maxVisible
	if end > len(lines) {
		end = len(lines)
	}

	visible := make([]string, end-scrollStart)
	copy(visible, lines[scrollStart:end])

	// The markers carry the count. A bare ellipsis said only "there is more",
	// which is the one thing the reader could already infer; what they cannot
	// see is whether one line is hidden or thirty, and that is what decides
	// whether scrolling is worth it. The list pane has said "[3/47]" in its
	// border all along — this is the detail pane's version of the same fact.
	if scrollStart > 0 {
		// The marker replaces the first visible line, so that line is hidden
		// too and counts toward the total above.
		visible[0] = dimStyle.Render(fmt.Sprintf(tr("  ↑ %d more"), scrollStart+1))
	}
	if end < len(lines) {
		visible[len(visible)-1] = dimStyle.Render(fmt.Sprintf(tr("  ↓ %d more"), len(lines)-end+1))
	}

	return strings.Join(visible, "\n")
}

// ── Footer builder ────────────────────────────────────────────────────────────

// buildFooterContent renders the footer for the current mode and clips it to
// the window. The clip lives here rather than in each branch because every
// footer variant has a minimum width of its own — the text input's box, the
// picker rows, a confirm sentence — and on a narrow window any of them would
// otherwise push past the terminal edge. View pads every line with one leading
// space, so the budget is one column less than the terminal.
func (m model) buildFooterContent(w int) string {
	out := m.footerContentFor(w)
	if out == "" || m.termWidth <= 1 {
		return out
	}
	lines := strings.Split(out, "\n")
	truncateLines(lines, m.termWidth-1)
	return strings.Join(lines, "\n")
}

func (m model) footerContentFor(w int) string {
	switch m.mode {
	case modeNormal, modeChatInput:
		hints := m.renderKeyHints(w)
		if t := m.runningTask(); t != nil {
			elapsed := ""
			if e := t.RunningEntry(); e != nil {
				elapsed = formatDurationLive(time.Since(e.StartedAt))
			}
			timerLine := timerStyle.Render("    ◉ "+truncate(t.Title, w/2)) +
				normalStyle.Render(" · "+elapsed) +
				helpStyle.Render(tr(" · t to stop"))
			return ansi.Truncate(timerLine, w, "") + "\n" + hints
		}
		return hints
	case modeInput, modeEditComment, modeEditTag, modeEditTitle, modeEditDue,
		modeAddSubtask, modeEditSubtask,
		modeEditProjectInline, modeEditTimeEntry, modeAddTimeEntry,
		modeEditSyncURL, modeEditSyncToken,
		modeEditServerListen, modeEditServerToken, modeEditStages,
		modeEditExportFolder, modeImportFile,
		modeEditAnthropicKey, modeEditOpenAIKey, modeEditGeminiKey, modeEditMistralKey, modeEditFFmpegInput,
		modeEditWhisperBinOverride, modeEditWhisperLanguage,
		modeAddMeeting, modeEditSuggestion:
		field := inputStyle.Width(w).Render(m.textInput.View())
		if m.mode == modeInput && m.pane == paneList {
			// Quick-add: on a blank input show the syntax reference (the keywords
			// stay English in every language — parsing is locale-free — so only
			// the example words are translated); once typing, replace it with a
			// live preview of the parsed fields so a mistyped token is visible.
			// While the caret sits in a #tag / @project token, the completion
			// row is the useful feedback — the parse preview comes back the
			// moment the token is finished.
			if sigil, matches := m.completionMatches(); len(matches) > 0 {
				return field + "\n" + renderQuickAddSuggestions(sigil, matches, m.suggestIndex(len(matches)), w)
			}
			if strings.TrimSpace(m.textInput.Value()) == "" {
				return field + "\n" +
					helpStyle.Render("    "+truncate(quickAddHint(), w))
			}
			return field + "\n" + renderQuickAddPreview(m.textInput.Value(), w)
		}
		if m.mode == modeEditExportFolder || m.mode == modeImportFile {
			return field + "\n" + helpStyle.Render("    "+truncate(tr("tab completes the name · enter confirms · esc cancels"), w))
		}
		if m.mode == modeEditStages {
			// The last column holds the completed tasks whatever it is called,
			// so say which one that is — otherwise renaming it looks like it
			// might have added a fifth column, or lost the done cards.
			return field + "\n" + helpStyle.Render("    "+truncate(tr("Comma-separated column names · [x] before a name gives it an icon · the last holds completed tasks"), w))
		}
		// The single-line comment inputs get a ctrl+e escape hatch to compose
		// in $EDITOR; advertise it under the field.
		switch {
		case m.mode == modeEditComment,
			m.mode == modeInput && m.pane != paneList && m.detail.field == fieldComments:
			return field + "\n" + helpStyle.Render("    "+tr("ctrl+e  edit in $EDITOR"))
		}
		return field
	case modeIdlePrompt, modeConfirmUpdate:
		// Same 4-space gutter as the key hints these prompts replace, so a
		// prompt appears where the line it stands in for was.
		return calTodayStyle.Render("    " + m.confirmMsg)
	case modeSearch:
		field := searchStyle.Width(w).Render(m.searchInput.View())
		// Same two-stage footer as quick-add: while the caret sits in a #tag /
		// @project token the completion row is the useful feedback, and the
		// parse preview comes back the moment the token is finished.
		if sigil, matches := m.completionMatches(); len(matches) > 0 {
			return field + "\n" + renderQuickAddSuggestions(sigil, matches, m.suggestIndex(len(matches)), w)
		}
		// Both are gated on the tab actually running the token grammar — on
		// Projects the query is a plain name substring, and a chip preview
		// would describe a filter that is not the one in effect.
		if m.searchUsesTokenGrammar() {
			if val := m.searchInput.Value(); strings.TrimSpace(val) != "" {
				return field + "\n" + renderSearchPreview(val, w)
			}
		}
		return field
	case modeSearchTagTab:
		return searchStyle.Width(w).Render(m.tagTabSearchInput.View())
	case modeSearchDep:
		b := getBuilder()
		defer putBuilder(b)
		b.WriteString(searchStyle.Width(w).Render(m.depSearchInput.View()))
		results := m.depSearchResults()
		shown := 0
		start, hasAbove, hasBelow := pickerWindowStart(m.depSearch.cursor, len(results), maxDepSearchResults)
		for slot := 0; slot < maxDepSearchResults; slot++ {
			idx := start + slot
			switch {
			case hasAbove && slot == 0:
				b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more above", start+1)))
				shown++
			case hasBelow && slot == maxDepSearchResults-1:
				below := len(results) - (start + slot)
				b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more below", below)))
				shown++
			case idx < len(results):
				r := results[idx]
				if idx == m.depSearch.cursor {
					b.WriteString("\n" + selectedStyle.Render(cursorGap+cursorMark+r.Title))
				} else {
					b.WriteString("\n" + normalStyle.Render("    "+r.Title))
				}
				shown++
			default:
				b.WriteString("\n")
				shown++
			}
		}
		return b.String()
	case modeSearchTag:
		b := getBuilder()
		defer putBuilder(b)
		b.WriteString(searchStyle.Width(w).Render(m.tagSearchInput.View()))
		results := m.tagSearchResults()
		shown := 0
		if len(results) == 0 && m.tagSearch.query != "" {
			b.WriteString("\n" + dimStyle.Render(cursorGap+cursorMark+tr("create new tag: ")) + tagStyle.Render(m.tagSearch.query))
			shown++
		} else {
			start, hasAbove, hasBelow := pickerWindowStart(m.tagSearch.cursor, len(results), maxTagSearchResults)
			for slot := 0; slot < maxTagSearchResults; slot++ {
				idx := start + slot
				switch {
				case hasAbove && slot == 0:
					b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more above", start+1)))
					shown++
				case hasBelow && slot == maxTagSearchResults-1:
					below := len(results) - (start + slot)
					b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more below", below)))
					shown++
				case idx < len(results):
					r := results[idx]
					if idx == m.tagSearch.cursor {
						b.WriteString("\n" + selectedStyle.Render(cursorGap+cursorMark+"#"+r))
					} else {
						b.WriteString("\n" + normalStyle.Render("    #"+r))
					}
					shown++
				default:
					b.WriteString("\n")
					shown++
				}
			}
		}
		for shown < maxTagSearchResults {
			b.WriteString("\n")
			shown++
		}
		return b.String()
	case modeSearchProject:
		b := getBuilder()
		defer putBuilder(b)
		b.WriteString(searchStyle.Width(w).Render(m.projSearchInput.View()))
		results := m.projSearchResults()
		shown := 0
		if len(results) == 0 && m.projSearch.query != "" {
			b.WriteString("\n" + dimStyle.Render(cursorGap+cursorMark+tr("create new project: ")) + selectedStyle.Render(m.projSearch.query))
			shown++
		} else {
			start, hasAbove, hasBelow := pickerWindowStart(m.projSearch.cursor, len(results), maxProjSearchResults)
			for slot := 0; slot < maxProjSearchResults; slot++ {
				idx := start + slot
				switch {
				case hasAbove && slot == 0:
					b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more above", start+1)))
					shown++
				case hasBelow && slot == maxProjSearchResults-1:
					below := len(results) - (start + slot)
					b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more below", below)))
					shown++
				case idx < len(results):
					r := results[idx]
					if idx == m.projSearch.cursor {
						b.WriteString("\n" + selectedStyle.Render(cursorGap+cursorMark+r))
					} else {
						b.WriteString("\n" + normalStyle.Render("    "+r))
					}
					shown++
				default:
					b.WriteString("\n")
					shown++
				}
			}
		}
		for shown < maxProjSearchResults {
			b.WriteString("\n")
			shown++
		}
		return b.String()
	case modePalette:
		return m.renderPalette(w)
	case modeConfirm:
		return confirmStyle.Render("    " + m.confirmMsg)
	case modeBoardCarry:
		return helpStyle.Render("    " + tr("←/→ carry to a column · enter/esc put it down"))
	case modeBoardCard:
		return helpStyle.Render("    " + tr("↑/↓ previous/next card · enter edit in Tasks · esc close"))
	}
	return ""
}

// renderPalette draws the command palette: the query field, then the matching
// commands with the key each one presses and the tab it belongs to. Sized to
// maxPaletteResults so the block above it never reflows as the list narrows.
func (m model) renderPalette(w int) string {
	b := getBuilder()
	defer putBuilder(b)
	b.WriteString(searchStyle.Width(w).Render(m.paletteInput.View()))

	results := m.paletteResults(m.paletteInput.Value())
	sel := m.paletteSelection(len(results))
	if len(results) == 0 {
		b.WriteString("\n" + dimStyle.Render("    "+tr("No command matches that.")))
		for i := 1; i < maxPaletteResults; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}
	start, hasAbove, hasBelow := pickerWindowStart(sel, len(results), maxPaletteResults)
	for slot := 0; slot < maxPaletteResults; slot++ {
		idx := start + slot
		switch {
		case hasAbove && slot == 0:
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more above", start+1)))
		case hasBelow && slot == maxPaletteResults-1:
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  … %d more below", len(results)-(start+slot))))
		case idx < len(results):
			c := results[idx]
			// Right-align the key + section so the labels form a readable
			// column on the left, the way the list tabs do.
			meta := c.key
			if c.section != "" {
				meta += "  " + c.section
			}
			label := c.label
			// The row occupies the field's own text columns — a 4-cell gutter
			// for the cursor (margin 2 + border + padding), then the field's
			// inner width — so the palette reads as one block with the box
			// above it rather than a list shifted out from under it.
			inner := w - 2
			gap := inner - len([]rune(label)) - len([]rune(meta))
			if gap < 2 {
				gap = 2
				label = truncate(label, inner-2-len([]rune(meta)))
			}
			row := label + strings.Repeat(" ", gap) + meta
			if idx == sel {
				b.WriteString("\n" + selectedStyle.Render(cursorGap+cursorMark+row))
			} else {
				b.WriteString("\n" + normalStyle.Render("    "+row))
			}
		default:
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ── Key hints ─────────────────────────────────────────────────────────────────

// hintLabelOverrides names what the toggle keys would do to the currently
// selected task, rather than what they do in general. A footer that says
// "t track" over a task that is already being tracked is not a hint, it is a
// wrong answer; the keys toggle, so the label has to as well.
// An empty label drops the key from the line: it does nothing right now.
func (m model) hintLabelOverrides() map[string]string {
	var over map[string]string
	set := func(action, label string) {
		if over == nil {
			over = make(map[string]string, 2)
		}
		over[action] = label
	}
	if m.tab == tabStats && !m.statsChartShown() {
		set("statscycle", "")
	}
	t := m.currentTodo()
	if t == nil {
		return over
	}
	if t.IsTimerRunning() {
		set("track", "stop")
	}
	if t.Status == todo.Done {
		set("done", "reopen")
	}
	return over
}

func (m model) renderKeyHints(w int) string {
	// Both the hint line and the help overlay are generated from the keymap
	// registry (keymap.go), so they can't drift from each other or from
	// dispatch.
	ctx := m.currentKeyCtx()
	over := m.hintLabelOverrides()
	hints := hintString(ctx, false, over)
	// Prefer the full hint line; when it can't fit, fall back to the curated
	// short (primary-only) set instead of truncating mid-list — plain
	// truncation always cut the same trailing keys (e.g. / filter on the Tasks
	// tab), hiding them at common terminal widths. hints is pre-Render plain
	// text, so rune length is the display width.
	if short := hintString(ctx, true, over); short != "" && len([]rune(hints)) > w {
		hints = short
	}
	// 4-space indent aligns the hint under the box's inner content (margin 2 +
	// border 1 + padding 1) — so it begins at the same column as the task rows.
	return helpStyle.Render("    " + truncate(hints, w))
}

// ── Detail content ────────────────────────────────────────────────────────────

func (m model) buildDetailContent() string {
	switch {
	// A task opened out of the tag drill shows its detail, wherever
	// detail_position puts it; without this the pane would keep showing the
	// tag summary while the detail keyset was live.
	case m.tab == tabTags && m.pane == paneDetail && m.currentTodo() != nil:
		t := m.currentTodo()
		return m.renderDetailPage1(t) + "\n" +
			m.renderDetailPage2(t) + "\n" +
			m.renderDetailPage3(t)
	case m.tab == tabTags:
		lines := m.buildTagDetailLines()
		if len(lines) == 0 {
			return ""
		}
		return strings.Join(lines, "\n")
	case m.tab == tabStats:
		return m.renderStatsDetail()
	default:
		t := m.currentTodo()
		if t == nil {
			return dimStyle.Render("  No task selected.")
		}
		// One continuous column: fields+tags, relations, comments. Sections
		// scroll as a single document; left/right jump between section heads.
		return m.renderDetailPage1(t) + "\n" +
			m.renderDetailPage2(t) + "\n" +
			m.renderDetailPage3(t)
	}
}

// drillListLines is the tag's or project's task list the cursor is walking,
// drawn as a list panel of visible rows (see drillDetailOpen).
func (m model) drillListLines(visible int) []string {
	tasks, _ := m.drillTaskList()
	var sum *groupSummary
	if m.tab == tabTags {
		if tags := m.getFilteredTagsForTab(); m.tagTabCursor < len(tags) {
			sum = m.cache.tagGroups[tags[m.tagTabCursor]]
		}
		return m.renderDrillTaskList(tasks, sum, true, visible)
	}
	if projects := m.allProjectsForList(); m.projectCursor < len(projects) {
		sum = m.cache.projectGroups[projects[m.projectCursor]]
	}
	return m.renderDrillTaskList(tasks, sum, false, visible)
}

// drillListTitle names the drill list's panel after its group.
func (m model) drillListTitle() string {
	if m.tab == tabTags {
		tags := m.getFilteredTagsForTab()
		switch {
		case m.tagTabCursor >= len(tags):
			return tr("Overview")
		case tags[m.tagTabCursor] == untaggedKey:
			return tr("Overview") + " · " + tr("(untagged)")
		}
		return tr("Overview") + " · #" + tags[m.tagTabCursor]
	}
	if projects := m.allProjectsForList(); m.projectCursor < len(projects) {
		return projectTasksTitle(projects[m.projectCursor])
	}
	return projectTasksTitle("")
}

// ── List content builder ──────────────────────────────────────────────────────

func (m model) buildListContent(w, outerH int) string {
	if m.drillDetailOpen() {
		if m.sideBySide() {
			return m.buildSideBySide(w, outerH)
		}
		// Detail at the bottom: the group's list is the list panel, and View
		// stacks the detail under it as on the Tasks tab.
		innerH := max(panelContentHeight(outerH), 0)
		lines := m.drillListLines(m.drillTaskVisibleRows())
		for len(lines) < innerH {
			lines = append(lines, "")
		}
		lines = lines[:innerH]
		truncateLines(lines, w-2)
		panel := listPanelStyle.Width(w).Render(strings.Join(lines, "\n"))
		return withBorderTitle(panel, m.drillListTitle(), w, false)
	}
	if m.tab == tabProjects {
		return m.buildProjectListContent(w, outerH)
	}
	if m.tab == tabCalendar {
		return m.buildCalendarContent(w, outerH)
	}
	if m.tab == tabSettings {
		return m.buildSettingsContent(w, outerH)
	}
	if m.tab == tabMeetings {
		return m.buildMeetingsContent(w, outerH)
	}
	if m.tab == tabChat {
		return m.buildChatContent(w, outerH)
	}
	if m.sideBySide() {
		return m.buildSideBySide(w, outerH)
	}

	innerH := panelContentHeight(outerH)
	rawList := m.buildListLines()
	if m.tab == tabStats {
		rawList = scrollWindowLines(trimTrailingBlank(rawList), min(m.statsScroll, m.statsMaxScroll()), innerH)
	}
	for len(rawList) < innerH {
		rawList = append(rawList, "")
	}
	if len(rawList) > innerH {
		rawList = rawList[:innerH]
	}
	truncateLines(rawList, w-2)
	panel := listPanelStyle.Width(w).Render(strings.Join(rawList, "\n"))
	return withBorderTitle(panel, m.listPanelTitle(), w, false)
}

// scrollWindowLines is the h lines of lines from offset on, with the first
// and last replaced by a count of what is hidden above and below — the detail
// pane's markers, for a pane scrolled without a cursor.
func scrollWindowLines(lines []string, offset, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}
	offset = max(0, min(offset, len(lines)-h))
	visible := make([]string, h)
	copy(visible, lines[offset:offset+h])
	if offset > 0 {
		visible[0] = dimStyle.Render(fmt.Sprintf(tr("  ↑ %d more"), offset+1))
	}
	if end := offset + h; end < len(lines) {
		visible[h-1] = dimStyle.Render(fmt.Sprintf(tr("  ↓ %d more"), len(lines)-end+1))
	}
	return visible
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// statsSummaryRows is how many lines of the Stats summary the list panel
// shows: what View leaves it under the Activity chart and above the footer.
func (m model) statsSummaryRows() int {
	w := m.termWidth - 6
	_, detailLines := m.buildStackedDetail(w)
	footerLines := 0
	if f := m.buildFooterContent(w); f != "" {
		footerLines = strings.Count(f, "\n") + 1
	}
	return panelContentHeight(m.listPanelOuterH(detailLines, footerLines))
}

// statsMaxScroll is the furthest ↓ scrolls the Stats summary: its last line
// at the bottom of the panel.
func (m model) statsMaxScroll() int {
	return max(0, len(trimTrailingBlank(m.buildListLines()))-m.statsSummaryRows())
}

// buildSideBySide renders the Tasks tab side by side as
// two columns: the list keeps full height on the left and the detail pane is an
// always-on preview of the cursor item on the right. Mirrors buildCalendarContent's
// approach — each
// column is rendered through a model copy whose termWidth is the column's
// share, so the existing width math (list columns, tag fitting, the no-wrap
// contract, the detail's own two-column threshold) applies per column
// unchanged. The focused pane carries the accent border.
func (m model) buildSideBySide(w, outerH int) string {
	innerH := panelContentHeight(outerH)
	detailW := w * sideDetailColPct / 100
	if detailW < sideDetailColMin {
		detailW = sideDetailColMin
	}
	if detailW > sideDetailColMax {
		detailW = sideDetailColMax
	}
	listW := w - detailW - 4
	if listW < minInnerWidth {
		listW = minInnerWidth
	}

	lm := m
	lm.termWidth = listW + 6 // View hands buildListContent w = termWidth-6
	// The narrowed copy is only for responsive column sizing. If the detail
	// column owns focus, leaving paneDetail set makes the list-height helpers
	// interpret this now-narrow model as the stacked layout and reserve rows
	// for a second detail panel below the list. The real detail is already in
	// the right column, so size the list copy as the list pane.
	lm.pane = paneList
	listLines := lm.buildListLines()
	listTitle := m.listPanelTitle()
	if m.drillDetailOpen() {
		// The copy's pane is the list's, so it no longer counts as open; ask
		// the real model and render the drill list through the narrowed copy.
		listLines, listTitle = lm.drillListLines(m.drillTaskVisibleRows()), m.drillListTitle()
	}

	dm := m
	dm.termWidth = detailW + 6
	var detailLines []string
	switch {
	case m.currentTodo() == nil:
		detailLines = []string{"", dimStyle.Render(tr("  No task selected."))}
	default:
		detailLines = strings.Split(dm.applyDetailScrollN(dm.buildDetailContent(), innerH), "\n")
	}

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
	listLines = fitLines(listLines, innerH, listW-2)
	detailLines = fitLines(detailLines, innerH, detailW-2)

	listStyle, detailStyle := listPanelFocusedStyle, detailPanelStyle
	detailFocused := m.pane == paneDetail
	if detailFocused {
		listStyle, detailStyle = listPanelStyle, detailPanelFocusedStyle
	}
	listPanel := listStyle.Width(listW).Render(strings.Join(listLines, "\n"))
	if m.currentTodo() != nil {
		// The section bar takes the panel's blank top row.
		detailStyle = detailStyle.PaddingTop(0)
		detailLines = append([]string{m.detailSectionBar(detailW - 2)}, detailLines...)
	}
	detailPanel := detailStyle.Width(detailW).Render(strings.Join(detailLines, "\n"))
	listPanel = withBorderTitle(listPanel, listTitle, listW, !detailFocused)
	detailPanel = withBorderTitle(detailPanel, m.detailPanelTitle(), detailW, detailFocused)
	// Only the order changes with the placement: both columns are already
	// sized and clipped, so mirroring the layout is one swap rather than a
	// second set of width math that could drift from this one.
	if m.detailPos == detailLeft {
		return lipgloss.JoinHorizontal(lipgloss.Top, detailPanel, listPanel)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, detailPanel)
}

func (m model) buildProjectListContent(w, listH int) string {
	projects := m.allProjectsForList()
	if len(projects) == 0 {
		empty := m.renderProjectListContent(nil)
		if m.searchQuery == "" && len(m.cache.projectGroups) == 0 {
			empty += "\n" + dimStyle.Render(tr("  A project groups its tasks into a timeline on this tab."))
		}
		innerH := panelContentHeight(listH)
		emptyLines := strings.Split(empty, "\n")
		for len(emptyLines) < innerH {
			emptyLines = append(emptyLines, "")
		}
		if len(emptyLines) > innerH {
			emptyLines = emptyLines[:innerH]
		}
		panel := listPanelStyle.Width(w).Render(strings.Join(emptyLines, "\n"))
		return withBorderTitle(panel, m.projectListTitle(), w, false)
	}

	// ── Project list + the selected project's pane, stacked ────────────────
	// Drilled in, the cursor walks the pane's task list and the project list
	// stays above it, as on the Tags tab.
	listOuter := m.projectListOuter(listH)
	projMaxH := panelContentHeight(listOuter)
	projLines := strings.Split(m.renderProjectListContent(projects), "\n")
	projEnd := len(projLines)
	for projEnd > 0 && strings.TrimSpace(projLines[projEnd-1]) == "" {
		projEnd--
	}
	projLines = projLines[:projEnd]
	if len(projLines) > projMaxH {
		projLines = projLines[:projMaxH]
	}
	for len(projLines) < projMaxH {
		projLines = append(projLines, "")
	}
	truncateLines(projLines, w-2)
	projRendered := listPanelStyle.Width(w).Render(strings.Join(projLines, "\n"))
	projRendered = withBorderTitle(projRendered, m.projectListTitle(), w, false)

	projRenderedLines := strings.Split(projRendered, "\n")
	ganttOuterH := listH - len(projRenderedLines)
	if ganttOuterH < minListPanelLines+3 {
		ganttOuterH = minListPanelLines + 3
	}
	ganttInnerH := panelContentHeight(ganttOuterH)

	ganttLines, paneTitle := m.projectPane(projects, ganttInnerH)
	if len(ganttLines) > ganttInnerH {
		ganttLines = ganttLines[:ganttInnerH]
	}
	for len(ganttLines) < ganttInnerH {
		ganttLines = append(ganttLines, "")
	}
	truncateLines(ganttLines, w-2)
	paneStyle := listPanelStyle
	if m.projectTaskMode {
		paneStyle = listPanelFocusedStyle
	}
	ganttRendered := paneStyle.Width(w).Render(strings.Join(ganttLines, "\n"))
	ganttRendered = withBorderTitle(ganttRendered, paneTitle, w, m.projectTaskMode)

	b := getBuilder()
	defer putBuilder(b)
	b.WriteString(projRendered)
	b.WriteString("\n")
	b.WriteString(ganttRendered)
	return b.String()
}

// projectPane is the pane under the project list: the selected project's
// summary and its task list — the one the cursor walks once enter drills in —
// with the timeline beside the rows when there is dated open work to put on
// one (projectPaneRows).
func (m model) projectPane(projects []string, maxLines int) (lines []string, title string) {
	if m.projectCursor >= len(projects) {
		return nil, ""
	}
	project := projects[m.projectCursor]
	tasks := m.getProjectTasks(project)
	sel := -1
	if m.projectTaskMode {
		sel = m.cursor
	}
	lines = m.groupPane(m.cache.projectGroups[project], tasks, sel, nil, maxLines, func(start, shown int) []string {
		return m.projectPaneRows(tasks, start, shown, sel)
	})
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end], "@" + project
}

// projectPaneRows draws a project's column header and task rows, with the
// timeline beside them when the project has dated open work and the pane is
// wide enough for both. The timeline is bars only, row for row with the list,
// so the list's titles and due dates are its labels and the selected row's
// guide carries the eye across the gap.
func (m model) projectPaneRows(tasks []todo.Todo, start, shown, sel int) []string {
	w := m.termWidth - 8
	if w < projStripMinWidth || !hasDatedOpenTask(tasks) {
		return m.renderGroupTaskRows(tasks, start, shown, sel, false)
	}
	stripW := min(max(w*sideDetailColPct/100, sideDetailColMin), sideDetailColMax)
	listW := w - stripW - projStripGap
	lm := m
	lm.termWidth = listW + 8 // the row renderers draw termWidth-8 cells
	// The list is what the pane is for and the timeline an extra: when the
	// strip would cost the list a column it shows at full width (Score, Due
	// or Size), the list keeps the width and the strip waits for a wider
	// window.
	full, _ := m.groupTaskCols(tasks, false)
	if beside, _ := lm.groupTaskCols(tasks, false); beside.showLast != full.showLast ||
		beside.showDue != full.showDue || beside.showSize != full.showSize {
		return m.renderGroupTaskRows(tasks, start, shown, sel, false)
	}
	left := lm.renderGroupTaskRows(tasks, start, shown, sel, false)
	right := m.renderGanttStrip(tasks, stripW, start, shown, sel)
	sep := dimStyle.Render(" │ ")
	out := make([]string, max(len(left), len(right)))
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if lw := ansi.StringWidth(l); lw > listW {
			l = ansi.Truncate(l, listW, "")
		} else {
			l += strings.Repeat(" ", listW-lw)
		}
		out[i] = l + sep + r
	}
	return out
}

// ── Help ──────────────────────────────────────────────────────────────────────

// helpChromeLines is the number of fixed rows renderHelpFullscreen spends on
// the title block and footer around the scrolling body. Kept in sync with the
// literal writes below so helpViewportH can size the body to never push the
// footer off-screen (the final pad/truncate then lands the footer at the
// bottom exactly).
const helpChromeLines = 7

// helpBodyLines renders the scrollable body of the help overlay — every key
// section plus the date-input reference — as one styled line per slice entry,
// with a blank line between sections. Title and footer are chrome and live in
// renderHelpFullscreen. Shared with the scroll clamp so both agree on length.
// helpSec is one titled block of the help overlay: a section name and its
// key/description rows.
type helpSec struct {
	title string
	keys  [][2]string
}

// filterHelpSections keeps the rows matching query — case-insensitively, in the
// key, the description or the section title, so both "x" and "delete" and
// "board" find something — and drops sections left with no rows. An empty query
// keeps everything.
func filterHelpSections(sections []helpSec, query string) []helpSec {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return sections
	}
	out := make([]helpSec, 0, len(sections))
	for _, sec := range sections {
		if strings.Contains(strings.ToLower(sec.title), q) {
			out = append(out, sec)
			continue
		}
		var kept [][2]string
		for _, kv := range sec.keys {
			if strings.Contains(strings.ToLower(kv[0]), q) || strings.Contains(strings.ToLower(kv[1]), q) {
				kept = append(kept, kv)
			}
		}
		if len(kept) > 0 {
			out = append(out, helpSec{sec.title, kept})
		}
	}
	return out
}

func (m model) helpBodyLines() []string {
	// Key sections are generated from the keymap registry (keymap.go), so the
	// help overlay can't drift from the footer hints or from dispatch.
	var sections []helpSec
	for _, title := range helpSectionOrder {
		var keys [][2]string
		for i := range keymap {
			if bd := &keymap[i]; bd.section == title {
				keys = append(keys, [2]string{effectiveKey(bd.action, bd.key), tr(bd.desc)})
			}
		}
		if len(keys) > 0 {
			sections = append(sections, helpSec{tr(title), keys})
		}
	}
	// Reference sections: the token grammars. They lived only in the README and
	// in the one-line hint under the input, which is gone the moment you need to
	// look something up. Keep in sync with parseQuickAdd (helpers.go) and
	// compileSearch — TestHelpDocumentsEveryToken asserts every token the
	// parsers accept appears here.
	//
	// Tokens are spelled in the active language (inputWord / inputToken,
	// lang_input.go) because that is what the parser now accepts there —
	// advertising `due:friday` on a Danish screen was the visible half of the
	// mismatch. English keeps parsing everywhere, so nothing here revokes it.
	sections = append(sections, helpSec{tr("Quick-add syntax"), [][2]string{
		{"#tag", tr("add a tag (existing tags are suggested; tab inserts)")},
		{"@project", tr("put it in a project")},
		{inputWord("due:") + inputToken("tomorrow"), tr("set a due date (see Date input below)")},
		{"p:" + inputWord("high"), fmt.Sprintf(tr("priority: %s / %s / %s (p:h, p:m, p:l)"),
			inputWord("high"), inputWord("medium"), inputWord("low"))},
		{"s:l", fmt.Sprintf(tr("size: s / m / l (also %s)"), inputWord("size:")+inputWord("large"))},
		{"r:" + inputWord("weekly"), fmt.Sprintf(tr("repeat: %s / %s / %s / %s / %s"),
			inputWord("daily"), inputWord("weekdays"), inputWord("weekly"),
			inputWord("monthly"), inputWord("yearly"))},
		{inputWord("dep:") + "^", fmt.Sprintf(tr("block on the last added task (or %s<id prefix>)"), inputWord("dep:"))},
	}})
	sections = append(sections, helpSec{tr("Filters"), [][2]string{
		{"#tag", tr("only tasks carrying the tag")},
		{"@project", tr("only tasks in the project")},
		{"p:" + inputWord("high"), tr("only that priority")},
		{inputWord("due:") + "<" + strings.ToLower(localizedWeekday(time.Friday)), tr("due before a date (also >, <=, >= and an exact date)")},
		{inputWord("overdue"), tr("only overdue tasks")},
		{"grcrs", tr("anything else fuzzy-matches the title, or the description as text")},
	}})

	// Reference section: the annotation glyphs a task row can carry. Not key
	// bindings, so like Date input it lives outside the keymap registry. Keep in
	// sync with renderTaskLineWithSet.
	sections = append(sections, helpSec{tr("Row symbols"), [][2]string{
		{"[ ]", tr("ready to start (ST column)")},
		{"[>]", tr("in progress: time has been logged against it (ST column)")},
		{"[!]", tr("overdue (ST column)")},
		{"[✓]", tr("done (ST column)")},
		{"[B]", tr("its board column's icon, once columns have icons (ST column)")},
		{"⧗", tr("timer running")},
		{"↧", tr("blocked: waiting on an unfinished dependency; sorts last")},
		{"↥", tr("others depend on this: finishing it unblocks them")},
		{"↻", tr("recurring task")},
		{"(2/5)", tr("subtasks done / total")},
		{"+ / -", tr("subtasks collapsed / expanded")},
		{"↑", tr("score lifted by a subtask or by work waiting on it (detail pane)")},
	}})

	// Reference section: the status line. Everything here appears only in a
	// state that is not the default — the line is empty when there is nothing
	// to say — so each entry answers "why is that there?", which is the
	// question a symbol in the corner of the screen actually provokes.
	sections = append(sections, helpSec{tr("Status line"), [][2]string{
		{"✕ sync", tr("background sync is failing: Settings has the error")},
		{tr("FOCUS"), tr("the focus filter is on: today + overdue only")},
		{"/…", tr("a filter is narrowing the list")},
	}})

	// Reference section: date-input grammar. Not key bindings, so it lives
	// outside the registry and is appended last.
	sections = append(sections, helpSec{tr("Date input"), [][2]string{
		{"dd-mm-yy", tr("exact date (e.g. 15-06-25)")},
		{inputWord("today"), tr("today's date")},
		{inputWord("tomorrow"), tr("tomorrow")},
		{inputWord("next week"), tr("7 days from now")},
		{inputWord("next month"), tr("1 month from now")},
		{strings.ToLower(localizedWeekday(time.Monday)) + ".." + strings.ToLower(localizedWeekday(time.Sunday)), tr("next occurrence of weekday")},
		{"+3d / +2w / +1m", tr("relative days/weeks/months")},
	}})

	// A filter narrows the rows and drops the sections left empty, so a query
	// answers "which key was it?" without scrolling the whole registry.
	sections = filterHelpSections(sections, m.helpFilter)
	if len(sections) == 0 {
		return []string{helpStyle.Render("  " + tr("No shortcut matches that."))}
	}

	var lines []string
	for _, section := range sections {
		lines = append(lines, detailLabelStyle.Render("  "+section.title))
		for _, kv := range section.keys {
			key := padRight(kv[0], 24)
			lines = append(lines,
				helpStyle.Render("  ")+
					selectedStyle.Render(key)+
					normalStyle.Render(kv[1]))
		}
		lines = append(lines, "")
	}
	return lines
}

// helpViewportH is how many body rows fit on screen once the title block and
// footer are reserved. Floored so tiny terminals still show something.
func (m model) helpViewportH() int {
	h := m.termHeight - helpChromeLines
	if h < 3 {
		h = 3
	}
	return h
}

// clampHelpScroll keeps a proposed scroll offset within [0, maxScroll] for a
// body of `total` lines shown through a `viewport`-row window.
func clampHelpScroll(scroll, total, viewport int) int {
	max := total - viewport
	if max < 0 {
		max = 0
	}
	if scroll > max {
		scroll = max
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

func (m model) renderHelpFullscreen() string {
	body := m.helpBodyLines()
	vh := m.helpViewportH()
	scroll := clampHelpScroll(m.helpScroll, len(body), vh)
	end := scroll + vh
	if end > len(body) {
		end = len(body)
	}

	b := getBuilder()
	defer putBuilder(b)

	b.WriteString("\n")
	title := titleStyle.Render("  " + tr("Keyboard shortcuts"))
	if m.helpFilter != "" || m.helpFiltering {
		caret := ""
		if m.helpFiltering {
			caret = "▌"
		}
		// A plain inline suffix, not searchStyle — that one draws the boxed
		// input used at the bottom of the screen.
		title += helpStyle.Render("   /") + selectedStyle.Render(m.helpFilter+caret)
	}
	b.WriteString(title + "\n")
	b.WriteString("\n")

	for _, line := range body[scroll:end] {
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	hint := tr("/ filter  ·  ? or esc to close")
	if m.helpFiltering {
		hint = tr("type to filter  ·  enter keep  ·  esc clear")
	} else if m.helpFilter != "" {
		hint = tr("/ filter  ·  esc clear  ·  ? to close")
	}
	if len(body) > vh {
		var scrollHint string
		switch {
		case scroll > 0 && end < len(body):
			scrollHint = tr("↑/↓ scroll")
		case scroll > 0:
			scrollHint = tr("↑ scroll up")
		default:
			scrollHint = tr("↓ scroll down")
		}
		hint = scrollHint + "  ·  " + hint
	}
	b.WriteString(helpStyle.Render("  "+hint) + "\n")

	lines := strings.Split(b.String(), "\n")
	// The overlay is full-screen chrome of its own, so it clips itself — the
	// key column alone is wider than a narrow window.
	if m.termWidth > 0 {
		truncateLines(lines, m.termWidth)
	}
	// A terminal reporting height 0 (startup, a mid-drag resize) would make the
	// target negative and slice out of range below.
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

// ── Stats detail (activity heatmap) ──────────────────────────────────────────

// statsCell is one position in the activity histogram grid. gi is the gradient
// index, or -1 for dim/structural glyphs (baseline, separators, labels).
// bg is an optional second gradient index for half-block cells (▀ ▄), where
// the cell shows two stacked colours; -1 means no background colour.
type statsCell struct {
	ch rune
	gi int
	bg int
}

// statsBucket is one column of the Activity chart: the day (or Monday-started
// week) it covers, and how many top-level tasks were completed inside it.
type statsBucket struct {
	start time.Time
	count int
}

// statsActivity builds the Activity chart's buckets for the selected range,
// with the range's label, whether a bucket spans a week, and the total. Shared
// by the chart and by the panel title that names the range, so the two cannot
// disagree about what the count counts.
func (m model) statsActivity() (label string, buckets []statsBucket, weekly bool, total int) {
	now := m.frameTime
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch m.statsRange {
	case statsRange30Days:
		label = tr("Last 30 days")
		for d := 29; d >= 0; d-- {
			buckets = append(buckets, statsBucket{start: today.AddDate(0, 0, -d)})
		}
	case statsRange6Months:
		label = tr("Last 26 weeks")
		weekly = true
		curMon := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		for w := 25; w >= 0; w-- {
			buckets = append(buckets, statsBucket{start: curMon.AddDate(0, 0, -w*7)})
		}
	default:
		label = tr("Last 7 days")
		for d := 6; d >= 0; d-- {
			buckets = append(buckets, statsBucket{start: today.AddDate(0, 0, -d)})
		}
	}

	first := buckets[0].start
	for _, t := range m.tasks {
		if t.Status != todo.Done || t.CompletedAt.IsZero() || t.ParentID != "" {
			continue
		}
		// CompletedAt comes back from storage in UTC; resolve the day in
		// the user's local zone (same zone as `today`/`first`), otherwise
		// a task closed in the morning local-time can land in tomorrow's
		// UTC date and get skipped as "after today".
		ca := t.CompletedAt.In(now.Location())
		d := time.Date(ca.Year(), ca.Month(), ca.Day(), 0, 0, 0, 0, now.Location())
		if d.Before(first) || d.After(today) {
			continue
		}
		idx := int(d.Sub(first).Hours()/24 + 0.5)
		if weekly {
			idx /= 7
		}
		if idx < 0 || idx >= len(buckets) {
			continue
		}
		buckets[idx].count++
		total++
	}
	return label, buckets, weekly, total
}

// statsPanelTitle is the Activity pane's border title, carrying the caption
// so it costs no row: the range, the completions in it, and what
// a block stands for. On the border it costs nothing, and the row it gives
// back is one more block a busy day can stack. Dropped back to front as the
// window narrows, so the least useful half goes first.
func (m model) statsPanelTitle() string {
	name := tr("Activity")
	label, _, _, total := m.statsActivity()
	scope := "[" + label + " · " + trCount("%d done", total, total) + "]"
	legend := "[" + tr("1 block = 1 completed task") + "]"
	budget := m.termWidth - 10 // withBorderTitle's own max: (termWidth-6) - 4
	for _, form := range []string{
		name + "  " + scope + "  " + legend,
		name + "  " + scope,
		scope, // narrow: the range outranks the pane's own name, which the tab already gives
		"[" + label + "]",
		name,
	} {
		if ansi.StringWidth(form) <= budget {
			return form
		}
	}
	return name // withBorderTitle truncates from here
}

// statsChartShown reports whether the window is tall enough for the Activity
// chart under the summary. Below statsChartMinTermH the chart and the summary
// would each get a sliver; the summary holds the numbers, so it takes the
// height, and enter (which only changes the chart's range) goes quiet.
func (m model) statsChartShown() bool {
	return m.termHeight >= statsChartMinTermH
}

// statsChartHeight is the tallest the Activity chart may draw, and so the
// per-bar cap: past it the bars halve to two tasks a row (▀/▄), and past twice
// it they cap with `+`. The caption moved to the border title, so the pane is
// nothing but chart and takes the panel's budget less the baseline and
// axis-label rows. Capped both ways: a floor so a short window still shows a
// chart, and a ceiling near the gradient's length, past which more blocks stop
// reading as taller and the stats list is the better use of the rows.
//
// On a tall window the summary can end well above its panel's bottom edge.
// Those rows go to the chart instead (statsChartSpareRows), up to what the
// layout gives a stacked pane, so the space shows taller bars rather than
// nothing.
func (m model) statsChartHeight() int {
	h := m.termHeight*detailMaxHeightPct/100 - 2 - 8
	if h < statsChartMinH {
		return statsChartMinH
	}
	if h > statsChartMaxH {
		h = statsChartMaxH
	}
	if spare := m.statsChartSpareRows(h); spare > 0 {
		h = min(h+spare, max(m.statsChartCeiling(), h))
	}
	return h
}

// statsChartCeiling is the most bar rows the layout's cap on a stacked pane
// leaves room for once the pane's border and the baseline and label rows are
// taken, so a chart grown into spare rows is never clipped at the bottom.
func (m model) statsChartCeiling() int {
	return m.termHeight*detailMaxHeightPct/100 - 2 - 2
}

// statsChartSpareRows is how many rows the Stats summary's panel would leave
// blank under its last line if the chart drew chartH rows: the window less the
// header, the footer, both panels' borders, the chart with its baseline and
// label row, and the summary itself. Negative when the summary already
// scrolls.
func (m model) statsChartSpareRows(chartH int) int {
	footerLines := footerHeight
	if f := m.buildFooterContent(m.termWidth - 6); f != "" {
		footerLines = strings.Count(f, "\n") + 1
	}
	summary := len(trimTrailingBlank(strings.Split(m.renderStatsList(), "\n")))
	return m.termHeight - minHeaderLines - footerLines - 2 - summary - 2 - (chartH + 2)
}

// statsChartRows is the height the chart actually draws at: the budget, or the
// busiest bucket when that is shorter. Rows above the tallest bar are blank in
// a fixed-height chart — spending the pane's height on them costs the stats
// list rows to show nothing.
func statsChartRows(budget int, buckets []statsBucket) int {
	peak := 0
	for _, bk := range buckets {
		if bk.count > peak {
			peak = bk.count
		}
	}
	if peak >= budget {
		return budget
	}
	if peak < statsChartMinH {
		return statsChartMinH
	}
	return peak
}

func (m model) renderStatsDetail() string {
	b := getBuilder()
	defer putBuilder(b)

	innerW := m.termWidth - 8
	if innerW < 12 {
		innerW = 12
	}
	gradLen := len(statsGradient)

	// The range's name is on the border title now; the chart only needs its
	// shape.
	_, buckets, weekly, total := m.statsActivity()

	budget := m.statsChartHeight()
	chartH := statsChartRows(budget, buckets)

	if total == 0 {
		// The range and the count are on the border; all this row has to say
		// is that there is nothing to draw.
		b.WriteString("  " + dimStyle.Render(tr("No completions in this range.")) + "\n")
		return b.String()
	}
	// Pick a bar width that fills the available width (capped so a handful of
	// bars don't become absurdly fat), with a 1-column gap between bars.
	avail := innerW - 2
	n := len(buckets)
	maxBw := 3
	if m.statsRange == statsRange7Days {
		maxBw = 10 // wide enough to spell weekday names under each bar
	}
	bw := (avail - (n - 1)) / n
	if bw < 1 {
		bw = 1
	}
	if bw > maxBw {
		bw = maxBw
	}
	slot := bw + 1 // bar + gap
	if maxN := (avail + 1) / slot; n > maxN {
		buckets = buckets[n-maxN:] // most recent that fit
		n = len(buckets)
	}
	chartW := n*bw + (n - 1)
	leftMargin := 2 + (avail-chartW)/2 // centre the chart in the pane

	// Every week is numbered. One-column bars leave no room for two digits
	// side by side, so the numbers alternate between two label rows, and the
	// second row comes out of the bars' height so the chart stays as tall.
	labelRows := 1
	if weekly && bw < 2 && chartH > 1 {
		labelRows = 2
		chartH--
	}

	// Stretch the vertical scale when any bucket overflows chartH, so a busy
	// day collapses to half-height (with a ▄ cap for odd counts) instead of
	// capping immediately with a `+`. One step (×2) keeps the chart honest.
	blockScale := 1
	peak := 0
	for _, bk := range buckets {
		peak = max(peak, bk.count)
	}
	if peak > chartH {
		blockScale = 2
	}
	// The other way, when the summary leaves rows blank under its last line,
	// each task takes several rows, so the bars grow into that space rather
	// than sit at its foot. A block is still one task: its rows share one
	// colour.
	rowsPerTask := 1
	if peak > 0 && blockScale == 1 {
		room := min(chartH+max(m.statsChartSpareRows(chartH+labelRows-1), 0), m.statsChartCeiling()-(labelRows-1))
		if k := min(room/peak, statsChartRowsPerTaskMax); k > 1 {
			rowsPerTask = k
			chartH = peak * k
		}
	}

	if leftMargin < 2 {
		leftMargin = 2
	}

	// Compose into a grid (gi: -1 = dim/structural, >=0 = gradient index), then
	// render each row grouping same-styled runs.
	rows := chartH + 1 + labelRows // bars + baseline + labels
	grid := make([][]statsCell, rows)
	for r := range grid {
		grid[r] = make([]statsCell, chartW)
		for c := range grid[r] {
			grid[r][c] = statsCell{' ', -1, -1}
		}
	}
	barStart := func(k int) int { return k * slot }
	gradIdx := func(task int) int {
		if task >= gradLen {
			return gradLen - 1
		}
		return task
	}

	// At scale=1 each task is a full row (█). At scale=2 (any bucket > chartH)
	// the bars halve in row-height: each row holds two tasks via ▀/▄ half
	// blocks (fg=top task, bg=bottom task), so up to 2*chartH = 10 tasks fit
	// before `+` kicks in.
	for k := 0; k < n; k++ {
		start := barStart(k)
		cnt := buckets[k].count

		if blockScale == 1 {
			for r := 0; r < chartH && r < cnt*rowsPerTask; r++ {
				ch := '█'
				gi := gradIdx(r / rowsPerTask)
				if cnt > chartH && r == chartH-1 {
					ch = '+'
				}
				rowIdx := chartH - 1 - r
				for c := 0; c < bw; c++ {
					grid[rowIdx][start+c] = statsCell{ch, gi, -1}
				}
			}
			continue
		}

		overflow := cnt > 2*chartH
		for r := 0; r < chartH; r++ { // r = 0 is the bottom row
			bot := 2 * r   // bottom-half task index
			top := 2*r + 1 // top-half task index
			rowIdx := chartH - 1 - r
			var ch rune
			var gi int
			bg := -1
			switch {
			case overflow && r == chartH-1:
				ch = '+'
				gi = gradIdx(top)
			case cnt > top: // both halves present
				botGi := gradIdx(bot)
				topGi := gradIdx(top)
				if botGi == topGi {
					ch = '█'
					gi = topGi
				} else {
					ch = '▀' // upper half: fg = top, bg = bottom
					gi = topGi
					bg = botGi
				}
			case cnt > bot: // only bottom half
				ch = '▄' // lower half: fg = bottom
				gi = gradIdx(bot)
			default:
				continue
			}
			for c := 0; c < bw; c++ {
				grid[rowIdx][start+c] = statsCell{ch, gi, bg}
			}
		}
	}

	// Baseline.
	for c := 0; c < chartW; c++ {
		grid[chartH][c] = statsCell{'─', -1, -1}
	}

	// Dotted separators between weeks (30-day view).
	if m.statsRange == statsRange30Days {
		for k := 0; k < n-1; k++ {
			if buckets[k+1].start.Weekday() == time.Monday {
				col := barStart(k) + bw // the gap column after bar k
				for r := 0; r < rows; r++ {
					grid[r][col] = statsCell{'·', -1, -1}
				}
			}
		}
	}

	// Axis labels. Tagged with gi=-2 so renderCellRow picks the brighter
	// axis style (statsAxisStyle) — distinct from baseline ─ / dotted ·
	// separators which stay dim (gi=-1) to keep the chart structural
	// elements visually quiet.
	label := grid[chartH+1]
	if weekly {
		// "w42" where the bar is wide enough to hold it, the bare number
		// where it is not. A label that would run off the right edge is
		// pulled back inside it.
		for k := 0; k < n; k++ {
			_, wk := buckets[k].start.ISOWeek()
			lbl := fmt.Sprintf("w%d", wk)
			if len(lbl) > bw {
				lbl = strconv.Itoa(wk)
			}
			row := label
			if labelRows == 2 && (n-1-k)%2 == 1 {
				row = grid[chartH+2]
			}
			start := min(barStart(k), chartW-len(lbl))
			for j, ch := range lbl {
				if c := start + j; c >= 0 && c < chartW {
					row[c] = statsCell{ch, -2, -1}
				}
			}
		}
	} else {
		// Weekday labels under each daily bar, widening with the bars: full names
		// when there's room (7-day view), a 3-letter abbreviation when medium, a
		// single initial when narrow.
		for k := 0; k < n; k++ {
			wd := buckets[k].start.Weekday()
			var lbl string
			switch {
			case m.statsRange == statsRange7Days && bw >= 9:
				lbl = localizedWeekday(wd) // e.g. "Wednesday"
			case m.statsRange == statsRange7Days && bw >= 3:
				lbl = localizedWeekdayShort(wd) // e.g. "Wed"
			default:
				lbl = string(localizedWeekdayInitial(wd))
			}
			// Cells, not bytes: "Lørdag" is six cells in seven bytes, and a
			// byte index would leave a hole after the ø.
			runes := []rune(lbl)
			start := barStart(k) + (bw-len(runes))/2
			if start < barStart(k) {
				start = barStart(k)
			}
			for j, ch := range runes {
				if c := start + j; c >= 0 && c < chartW {
					label[c] = statsCell{ch, -2, -1}
				}
			}
		}
	}

	margin := strings.Repeat(" ", leftMargin)
	for r := 0; r < rows; r++ {
		b.WriteString(margin + renderCellRow(grid[r]) + "\n")
	}
	return b.String()
}

// renderCellRow renders a histogram grid row, grouping consecutive cells that
// share a style into one Render call and dropping trailing blanks. When a
// cell has bg >= 0, the segment is rendered with that secondary colour as the
// terminal background — used by ▀ half-blocks to stack two task colours in
// the same cell.
func renderCellRow(cells []statsCell) string {
	last := -1
	for c := range cells {
		if cells[c].ch != ' ' {
			last = c
		}
	}
	if last < 0 {
		return ""
	}
	var sb strings.Builder
	for c := 0; c <= last; {
		g := cells[c].gi
		bg := cells[c].bg
		start := c
		for c <= last && cells[c].gi == g && cells[c].bg == bg {
			c++
		}
		seg := make([]rune, 0, c-start)
		for _, cl := range cells[start:c] {
			seg = append(seg, cl.ch)
		}
		switch {
		case g == -2:
			sb.WriteString(statsAxisStyle.Render(string(seg)))
		case g < 0:
			sb.WriteString(dimStyle.Render(string(seg)))
		default:
			if bg >= 0 && bg < len(statsGradient) {
				style := lipgloss.NewStyle().
					Foreground(statsGradient[g].GetForeground()).
					Background(statsGradient[bg].GetForeground())
				sb.WriteString(style.Render(string(seg)))
			} else {
				sb.WriteString(statsGradient[g].Render(string(seg)))
			}
		}
	}
	return sb.String()
}

// ── Build helpers ─────────────────────────────────────────────────────────────

func (m model) buildListLines() []string {
	return strings.Split(m.renderListContent(), "\n")
}

// buildTagDetailLines is the pane stacked under the tag list, windowed to its
// share of the height (tagStackRows).
func (m model) buildTagDetailLines() []string {
	_, maxLines := m.tagStackRows()
	return m.tagPaneLines(maxLines)
}

// tagPaneLines is the selected tag's pane with its task list windowed to
// maxLines around the drill cursor.
func (m model) tagPaneLines(maxLines int) []string {
	tags := m.getFilteredTagsForTab()
	if len(tags) == 0 || m.tagTabCursor >= len(tags) {
		return strings.Split(dimStyle.Render("  No tag selected."), "\n")
	}
	tag := tags[m.tagTabCursor]

	// availW is the panel's inner text width (see View: w = termWidth-6, minus
	// the panel's horizontal padding).
	availW := max(m.termWidth-8, 12)

	// The task list comes from tagTaskList — the same ordered slice the drill
	// cursor walks, so the row highlighted here is the row the keys act on.
	tasks := m.tagTaskList(tag)
	var extra []string
	if line := m.cooccurringTagsLine(tag, tasks, availW); line != "" {
		extra = append(extra, line)
	}
	sel := -1
	if m.tagTaskMode {
		sel = m.cursor
	}
	// Windowed here, around the cursor, rather than left to the generic scroll.
	return m.groupPaneLines(m.cache.tagGroups[tag], tasks, sel, extra, maxLines, true)
}

// cooccurringTagsLine names the tags most often found beside tag on its open
// tasks, as many as fit on one line.
func (m model) cooccurringTagsLine(tag string, tasks []todo.Todo, availW int) string {
	cooccur := make(map[string]int)
	for i := range tasks {
		for _, tt := range tasks[i].Tags {
			if tt != tag {
				cooccur[tt]++
			}
		}
	}
	if len(cooccur) == 0 {
		return ""
	}
	type coTag struct {
		name string
		n    int
	}
	co := make([]coTag, 0, len(cooccur))
	for name, n := range cooccur {
		co = append(co, coTag{name, n})
	}
	sort.Slice(co, func(i, j int) bool {
		if co[i].n != co[j].n {
			return co[i].n > co[j].n
		}
		return co[i].name < co[j].name
	})
	label := tr("  often with: ")
	budget := availW - len([]rune(label))
	var chips []string
	used := 0
	for _, c := range co {
		chip := "#" + c.name
		w := len([]rune(chip))
		if len(chips) > 0 {
			w++ // separating space
		}
		if used+w > budget {
			break
		}
		chips = append(chips, chip)
		used += w
	}
	if len(chips) == 0 {
		return ""
	}
	return dimStyle.Render(label) + tagStyle.Render(strings.Join(chips, " "))
}

// groupPaneLines is the pane under a Tags or Projects list, and the Tags tab's
// drilled-in list: the group's counts, any extra lines the tab adds, then its
// tasks in the order enter walks them, windowed to maxLines around sel (-1
// when nothing is selected), and the done tasks' fold line.
func (m model) groupPaneLines(s *groupSummary, tasks []todo.Todo, sel int, extra []string, maxLines int, showProject bool) []string {
	return m.groupPane(s, tasks, sel, extra, maxLines, func(start, shown int) []string {
		return m.renderGroupTaskRows(tasks, start, shown, sel, showProject)
	})
}

// groupPane lays out the pane under a Tags or Projects list: the group's
// summary, then its task list windowed around sel, drawn by rows — the column
// header and tasks[start:start+shown] — so a tab can draw more beside each row
// (the Projects timeline) and still be windowed and sized the same way.
func (m model) groupPane(s *groupSummary, tasks []todo.Todo, sel int, extra []string, maxLines int, rows func(start, shown int) []string) []string {
	lines := append(m.groupPaneHead(s, m.termWidth-8), extra...)
	lines = append(lines, "")
	fold := m.groupFoldNote(s)
	// On a short pane the summary above gives way, last line first, until the
	// column header fits with a few rows around the cursor (one row when the
	// list is only a preview) and the line under them that says what is not
	// shown. The counts line goes last.
	want := min(len(tasks), 1)
	if sel >= 0 {
		want = min(len(tasks), 3)
	}
	if len(tasks) > want || fold != "" {
		want++
	}
	for len(lines) > 1 && maxLines-len(lines)-1 < want {
		lines = lines[:len(lines)-1]
	}
	if len(tasks) == 0 {
		lines = append(lines, dimStyle.Render(tr("  Nothing open here.")))
		if fold != "" {
			lines = append(lines, fold)
		}
		return lines
	}

	budget := max(maxLines-len(lines)-1, 1) // -1: the column header
	if fold != "" && len(tasks) < budget {
		budget-- // room for the fold line under a list that fits
	}
	start, shown := 0, min(len(tasks), budget)
	more := 0
	if len(tasks) > budget {
		shown = max(budget-1, 1) // a line for the "and N more" notice, when there is one to spare
		if sel >= shown {
			start = sel - shown + 1
		}
		start = max(min(start, len(tasks)-shown), 0)
		more = len(tasks) - start - shown
	}
	lines = append(lines, rows(start, shown)...)
	switch {
	case more > 0 && len(lines) < maxLines:
		lines = append(lines, dimStyle.Render(fmt.Sprintf(tr("  … and %d more"), more)))
	case more == 0 && fold != "" && len(lines) < maxLines:
		lines = append(lines, fold)
	}
	return lines
}

// ── Tabs ──────────────────────────────────────────────────────────────────────

func (m model) renderTabs(avail int) string {
	activeStyles := [numTabs]lipgloss.Style{
		tabTasksActiveStyle,
		tabCalendarActiveStyle,
		tabTagsActiveStyle,
		tabProjectsActiveStyle,
		tabBoardActiveStyle,
		tabStatsActiveStyle,
		tabSettingsActiveStyle,
		tabMeetingsActiveStyle,
		tabChatActiveStyle,
	}
	inactiveStyles := [numTabs]lipgloss.Style{
		tabTasksInactiveStyle,
		tabCalendarInactiveStyle,
		tabTagsInactiveStyle,
		tabProjectsInactiveStyle,
		tabBoardInactiveStyle,
		tabStatsInactiveStyle,
		tabSettingsInactiveStyle,
		tabMeetingsInactiveStyle,
		tabChatInactiveStyle,
	}
	// The selected tab renders as a solid colored pill. Unselected tabs use
	// the per-tab color as the foreground so each tab keeps its identity
	// without a background block.
	full := [numTabs]string{tr("1 Tasks"), tr("2 Calendar"), tr("3 Tags"), tr("4 Projects"), tr("5 Board"), tr("6 Stats"), tr("7 Settings"), tr("8 Meetings"), tr("9 Chat")}
	nums := [numTabs]string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

	// abbr is the curated short label, one per tab, not a mechanical cut of the
	// full one. Clipping to three letters produced "5 Boa", "6 Sta", "7 Set" —
	// three tabs whose short forms say nothing and two that are nearly the same
	// word — and it degrades worse in translation, where a German "7 Ein" is
	// left of "Einstellungen". A tab that is already short keeps its full label
	// here, which is why several English entries repeat it: shortening a
	// five-letter word to buy two cells costs more than the cells are worth.
	// The labels live in tabShortLabels (lang.go); a language without a row
	// falls back to English.
	abbr, ok := tabShortLabels[activeLang]
	if !ok {
		abbr = tabShortLabels[langEN]
	}

	// No overdue badge here. A count pinned to the Tasks label ("1 Tasks !3")
	// puts two numbers on one tab, and the leading one is the key you press —
	// so the second read as a shortcut, not a warning. Overdue work is stated
	// where it can be acted on: the red Due cells in the list, the Tasks-tab
	// counter, and Stats.

	// The bar degrades a level at a time, most verbose first: everything full;
	// the selected tab full and the rest abbreviated; everything abbreviated;
	// the selected tab full and the rest bare numbers. The third level is what
	// an 80-column window needs on the tabs with long names — without it,
	// switching to Calendar, Projects or Settings kept "2 Calendar" and paid
	// for it by collapsing every other tab to a digit, so the bar changed shape
	// under the user on every tab switch. The selected tab is a solid pill, so
	// its short label identifies it as well as the long one does.
	// tabsWidthMixed measures an arrangement where the selected tab is fixed at
	// selLabel and the others use the given candidates array.
	selFull := full[m.tab]
	if selRunes := []rune(selFull); avail > 0 && len(selRunes) > avail {
		// Degenerate: selected title alone exceeds avail — clip it rather than
		// overflow; unselected tabs collapse to bare numbers.
		selFull = string(selRunes[:avail])
	}

	levels := []struct {
		unsel [numTabs]string
		sel   string
	}{
		{full, selFull},
		{abbr, selFull},
		{abbr, abbr[m.tab]},
	}
	// Fallback: bare numbers always fit (single rune each).
	unselNames, selLabel := nums, selFull
	for _, l := range levels {
		if m.tabsWidthMixed(l.unsel, m.tab, l.sel) <= avail {
			unselNames, selLabel = l.unsel, l.sel
			break
		}
	}

	names := unselNames
	names[m.tab] = selLabel

	// A hidden tab is dropped from the bar, leaving a visible gap in the
	// numbering (…4 Tags  6 Stats…). The numbers stay fixed on purpose: they
	// are baked into the labels and their translations, and renumbering would
	// move Stats under the user's fingers every time the board is toggled.
	// A gap reads as "something is off", which is exactly true.
	parts := make([]string, 0, numTabs)
	for i := range names {
		if !m.boardCfg.tabVisible(tab(i)) {
			continue
		}
		if tab(i) == m.tab {
			parts = append(parts, activeStyles[i].Render(names[i]))
		} else {
			parts = append(parts, inactiveStyles[i].Render(names[i]))
		}
	}
	return strings.Join(parts, " ")
}

// tabsWidthMixed measures the width of a mixed tab bar where tab sel uses
// selLabel and all other tabs use the corresponding label from names
// (rune length of the pre-style plain text, single-space separators).
//
// Every tab is rendered through a style with Padding(0, 1), so each one is two
// cells wider than its label. Leaving that out let renderTabs pick a level that
// measured within budget and rendered fourteen cells past it, which the header
// paid for by truncating whatever sat to the right of the bar — the shortcut
// hint, mid-word.
func (m model) tabsWidthMixed(names [numTabs]string, sel tab, selLabel string) int {
	visible := m.boardCfg.visibleTabCount()
	w := visible - 1 // single-space separators
	w += visible * 2 // the per-tab Padding(0, 1)
	for i, n := range names {
		if !m.boardCfg.tabVisible(tab(i)) {
			continue
		}
		if tab(i) == sel {
			w += len([]rune(selLabel))
		} else {
			w += len([]rune(n))
		}
	}
	return w
}

func (m model) renderListContent() string {
	switch m.tab {
	case tabTasks:
		if m.showHistory {
			return m.renderHistoryList()
		}
		return m.renderTaskList()
	case tabProjects:
		return m.renderProjectListContent(m.allProjectsForList())
	case tabTags:
		return m.renderTagList()
	case tabBoard:
		return m.renderBoardList()
	case tabStats:
		return m.renderStatsList()
	case tabSettings:
		return m.renderSettingsList()
	case tabMeetings:
		return m.renderMeetingsList()
	case tabChat:
		return m.renderChatList()
	}
	return ""
}
