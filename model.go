package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Iliorn/tjek/aiprovider"
	"github.com/Iliorn/tjek/meeting"
	"github.com/Iliorn/tjek/paths"
	"github.com/Iliorn/tjek/rank"
	"github.com/Iliorn/tjek/tasksync"
	"github.com/Iliorn/tjek/todo"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Types & constants ─────────────────────────────────────────────────────────

type tab int

const (
	tabTasks tab = iota
	tabCalendar
	tabTags
	tabProjects
	tabBoard
	tabStats
	tabSettings
	tabMeetings
)

const numTabs = 8

// Stable row IDs for the Settings tab. The renderer draws them in groups; their
// numeric order remains independent of their visual navigation order.
const (
	settingBiasDeadline = iota
	settingBiasPriority
	settingBiasMomentum
	settingAging
	settingAutoCloseParent
	settingAutoCloseSubtasks
	settingShowBoard
	settingTheme
	settingLanguage
	settingDetailPos
	settingStages
	settingSyncAuto
	settingSyncBoard
	settingSyncServer
	settingSyncToken
	settingSyncNow
	settingServerOn
	settingServerListen
	settingServerToken
	settingVersion
	settingCheckUpdate
	settingReminder
	settingSubtaskTags
	settingReminderTime
	settingExportFolder
	settingImportFile
	settingAIProvider
	settingAnthropicKey
	settingOpenAIKey
	settingGeminiKey
	settingFFmpegInput
	numSettingsRows
)

type pane int

const (
	paneList pane = iota
	paneDetail
)

// tabView is the slice of UI state that several tabs share the same fields for
// (the cursor, scroll offset, open pane and its task, and task/project search
// query).
// switchTab snapshots it on the way out of a tab and restores it on the way
// back, so glancing at another tab doesn't wipe your position or filter. Tab-
// private state (projectCursor, tagTabCursor, showHistory, projectTaskMode, the
// per-tab search queries, …) lives in its own fields and simply persists.
type tabView struct {
	cursor       int
	listOffset   int
	pane         pane
	search       string
	detailTaskID string
	detailStack  []string
}

type detailField int

const (
	fieldStartDate detailField = iota
	fieldDueDate
	fieldCompleted
	fieldRecurrence
	fieldPriority
	fieldSize
	fieldStage
	fieldProject
	fieldNotes
	fieldTags
	fieldDependencies
	fieldSubtasks
	fieldTimeEntries
	fieldComments
)

type appMode int

const (
	modeNormal appMode = iota
	modeHelp
	modeInput
	modeSearch
	modeSearchDep
	modeSearchTag
	modeSearchProject
	modeSearchTagTab
	// modeConfirm is the generic yes/no prompt: confirmMsg holds the question
	// and confirmOnYes the action to run on y/enter. It replaced a dozen
	// near-identical per-action confirm modes.
	modeConfirm
	modeConfirmUpdate
	modeEditTimeEntry
	modeIdlePrompt
	modeEditComment
	modeEditTag
	modeEditProjectInline
	modeEditTitle
	modeEditDue
	modeAddSubtask
	modeEditSubtask
	modeAddTimeEntry
	modeEditSyncURL
	modeEditSyncToken
	modeEditServerListen
	modeEditServerToken
	modeEditStages
	modeEditExportFolder
	modeImportFile
	modePalette
	// modeExplain is the "why this rank" overlay: a read-only screen over the
	// current task, like modeHelp, that any key dismisses.
	modeExplain
	// modeBoardCarry is a Board card picked up with enter: ←/→ carry it
	// across the columns and enter/esc put it down (update_board.go).
	modeBoardCarry
	// modeBoardCard is the read-only card view: the selected card's fields in
	// place of the columns, until esc (update_board.go).
	modeBoardCard
	// The Settings tab's four AI-config inline text editors (aisettings.go),
	// one mode per field, matching modeEditSyncToken/modeEditServerToken.
	modeEditAnthropicKey
	modeEditOpenAIKey
	modeEditGeminiKey
	modeEditFFmpegInput
	// modeAddMeeting is the title prompt for a new meeting (update_meetings.go).
	modeAddMeeting
	// modeEditSuggestion edits one action-item suggestion using quick-add
	// syntax (parseQuickAdd), pre-filled from its current fields.
	modeEditSuggestion
	// modeEditMeetingText is the fullscreen textarea for a meeting's Notes or
	// Transcript (update_meetings.go) — which field is set by
	// meetingEditField. Both ctrl+s and esc save; esc never discards, so it
	// can't be used to lose a note by reflex. No $EDITOR round trip.
	modeEditMeetingText
)

// untaggedKey is a sentinel used both as the Tags-tab virtual row for tasks
// with no tags and as the Tasks-tab search token that filters to them. The
// NUL prefix guarantees it can never collide with a real (normalized) tag.
const untaggedKey = "\x00untagged"

// statsRangeMode selects the window shown by the stats activity histogram,
// cycled with Enter on the Stats tab.
type statsRangeMode int

const (
	statsRange7Days statsRangeMode = iota
	statsRange30Days
	statsRange6Months
	statsRangeCount
)

type taskSortMode int

// Three sort modes survive the sequencing engine: Sequence (the score-based
// default), DueDate (strict deadline view), and Size (Small → Medium → Large
// for "show me the quick wins"). Each mode lines up with a visible column so
// the >..< header marker is always meaningful.
const (
	taskSortSequence taskSortMode = iota
	taskSortDueDate
	taskSortSize
)

type historySortMode int

// The history (completed-tasks) list has its own sort, independent of the
// active-task taskSort: the active modes (Sequence score, Size) are
// meaningless once a task is done. History sorts by completion time (most
// recent first) or task title — each lining up with a visible history column
// so the >..< header marker stays meaningful here too.
const (
	historySortCompleted historySortMode = iota // most-recent completion first
	historySortAlpha                            // title A→Z
)

// toastKind selects the style of a transient toast (m.err). The zero value is
// toastError so any plain assignment renders as an error; success/info are set
// via flashSuccess/flashInfo. See renderStatusLine.
type toastKind int

const (
	toastError toastKind = iota
	toastSuccess
	toastInfo
)

// flashError/flashSuccess/flashInfo set the toast text and its kind together so
// the two can't drift — an error can never inherit a prior success's colour.
// clearErrAfter is still returned by the call site to expire the toast.
func (m *model) flashError(s string)   { m.err, m.errKind = s, toastError }
func (m *model) flashSuccess(s string) { m.err, m.errKind = s, toastSuccess }
func (m *model) flashInfo(s string)    { m.err, m.errKind = s, toastInfo }

// ── Messages ──────────────────────────────────────────────────────────────────

type clearErrMsg struct{}
type saveDoneMsg struct{}
type saveErrMsg struct{ err error }
type editorFinishedMsg struct {
	taskID   string
	err      error
	fallback bool // true when this run already used the notepad fallback
}
type saveTickMsg struct{}
type updateDoneMsg struct{ err error }
type updateCheckMsg struct {
	latest string
	err    error
}
type timerTickMsg struct{}

// ── Sub-state structs ─────────────────────────────────────────────────────────

type searchState struct {
	query  string
	cursor int
}

type detailState struct {
	field           detailField
	commentCursor   int
	depCursor       int
	tagCursor       int
	subtaskCursor   int
	timeEntryCursor int
	// scroll is the first line of the detail document the pane shows. It
	// persists between keystrokes on purpose: deriving the top from the cursor
	// instead glues the cursor to a fixed row and slides the whole document
	// under it on every press, so a one-field move reads as a jump the size of
	// the gap between those fields. Kept valid by clampDetailScroll, and
	// re-derived against the real rendered lines in applyDetailScrollN.
	scroll int
}

type calendarState struct {
	selected      time.Time // selected day, normalized to midnight
	entryCursor   int
	focusTimeline bool
}

