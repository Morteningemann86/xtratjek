package main

// focus.go — the unified focus/esc stack. Every enterable UI state (detail
// pane, project drill-in, calendar timeline, history, filter, the committed
// searches) is recorded here in entry order, so esc always backs out of the
// most recently entered state instead of following a per-tab priority list.
// Entries are tab-tagged because tab switching is non-destructive: a state
// survives a detour to another tab, and esc must only pop states belonging
// to the tab the user is looking at.

// uiState identifies one enterable UI state whose exit esc owns.
type uiState uint8

const (
	stateDetailPane uiState = iota
	stateProjectDrill
	stateTagDrill
	stateCalTimeline
	stateHistory
	stateFocusFilter
	stateSearch
	stateTagSearch
)

type focusEntry struct {
	tab   tab
	state uiState
}

// pushFocus records entering a state on the current tab. Re-entering a state
// (committing a new search over an old one) moves it to the top instead of
// duplicating it.
func (m *model) pushFocus(s uiState) {
	m.removeFocus(m.tab, s)
	m.focusStack = append(m.focusStack, focusEntry{tab: m.tab, state: s})
}

// dropFocus removes the current tab's entry for a state without running its
// exit action — for states that exit through their own toggle key (h/f)
// or a side effect rather than esc.
func (m *model) dropFocus(s uiState) {
	m.removeFocus(m.tab, s)
}

// removeFocus rebuilds the stack without the given entry. It copies rather
// than splicing in place: model values are copied throughout the tea loop,
// and an in-place shift would mutate every copy sharing the backing array.
func (m *model) removeFocus(tb tab, s uiState) {
	if len(m.focusStack) == 0 {
		return
	}
	out := make([]focusEntry, 0, len(m.focusStack))
	for _, e := range m.focusStack {
		if e.tab == tb && e.state == s {
			continue
		}
		out = append(out, e)
	}
	m.focusStack = out
}

// focusActive reports whether a recorded state is still actually on. A stale
// entry (state exited without dropFocus) is discarded by popFocus rather
// than eating an esc press.
func (m *model) focusActive(e focusEntry) bool {
	switch e.state {
	case stateDetailPane:
		return m.pane == paneDetail
	case stateProjectDrill:
		return m.projectTaskMode
	case stateTagDrill:
		return m.tagTaskMode
	case stateCalTimeline:
		return m.calendar.focusTimeline
	case stateHistory:
		return m.showHistory
	case stateFocusFilter:
		return m.focusFilter
	case stateSearch:
		return m.searchQuery != ""
	case stateTagSearch:
		return m.tagTabSearchQuery != ""
	}
	return false
}

// popFocus exits the most recently entered state belonging to the current
// tab — the single esc behavior for every tab.
func (m *model) popFocus() {
	stack := m.focusStack
	for i := len(stack) - 1; i >= 0; i-- {
		e := stack[i]
		if e.tab != m.tab {
			continue
		}
		m.removeFocus(e.tab, e.state)
		if !m.focusActive(e) {
			continue
		}
		m.exitFocus(e.state)
		return
	}
}

// exitFocus is the single owner of every state's exit action.
func (m *model) exitFocus(s uiState) {
	switch s {
	case stateDetailPane:
		m.pane = paneList
		m.detailTaskID = ""
		m.detailStack = nil
		m.detail = detailState{field: fieldStartDate}
		m.invalidateDetailCache()
		// Meetings has no task to clear, but shares this same exit path — see
		// handleMeetingsEnter, which pushes stateDetailPane the same way the
		// Tasks/Projects/Tags detail panes do.
		m.openMeetingID = ""
		m.meetingSuggestions = nil
		m.meetingReviewCursor = -1
	case stateProjectDrill:
		m.projectTaskMode = false
		m.cursor = 0
	case stateTagDrill:
		m.tagTaskMode = false
		m.cursor = 0
	case stateCalTimeline:
		m.calendar.focusTimeline = false
	case stateHistory:
		m.showHistory = false
		m.cursor = 0
		m.listOffset = 0
	case stateFocusFilter:
		m.focusFilter = false
		m.cursor = 0
		m.listOffset = 0
		m.markFilterDirty()
	case stateSearch:
		m.searchQuery = ""
		m.searchInput.SetValue("")
		m.cursor = 0
		m.listOffset = 0
		m.markFilterDirty()
		// The filter is persisted, so clearing it has to reach disk too —
		// otherwise esc drops it on screen and the next start brings it back.
		m.persistSettings()
	case stateTagSearch:
		m.tagTabSearchQuery = ""
		m.tagTabCursor = 0
	}
}
