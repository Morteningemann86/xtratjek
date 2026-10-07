package main

import (
	"strings"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── Detail scroll estimation ──────────────────────────────────────────────────

// estimateDetailCursorLine is which line of the rendered detail document the
// field cursor sits on. It has to agree with what view_detail.go actually
// emits, section for section: the scroll window is placed from this number, so
// a section left out here scrolls the cursor off the pane by exactly the rows
// it forgot.
func (m model) estimateDetailCursorLine() int {
	t := m.currentTodo()
	if t == nil {
		return 0
	}
	// Title moved to the border; content starts at the first field. The Stage
	// row is conditional, so the rows below it shift — walk the render order
	// rather than hard-coding an offset per field.
	rows := []detailField{fieldStartDate, fieldDueDate}
	if completedFieldVisible(t) {
		rows = append(rows, fieldCompleted)
	}
	rows = append(rows, fieldRecurrence, fieldPriority, fieldSize)
	if m.boardCfg.stageFieldVisible(t) {
		rows = append(rows, fieldStage)
	}
	rows = append(rows, fieldProject, fieldNotes)
	for i, f := range rows {
		if m.detail.field == f {
			return i
		}
	}
	if m.detail.field == fieldTags {
		// Tags label sits below the fields block; +1 skips the label row.
		return m.detailMainHeight(t) - m.detailTagsRows(t) + m.detail.tagCursor
	}

	// Relations section: one blank line after the main block, label first.
	relStart := m.detailMainHeight(t) + 1
	subRows := m.subtaskCount(t.ID)
	if subRows == 0 {
		subRows = 1
	}
	switch m.detail.field {
	case fieldSubtasks:
		return relStart + 1 + m.detail.subtaskCursor
	case fieldDependencies:
		return relStart + 1 + subRows + 2 + m.detail.depCursor
	}

	// Time entries: blank after the relations block, label first.
	teStart := relStart + m.detailRelationsHeight(t) + 1
	if m.detail.field == fieldTimeEntries {
		return teStart + 1 + m.detail.timeEntryCursor
	}

	// Comments close the document, straight after the time-entry block's own
	// trailing blank. Comments wrap, so sum the rendered line counts of
	// everything above the cursor — counting one line per comment undershoots
	// in narrow columns and the window loses the selected comment off the
	// bottom.
	line := teStart + m.detailTimeEntriesHeight(t) + 1
	available := m.termWidth - 32
	if available < 10 {
		available = 10
	}
	for i := 0; i < m.detail.commentCursor && i < len(t.Comments); i++ {
		line += commentLineCount(t.Comments[i].Text, available)
	}
	return line
}

// ── Detail scroll window ──────────────────────────────────────────────────────

// detailScrollWindow places the detail viewport: keep the offset where it is
// unless the cursor would come within detailScrollMargin of an edge, and then
// move by the least that takes. This is the whole of "scroll gradually" — the
// pane holds still while the cursor travels through it, and follows a line at
// a time once the cursor reaches the margin. Pure, so the model can pace the
// offset against its estimated geometry and the renderer can re-derive it
// against the lines it actually produced; running it twice changes nothing.
func detailScrollWindow(offset, cursor, visible, total int) int {
	if visible >= total {
		return 0
	}
	// A pane too short to hold both margins would have the two clamps cross and
	// push the cursor out of the window it is supposed to keep it in, so shrink
	// the margin to what fits: the window then centres on the cursor, which is
	// the best a four-line pane can do.
	margin := detailScrollMargin
	if fits := (visible - 1) / 2; margin > fits {
		margin = fits
	}
	if bottom := cursor - visible + 1 + margin; offset < bottom {
		offset = bottom
	}
	if top := cursor - margin; offset > top {
		offset = top
	}
	if max := total - visible; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// detailViewportHeight is how many rendered lines of the detail document the
// pane shows — the stacked panel's percentage cap, or the full column height
// when the detail sits beside the list. An estimate: the exact figure comes
// from panel geometry View computes. It only paces the stored offset, since
// applyDetailScrollN re-runs detailScrollWindow against the real line count,
// so an estimate that is off by a line costs a slightly larger step once and
// never a cursor scrolled out of sight.
func (m model) detailViewportHeight() int {
	h := m.termHeight*detailMaxHeightPct/100 - 2
	if n := m.stackedTaskDetailLines(); n > 0 {
		h = n - detailBorderLines
	}
	if m.sideBySide() {
		// A full-height column beside the list: the window less the fixed
		// header and footer, less the panel's two borders and the blank row
		// under its border title. Deliberately not listVisible(), which
		// additionally subtracts a row per active filter — those render into
		// the one fixed status line and cost the columns nothing.
		h = m.termHeight - minHeaderLines - footerHeight - m.extraOverheadLines() - 3
	}
	if h < 3 {
		return 3
	}
	return h
}

// clampDetailScroll keeps the stored offset a window that contains the field
// cursor, so the scroll position survives between keystrokes instead of being
// recomputed from the cursor every frame. Called from clampCursors with the
// other cursors: the document shrinks under the offset just as often as a list
// shrinks under its cursor — a deleted comment, a closed subtask, a task
// switched to underneath the pane.
func (m *model) clampDetailScroll() {
	if m.currentTodo() == nil {
		m.detail.scroll = 0
		return
	}
	m.detail.scroll = detailScrollWindow(
		m.detail.scroll, m.estimateDetailCursorLine(), m.detailViewportHeight(), m.detailContentHeight())
}

// ── List offset clamping ──────────────────────────────────────────────────────

// clampListOffsetVisible keeps listOffset so `cursor` stays within the next
// `visible` rows: the rows the list's renderer draws, which each caller in
// clampCursors passes (taskListRows, tagListVisibleRows,
// projectListVisibleRows).
func (m *model) clampListOffsetVisible(cursor, listLen, visible int) {
	if visible < 1 {
		visible = 1
	}
	if cursor < m.listOffset {
		m.listOffset = cursor
	}
	if cursor >= m.listOffset+visible {
		m.listOffset = cursor - visible + 1
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
	if max := listLen - visible; m.listOffset > max {
		if max < 0 {
			m.listOffset = 0
		} else {
			m.listOffset = max
		}
	}
}

// ── Detail pane placement ─────────────────────────────────────────────────────

// detailPos is where the detail pane sits relative to the list on the tabs
// that have one. Right and left are the two-column layout mirrored; bottom is
// the stacked panel a narrow window already falls back to, chosen on purpose
// at any width. There is no "top": the list is what the cursor lives in, and a
// pane above it would push the rows the keys move through away from the tab
// bar that says which list they are.
type detailPos uint8

const (
	detailRight detailPos = iota
	detailLeft
	detailBottom
)

// String is the settings.json spelling, and the only one — the file is
// hand-editable, so the words are the API. An unknown value reads as the
// default rather than erroring: a typo should cost the setting, not the start.
func (p detailPos) String() string {
	switch p {
	case detailLeft:
		return "left"
	case detailBottom:
		return "bottom"
	}
	return "right"
}

func detailPosFromSettings(s string) detailPos {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "left":
		return detailLeft
	case "bottom":
		return detailBottom
	}
	return detailRight
}

// nextDetailPos cycles the placement by dir (+1 →, -1 ←), wrapping.
func nextDetailPos(p detailPos, dir int) detailPos {
	const n = 3
	if dir == 0 {
		return p
	}
	step := 1
	if dir < 0 {
		step = n - 1
	}
	return detailPos((int(p) + step) % n)
}

// trDetailPos is the placement as the Settings row says it. The tr() calls are
// literals so lang_test.go's source scan finds them — going through a table
// keyed by the enum would hide them from it.
func trDetailPos(p detailPos) string {
	switch p {
	case detailLeft:
		return tr("Left")
	case detailBottom:
		return tr("Bottom")
	}
	return tr("Right")
}

// sideBySide reports whether the current view renders list and detail as two
// columns (list full-height on one side, the task detail on the other). The
// Tasks tab does, and so does a task opened from a tag's or project's list
// (drillDetailOpen), which is laid out the same way; below the width threshold
// both fall back to the stacked enter-to-open detail, and so does every width
// once the user has put the detail at the bottom. The Tags and Projects rows
// otherwise stack their pane under the list (splitStack): it holds a task list
// or a timeline, which read best at full width. Which side the detail takes is
// buildSideBySide's business; here it is only two columns or one, because that
// is the question every height helper is asking.
func (m model) sideBySide() bool {
	return m.detailPos != detailBottom &&
		(m.tab == tabTasks || m.drillDetailOpen()) &&
		m.termWidth >= sideBySideMinWidth
}

// drillDetailOpen reports whether a task opened from a tag's or project's task
// list has the detail pane. That view is the Tasks tab's layout with the
// group's list in the list's place, so detail_position means one thing on
// every tab.
func (m model) drillDetailOpen() bool {
	_, drilled := m.drillTaskList()
	return drilled && m.pane == paneDetail
}

// detailVisible reports whether the detail pane will be rendered as its own
// stacked panel for the current tab/mode/pane. Mirrors the showDetail
// decision in view.View so the list-height math matches what the renderer
// actually emits. In side-by-side mode the detail lives inside the list
// region's right column, so it costs no list rows and reports false here.
func (m model) detailVisible() bool {
	if m.mode != modeNormal {
		return false
	}
	if m.drillDetailOpen() {
		return !m.sideBySide()
	}
	switch m.tab {
	case tabTasks:
		return m.pane == paneDetail && !m.sideBySide()
	case tabTags:
		return true // always-on preview, stacked under the list
	case tabProjects:
		// The pane under the project list is drawn by the list itself, and a
		// task opened from it is drillDetailOpen's.
		return m.pane == paneDetail && !m.projectTaskMode
	case tabSettings, tabBoard:
		return false
	}
	return true
}

func (m model) listVisible() int {
	detailTotal := 0
	if m.detailVisible() {
		contentH := m.detailContentHeight()
		if maxH := m.maxDetailHeight(); contentH > maxH {
			contentH = maxH
		}
		// Content plus the detail pane's borders, title spacing row, and the
		// separator used when it is stacked below the list.
		detailTotal = contentH + 5
	}
	// Header/footer chrome plus the list pane's blank title-spacing row.
	fixedLines := 5
	if m.err != "" {
		fixedLines++
	}
	if m.searchQuery != "" {
		fixedLines++
	}
	if m.focusFilter {
		fixedLines++
	}
	if m.anyTimerRunning() {
		fixedLines++ // live timer line above the key hints
	}
	fixedLines += m.extraOverheadLines()
	if available := m.termHeight - fixedLines - detailTotal; available >= minListHeight {
		return available
	}
	return minListHeight
}

// taskListRows is how many rows a task list shows under its column header —
// the Tasks list, its history, and a tag's or project's list — measured the
// way View lays the screen out: the window less the header, the footer as
// drawn, a stacked task detail, and the panel's two borders, the blank row
// under its title and the column header. The renderers draw this many rows and
// the scroll clamps keep the cursor inside them, so the selected row can never
// sit below the panel's edge.
func (m model) taskListRows() int {
	return max(m.taskStackArea()-m.stackedTaskDetailLines()-taskListChromeLines, 1)
}

// taskListChromeLines is what the task list panel spends on anything but rows:
// its two borders, the blank row under its title and the column header.
const taskListChromeLines = 4

// taskStackArea is the height the task list and a detail stacked under it
// share: the window less the header and the footer as drawn.
func (m model) taskStackArea() int {
	footer := 0
	if f := m.footerContentFor(m.termWidth - 6); f != "" {
		footer = strings.Count(f, "\n") + 1
	}
	return m.termHeight - minHeaderLines - footer
}

// stackedTaskDetailLines is the height of a task detail stacked under the list,
// borders and title row included. The two split the height as the Tags and
// Projects tabs do (splitStack): a short list keeps only its rows and the
// detail takes the rest, and two full panels get half each. Zero when the
// detail sits beside the list or is shut.
func (m model) stackedTaskDetailLines() int {
	if !m.detailVisible() || (m.tab != tabTasks && !m.drillDetailOpen()) {
		return 0
	}
	area := m.taskStackArea()
	rows := 0
	if tasks, drilled := m.drillTaskList(); drilled {
		rows = len(tasks)
	} else {
		rows = m.currentTaskListLen()
	}
	listOuter := splitStack(area, rows+taskListChromeLines, m.detailContentHeight()+detailBorderLines)
	return max(area-listOuter, minDetailHeight+detailBorderLines)
}

func (m model) estimateListHeight() int {
	headerH := minHeaderLines
	if m.err != "" {
		headerH++
	}
	if m.focusFilter {
		headerH++
	}
	if m.searchQuery != "" {
		headerH++
	}
	if m.anyTimerRunning() {
		headerH++ // live timer line above the key hints
	}
	detailH := 0
	if m.detailVisible() && m.tab != tabStats {
		detailH = 13
	}
	available := m.termHeight - headerH - footerHeight - detailH - 3
	if available < minListHeight {
		return minListHeight
	}
	return available
}

// splitStack divides area rows between a Tags or Projects list and the pane
// stacked under it, both counted in outer panel rows. Each gets what it needs
// when that fits. When it doesn't, the list keeps its need up to half the
// area, and past half only what the pane leaves over: a few groups hand the
// room to the tasks under them, a short pane hands it to a long list, and two
// full panels split evenly. Returns the list's share; the pane gets the rest.
func splitStack(area, listNeed, paneNeed int) int {
	list := min(listNeed, max(area/2, area-paneNeed))
	return max(list, minListPanelLines+detailBorderLines)
}

// stackArea is the height the stacked group list and its pane share: the
// window less the header, the key hints, and the timer line above them.
func (m model) stackArea() int {
	h := m.termHeight - minHeaderLines - footerHeight
	if m.anyTimerRunning() {
		h--
	}
	return h
}

// tagStackRows is the stacked Tags tab's split: how many tag rows the list
// shows (its panel less the column header) and how many lines the pane under
// it has. View, renderTagList and the offset clamp all read it, so the rows
// drawn are the rows the cursor is kept inside.
func (m model) tagStackRows() (listRows, paneLines int) {
	area := m.stackArea()
	paneNeed := len(m.tagPaneLines(area)) + detailBorderLines
	listOuter := splitStack(area, len(m.getFilteredTagsForTab())+1+detailBorderLines, paneNeed)
	return max(panelContentHeight(listOuter)-1, 1), max(area-listOuter-detailBorderLines, minDetailHeight)
}

// tagListVisibleRows is how many tag rows the Tags tab shows: its share of a
// stacked split while the pane sits under it, and otherwise the whole list.
func (m model) tagListVisibleRows() int {
	if m.detailVisible() {
		rows, _ := m.tagStackRows()
		return rows
	}
	return m.estimateListHeight()
}

// projectListVisibleRows is how many project rows the Projects tab shows: the
// list panel's share of the list area (splitStack against the pane under it),
// less one line for the header. Both the render window (renderProjectListContent)
// and the offset clamp read this, so the project cursor can't scroll below the
// visible rows. The Projects tab hides the task detail pane, so estimateListHeight
// (detailH = 0 there) stays at or below the layout's actual list height, which
// keeps the rendered window from being clipped by the panel's own height cap.
func (m model) projectListVisibleRows() int {
	return panelContentHeight(m.projectListOuter(m.estimateListHeight())) - 1
}

// projectListOuter is the project list panel's outer height within listH.
func (m model) projectListOuter(listH int) int {
	projects := m.allProjectsForList()
	pane, _ := m.projectPane(projects, listH)
	paneNeed := len(pane) + detailBorderLines
	return splitStack(listH, len(projects)+1+detailBorderLines, paneNeed)
}

// drillTaskVisibleRows is the number of task rows a drill-in list shows when it
// is the list panel: a tag's or project's list beside or above an opened task. It is taskListRows, as the panel is the
// Tasks list's; both renderDrillTaskList and the drill offset clamp read it.
func (m model) drillTaskVisibleRows() int {
	return m.taskListRows()
}

func (m model) maxDetailHeight() int {
	available := m.termHeight - minHeaderLines - footerHeight - detailBorderLines - minListPanelLines
	if available < minDetailHeight {
		return minDetailHeight
	}
	return available
}

// detailTagsRows is the number of rows below the tags label: the tag list,
// or the one-line "no tags" hint.
func (m model) detailTagsRows(t *todo.Todo) int {
	if len(t.Tags) == 0 {
		return 1
	}
	return len(t.Tags)
}

// detailMainHeight is the rendered height of the detail column's first
// section: the fields block, blank, tags label, tag rows. The task title sits
// on the panel's top border and is not counted.
func (m model) detailMainHeight(t *todo.Todo) int {
	h := 0 // title is on the border now; content starts at the first field
	h += 9 // start, due, recurrence, priority, size, project, notes, created, id
	if m.boardCfg.stageFieldVisible(t) {
		h++
	}
	// Modified is drawn only when it differs from Created (see
	// renderDetailPage1) — an untouched task has nothing to say there.
	if !t.ModifiedAt.Equal(t.CreatedAt) {
		h++
	}
	if len(t.TimeEntries) > 0 || m.descendantTimeSpent(t.ID) > 0 {
		h++
	}
	if completedFieldVisible(t) {
		h++
	}
	if t.Status == todo.Pending {
		h++ // score
	}
	h += 2 // blank + tags label
	return h + m.detailTagsRows(t)
}

// detailRelationsHeight is the rendered height of the subtasks/dependencies/
// relations section, starting at its first label row.
func (m model) detailRelationsHeight(t *todo.Todo) int {
	rows := func(n int) int {
		if n == 0 {
			return 1
		}
		return n
	}
	subH := 1 + rows(m.subtaskCount(t.ID))
	return subH + 1 + 1 + m.detailDepRows(t)
}

// detailDepRows is the number of rows under the Dependencies label: outbound
// ↧ rows, then inbound ↥ rows, or the one-line hint when there are neither.
func (m model) detailDepRows(t *todo.Todo) int {
	out := len(t.Dependencies)
	in := len(dependentsOf(m.allTodos(), t.ID))
	if out == 0 && in == 0 {
		return 1
	}
	return out + in
}

// detailTimeEntriesHeight is the rendered height of the time-entries section:
// label, one row per entry (or the one-line empty hint), and the blank row
// that separates it from the comments below.
func (m model) detailTimeEntriesHeight(t *todo.Todo) int {
	rows := len(t.TimeEntries)
	if rows == 0 {
		rows = 1
	}
	return 1 + rows + 1
}

// detailCommentsHeight is the rendered height of the comments section:
// label plus wrapped comment lines (or the one-line empty hint).
func (m model) detailCommentsHeight(t *todo.Todo) int {
	lines := 1 // label
	if len(t.Comments) == 0 {
		return lines + 1
	}
	available := m.termWidth - 32
	if available < 10 {
		available = 10
	}
	for _, c := range t.Comments {
		lines += commentLineCount(c.Text, available)
	}
	return lines
}

// detailContentHeight is the full single-column detail document height. The
// two bare +1s are the blank rows buildDetailContent puts between its three
// pages; the time-entry block carries its own trailing blank.
func (m model) detailContentHeight() int {
	t := m.currentTodo()
	if t == nil {
		return 1
	}
	return m.detailMainHeight(t) + 1 +
		m.detailRelationsHeight(t) + 1 +
		m.detailTimeEntriesHeight(t) +
		m.detailCommentsHeight(t)
}

func (m model) extraOverheadLines() int {
	switch m.mode {
	case modeInput, modeEditComment, modeEditTag, modeEditTitle, modeEditDue,
		modeSearch, modeAddSubtask,
		modeEditSubtask, modeEditProjectInline, modeEditTimeEntry,
		modeAddTimeEntry, modeEditSyncURL, modeEditSyncToken,
		modeEditServerListen, modeEditServerToken, modeEditStages,
		modeEditExportFolder, modeImportFile,
		modeEditAnthropicKey, modeEditOpenAIKey, modeEditGeminiKey, modeEditMistralKey, modeEditFFmpegInput,
		modeEditWhisperBinOverride, modeEditWhisperLanguage,
		modeAddMeeting, modeEditSuggestion:
		return 3
	case modeSearchDep, modeSearchTag, modeSearchProject:
		return 8
	case modePalette:
		return 3 + maxPaletteResults
	case modeSearchTagTab:
		return 3
	case modeConfirm, modeConfirmUpdate, modeIdlePrompt:
		return 1
	}
	return 0
}
