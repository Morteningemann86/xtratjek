package main

// The keymap registry is the single source of truth for keyboard controls.
// Both the footer hint line (renderKeyHints) and the full help overlay
// (renderHelpFullscreen) are generated from it, so the two can never drift
// from each other.
//
// It is also the surface where cross-page control consistency is enforced:
// every binding carries a canonical action id, and TestKeymapActionsAre
// Consistent asserts that one action always uses the same key everywhere
// (delete is always x, edit is always r, …). Add a new list tab that binds
// "delete" to some other key and the test fails by construction.

// keyCtx is a bitmask of the UI contexts a binding is live in. A context is a
// tab, or a distinct pane/mode within a tab (the Tasks detail pane and the
// Calendar timeline have their own keysets).
type keyCtx uint16

const (
	ctxTasksList keyCtx = 1 << iota
	ctxTasksDetail
	ctxProjects
	ctxTags
	ctxStats
	ctxCalendar
	ctxCalendarTimeline
	ctxSettings
	ctxBoard
	// The Tags and Projects tabs have a second level: the cursor drills into
	// the selected row's task list, where the row-level task keys take over
	// from the tag/project ones. That is a distinct keyset, so it is a
	// distinct context.
	ctxTagDrill
	ctxProjectDrill
	// The Meetings tab's own list/detail split, parallel to
	// ctxTasksList/ctxTasksDetail — the detail pane is where recording,
	// generating and reviewing action items happen.
	ctxMeetingsList
	ctxMeetingsDetail
	// ctxChat is the Chat tab, live for as long as modeChatInput is (see its
	// doc comment in model.go). Deliberately NOT part of ctxAll: every key
	// that would otherwise be global (u undo, q quit, the 1-9 digit tab
	// jump, …) is ordinary text in a chat message instead, so listing them
	// here would advertise shortcuts that do not actually fire — see
	// updateChatInput.
	ctxChat

	// ctxAll marks the global bindings (navigation, help, undo, quit) that are
	// live in every context.
	ctxAll = ctxTasksList | ctxTasksDetail | ctxProjects | ctxTags |
		ctxStats | ctxCalendar | ctxCalendarTimeline | ctxSettings |
		ctxBoard | ctxTagDrill | ctxProjectDrill | ctxMeetingsList | ctxMeetingsDetail

	// ctxDrill is the pair of drill-in lists, which share their whole keyset.
	ctxDrill = ctxTagDrill | ctxProjectDrill
)

// binding is one row of the registry.
type binding struct {
	ctx     keyCtx // where the key is live
	key     string // display form: "x", "←/→", "tab / 1-7"
	action  string // canonical id — consistency is enforced per action
	desc    string // human description (English; tr()'d at render)
	section string // help-overlay grouping
	inHint  bool   // show in the context's footer hint
	primary bool   // keep in the curated short hint when the full line won't fit
}

// Help sections are rendered in this order; a binding's section must be one of
// these. Navigation and App collect the global bindings.
var helpSectionOrder = []string{
	secNavigation, secTasks, secDetail, secTagsProjects,
	secBoard, secCalendar, secStats, secSettings, secMeetings, secChat, secApp,
}

const (
	secNavigation   = "Navigation"
	secTasks        = "Tasks"
	secDetail       = "Detail view"
	secTagsProjects = "Tags & Projects"
	secDrill        = "Inside a tag / project"
	secBoard        = "Board"
	secCalendar     = "Calendar"
	secStats        = "Stats"
	secSettings     = "Settings"
	secMeetings     = "Meetings"
	secChat         = "Chat"
	secApp          = "App"
)