// boardState is the Board tab's cursor: which column is focused and which
// card within it. col indexes boardConfig.stages, whose last entry (doneColumn) is
// the Done column. Both are clamped at render/move time, so stale values after
// a stage-list edit or task completion degrade to the nearest valid card.
type boardState struct {
	col    int
	cursor int
	// colOffset is the first column on screen. A board with more stages than
	// fit is not a reason to shrink every column past readability, so the view
	// is a window over the columns that follows the focus — see boardWindow.
	colOffset int
	// The card picked up in modeBoardCarry, and the column it is held over.
	// Nothing is changed until it is put down.
	carryID  string
	carryCol int
	// cardScroll is the first card drawn in column scrollCol, the focused
	// one; clampBoardWindow keeps the cursor inside it (boardCardWindow).
	cardScroll int
	scrollCol  int
	// addCol is the column a quick-add started from the Board files into.
	addCol int
}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	Store  // embedded source of truth (tasks map, indexes, undo) — promotes m.tasks, m.add, m.pushUndo, etc.
	repo   Repository
	rank   rank.Ranker // bias knobs, activity heat and the 100% mark every score reads
	cursor int
	tab    tab
	pane   pane
	mode   appMode

	// boardCfg is the board's columns and switches; see boardConfig.
	boardCfg boardConfig

	// detail render cache
	detailRC detailRenderCache

	// Detail pane state
	detail detailState

	// Calendar tab state
	calendar    calendarState
	timerTickOn bool

	// Board tab state
	board boardState

	// Search state per context
	depSearch  searchState
	tagSearch  searchState
	projSearch searchState

	// Inputs
	textInput         textinput.Model
	searchInput       textinput.Model
	depSearchInput    textinput.Model
	tagSearchInput    textinput.Model
	projSearchInput   textinput.Model
	tagTabSearchInput textinput.Model
	paletteInput      textinput.Model

	// paletteCursor is the highlighted row of the command palette (palette.go).
	paletteCursor int

	// suggestCursor is the highlighted chip of the quick-add completion row
	// (suggest.go). Reset to 0 whenever the field's text or caret moves, so
	// typing always re-aims at the best match.
	suggestCursor int

	// UI state
	confirmMsg string
	// confirmOnYes is the action modeConfirm runs on y/enter; nil is a no-op.
	// Reading pending* fields at call time keeps each action a plain method.
	confirmOnYes         func(*model) tea.Cmd
	pendingDeleteID      string
	pendingComment       int
	pendingDep           int
	pendingTag           int
	pendingSubtask       int
	pendingCloseParentID string
	pendingReopenID      string
	pendingEntryTaskID   string
	pendingEntryID       string
	termWidth            int
	termHeight           int
	err                  string
	errKind              toastKind // styles the toast: error (default) / success / info
	projectCursor        int
	tagTabCursor         int
	settingsCursor       int
	// Set when the Server toggle opened the token editor because there was
	// no token yet: saving one then completes the action the user asked for
	// (start the server) instead of leaving them in a settings row.
	serverStartAfterToken bool
	searchQuery           string
	tagTabSearchQuery     string
	listOffset            int
	helpScroll            int
	// helpFilter narrows the help overlay's rows; helpFiltering is true while
	// the user is typing it, so the overlay's own keys (q, space, ?) don't get
	// swallowed by the filter and vice versa.
	helpFilter      string
	helpFiltering   bool
	tabViews        [numTabs]tabView
	projectTaskMode bool
	// tagTaskMode is the Tags-tab mirror of projectTaskMode: the cursor has
	// left the tag list and is walking the selected tag's tasks, where the
	// row-level keys (d/t/p/r/x/enter) act on the task under it.
	tagTaskMode   bool
	showHistory   bool
	focusFilter   bool
	focusStack    []focusEntry
	expandedTasks map[string]bool
	detailTaskID  string
	detailStack   []string
	// explainTaskID pins the task the "why this rank" overlay is describing.
	// The overlay is opened over a list that re-sorts under it, so it holds an
	// ID rather than trusting the cursor to still be on the same row.
	explainTaskID      string
	editingTagName     string
	editingProjectName string
	// pendingProjectName is the project the x confirm on the Projects tab is
	// about to clear off its tasks.
	pendingProjectName string
	tagOrder           groupSort
	projectOrder       groupSort
	// showFinishedGroups brings back what the Tags and Projects tabs hide by
	// default: groups with nothing open, and the done tasks inside a group.
	showFinishedGroups bool
	// tagPinned and projectPinned name the group the cursor drilled into.
	// While drilled in, the list keeps that group visible and the cursor on
	// it, however a change inside it re-sorts the list (see groups.go).
	tagPinned     string
	projectPinned string
	taskSort      taskSortMode
	historySort   historySortMode
	statsRange    statsRangeMode
	statsScroll   int // first Stats summary line shown; see scrollWindowLines
	themeName     string
	// detailPos is which side of the list the detail pane takes (right, left,
	// or stacked at the bottom). Read by sideBySide, so it decides the layout
	// for every height helper at once rather than per renderer.
	detailPos         detailPos
	updateStatus      string
	autoCloseParent   bool
	autoCloseSubtasks bool
	// subtaskTags: a new subtask copies its parent's tags.
	subtaskTags bool
	// reminderAt is the daily reminder's time in minutes after midnight, kept
	// while reminderOn is off; remindedOn is the day this device last reminded.
	reminderAt int
	reminderOn bool
	remindedOn string
	// exportFolder is where tjek-export.json is kept current ("" = off);
	// exportDirty/exportScheduled/lastExport pace the writes (exportSoon).
	exportFolder    string
	exportDirty     bool
	exportScheduled bool
	lastExport      time.Time

	// Persistence
	dirty         bool
	savePending   bool
	saveScheduled bool
	editorTaskID  string
	editorCmd     string
	// editorToInput routes the next editor round-trip back into the active text
	// input (the ctrl+e escape hatch from a comment draft) instead of
	// committing to a task's notes.
	editorToInput bool

	// Frame
	frameTime time.Time

	// Gantt reusable buffers
	ganttBarBuf   []rune
	ganttColorBuf []int

	// Caches
	cache *cacheState

	// Filesystem watcher state. nil if the watcher couldn't start (in which
	// case the TUI behaves exactly as before — no live reload, no errors).
	watcher *watcherState
	// watcherStop releases the watcher's file descriptor. Call it through
	// closeWatcher, which is idempotent.
	watcherStop func()

	// Cross-device sync config, loaded once at startup. autoSync gates the
	// periodic background sync (and the launch/exit syncs); it is on whenever a
	// sync URL+token are configured and not explicitly disabled. syncStatus is
	// the last sync outcome shown in the Settings footer.
	syncCfg    syncConfig
	autoSync   bool
	syncStatus string
	// lastSyncFailed drives the header sync-health glyph: true after a failed
	// background sync, cleared on the next success. syncStatus keeps the full
	// message for the Settings footer.
	lastSyncFailed bool
	// inprocServer is the in-process sync server when "Server" is toggled on
	// (nil otherwise). serverExternal is set by probeServer when a headless
	// `tjek serve` is answering at the configured address.
	inprocServer   *http.Server
	inprocStop     func() // stops the in-process server's change watcher
	serverExternal bool
	liveSync       *tasksync.Listener // SSE listener for real-time inbound push
	// lastTimerHeartbeat throttles how often the running timer's last_seen is
	// written to the DB (see the timer tick) so a live timer stays "fresh"
	// against the stale-timer recoverer without writing every second.
	lastTimerHeartbeat time.Time

	// AI provider configuration for the Meetings tab (aisettings.go,
	// aiprovider/). aiProvider is one of aiprovider.ProviderNames; aiKeys
	// holds all three keys regardless of which is active, since transcription
	// always needs OpenAI's even when it isn't the text provider. ffmpegInput
	// overrides audiorecorder.go's per-platform microphone default.
	aiProvider  string
	aiKeys      aiprovider.Keys
	ffmpegInput string

	// Meetings tab state (meetingops.go, update_meetings.go, view_meetings.go).
	// The list itself is indexed by the shared m.cursor/m.listOffset like
	// every other tab; m.pane (paneList/paneDetail) decides whether the list
	// or openMeetingID's detail is showing.
	meetings      []*meeting.Meeting
	openMeetingID string
	// meetingSuggestions holds the open meeting's suggestions once it has
	// reached StatusReady — empty otherwise. meetingReviewCursor is the
	// focused row within them, active (y/n/e) only while pane is paneDetail
	// and the list is non-empty.
	meetingSuggestions  []meeting.Suggestion
	meetingReviewCursor int
	// recorder is non-nil while a recording is in progress; recordingMeetingID
	// names which meeting owns it (StartRecording/Stop run outside the model,
	// so the pointer is the only handle back to the live ffmpeg process).
	recorder           *AudioRecorder
	recordingMeetingID string
	recordStart        time.Time
	// pendingFFmpegInstallMeetingID names the meeting "r" was trying to
	// record when it found ffmpeg missing, while the y/n install prompt
	// (promptInstallFFmpeg) or the install itself is in flight — so
	// handleFFmpegInstallFinished knows which recording to resume once
	// ffmpeg is there.
	pendingFFmpegInstallMeetingID string
	// meetingTextarea is the in-app editor for a meeting's Notes or
	// Transcript (modeEditMeetingText) — ctrl+s saves, esc discards.
	// meetingEditID/meetingEditField name which meeting and which field
	// ("notes" or "transcript") it's currently holding.
	meetingTextarea  textarea.Model
	meetingEditID    string
	meetingEditField string
}

