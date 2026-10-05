package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Iliorn/tjek/paths"
	"github.com/Iliorn/tjek/rank"
	"github.com/Iliorn/tjek/todo"
)

// getStoragePath is the legacy JSON file, kept only as the first-run import
// source. It sits with the database: it *was* the database.
func getStoragePath() string {
	return paths.For(paths.Data, "tasks.json")
}

// tjekDir is the directory holding tasks.db — what the filesystem watcher
// watches and what the doctor reports as the data directory.
func tjekDir() string {
	dir, _ := paths.Dir(paths.Data)
	return dir
}

// ── Settings ──────────────────────────────────────────────────────────────────

// currentSettingsVersion is stamped into newly written settings.json. Bump it
// when the on-disk shape changes in a way migrateSettings must handle.
const currentSettingsVersion = 2

type appSettings struct {
	// Version is the schema marker for migrateSettings. Zero means "legacy
	// pre-versioning file" (read as-is); newly saved settings always have
	// the current version.
	Version int `json:"version"`

	TaskSort     taskSortMode    `json:"task_sort"`
	HistorySort  historySortMode `json:"history_sort"`
	TagOrder     groupSort       `json:"tag_order"`
	ProjectOrder groupSort       `json:"project_order"`
	Theme        string          `json:"theme"`
	Language     string          `json:"language"`

	// Sequencing biases: ints 0/1/2 mapping to rank.Balanced/Relaxed/Intense.
	// Stored as ints (not enum names) to match the existing convention used by
	// TaskSort and friends. Zero value = rank.Balanced, which is the neutral
	// default so an unset settings.json keeps the engine "Balanced" out of the
	// box without explicit migration.
	SeqBiasDeadline rank.Level `json:"seq_bias_deadline"`
	SeqBiasPriority rank.Level `json:"seq_bias_priority"`
	SeqBiasMomentum rank.Level `json:"seq_bias_momentum"`

	// SeqAgingDisabled gates the per-day Age contribution. Stored as the
	// inverse of the user-facing "Aging" toggle so the zero value (=false)
	// keeps aging on by default — matches pre-toggle behaviour without
	// migration.
	SeqAgingDisabled bool `json:"seq_aging_disabled"`

	// AutoCloseParent: when on, a parent task is auto-marked Done the moment
	// its last open subtask closes. Off by default because the parent often
	// represents review/sign-off work that survives the children. Opt-in
	// for users who prefer parents as folders.
	AutoCloseParent bool `json:"auto_close_parent"`

	// AutoCloseSubtasks is the mirror of AutoCloseParent: when on, marking a
	// parent Done also closes its still-open subtasks, so a done parent never
	// strands pending children (invisible in every list but export). Off by
	// default so closing a parent doesn't silently finish work you meant to
	// keep open — the confirm/prompt path stays the default.
	AutoCloseSubtasks bool `json:"auto_close_subtasks"`

	// BoardDisabled hides the kanban surface: the Board tab and the detail
	// pane's Stage row. Negative like SeqAgingDisabled, so the zero value
	// leaves the board on and no existing settings.json needs migrating.
	BoardDisabled bool `json:"board_disabled"`

	// Stages is the ordered kanban column list for the Board tab (edited by
	// hand — everyone has their own naming scheme). Empty means the defaults
	// (Backlog / In progress / Review / Done); persistSettings writes the
	// active list out so the field is discoverable in settings.json. The last
	// entry is the Done column: renameable like any other, but always the one
	// holding the completed tasks (see board.go).
	Stages []string `json:"stages,omitempty"`

	// StagesModifiedAt is when the list above was last edited on this device,
	// and the only thing that decides whose list a fleet keeps (boardsync.go).
	// Absent means never edited here, which can never outrank a device that
	// has — so a hand-edited settings.json should carry a timestamp if its
	// list is meant to win.
	StagesModifiedAt time.Time `json:"stages_modified_at,omitempty"`

	// StageIcons gives working columns a one-cell mark, keyed by the column's
	// lower-cased name: {"in progress": "◐"}. Written from the editor's
	// "[◐] In progress" form; see boardConfig.icons.
	StageIcons map[string]string `json:"stage_icons,omitempty"`

	// SyncBoardDisabled opts out of sharing the column list with the fleet.
	// Negative like BoardDisabled and SeqAgingDisabled, so the zero value
	// shares: the names are what make a synced Stage field mean the same thing
	// on two machines, and a device that has never edited its columns cannot
	// overwrite anyone's (see tasksync.MergeBoard).
	SyncBoardDisabled bool `json:"sync_board_disabled,omitempty"`

	// DetailPosition is where the detail pane sits on the tabs that have one:
	// "right" (default), "left", or "bottom". Stored as the word rather than
	// the enum's number because settings.json is hand-edited, and a number
	// there would mean nothing without this file open beside it.
	DetailPosition string `json:"detail_position,omitempty"`

	// Search is the committed `/` filter on the Tasks tab, restored at startup
	// so a filter you were working under survives a restart like every other
	// view preference. Only the Tasks tab's query is kept: it is the tab the
	// app opens on, and the other tabs that share the query (Board, Stats)
	// restore theirs from tabViews, which starts empty. Written when the
	// search settles (committed, cancelled, or cleared with esc), never per
	// keystroke — settings.json is rewritten atomically on every save.
	Search string `json:"search,omitempty"`

	// SubtaskTagsDisabled stops a new subtask copying its parent's tags.
	// Negative like BoardDisabled, so the zero value keeps copying them.
	SubtaskTagsDisabled bool `json:"subtask_tags_disabled,omitempty"`

	// Reminder is the time of the daily due-date reminder, "HH:MM"; absent
	// means the default (see reminder.go). A word rather than minutes for the
	// same reason as DetailPosition: the file is hand-edited. ReminderOff
	// switches the reminder off and keeps the time for when it comes back on;
	// a Reminder of "off", from before the switch existed, reads as off too.
	Reminder    string `json:"reminder,omitempty"`
	ReminderOff bool   `json:"reminder_off,omitempty"`

	// ExportFolder is where the TUI keeps tjek-export.json current
	// (exportsettings.go); empty means no auto-export.
	ExportFolder string `json:"export_folder,omitempty"`

	// Keys rebinds actions to keys: {"done": "D", "search": "s"}. Keyed by the
	// action ids in keymap.go, which is why they exist — see keys.go for what
	// can be rebound and how a broken entry is handled (dropped with a warning,
	// never leaving the action unreachable).
	Keys map[string]string `json:"keys,omitempty"`

	// AIProvider and the three keys below configure the Meetings tab's AI
	// calls (aiprovider/): summarizing a transcript and mining it for action
	// items. Local only, like every other field here — settings.json as a
	// whole is never part of the tasksync wire protocol (only individual
	// fields plumbed through Request/Response are), so these keys never
	// leave this machine through sync the way a sync token or server token
	// would have to be guarded against if they did.
	AIProvider   string `json:"ai_provider,omitempty"`
	AnthropicKey string `json:"anthropic_key,omitempty"`
	OpenAIKey    string `json:"openai_key,omitempty"`
	GeminiKey    string `json:"gemini_key,omitempty"`

	// FFmpegInput overrides audiorecorder.go's per-platform microphone
	// default: "format:input", e.g. "dshow:audio=Microphone Array". Required
	// on Windows (DirectShow device names aren't guessable); optional
	// elsewhere, for an ALSA-only Linux box or a non-default input device.
	FFmpegInput string `json:"ffmpeg_input,omitempty"`
}