// keymap is the registry. Grouped by section for readability; render order
// within a section follows this slice.
var keymap = []binding{
	// ── Navigation (global) ──────────────────────────────────────────────
	// Registered contexts are the contexts where the key actually does
	// something: the Stats tab scrolls rather than moves a cursor, so it
	// registers ↑/↓ under its own description; the jump/page keys only drive
	// the linear list tabs (listNavTarget). A binding the help shows must be a
	// binding dispatch honours.
	{ctxAll &^ ctxStats, "↑/↓ · j/k", "navigate", "navigate list", secNavigation, false, false},
	{ctxTasksList | ctxProjects | ctxTags | ctxDrill, "home/end · pgup/pgdn", "listpage", "jump to ends / page through list", secNavigation, false, false},
	// enter has no global meaning — each context defines its own (open details,
	// edit field, activate, cycle) — so it is registered per context, not here.
	{ctxAll, "esc", "back", "go back", secNavigation, false, false},
	{ctxAll, "tab / shift+tab / 1-9", "tabs", "switch tabs (forward / back / direct)", secNavigation, false, false},
	{ctxAll, "?", "help", "toggle this help", secNavigation, false, false},
	{ctxAll, "ctrl+k", "palette", "command palette: find any action by name", secNavigation, false, false},

	// ── Tasks list ───────────────────────────────────────────────────────
	{ctxTasksList, "enter", "detail", "open details", secTasks, true, false},
	{ctxTasksList, "a", "add", "add task (#tag due:date p:high @proj s:M)", secTasks, true, true},
	{ctxTasksList, "d", "done", "toggle done", secTasks, true, true},
	{ctxTasksList, "t", "track", "start/stop time tracking", secTasks, true, true},
	{ctxTasksList, "T", "timeentry", "add manual time entry", secTasks, false, false},
	{ctxTasksList, "p", "priority", "cycle priority low/med/high", secTasks, false, false},
	{ctxTasksList, "D", "setdue", "set / clear due date", secTasks, false, false},
	{ctxTasksList, "r", "edit", "rename task", secTasks, false, false},
	{ctxTasksList, "x", "delete", "delete", secTasks, true, true},
	{ctxTasksList, "n", "notes", "edit description (opens $EDITOR)", secTasks, false, false},
	{ctxTasksList, "f", "focus", "focus: today + overdue only", secTasks, false, false},
	{ctxTasksList, "w", "why", "why this rank: the score, its causes, what moves it", secTasks, false, false},
	{ctxTasksList, "s", "sort", "cycle sort order", secTasks, false, false},
	{ctxTasksList, "h", "history", "toggle history", secTasks, false, false},
	{ctxTasksList | ctxDrill, "←/→", "foldsub", "expand/collapse subtasks", secTasks, false, false},
	{ctxTasksList, "/", "search", "filter", secTasks, true, true},

	// ── Tasks detail pane ────────────────────────────────────────────────
	{ctxTasksDetail, "←/→", "detailsection", "jump section", secDetail, true, false},
	{ctxTasksDetail, "enter", "editfield", "edit field / open subtask", secDetail, true, false},
	{ctxTasksDetail, "a", "add", "add tag / dep / comment / subtask", secDetail, true, false},
	{ctxTasksDetail, "#", "quicktag", "quick add tag", secDetail, false, false},
	{ctxTasksDetail, "@", "quickproject", "quick add / change project", secDetail, false, false},
	{ctxTasksDetail, "d", "done", "toggle subtask done", secDetail, false, false},
	{ctxTasksDetail, "t", "track", "start/stop subtask timer", secDetail, false, false},
	{ctxTasksDetail, "T", "timeentry", "add manual time entry", secDetail, false, false},
	{ctxTasksDetail, "n", "notes", "edit description (opens $EDITOR)", secDetail, false, false},
	{ctxTasksDetail, "r", "edit", "rename subtask / edit time entry", secDetail, false, false},
	{ctxTasksDetail, "x", "delete", "remove field / delete subtask", secDetail, true, false},
	{ctxTasksDetail, "esc", "back", "back to list", secDetail, true, false},

	// ── Tags & Projects ──────────────────────────────────────────────────
	{ctxProjects | ctxTags, "enter", "detail", "open the tasks in it", secTagsProjects, true, true},
	{ctxProjects | ctxTags, "a", "add", "new task in it", secTagsProjects, true, false},
	{ctxProjects | ctxTags, "h", "history", "show / hide finished", secTagsProjects, true, true},
	{ctxProjects | ctxTags, "f", "tagfilter", "show its tasks on the Tasks tab", secTagsProjects, false, false},
	{ctxProjects | ctxTags, "s", "sort", "cycle sort order", secTagsProjects, false, false},
	{ctxProjects | ctxTags, "r", "edit", "rename globally", secTagsProjects, false, false},
	{ctxTags, "m", "merge", "merge tags (Tags tab)", secTagsProjects, false, false},
	{ctxProjects | ctxTags, "x", "delete", "delete globally", secTagsProjects, false, false},
	{ctxProjects | ctxTags, "/", "search", "filter", secTagsProjects, true, true},

	// ── Inside a tag / project (the drilled-in task list) ────────────────
	{ctxDrill, "enter", "detail", "open details", secDrill, true, true},
	{ctxDrill, "d", "done", "toggle done", secDrill, true, true},
	{ctxDrill, "t", "track", "start/stop time tracking", secDrill, true, true},
	{ctxDrill, "T", "timeentry", "add manual time entry", secDrill, false, false},
	{ctxDrill, "p", "priority", "cycle priority low/med/high", secDrill, false, false},
	{ctxDrill, "D", "setdue", "set / clear due date", secDrill, false, false},
	{ctxDrill, "a", "add", "new task in it", secDrill, true, false},
	{ctxDrill, "w", "why", "why this rank", secDrill, false, false},
	{ctxDrill, "h", "history", "show / hide done tasks", secDrill, false, false},
	{ctxDrill, "r", "edit", "rename task", secDrill, false, false},
	{ctxDrill, "x", "delete", "delete task", secDrill, true, true},
	{ctxDrill, "esc", "back", "back to the list", secDrill, true, false},

	// ── Calendar ─────────────────────────────────────────────────────────
	{ctxCalendar, "←/→ ↑/↓", "calnav", "move by day / week", secCalendar, true, false},
	{ctxCalendar, "[ / ]", "calmonth", "previous / next month", secCalendar, true, false},
	{ctxCalendar, "t", "today", "jump to today", secCalendar, true, false},
	{ctxCalendar, "enter", "calfocus", "focus the day's entries", secCalendar, true, false},
	{ctxCalendarTimeline, "↑/↓ · j/k", "navigate", "select entry", secCalendar, true, false},
	{ctxCalendarTimeline, "r", "edit", "edit entry times (09:12-10:00 or 45m)", secCalendar, true, false},
	{ctxCalendarTimeline, "x", "delete", "delete selected entry", secCalendar, true, false},
	{ctxCalendarTimeline, "esc", "back", "back", secCalendar, true, false},

	// ── Stats ────────────────────────────────────────────────────────────
	// ── Board ────────────────────────────────────────────────────────────
	{ctxBoard, "←/→", "boardcolumn", "focus previous/next column", secBoard, true, true},
	{ctxBoard, "enter", "boardcarry", "pick up card to move it", secBoard, true, true},
	{ctxBoard, "space", "boardcard", "show card details", secBoard, false, false},
	{ctxBoard, "a", "add", "new card in this column", secBoard, true, false},
	{ctxBoard, "H/L", "boardmove", "move card between stages (into Done completes it)", secBoard, false, false},
	{ctxBoard, "d", "done", "toggle done", secBoard, false, false},
	{ctxBoard, "w", "why", "why this rank", secBoard, false, false},
	{ctxBoard, "/", "search", "filter cards (#tag, @project, text)", secBoard, true, true},

	{ctxStats, "↑/↓ · j/k", "navigate", "scroll the summary", secStats, true, false},
	{ctxStats, "enter", "statscycle", "cycle activity range", secStats, true, false},

	// ── Settings ─────────────────────────────────────────────────────────
	{ctxSettings, "↑/↓ · j/k", "navigate", "select setting", secSettings, true, false},
	{ctxSettings, "←/→", "setchange", "change value / theme", secSettings, true, false},
	{ctxSettings, "enter", "setapply", "activate / edit the selected setting", secSettings, true, false},
	{ctxSettings, "y / n", "confirmupdate", "confirm update when one is offered", secSettings, false, false},

	// ── Meetings ─────────────────────────────────────────────────────────
	{ctxMeetingsList, "enter", "detail", "open meeting", secMeetings, true, true},
	{ctxMeetingsList, "a", "add", "add meeting", secMeetings, true, true},
	{ctxMeetingsList, "r", "record", "start/stop recording", secMeetings, true, true},
	{ctxMeetingsList, "n", "notes", "edit my notes (ctrl+s or esc saves)", secMeetings, false, false},
	{ctxMeetingsList, "T", "edittranscript", "edit the transcript (ctrl+s or esc saves)", secMeetings, false, false},
	{ctxMeetingsList, "g", "generate", "summarize + find action items", secMeetings, true, false},
	{ctxMeetingsList, "x", "delete", "delete meeting", secMeetings, false, false},
	{ctxMeetingsDetail, "r", "record", "start/stop recording", secMeetings, true, true},
	{ctxMeetingsDetail, "n", "notes", "edit my notes (ctrl+s or esc saves)", secMeetings, false, false},
	{ctxMeetingsDetail, "T", "edittranscript", "edit the transcript (ctrl+s or esc saves)", secMeetings, false, false},
	{ctxMeetingsDetail, "g", "generate", "summarize + find action items", secMeetings, true, false},
	{ctxMeetingsDetail, "↑/↓ · j/k", "navigate", "move between action items", secMeetings, false, false},
	{ctxMeetingsDetail, "y", "accept", "accept the focused action item → creates a task", secMeetings, true, true},
	{ctxMeetingsDetail, "x", "delete", "reject the focused action item", secMeetings, true, true},
	{ctxMeetingsDetail, "e", "editsuggestion", "edit the focused action item (quick-add syntax)", secMeetings, false, false},

	// ── Chat ─────────────────────────────────────────────────────────────
	{ctxChat, "enter", "chatsend", "send message", secChat, true, true},
	{ctxChat, "y / n", "chatconfirm", "confirm / decline a proposed action", secChat, true, true},
	{ctxChat, "esc", "chatreset", "reset the conversation (asks y/n)", secChat, true, true},
	{ctxChat, "pgup/pgdn", "chatscroll", "scroll the conversation", secChat, false, false},
	{ctxChat, "tab / shift+tab", "chattabs", "switch tabs", secChat, true, false},

	// ── App (global) ─────────────────────────────────────────────────────
	{ctxAll, "u", "undo", "undo last change", secApp, false, false},
	{ctxAll, "q", "quit", "quit", secApp, false, false},
}