func initialModel(repo Repository) model {
	ti := textinput.New()
	ti.CharLimit = 500

	si := textinput.New()
	si.CharLimit = 100

	di := textinput.New()
	di.CharLimit = 100

	tagi := textinput.New()
	tagi.CharLimit = 50

	proji := textinput.New()
	proji.CharLimit = 100

	pal := textinput.New()
	pal.Placeholder = tr("Type a command…")
	pal.Prompt = "❯ "
	tagTabSearch := textinput.New()
	tagTabSearch.CharLimit = 50

	meetingTA := textarea.New()
	meetingTA.ShowLineNumbers = false

	todos, err := repo.Load()
	errMsg := ""
	if err != nil {
		errMsg = fmt.Sprintf("Error loading tasks: %v", err)
	}

	settings, settingsErr := loadSettings()
	if settingsErr != nil {
		// Settings load failed in a way that *isn't* "file doesn't exist".
		// Don't silently reset the user's preferences — surface the cause
		// so they know what to fix. If a task-load error is also pending,
		// keep that one (it's the more user-blocking failure).
		if errMsg == "" {
			errMsg = fmt.Sprintf("Settings load failed (using defaults): %v", settingsErr)
		}
	}
	th := themeByName(settings.Theme)
	applyTheme(th)
	applyLang(settings.Language)
	// A rejected rebind must be visible: silently falling back to the default
	// looks like the setting was ignored at random.
	keys, keyProblems := sanitizeKeyOverrides(settings.Keys)
	applyKeys(keys)
	if len(keyProblems) > 0 && errMsg == "" {
		errMsg = fmt.Sprintf("Keybinding ignored: %s", keyProblems[0])
	}

	store := Store{}
	store.ensureTasks()
	for i := range todos {
		store.add(todos[i])
	}
	// Restore persisted delete-undo entries so a user can `u` a task they
	// removed in a prior session. A corrupt file surfaces in errMsg; the
	// model still builds normally with an empty stack.
	if persisted, err := loadPersistedUndoEntries(); err != nil {
		if errMsg == "" {
			errMsg = fmt.Sprintf("Undo history corrupt (ignored): %v", err)
		}
	} else {
		store.undoStack = append(store.undoStack, persisted...)
	}
	m := model{
		Store:             store,
		repo:              repo,
		rank:              rank.Ranker{Biases: biasesFromSettings(settings)},
		boardCfg:          boardConfigFromSettings(settings),
		textInput:         ti,
		searchInput:       si,
		depSearchInput:    di,
		tagSearchInput:    tagi,
		projSearchInput:   proji,
		tagTabSearchInput: tagTabSearch,
		paletteInput:      pal,
		meetingTextarea:   meetingTA,
		mode:              modeNormal,
		pane:              paneList,
		tab:               tabTasks,
		termWidth:         80,
		termHeight:        24,
		err:               errMsg,
		tagOrder:          settings.TagOrder,
		projectOrder:      settings.ProjectOrder,
		taskSort:          settings.TaskSort,
		historySort:       settings.HistorySort,
		autoCloseParent:   settings.AutoCloseParent,
		autoCloseSubtasks: settings.AutoCloseSubtasks,
		subtaskTags:       !settings.SubtaskTagsDisabled,
		themeName:         th.name,
		detailPos:         detailPosFromSettings(settings.DetailPosition),
		aiProvider:        settings.AIProvider,
		aiKeys: aiprovider.Keys{
			Anthropic: settings.AnthropicKey,
			OpenAI:    settings.OpenAIKey,
			Gemini:    settings.GeminiKey,
		},
		ffmpegInput:         settings.FFmpegInput,
		meetingReviewCursor: -1,
		remindedOn:          loadRemindedOn(),
		// The top of the one settings pane. The zero value is a row ID, not a
		// position, and it happens to be the first bias knob — which opened
		// the tab with the cursor parked in the middle of the list.
		settingsCursor: settingTheme,
		expandedTasks:  make(map[string]bool),
		editorCmd:      resolveEditorCmd(),
		frameTime:      time.Now(),
		ganttBarBuf:    make([]rune, 256),
		ganttColorBuf:  make([]int, 256),
		cache: &cacheState{
			dirty:         true,
			overdueSet:    make(map[string]bool),
			blockedSet:    make(map[string]bool),
			blockerSet:    make(map[string]bool),
			taskTagRender: make(map[string]string, 64),
			tagLastUsed:   make(map[string]time.Time),
			projLastUsed:  make(map[string]time.Time),
		},
	}
	// Restore the `/` filter the last session left committed. The tab is
	// Tasks, which is the tab the query was saved from, and the focus entry
	// has to be pushed by hand: esc clears the filter by popping the state
	// that entering it recorded, and nothing entered it this run.
	if settings.Search != "" {
		m.searchQuery = settings.Search
		m.searchInput.SetValue(settings.Search)
		m.pushFocus(stateSearch)
	}
	m.applyLangPlaceholders()
	m.refreshCaches()
	// Absorb Age drift since the last open: every task's score creeps daily,
	// so a startup resync keeps the persisted column truthful even when the
	// user hasn't touched any task since yesterday.
	if err := m.repo.ResyncScores(); err != nil {
		m.flashError(fmt.Sprintf("Score resync failed: %v", err))
	}
	// Load cross-device sync config once; auto-sync drives launch/periodic/exit
	// syncs when a server is configured.
	m.syncCfg = loadSyncConfig()
	m.autoSync = autoSyncEnabled(m.syncCfg)
	// Real-time inbound push: hold an SSE stream to the server so changes from
	// other devices arrive in near-instant, with the periodic tick as fallback.
	if m.autoSync {
		m.liveSync = startLiveSync(m.syncCfg)
	}
	// If this machine is set to serve, start the in-process endpoint now. A bind
	// failure (e.g. an external tjek serve already on that address) is non-fatal
	// — the TUI keeps working and the Settings row will show it's served
	// externally instead.
	if m.syncCfg.ServerOn && m.syncCfg.ServerToken != "" {
		if srv, stop, err := startSyncServer(m.syncCfg.listenAddr(), m.syncCfg.ServerToken); err == nil {
			m.inprocServer = srv
			m.inprocStop = stop
		}
	}
	// Meetings load eagerly like everything else at startup (a small local
	// read, same cost class as repo.Load()); a failure is non-fatal and just
	// starts the tab empty rather than blocking launch over it. Loaded as
	// *meeting.Meeting (not values) so a pointer handed out by currentMeeting/
	// meetingByID stays valid even if m.meetings is later reallocated by append
	// — the same reasoning Store.tasks being map[string]*todo.Todo follows.
	if loaded, err := loadMeetings(); err == nil {
		m.meetings = make([]*meeting.Meeting, len(loaded))
		for i := range loaded {
			m.meetings[i] = &loaded[i]
		}
	}
	m.calendar.selected = startOfDay(time.Now())
	m.reminderAt, m.reminderOn = storedReminder(settings)
	// A launch refreshes the export: the store may have changed since the last
	// session wrote it (a sync, a CLI edit). Init schedules the write.
	m.exportFolder = settings.ExportFolder
	m.exportDirty = m.exportFolder != ""
	m.settleReminderAtLaunch(time.Now())
	if t := m.runningTask(); t != nil {
		m.timerTickOn = true
		if e := t.RunningEntry(); e != nil && time.Since(e.StartedAt) > idleThreshold {
			m.openIdlePrompt(t)
		}
	}
	return m
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.timerTickOn {
		cmds = append(cmds, timerTick())
	}
	if m.watcher != nil {
		cmds = append(cmds, waitForDBChange(m.watcher.ch))
	}
	if m.liveSync != nil {
		cmds = append(cmds, waitForSyncEvent(m.liveSync.C))
	}
	// Keep a periodic sync tick running for the whole session so enabling sync
	// from Settings mid-session takes effect; only sync immediately on launch
	// when it's already configured.
	cmds = append(cmds, syncTick(), reminderTick())
	if m.exportDirty {
		cmds = append(cmds, tea.Tick(exportDebounce, func(time.Time) tea.Msg { return exportTickMsg{} }))
	}
	if m.autoSync {
		cmds = append(cmds, m.backgroundSync())
	}
	if p := m.probeServer(); p != nil {
		cmds = append(cmds, p)
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

// ── Error timer ───────────────────────────────────────────────────────────────

func clearErrAfter() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearErrMsg{}
	})
}