// migrateSettings brings settings saved under an older schema version up to
// the current one, so old files are converted rather than silently misread.
//
// v2 made the last entry of "stages" the Done column. Under v1 that column was
// implicit and appended by the renderer, so reading a v1 list unchanged would
// silently promote its last working column ("Review") to Done and file every
// card in it under completed work. Appending the column the old renderer drew
// is the exact inverse — and it is appended in the *user's* language, because
// that is the heading v1 showed them.
func migrateSettings(version int, s appSettings) appSettings {
	if version < 2 && len(s.Stages) > 0 {
		s.Stages = append(s.Stages, doneStageIn(language(s.Language)))
	}
	s.Version = currentSettingsVersion
	return s
}

// doneStageIn is the default Done heading in a given language. Migration runs
// before applyLang (loadSettings is what tells us which language to apply), so
// it cannot go through tr.
func doneStageIn(l language) string {
	if t, ok := translations[l][defaultDoneStage]; ok && t != "" {
		return t
	}
	return defaultDoneStage
}

// biasesFromSettings is the small adapter between the persisted appSettings
// shape and the in-memory biases the score functions read.
func biasesFromSettings(s appSettings) rank.Biases {
	return rank.Biases{
		Deadline: s.SeqBiasDeadline,
		Priority: s.SeqBiasPriority,
		Momentum: s.SeqBiasMomentum,
		Aging:    !s.SeqAgingDisabled,
	}
}