// currentKeyCtx maps the live tab/pane/mode to a keyCtx. Only the Tasks tab
// has a distinct detail-pane keyset; the Calendar timeline has its own.
func (m model) currentKeyCtx() keyCtx {
	switch m.tab {
	case tabTasks:
		if m.pane == paneDetail {
			return ctxTasksDetail
		}
		return ctxTasksList
	case tabProjects:
		if m.projectTaskMode {
			return ctxProjectDrill
		}
		return ctxProjects
	case tabTags:
		if m.tagTaskMode {
			return ctxTagDrill
		}
		return ctxTags
	case tabStats:
		return ctxStats
	case tabCalendar:
		if m.calendar.focusTimeline {
			return ctxCalendarTimeline
		}
		return ctxCalendar
	case tabSettings:
		return ctxSettings
	case tabBoard:
		return ctxBoard
	case tabMeetings:
		if m.pane == paneDetail {
			return ctxMeetingsDetail
		}
		return ctxMeetingsList
	case tabChat:
		return ctxChat
	}
	return ctxTasksList
}

// shortLabel is the terse form used in the curated short hint (the width
// fallback), keyed by action. The full descriptions are written for the help
// overlay and are too long to survive a narrow footer, so primary keys carry a
// one-word label here instead.
var shortLabel = map[string]string{
	"add":    "add",
	"done":   "done",
	"track":  "track",
	"delete": "del",
	"search": "filter",
}

// hintString renders the footer hint for a context from the registry. With
// primaryOnly it emits just the curated short set (used when the full line
// won't fit the terminal width), with terse labels.
//
// overrides replaces an action's label with what that key would do to the row
// the cursor is on right now — "stop" rather than "track" on a task whose timer
// is running. The keys themselves are unconditional, so the hint stayed
// unconditional with them and offered to start a timer that was already
// running. Nil for a context with nothing to say about its selection.
func hintString(ctx keyCtx, primaryOnly bool, overrides map[string]string) string {
	var b []byte
	first := true
	for i := range keymap {
		bd := &keymap[i]
		if bd.ctx&ctx == 0 || !bd.inHint {
			continue
		}
		if primaryOnly && !bd.primary {
			continue
		}
		label := bd.desc
		if primaryOnly {
			if s, ok := shortLabel[bd.action]; ok {
				label = s
			}
		}
		if s, ok := overrides[bd.action]; ok {
			if s == "" {
				continue // the key does nothing in the current state
			}
			label = s
		}
		if !first {
			b = append(b, " · "...)
		}
		first = false
		b = append(b, effectiveKey(bd.action, bd.key)...)
		b = append(b, ' ')
		b = append(b, tr(label)...)
	}
	return string(b)
}