// ── Timer tick ────────────────────────────────────────────────────────────────

// A timer running longer than this is assumed forgotten and triggers the
// idle prompt (on startup and when stopping it).
const idleThreshold = 4 * time.Hour

func timerTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return timerTickMsg{}
	})
}

// ── Debounced save ────────────────────────────────────────────────────────────

const saveDebounceDuration = 300 * time.Millisecond

func scheduleSave() tea.Cmd {
	return tea.Tick(saveDebounceDuration, func(t time.Time) tea.Msg {
		return saveTickMsg{}
	})
}

// watcherDisabled reports whether TJEK_NO_WATCH asks for live reload to stay
// off. Shared with the doctor so what it prints is what startModelWatcher does.
func watcherDisabled() bool {
	v := strings.TrimSpace(os.Getenv("TJEK_NO_WATCH"))
	return v != "" && v != "0" && v != "false"
}

// startModelWatcher spins up the filesystem watcher so CLI writes (and any
// other process touching tasks.db) refresh the TUI without a restart.
// If it fails to start (weird filesystem, permissions, OS limits), the TUI keeps
// working — live reload just isn't available.
//
// Called from main rather than initialModel because a watcher is a live OS
// resource with a file descriptor, and only a running program wants one. Doing
// it in the constructor meant the test binary opened one per model — 129 by the
// end of the suite — which is exactly how many the macOS runner ran out of
// (a process there may hold 256 open files).
func startModelWatcher(m *model) {
	// TJEK_NO_WATCH turns live reload off. The watcher is the one thing tjek
	// does continuously against the operating system — an inotify/kqueue watch
	// on the data directory, woken by every WAL write the app itself makes — so when
	// input feels laggy it is the first variable worth removing. Without it,
	// the TUI simply won't notice a `tjek add` from another shell until it
	// next reloads.
	if watcherDisabled() {
		return
	}
	dir, err := paths.Dir(paths.Data)
	if err != nil {
		return
	}
	state := newWatcherState()
	// Startup already wrote to the database — initialModel resyncs the score
	// column — and WAL flushes that write to disk after we start watching. Seed
	// the self-write window so our own startup write doesn't come straight back
	// as an external change and make the first keystroke pay for a reload.
	state.recordSelfSave()
	if stop, werr := startWatcher(state, dir); werr == nil {
		m.watcher = state
		m.watcherStop = stop
	}
}

// closeWatcher releases the filesystem watcher. Idempotent, so the quit path
// and a test cleanup can both call it.
func (m *model) closeWatcher() {
	if m.watcherStop != nil {
		m.watcherStop()
		m.watcherStop = nil
	}
	m.watcher = nil
}

// flushPendingWrites synchronously persists any dirty tasks / tombstones. The
// debounced save returns a tea.Tick command — batched with tea.Quit it races
// the program shutdown and loses the most recent mutation (e.g. add a task,
// hit q within 300ms, the task is gone on next launch). Calling this from the
// quit path closes that window. Best-effort: a save error here can't be shown
// in the TUI anymore, so we surface it on stderr.
func (m *model) flushPendingWrites() {
	m.flushExport()
	dirty, tombstones := m.Store.drainDirty()
	if len(dirty) == 0 && len(tombstones) == 0 {
		return
	}
	if m.watcher != nil {
		m.watcher.recordSelfSave()
	}
	if err := m.repo.Save(dirty, tombstones); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving tasks on quit: %v\n", err)
	}
	m.dirty = false
	m.savePending = false
}

// copyTodo deep-copies the nested slices of a single task so the result can be
// safely mutated (or read from another goroutine) without affecting the source.
// subProgress is one parent's subtask tally, cached per refresh.
type subProgress struct{ done, total int }

// todoValues is todoPtrs' inverse: a copied value slice, for the places that
// genuinely need one — the sync wire format, an undo snapshot, a digest. It
// copies, so reach for it only when a durable snapshot is the point.
func todoValues(ptrs []*todo.Todo) []todo.Todo {
	out := make([]todo.Todo, 0, len(ptrs))
	for _, t := range ptrs {
		out = append(out, *t)
	}
	return out
}

// todoPtrs adapts a value slice to the pointer form the read-only selectors
// take. The pointers alias the caller's slice, so it is for reading only —
// exactly the contract Store.allTodos carries. Used where a []todo.Todo is the
// natural shape: the CLI (loaded from storage), the sync package, and tests.
func todoPtrs(todos []todo.Todo) []*todo.Todo {
	out := make([]*todo.Todo, len(todos))
	for i := range todos {
		out[i] = &todos[i]
	}
	return out
}

func copyTodo(t todo.Todo) todo.Todo {
	cp := t
	if len(t.Tags) > 0 {
		cp.Tags = append([]string{}, t.Tags...)
	}
	if len(t.Dependencies) > 0 {
		cp.Dependencies = append([]string{}, t.Dependencies...)
	}
	if len(t.Comments) > 0 {
		cp.Comments = append([]todo.Comment{}, t.Comments...)
	}
	if len(t.TimeEntries) > 0 {
		cp.TimeEntries = append([]todo.TimeEntry{}, t.TimeEntries...)
	}
	return cp
}

// copyTodos creates an independent copy of a task slice. Used by the undo stack
// (still snapshot-based until step 8 swaps it for a patch log).
func copyTodos(todos []todo.Todo) []todo.Todo {
	cp := make([]todo.Todo, len(todos))
	for i := range todos {
		cp[i] = copyTodo(todos[i])
	}
	return cp
}

// ── Model mutations ───────────────────────────────────────────────────────────