// storedBiases reads the bias knobs from settings.json, for the paths that
// score rows without a model or a loaded CLI store: the headless server and
// the syncs that run beside a command. An unreadable file reads as the
// defaults, as it does everywhere else.
func storedBiases() rank.Biases {
	s, _ := loadSettings()
	return biasesFromSettings(s)
}

// storedSubtaskTags reads whether new subtasks copy their parent's tags, for
// the CLI, which has no model to hold it.
func storedSubtaskTags() bool {
	s, _ := loadSettings()
	return !s.SubtaskTagsDisabled
}

func settingsPath() string {
	return paths.For(paths.Config, "settings.json")
}

// loadSettings reads settings.json and applies any schema migration.
// Missing file is *not* an error — a brand-new install legitimately has no
// settings yet and gets all-zero defaults. Any other failure (corrupt JSON,
// permissions, partial write) is returned so the caller can surface it
// instead of silently resetting the user's preferences.
func loadSettings() (appSettings, error) {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return appSettings{}, nil
		}
		return appSettings{}, err
	}
	var s appSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return appSettings{}, fmt.Errorf("settings.json is corrupt: %w", err)
	}
	if s.Version != currentSettingsVersion {
		s = migrateSettings(s.Version, s)
	}
	return s, nil
}

func saveSettings(s appSettings) error {
	s.Version = currentSettingsVersion
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := paths.Ensure(paths.Config); err != nil {
		return err
	}
	return writeFileAtomic(settingsPath(), data, 0644)
}

// ensureStorageDir creates the data directory (the database's home).
func ensureStorageDir() error {
	_, err := paths.Ensure(paths.Data)
	return err
}

// currentTaskFileVersion is the schema version stamped into newly written task
// files. Bump it when the on-disk shape changes in a way migrate() must handle.
const currentTaskFileVersion = 1

// taskFile is the versioned on-disk envelope. Older releases wrote a bare
// []todo.Todo with no version; decodeTaskFile reads both shapes so existing
// users' data keeps loading.
type taskFile struct {
	Version int         `json:"version"`
	Todos   []todo.Todo `json:"todos"`
}

// migrate brings todos saved under an older schema version up to the current
// one. It is a no-op today; future breaking field changes get a case here so
// data is converted rather than silently dropped.
func migrate(version int, todos []todo.Todo) []todo.Todo {
	return todos
}

// decodeTaskFile unmarshals either the versioned envelope or the legacy
// bare-array format. A bare array fails to decode into the struct, which cleanly
// routes it to the legacy path.
func decodeTaskFile(data []byte) ([]todo.Todo, error) {
	var tf taskFile
	if err := json.Unmarshal(data, &tf); err == nil && tf.Version > 0 {
		return migrate(tf.Version, tf.Todos), nil
	}
	var todos []todo.Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil, err
	}
	return todos, nil
}

// OPTIMIZATION: use json.Marshal (no indentation) for faster serialization.
// The file is still valid JSON, just compact. Saves ~30-40% marshalling time
// and produces smaller files.
func marshalTodos(todos []todo.Todo) ([]byte, error) {
	return json.Marshal(taskFile{Version: currentTaskFileVersion, Todos: todos})
}

// loadBackup attempts to load the most recent backup file.
func loadBackup() ([]todo.Todo, error) {
	backupPath := getStoragePath() + ".bak"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []todo.Todo{}, nil
		}
		return nil, err
	}
	todos, err := decodeTaskFile(data)
	if err != nil {
		return nil, fmt.Errorf("backup file is also corrupt: %w", err)
	}
	return todos, nil
}