// markModified marks the named IDs dirty for the next save and flags derived
// caches as stale. The cache is refreshed lazily on the next read (via
// ensureCache), so several mutations within one Update only pay for one
// refresh instead of one per mutation. With no IDs, falls back to marking
// every task dirty — used by mass operations not yet refactored to return
// touched IDs. Undo is NOT pushed here: callers that want undo must call
// m.pushUndo(desc, ids...) BEFORE mutating, so the snapshot captures the
// pre-mutation state.
func (m *model) markModified(ids ...string) {
	taskID := m.currentTaskID()
	if len(ids) == 0 {
		m.markAllDirty()
	} else {
		m.markDirty(ids...)
	}
	m.dirty = true
	m.cache.dirty = true
	m.invalidateDetailCache()
	m.ensureCache()
	m.followTask(taskID)
}

func (m *model) markCacheDirty() {
	m.cache.dirty = true
	m.cache.groupLists, m.cache.dayActs = nil, nil
	m.invalidateDetailCache()
}

// markFilterDirty marks only the filter-derived views (the active/done split and
// its tag-render cache) for rebuild, leaving the data-derived caches — overdue
// set, tag stats, sorted tags, per-project task lists — intact. Use this when
// only the search query or focus filter changed; a full markCacheDirty would
// make every search keystroke rescan and re-sort the whole task set. A pending
// full rebuild (m.cache.dirty) still wins in ensureCache, so combining the two
// in one Update is safe.
func (m *model) markFilterDirty() {
	m.cache.filterDirty = true
	m.invalidateDetailCache()
}

func (m *model) currentTaskID() string {
	// Anchor to the cursor's task on the Tasks tab in either pane — an edit that
	// reorders the list (cycling priority, changing due date, …) should keep the
	// cursor on the same task, not the same row. Arrow/nav keys move the cursor
	// directly without markModified, so they still move freely.
	//
	// The tag/project drill lists re-sort on the same edits (completing a task
	// sinks it), so they anchor too — otherwise the cursor slides onto the
	// neighbouring task and the next key hits the wrong one.
	if m.tab != tabTasks {
		if _, drilled := m.drillTaskList(); !drilled {
			return ""
		}
	}
	if t := m.currentTodo(); t != nil {
		return t.ID
	}
	return ""
}

func (m *model) followTask(taskID string) {
	if taskID == "" {
		return
	}
	// A drill-in list (tag / project) has its own index space, so translating
	// to the Tasks-tab index here would throw the cursor across the list on
	// every mutation. Follow the task inside the list actually on screen.
	if tasks, ok := m.drillTaskList(); ok {
		for i := range tasks {
			if tasks[i].ID == taskID {
				m.cursor = i
				return
			}
		}
		return
	}
	if m.showHistory {
		for i, t := range m.cache.done {
			if t.ID == taskID {
				m.cursor = i
				return
			}
		}
		return
	}
	if idx := m.visibleActiveIndexOf(taskID); idx >= 0 {
		m.cursor = idx
	}
}

// ── Recurrence ────────────────────────────────────────────────────────────────

// buildNextRecurrence constructs (but does not store) a fresh pending instance
// for a just-completed recurring task. Returns (zero, false) if the source
// isn't recurring or the rule is unparseable. The new instance inherits
// identity-ish fields (title, priority, size, project, notes, tags, recurrence
// rule) but starts clean on history (no time entries, comments,
// dependencies, subtasks).
//
// The next DueDate is computed by rolling forward from the previous DueDate (or
// CompletedAt if no due date was set) until it lands at or after today — that
// way a long-overdue "monthly" task doesn't immediately reappear in the past.
// StartDate, if set on the source, is shifted by the same delta the DueDate
// moved by, so the lead time between start and due is preserved.
func buildNextRecurrence(src todo.Todo) (todo.Todo, bool) {
	if !src.IsRecurring() {
		return todo.Todo{}, false
	}
	rule := src.Recurrence
	// Anchor the next instance on the source's due date. A recurring task with
	// no due date intentionally still gets one on respawn: we fall back to its
	// completion time (or now) as the base, so e.g. a "weekly" task closed today
	// yields a fresh instance due one interval out ("next one's due in a week").
	// That keeps urgency scoring meaningful rather than leaving recur rules inert.
	base := src.DueDate
	if base.IsZero() {
		base = src.CompletedAt
	}
	if base.IsZero() {
		base = time.Now()
	}
	next, ok := todo.NextRecurrenceFrom(rule, base)
	if !ok {
		return todo.Todo{}, false
	}
	today := startOfDay(time.Now())
	for next.Before(today) {
		advanced, ok := todo.NextRecurrenceFrom(rule, next)
		if !ok {
			break
		}
		next = advanced
	}

	clone := todo.New(src.Title)
	clone.Priority = src.Priority
	clone.Size = src.Size
	clone.Project = src.Project
	clone.Notes = src.Notes
	clone.Recurrence = src.Recurrence
	if len(src.Tags) > 0 {
		clone.Tags = append([]string{}, src.Tags...)
	}
	clone.DueDate = next
	if !src.StartDate.IsZero() && !src.DueDate.IsZero() {
		clone.StartDate = next.Add(-src.DueDate.Sub(src.StartDate))
	}
	return clone, true
}

// spawnNextRecurrence builds the next instance and adds it to the store.
// Returns the spawned ID, or "" when the source isn't recurring or the rule
// is unparseable. Also clones the source's subtree onto the new parent with
// every child reset to Pending, so a recurring "weekly review" keeps its
// checklist on each spawn instead of losing it.
func (m *model) spawnNextRecurrence(src *todo.Todo) string {
	if src == nil {
		return ""
	}
	next, ok := buildNextRecurrence(*src)
	if !ok {
		return ""
	}
	m.add(next)
	// The whole-parent due-date delta shifts child dates by the same amount,
	// so a "due 2 days before parent" child stays "due 2 days before parent"
	// on the next instance. Zero when either end has no due date.
	var delta time.Duration
	if !src.DueDate.IsZero() && !next.DueDate.IsZero() {
		delta = next.DueDate.Sub(src.DueDate)
	}
	m.cloneSubtreeReset(src.ID, next.ID, delta)
	return next.ID
}

// cloneSubtreeReset clones every descendant of srcParentID, reparented under
// newParentID, with each clone reset to Pending and history wiped
// (CompletedAt, TimeEntries, Comments cleared). DueDate and
// StartDate are shifted by `delta` so the subtree's internal scheduling is
// preserved relative to the new parent. Shared traversal in taskops.go.
func (m *model) cloneSubtreeReset(srcParentID, newParentID string, delta time.Duration) {
	for _, clone := range cloneSubtreeResetFrom(m.subtaskIDs, m.get, srcParentID, newParentID, delta) {
		m.add(clone)
	}
}

// ── Time tracking helpers ─────────────────────────────────────────────────────

// runningTask returns the task with the active timer, or nil. Reads from the
// maintained runningTimers index — O(1) instead of a full map scan.
func (m model) runningTask() *todo.Todo {
	for id := range m.runningTimers {
		if t := m.get(id); t != nil {
			return t
		}
	}
	return nil
}

func (m model) anyTimerRunning() bool {
	return len(m.runningTimers) > 0
}

// openIdlePrompt switches to the runaway-timer prompt for the task's
// running entry.
func (m *model) openIdlePrompt(t *todo.Todo) {
	if t == nil {
		return
	}
	e := t.RunningEntry()
	if e == nil {
		return
	}
	m.pendingEntryTaskID = t.ID
	m.pendingEntryID = e.ID
	m.mode = modeIdlePrompt
	m.confirmMsg = fmt.Sprintf("◉ '%s' tracking for %s · [k]eep · [s]top · [e]dit · [d]iscard",
		truncate(t.Title, 30), formatDuration(time.Since(e.StartedAt)))
}

// toggleTimer stops t's timer if running, otherwise stops any other running
// timer (only one task is tracked at a time) and starts this one. The Store's
// runningTimers index is maintained by Store.startTimer / stopTimer.
func (m *model) toggleTimer(t *todo.Todo) {
	if t == nil {
		return
	}
	if t.IsTimerRunning() {
		m.stopTimer(t.ID)
		return
	}
	for otherID := range m.runningTimers {
		if otherID != t.ID {
			m.stopTimer(otherID)
		}
	}
	m.startTimer(t.ID)
}

// ── Lookup helpers ────────────────────────────────────────────────────────────

func (m model) findTodoByID(id string) *todo.Todo {
	return m.get(id)
}

// currentTodo returns the *Todo at the current cursor position in whichever
// task list the user is viewing (active / done / project-tasks), or nil if the
// cursor is past the end of that list.
func (m model) currentTodo() *todo.Todo {
	// A detail pane opened from another task's subtask list is not tied to a
	// visible list row (nested subtasks may not have one). Keep its target
	// explicitly while the pane is open, falling back to the list cursor for
	// tests and older entry paths that do not set one.
	if m.pane == paneDetail && m.detailTaskID != "" {
		return m.get(m.detailTaskID)
	}
	switch m.tab {
	case tabTasks:
		if m.showHistory {
			if m.cursor < len(m.cache.done) {
				return m.get(m.cache.done[m.cursor].ID)
			}
			return nil
		}
		return m.visibleActiveAt(m.cursor)
	case tabTags, tabProjects:
		// Both tabs resolve through the same drill list the keys and the cursor
		// bookkeeping use, so a task can't be under the cursor for one and
		// absent for the other.
		if tasks, ok := m.drillTaskList(); ok && m.cursor < len(tasks) {
			return m.get(tasks[m.cursor].ID)
		}
	}
	return nil
}

// ── Subtask helpers ───────────────────────────────────────────────────────────

// subtaskIDs returns parentID's child task IDs in CreatedAt order, read
// directly from the maintained subtaskOf index — O(1) lookup, no rebuild.
func (m model) subtaskIDs(parentID string) []string {
	return m.subtaskOf[parentID]
}

// visibleActiveTasks returns the Tasks-tab active list flattened with the
// subtasks of expanded parents interleaved in order, so the list cursor can
// land on a subtask. Returned by value (a snapshot) like cache.active —
// callers re-resolve by ID for mutation.
func (m model) visibleActiveTasks() []todo.Todo {
	active := m.cache.active
	out := make([]todo.Todo, 0, len(active))
	for i := range active {
		out = append(out, active[i])
		if !m.expandedTasks[active[i].ID] {
			continue
		}
		for _, subID := range m.subtaskIDs(active[i].ID) {
			if sub := m.get(subID); sub != nil {
				out = append(out, *sub)
			}
		}
	}
	return out
}

// visibleActiveLen reports how many rows the active Tasks list renders to —
// top-level tasks plus the subtasks of expanded parents — without materializing
// the flattened slice. Mirrors visibleActiveTasks' counting (including its skip
// of any dangling subtask ID) so flat indices line up with it.
func (m *model) visibleActiveLen() int {
	active := m.cache.active
	n := len(active)
	for i := range active {
		if !m.expandedTasks[active[i].ID] {
			continue
		}
		for _, subID := range m.subtaskOf[active[i].ID] {
			if m.get(subID) != nil {
				n++
			}
		}
	}
	return n
}

// visibleActiveAt returns the task at flat row idx in the active Tasks list, or
// nil if idx is out of range — walking the flattened order without building it.
func (m *model) visibleActiveAt(idx int) *todo.Todo {
	if idx < 0 {
		return nil
	}
	row := 0
	active := m.cache.active
	for i := range active {
		if row == idx {
			return m.get(active[i].ID)
		}
		row++
		if !m.expandedTasks[active[i].ID] {
			continue
		}
		for _, subID := range m.subtaskOf[active[i].ID] {
			sub := m.get(subID)
			if sub == nil {
				continue
			}
			if row == idx {
				return sub
			}
			row++
		}
	}
	return nil
}

// visibleActiveIndexOf returns the flat row index of taskID in the active Tasks
// list, or -1 if it isn't currently visible.
func (m *model) visibleActiveIndexOf(taskID string) int {
	row := 0
	active := m.cache.active
	for i := range active {
		if active[i].ID == taskID {
			return row
		}
		row++
		if !m.expandedTasks[active[i].ID] {
			continue
		}
		for _, subID := range m.subtaskOf[active[i].ID] {
			sub := m.get(subID)
			if sub == nil {
				continue
			}
			if sub.ID == taskID {
				return row
			}
			row++
		}
	}
	return -1
}

// visibleActiveWindow materializes only flat rows [start, end) of the active
// Tasks list, so the renderer copies the screenful it draws instead of the whole
// list. Rows outside the window are walked by pointer (no struct copy); only the
// emitted rows are copied into the returned slice.
func (m *model) visibleActiveWindow(start, end int) []todo.Todo {
	if start < 0 {
		start = 0
	}
	if end <= start {
		return nil
	}
	out := make([]todo.Todo, 0, end-start)
	row := 0
	done := false
	visit := func(t *todo.Todo) {
		if row >= start && row < end {
			out = append(out, *t)
		}
		row++
		if row >= end {
			done = true
		}
	}
	active := m.cache.active
	for i := range active {
		visit(&active[i])
		if done {
			break
		}
		if !m.expandedTasks[active[i].ID] {
			continue
		}
		for _, subID := range m.subtaskOf[active[i].ID] {
			if sub := m.get(subID); sub != nil {
				visit(sub)
				if done {
					break
				}
			}
		}
		if done {
			break
		}
	}
	return out
}

// subtaskCount returns how many subtasks parentID has, via the maintained
// subtaskOf index.
func (m *model) subtaskCount(parentID string) int {
	return len(m.subtaskOf[parentID])
}

// descendantIDs returns rootID followed by every transitive subtask ID in
// BFS order, via the maintained subtaskOf index. Shared traversal in
// taskops.go.
func (m model) descendantIDs(rootID string) []string {
	return descendantIDsFrom(m.subtaskIDs, rootID)
}

// subtaskProgress reports the (done, total) count of parentID's direct
// children. The Tasks-tab badge `(2/5)` reads this — direct children match
// the visible tree better than counting transitive descendants.
func (m *model) subtaskProgress(parentID string) (done, total int) {
	// Served from the cache the refresh builds in one pass. A *missing* key
	// means "no subtasks", which is most tasks — so the warm signal has to be
	// the map's existence, not the lookup's ok. Treating a miss as cold sent
	// every childless task down the walk below, which is what kept this at a
	// sixth of the whole refresh. The walk stays as the cold path: tests and the
	// CLI never build the cache.
	if m.cache.subProgress != nil {
		p := m.cache.subProgress[parentID]
		return p.done, p.total
	}
	ids := m.subtaskIDs(parentID)
	total = len(ids)
	for _, id := range ids {
		if c := m.get(id); c != nil && c.Status == todo.Done {
			done++
		}
	}
	return done, total
}

// descendantTimeSpent sums TotalTimeSpent across parentID's full subtree (not
// including parentID itself). Used by the detail view to roll subtask time
// up onto the parent's display.
func (m model) descendantTimeSpent(parentID string) time.Duration {
	var sum time.Duration
	for _, id := range m.subtaskIDs(parentID) {
		if c := m.get(id); c != nil {
			sum += c.TotalTimeSpent()
		}
		sum += m.descendantTimeSpent(id)
	}
	return sum
}

// allDescendantsDoneOrEmpty reports whether every transitive descendant of
// parentID is Done, returning true when parentID has no children. Used
// recursively so a leaf doesn't fail the "all done" check just by lacking
// subtasks.
func (m model) allDescendantsDoneOrEmpty(parentID string) bool {
	for _, id := range m.subtaskIDs(parentID) {
		c := m.get(id)
		if c == nil || c.Status != todo.Done {
			return false
		}
		if !m.allDescendantsDoneOrEmpty(id) {
			return false
		}
	}
	return true
}