// loadTodosJSON reads the legacy JSON file. It is no longer the live store —
// SQLite (storage_sqlite.go) is — but remains the one-time import source and a
// corruption fallback.
func loadTodosJSON() ([]todo.Todo, error) {
	path := getStoragePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []todo.Todo{}, nil
		}
		return nil, err
	}

	todos, err := decodeTaskFile(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: tasks.json is corrupt (%v), attempting backup...\n", err)
		return loadBackup()
	}

	sortTodosByMode(todos, taskSortSequence, rank.Default().ScoreNow())
	return todos, nil
}

// ── Sort comparators ─────────────────────────────────────────────────────────
//
// Each mode's ordering lives in one less-function over *todo.Todo, shared by
// the value sorts (which the CLI and storage use) and the pointer sorts the
// cache refresh uses. Every chain ends at ID, so each is a *total* order — that
// is what lets the sorts below use sort.Slice instead of sort.SliceStable: with
// no ties left to preserve, the stable merge only bought slower sorting.

func lessByDueDate(a, b *todo.Todo) bool {
	aZero, bZero := a.DueDate.IsZero(), b.DueDate.IsZero()
	if aZero && bZero {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	}
	if aZero {
		return false
	}
	if bZero {
		return true
	}
	if !a.DueDate.Equal(b.DueDate) {
		return a.DueDate.Before(b.DueDate)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func lessBySize(a, b *todo.Todo) bool {
	if ra, rb := a.Size.Rank(), b.Size.Rank(); ra != rb {
		return ra < rb
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func lessByTitle(a, b *todo.Todo) bool {
	ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title)
	if ta != tb {
		return ta < tb
	}
	return a.ID < b.ID
}

func lessByCompletedAt(a, b *todo.Todo) bool {
	if !a.CompletedAt.Equal(b.CompletedAt) {
		return a.CompletedAt.After(b.CompletedAt)
	}
	return a.ID < b.ID
}

// sortTodoPtrs sorts a pointer slice with one of the comparators above. This is
// the form the cache refresh uses: sorting []todo.Todo moves 416-byte structs
// on every swap, which made the stable merge inside selectActiveDone the single
// most expensive thing in a refresh. Sorting pointers moves 8 bytes.
func sortTodoPtrs(todos []*todo.Todo, less func(a, b *todo.Todo) bool) {
	sort.Slice(todos, func(i, j int) bool { return less(todos[i], todos[j]) })
}

// sortTodosByMode sorts todos by the given mode. After the sequencing engine
// only two modes exist; any other value falls through to Sequence.
func sortTodosByMode(todos []todo.Todo, mode taskSortMode, score func(*todo.Todo) float64) {
	if len(todos) <= 1 {
		return
	}
	switch mode {
	case taskSortDueDate:
		sortTodoValues(todos, lessByDueDate)
	case taskSortSize:
		// Small first, then Medium, then Large — "sort by Size" is the same
		// intent as "show me the quick wins".
		sortTodoValues(todos, lessBySize)
	default: // taskSortSequence
		rank.SortValues(todos, nil, nil, score)
	}
}

// sortTodoValues is the value-slice form of sortTodoPtrs, for the callers that
// hold a []todo.Todo (the CLI, the on-disk load path).
func sortTodoValues(todos []todo.Todo, less func(a, b *todo.Todo) bool) {
	sort.Slice(todos, func(i, j int) bool { return less(&todos[i], &todos[j]) })
}

// sortHistory orders the completed-tasks list by its own mode, independent of
// the active-task taskSort. Completed mode is most-recent-first (the usual
// "what did I just finish" view); Alpha is case-insensitive title A→Z. Ties
// break by ID for a stable order across cache rebuilds.
// historyLess picks the completed-list comparator: Completed mode is
// most-recent-first (the usual "what did I just finish" view); Alpha is
// case-insensitive title A→Z.
func historyLess(mode historySortMode) func(a, b *todo.Todo) bool {
	if mode == historySortAlpha {
		return lessByTitle
	}
	return lessByCompletedAt
}