// autoCloseAncestorsIfAllDone walks up from childID and, while the setting
// is enabled, closes every ancestor whose subtree is now fully done. Returns
// the closed ancestor IDs (plus any recurring-spawn IDs) so the caller can
// mark them dirty. No-op when the setting is off or childID itself isn't
// Done. Open ancestors with sibling work pending naturally stop the walk
// because allDescendantsDoneOrEmpty returns false.
func (m *model) autoCloseAncestorsIfAllDone(childID string) []string {
	if !m.autoCloseParent {
		return nil
	}
	var closed []string
	cur := m.get(childID)
	for cur != nil && cur.ParentID != "" {
		parent := m.get(cur.ParentID)
		if parent == nil || parent.Status == todo.Done {
			break
		}
		if !m.allDescendantsDoneOrEmpty(parent.ID) {
			break
		}
		if parent.IsTimerRunning() {
			m.stopTimer(parent.ID)
		}
		parent.Toggle()
		closed = append(closed, parent.ID)
		if parent.IsRecurring() {
			if newID := m.spawnNextRecurrence(parent); newID != "" {
				closed = append(closed, newID)
			}
		}
		cur = parent
	}
	return closed
}

// closePendingSubtree closes every still-pending, non-deleted descendant of
// parentID (the mirror of autoCloseAncestorsIfAllDone), so marking a parent
// Done doesn't strand open subtasks. Stops any running timers and stamps the
// sequence rank like a normal close. Returns the closed descendant IDs so the
// caller can mark them dirty. Does not touch parentID itself.
func (m *model) closePendingSubtree(parentID string) []string {
	var closed []string
	for _, id := range descendantIDsFrom(m.subtaskIDs, parentID)[1:] { // [0] is parentID
		s := m.get(id)
		if s == nil || s.Deleted || s.Status != todo.Pending {
			continue
		}
		if s.IsTimerRunning() {
			m.stopTimer(s.ID)
		}
		rank.CaptureRankAtDone(m.rank, m.allTodos(), s)
		s.Toggle()
		closed = append(closed, s.ID)
	}
	return closed
}

// extendParentDueIfNeeded walks up from subID and bumps each ancestor's
// DueDate forward to at least match the child's, recursively. Only extends
// — never shrinks an ancestor's date. Returns the ancestor IDs that were
// modified so the caller can mark them dirty. Shared walk in taskops.go.
func (m *model) extendParentDueIfNeeded(subID string) []string {
	bumped := extendAncestorsDue(m.get, m.get(subID))
	ids := make([]string, len(bumped))
	for i, p := range bumped {
		ids[i] = p.ID
	}
	return ids
}

// cyclePriority steps t through Low → Medium → High → Low and keeps the task
// tree legal around it: the step is capped at the parent's priority, and
// whatever value survives is pushed down over any subtask that outranks it.
// Reports whether the cap clawed the step back, so the caller can say why the
// keypress moved nothing. Shared by the list pane's `p` and the detail pane's
// Priority row — one rule, however the user reaches it.
func (m *model) cyclePriority(t *todo.Todo) bool {
	// The cap can touch a parent or a whole subtree, so undo captures the
	// full tree unless this task stands alone.
	if t.ParentID != "" || m.subtaskCount(t.ID) > 0 {
		m.pushUndo("cycle priority")
	} else {
		m.pushUndo("cycle priority", t.ID)
	}
	switch t.Priority {
	case todo.PriorityLow:
		t.SetPriority(todo.PriorityMedium)
	case todo.PriorityMedium:
		t.SetPriority(todo.PriorityHigh)
	default:
		t.SetPriority(todo.PriorityLow)
	}
	capped := clampPriorityToParent(m.get, t)
	ids := []string{t.ID}
	for _, child := range clampDescendantsPriority(m.subtaskIDs, m.get, t) {
		ids = append(ids, child.ID)
	}
	m.markModified(ids...)
	return capped
}

// setProject puts t, and every subtask under it, in project ("" takes them out
// of one) as one undo step.
func (m *model) setProject(t *todo.Todo, project, undoDesc string) {
	m.pushUndo(undoDesc, descendantIDsFrom(m.subtaskIDs, t.ID)...)
	t.SetProject(project)
	ids := []string{t.ID}
	for _, child := range propagateDescendantsProject(m.subtaskIDs, m.get, t) {
		ids = append(ids, child.ID)
	}
	m.markModified(ids...)
}

// propagateDueToSubtasks copies parentID's due date (including a cleared zero
// date) to every descendant and returns the changed IDs for dirty tracking.
func (m *model) propagateDueToSubtasks(parentID string) []string {
	changed := propagateDescendantsDue(m.subtaskIDs, m.get, m.get(parentID))
	ids := make([]string, len(changed))
	for i, child := range changed {
		ids[i] = child.ID
	}
	return ids
}

// toggleSubtask flips a child task's status and returns every ID the caller
// should mark dirty: the toggled subtask, plus the freshly-spawned next
// instance when the subtask was a recurring task closed by this call, plus
// any ancestors auto-closed because their subtree is now fully done.
func (m *model) toggleSubtask(parentID string, subtaskCursor int) []string {
	ids := m.subtaskIDs(parentID)
	if subtaskCursor >= len(ids) {
		return nil
	}
	subID := ids[subtaskCursor]
	t := m.get(subID)
	if t == nil {
		return nil
	}
	// Don't leave a dangling open time entry when closing a subtask. Mirrors
	// the top-level `d` handler in update.go.
	if t.Status == todo.Pending && t.IsTimerRunning() {
		m.stopTimer(t.ID)
	}
	wasPending := t.Status == todo.Pending
	t.Toggle()
	out := []string{subID}
	if wasPending && t.IsRecurring() {
		if newID := m.spawnNextRecurrence(t); newID != "" {
			out = append(out, newID)
		}
	}
	if wasPending {
		out = append(out, m.autoCloseAncestorsIfAllDone(subID)...)
	}
	return out
}

// ── Search/filter helpers ─────────────────────────────────────────────────────

func (m model) matchesSearch(t todo.Todo) bool {
	return todoMatchesSearch(t, m.searchQuery)
}

// loopingDepCandidates returns the task IDs that must not be offered as a new
// dependency of curID, because depending on them would create a cycle: curID
// itself, plus every task that already (transitively) depends on curID. It
// builds the dependents adjacency once and BFS-es out from curID; the visited
// set doubles as the result and guards against pre-existing/malformed cycles so
// the walk always terminates.
func loopingDepCandidates(tasks map[string]*todo.Todo, curID string) map[string]bool {
	excluded := map[string]bool{curID: true}
	dependents := make(map[string][]string)
	for _, c := range tasks {
		for _, dep := range c.Dependencies {
			dependents[dep] = append(dependents[dep], c.ID)
		}
	}
	queue := []string{curID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range dependents[id] {
			if !excluded[d] {
				excluded[d] = true
				queue = append(queue, d)
			}
		}
	}
	return excluded
}

func (m model) depSearchResults() []todo.Todo {
	t := m.currentTodo()
	q := strings.ToLower(m.depSearch.query)
	// Hide any candidate that would close a dependency loop — the current task
	// itself plus everything that already (transitively) depends on it — so the
	// picker can only ever offer a task that's safe to depend on. No error path
	// needed: a cycle-forming task simply isn't in the list.
	var excluded map[string]bool
	if t != nil {
		excluded = loopingDepCandidates(m.tasks, t.ID)
	}
	result := make([]todo.Todo, 0, maxDepSearchResults*2)
	for _, candidate := range m.tasks {
		if excluded[candidate.ID] {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(candidate.Title), q) {
			result = append(result, *candidate)
		}
	}
	// Most-recently-modified first, so the tasks you've just been working on are
	// the likeliest dependencies and sit at the top — matching the recency order
	// the tag/project pickers use. The ID tiebreak keeps the picker stable across
	// redraws (cursor blink, timer tick): a bare map range reshuffled every frame.
	sort.Slice(result, func(i, j int) bool {
		if !result[i].ModifiedAt.Equal(result[j].ModifiedAt) {
			return result[i].ModifiedAt.After(result[j].ModifiedAt)
		}
		return result[i].ID < result[j].ID
	})
	if len(result) > maxDepSearchResults*3 {
		result = result[:maxDepSearchResults*3]
	}
	return result
}

// getAllTagsSorted is every tag in use, alphabetical. The pickers and the
// quick-add completion re-sort it by recency themselves.
func (m model) getAllTagsSorted() []string {
	return m.cache.tagNames
}

// getFilteredTagsForTab is the Tags tab's list: the tags matching its own
// filter, in the tab's order, with finished tags left out unless shown (see
// visibleGroups). The virtual (untagged) row leads when it has anything to
// show and matches the filter text.
func (m model) getFilteredTagsForTab() []string {
	q := strings.ToLower(m.tagTabSearchQuery)
	pinned := ""
	if m.tagTaskMode {
		pinned = m.tagPinned
	}
	return visibleGroups(m.cache.tagGroups, m.tagOrder, m.showFinishedGroups, pinned, tagFilterMatch(q))
}

func tagFilterMatch(q string) func(string) bool {
	return func(key string) bool {
		if key == untaggedKey {
			return q == "" || strings.Contains("untagged", q)
		}
		return q == "" || strings.Contains(strings.ToLower(key), q)
	}
}

// allProjectsForList is the Projects tab's list. It shares the Tasks-list
// search, matched against project names.
func (m model) allProjectsForList() []string {
	pinned := ""
	if m.projectTaskMode {
		pinned = m.projectPinned
	}
	return visibleGroups(m.cache.projectGroups, m.projectOrder, m.showFinishedGroups, pinned, projectFilterMatch(m.searchQuery))
}

func projectFilterMatch(search string) func(string) bool {
	// The query is shared with the Tasks tab, where a project is asked for
	// as @name; here the list is already projects, so the sigil is noise.
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(search), "@"))
	return func(key string) bool { return q == "" || strings.Contains(strings.ToLower(key), q) }
}

// tagTaskList and getProjectTasks are groupTaskList for one tag or project:
// the list a row's enter opens, which the pane under the list previews.
func (m model) tagTaskList(tag string) []todo.Todo {
	return m.groupListMemo("#"+tag, func(t *todo.Todo) bool { return inTagGroup(t, tag) })
}

func (m model) getProjectTasks(project string) []todo.Todo {
	return m.groupListMemo("@"+project, func(t *todo.Todo) bool { return inProjectGroup(t, project) })
}

// setExpanded folds or unfolds id's subtasks. The one writer of
// expandedTasks, because the memoized group lists show unfolded subtasks and
// have to be rebuilt when a fold changes.
func (m *model) setExpanded(id string, open bool) {
	if open {
		m.expandedTasks[id] = true
	} else {
		delete(m.expandedTasks, id)
	}
	if m.cache != nil {
		m.cache.groupLists = nil
	}
}

// groupListMemo is groupTaskList through cache.groupLists. The slice is
// shared between readers, so it is read-only: copy a row before changing it.
func (m model) groupListMemo(key string, match func(*todo.Todo) bool) []todo.Todo {
	if m.cache == nil {
		return m.groupTaskList(match)
	}
	if m.showFinishedGroups {
		key += "|finished"
	}
	if list, ok := m.cache.groupLists[key]; ok {
		return list
	}
	list := m.groupTaskList(match)
	if m.cache.groupLists == nil {
		m.cache.groupLists = make(map[string][]todo.Todo)
	}
	m.cache.groupLists[key] = list
	return list
}

// currentTagTasks is tagTaskList for the tag under the Tags-tab cursor.
func (m model) currentTagTasks() []todo.Todo {
	tags := m.getFilteredTagsForTab()
	if m.tagTabCursor >= len(tags) {
		return nil
	}
	return m.tagTaskList(tags[m.tagTabCursor])
}

// drillTaskList returns the task list the cursor is walking when the Tags or
// Projects tab is drilled into a row, and false when it isn't. The single
// place that answers "is the cursor on a task right now?", so the row-level
// keys and the cursor bookkeeping can't disagree about it.
func (m model) drillTaskList() ([]todo.Todo, bool) {
	switch {
	case m.tab == tabTags && m.tagTaskMode:
		return m.currentTagTasks(), true
	case m.tab == tabProjects && m.projectTaskMode:
		// Through the accessor, not m.cache.projects: refreshCaches invalidates
		// that field and only allProjectsForList rebuilds it, so reading it raw
		// made every key between a mutation and the next render see no project
		// at all — the drill cursor jumped to the top after each edit.
		projects := m.allProjectsForList()
		if m.projectCursor >= len(projects) {
			return nil, true
		}
		return m.getProjectTasks(projects[m.projectCursor]), true
	}
	return nil, false
}

func (m model) tagSearchResults() []string {
	allTags := m.getAllTagsSorted()
	t := m.currentTodo()
	q := strings.ToLower(m.tagSearch.query)
	existing := make(map[string]struct{})
	if t != nil {
		for _, tag := range t.Tags {
			existing[tag] = struct{}{}
		}
	}
	result := make([]string, 0, len(allTags))
	for _, tag := range allTags {
		if _, added := existing[tag]; added {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(tag), q) {
			result = append(result, tag)
		}
	}
	sortByRecency(result, m.cache.tagLastUsed)
	return result
}

func (m model) projSearchResults() []string {
	q := strings.ToLower(m.projSearch.query)
	result := make([]string, 0, len(m.cache.projectNames))
	for _, p := range m.cache.projectNames {
		if q == "" || strings.Contains(strings.ToLower(p), q) {
			result = append(result, p)
		}
	}
	sortByRecency(result, m.cache.projLastUsed)
	return result
}

// sortByRecency orders names most-recently-used first (latest ModifiedAt of any
// task carrying the tag/project); names with equal/no usage time fall back to
// alphabetical.
func sortByRecency(names []string, lastUsed map[string]time.Time) {
	sort.SliceStable(names, func(i, j int) bool {
		ti, tj := lastUsed[names[i]], lastUsed[names[j]]
		if ti.Equal(tj) {
			return names[i] < names[j]
		}
		return ti.After(tj)
	})
}

// ── Global mutations ──────────────────────────────────────────────────────────

// renameTagGlobally rewrites every occurrence of oldName to newName and
// returns the IDs of the tasks it touched, for dirty marking.
func (m *model) renameTagGlobally(oldName, newName string) []string {
	newName = todo.NormalizeTag(newName)
	if newName == "" || newName == oldName {
		return nil
	}
	var touched []string
	for _, t := range m.tasks {
		has := false
		for _, tag := range t.Tags {
			if tag == oldName {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		// RemoveTag normalizes its argument, so it can't match a legacy
		// mixed-case stored tag (e.g. "Work") — strip the literal oldName,
		// then AddTag (which normalizes + dedups) to complete the merge.
		kept := t.Tags[:0]
		for _, tag := range t.Tags {
			if tag != oldName {
				kept = append(kept, tag)
			}
		}
		t.Tags = kept
		t.AddTag(newName)
		touched = append(touched, t.ID)
	}
	return touched
}

func (m *model) deleteTagGlobally(tagName string) []string {
	var touched []string
	for _, t := range m.tasks {
		hadTag := false
		tags := t.Tags[:0]
		for _, tag := range t.Tags {
			if tag == tagName {
				hadTag = true
				continue
			}
			tags = append(tags, tag)
		}
		t.Tags = tags
		if hadTag {
			touched = append(touched, t.ID)
		}
	}
	return touched
}

func (m *model) renameProjectGlobally(oldName, newName string) []string {
	var touched []string
	for _, t := range m.tasks {
		if t.Project == oldName {
			t.Project = newName
			touched = append(touched, t.ID)
		}
	}
	return touched
}
