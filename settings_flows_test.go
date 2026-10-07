package main

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/x/ansi"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Scripted flows for the Settings tab: the four inline sync editors and the
// preference toggles.
//
// These are modals, and ARCHITECTURE.md asks for a flow per modal for the usual
// reason — each one has to write config, reset the mode, and leave the shared
// text input in a state the *next* modal can use. The sync pair carries an
// extra obligation the others do not: it holds the bearer token, which
// SECURITY.md names as the only secret tjek stores. So the masking and the
// file mode are pinned here, not left to inspection.

// settingsModel builds a model on the Settings tab with a private HOME, so the
// editors write a real sync.json that the assertions can stat.
func settingsModel(t *testing.T) model {
	t.Helper()
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	m := modelWithTasks(t)
	m.tab = tabSettings
	return m
}

// openSetting puts the settings cursor on a row and activates it.
func openSetting(t *testing.T, m model, row int) model {
	t.Helper()
	m.settingsCursor = row
	return sendKey(t, m, "enter")
}

// ── The sync server URL editor ──────────────────────────────────────────────

func TestScriptEditSyncURL(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingSyncServer)
	if m.mode != modeEditSyncURL {
		t.Fatalf("mode = %v, want modeEditSyncURL", m.mode)
	}
	m = script(t, m, "https://sync.example.com", "enter")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v, want modeNormal", m.mode)
	}
	if m.syncCfg.URL != "https://sync.example.com" {
		t.Errorf("URL = %q", m.syncCfg.URL)
	}
	// It has to reach disk, not just the model — the whole point of the
	// editor is that the setting survives a restart.
	if got := loadSyncConfigFile().URL; got != "https://sync.example.com" {
		t.Errorf("sync.json URL = %q, want it persisted", got)
	}
}

func TestScriptEditSyncURLBlankIsADeliberateClear(t *testing.T) {
	m := settingsModel(t)
	m.syncCfg.URL = "https://old.example.com"
	m = openSetting(t, m, settingSyncServer)
	// The field arrives pre-filled, so emptying it is an instruction, not an
	// accident — treating blank as "no change" would make the URL
	// unremovable from the UI.
	m.textInput.SetValue("")
	m = sendKey(t, m, "enter")
	if m.syncCfg.URL != "" {
		t.Errorf("URL = %q, want it cleared", m.syncCfg.URL)
	}
}

func TestScriptEditSyncURLEscapeDiscards(t *testing.T) {
	m := settingsModel(t)
	m.syncCfg.URL = "https://keep.example.com"
	m = openSetting(t, m, settingSyncServer)
	m = script(t, m, "typed-but-abandoned", "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v, want modeNormal", m.mode)
	}
	if m.syncCfg.URL != "https://keep.example.com" {
		t.Errorf("URL = %q, want it unchanged after esc", m.syncCfg.URL)
	}
}

func TestScriptEditSyncURLWarnsOnPlainHTTPToAPublicHost(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingSyncServer)
	m = script(t, m, "http://sync.example.com", "enter")
	if !strings.Contains(m.syncStatus, "unencrypted") {
		t.Errorf("syncStatus = %q, want a warning that the token travels in the clear", m.syncStatus)
	}

	// Loopback is the normal local-hub setup and must not nag — the token
	// never leaves the machine. This also covers the harder half: the
	// warning has to be *cleared* when the URL stops being insecure, not
	// just set when it starts. A stale security notice is worse than none.
	m = openSetting(t, m, settingSyncServer)
	m.textInput.SetValue("")
	m = script(t, m, "http://127.0.0.1:8765", "enter")
	if strings.Contains(m.syncStatus, "unencrypted") {
		t.Errorf("loopback should not warn, got %q", m.syncStatus)
	}
}

func TestScriptEditSyncURLLeavesOtherStatusesAlone(t *testing.T) {
	// Clearing the warning must not clear whatever else put a message
	// there — a sync result is not this editor's to erase.
	m := settingsModel(t)
	m.syncStatus = "Last sync: sent 3, received 1"
	m = openSetting(t, m, settingSyncServer)
	m = script(t, m, "https://sync.example.com", "enter")
	if m.syncStatus != "Last sync: sent 3, received 1" {
		t.Errorf("syncStatus = %q, want the unrelated message preserved", m.syncStatus)
	}
}

// ── The token editors ───────────────────────────────────────────────────────

func TestScriptEditSyncTokenMasksAndUnmasks(t *testing.T) {
	m := settingsModel(t)
	m.syncCfg.Token = "existing-secret"
	m = openSetting(t, m, settingSyncToken)
	if m.mode != modeEditSyncToken {
		t.Fatalf("mode = %v, want modeEditSyncToken", m.mode)
	}
	// The pre-filled secret must not be echoed back in plaintext.
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Error("the token editor is not masked")
	}

	m = script(t, m, "", "enter")
	// And the mask has to come back off: the text input is shared, so a
	// leftover EchoPassword would render the *next* modal — quick-add, a
	// rename — as bullets.
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Error("the shared input stayed masked after the token editor closed")
	}
}

func TestScriptEditSyncTokenEscapeAlsoUnmasks(t *testing.T) {
	// The path most likely to be forgotten, and the one a user hits most:
	// open the token row by accident, press esc.
	m := settingsModel(t)
	m = openSetting(t, m, settingSyncToken)
	m = sendKey(t, m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v, want modeNormal", m.mode)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Error("escaping the token editor left the shared input masked")
	}
}

func TestScriptSyncTokenIsStoredPrivately(t *testing.T) {
	m := settingsModel(t)
	m = openSetting(t, m, settingSyncToken)
	m = script(t, m, "super-secret-token", "enter")

	if m.syncCfg.Token != "super-secret-token" {
		t.Fatalf("token = %q", m.syncCfg.Token)
	}
	info, err := os.Stat(syncConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	// SECURITY.md states this file is 0600. It is the only secret tjek
	// stores, so the mode is a documented property, not an accident of
	// whichever call happened to create the file. Windows has no POSIX mode
	// bits — Go reports 0666 for every file it creates there, and the file is
	// protected by the ACL on the user's profile instead — so the assertion
	// runs where the mode is real.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("sync.json mode = %v, want 0600", got)
		}
	}
}

func TestScriptEditServerListenAndToken(t *testing.T) {
	m := settingsModel(t)

	m = openSetting(t, m, settingServerListen)
	if m.mode != modeEditServerListen {
		t.Fatalf("mode = %v, want modeEditServerListen", m.mode)
	}
	m.textInput.SetValue("")
	m = script(t, m, "127.0.0.1:9999", "enter")
	if m.syncCfg.ServerListen != "127.0.0.1:9999" {
		t.Errorf("ServerListen = %q", m.syncCfg.ServerListen)
	}

	m = openSetting(t, m, settingServerToken)
	if m.mode != modeEditServerToken {
		t.Fatalf("mode = %v, want modeEditServerToken", m.mode)
	}
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Error("the server-token editor is not masked")
	}
	// A real token: the server-token editor refuses weak ones outright (see
	// TestServerTokenEditorRefusesWeakTokens), so a toy value here would be
	// testing the rejection path by accident.
	const serverToken = "kR7-server-side-secret-9fQ2xL"
	m = script(t, m, serverToken, "enter")
	if m.syncCfg.ServerToken != serverToken {
		t.Errorf("ServerToken = %q", m.syncCfg.ServerToken)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Error("the shared input stayed masked after the server-token editor")
	}
	if got := loadSyncConfigFile(); got.ServerListen != "127.0.0.1:9999" || got.ServerToken != serverToken {
		t.Errorf("server config did not persist: %+v", got)
	}
}

// ── Preference toggles ──────────────────────────────────────────────────────

func TestToggleAutoClosePreferencesPersist(t *testing.T) {
	m := settingsModel(t)

	before := m.autoCloseParent
	m.toggleAutoCloseParent()
	if m.autoCloseParent == before {
		t.Fatal("toggleAutoCloseParent did not flip the preference")
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.AutoCloseParent != m.autoCloseParent {
		t.Errorf("settings.json AutoCloseParent = %v, want %v", got.AutoCloseParent, m.autoCloseParent)
	}

	beforeSubs := m.autoCloseSubtasks
	m.toggleAutoCloseSubtasks()
	if m.autoCloseSubtasks == beforeSubs {
		t.Fatal("toggleAutoCloseSubtasks did not flip the preference")
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.AutoCloseSubtasks != m.autoCloseSubtasks {
		t.Errorf("settings.json AutoCloseSubtasks = %v", got.AutoCloseSubtasks)
	}
}

func TestToggleAgingFlipsAndPersists(t *testing.T) {
	m := settingsModel(t)
	before := m.rank.Biases.Aging
	m.toggleAging()

	if m.rank.Biases.Aging == before {
		t.Fatal("toggleAging did not flip the bias")
	}
	// Aging changes the ranking, so the derived caches must be invalidated
	// or the list keeps showing the old order until something else dirties
	// them.
	if !m.cache.dirty {
		t.Error("toggleAging left the derived caches warm")
	}
	// Stored as the inverse, so the zero value means "aging on" — which is
	// exactly the kind of inversion a test should pin rather than trust.
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.SeqAgingDisabled == m.rank.Biases.Aging {
		t.Errorf("settings.json SeqAgingDisabled = %v with Aging = %v; they must be inverses",
			got.SeqAgingDisabled, m.rank.Biases.Aging)
	}
}

func TestCycleThemeWrapsAndPersists(t *testing.T) {
	m := settingsModel(t)
	start := m.themeName

	m.cycleTheme(1)
	if m.themeName == start {
		t.Fatal("cycleTheme did not advance")
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.Theme != m.themeName {
		t.Errorf("settings.json Theme = %q, want %q", got.Theme, m.themeName)
	}

	// Stepping back must land where it started — an off-by-one in the
	// modulo shows up as a theme you cannot get back to.
	m.cycleTheme(-1)
	if m.themeName != start {
		t.Errorf("theme = %q after +1/-1, want %q", m.themeName, start)
	}

	// A full lap returns to the start rather than running off the end.
	for range themes {
		m.cycleTheme(1)
	}
	if m.themeName != start {
		t.Errorf("theme = %q after a full lap, want %q", m.themeName, start)
	}
}

func TestCycleLangVisitsEveryLanguageAndWraps(t *testing.T) {
	m := settingsModel(t)
	defer applyLang(string(langEN))

	applyLang(string(langEN))
	seen := map[language]bool{activeLang: true}
	for range availableLanguages {
		m.cycleLang(1)
		seen[activeLang] = true
	}
	// Every shipped language has to be reachable from the Settings toggle;
	// a language in the table that the cycle skips is one nobody can select.
	for _, want := range availableLanguages {
		if !seen[want] {
			t.Errorf("cycleLang never reached %q", want)
		}
	}
	// A full lap wraps back to English.
	if activeLang != langEN {
		t.Errorf("after a full lap activeLang = %q, want en", activeLang)
	}

	m.cycleLang(1)
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.Language != string(activeLang) {
		t.Errorf("settings.json Language = %q, want %q", got.Language, activeLang)
	}
	// Switching language must re-place the input placeholders, or the
	// prompts stay in the previous language until a restart.
	if m.searchInput.Placeholder != searchHint() {
		t.Errorf("placeholder = %q, not refreshed for %q", m.searchInput.Placeholder, activeLang)
	}
}

// ── Grouped Preferences pane ────────────────────────────────────────────────

// The cursor walks the whole list without stopping on Version. Enter on that
// row did nothing, so a stop there was a keypress that looked broken; the skip
// lives in settingsNavOrder so every caller inherits it.
func TestSettingsCursorSkipsTheVersionRow(t *testing.T) {
	m := settingsModel(t)
	m.settingsCursor = settingAutoCloseParent
	seen := map[int]bool{m.settingsCursor: true}
	for i := 0; i < numSettingsRows*2; i++ {
		m = sendKey(t, m, "down")
		if m.settingsCursor == settingVersion {
			t.Fatalf("the cursor stopped on the Version row after %d presses", i+1)
		}
		seen[m.settingsCursor] = true
	}
	for _, row := range []int{settingCheckUpdate, settingAging, settingServerOn} {
		if !seen[row] {
			t.Errorf("row %d is unreachable going down", row)
		}
	}
	// And the same on the way back up.
	for i := 0; i < numSettingsRows*2; i++ {
		m = sendKey(t, m, "up")
		if m.settingsCursor == settingVersion {
			t.Fatalf("the cursor stopped on the Version row going up, after %d presses", i+1)
		}
	}
}

// The selected line is what scrolls the pane on a short terminal, and the pane
// does not draw one line per row — the headings, the blank line between groups
// and the bias preview sit in between. The renderer hands that number back
// with the content; hunt the cursor mark in what it drew and compare, the way
// the detail pane's estimate is pinned to its document.
func TestSettingsSelectedLineMatchesTheRenderedPane(t *testing.T) {
	m := settingsModel(t)
	for _, g := range settingsGroups {
		for _, row := range g.rows {
			if !settingsSelectable(row) || !m.settingsRowVisible(row) {
				continue
			}
			m.settingsCursor = row
			content, selected := m.renderSettingsSection(60)
			lines := strings.Split(strings.TrimRight(ansi.Strip(content), "\n"), "\n")
			drawn := -1
			for i, line := range lines {
				if strings.HasPrefix(line, strings.TrimSpace(cursorMark)) || strings.HasPrefix(line, cursorMark) {
					drawn = i
					break
				}
			}
			if drawn < 0 {
				t.Fatalf("row %d: no cursor mark in the rendered pane:\n%s", row, content)
			}
			if selected != drawn {
				t.Errorf("row %d: selected line = %d, drawn on line %d", row, selected, drawn)
			}
		}
	}
	// A row the current state hides is on no line at all.
	m.settingsCursor = settingServerToken
	if _, selected := m.renderSettingsSection(60); selected != -1 {
		t.Errorf("a hidden row should report no line, got %d", selected)
	}
}

// A row whose enter opens a text editor is marked, and a row that cycles on
// ←/→ is not — the two used to look identical, so which key a row answered was
// only discoverable by pressing one.
func TestSettingsMarksTheRowsThatOpenAnEditor(t *testing.T) {
	m := settingsModel(t)
	content, _ := m.renderSettingsSection(60)
	for _, line := range strings.Split(ansi.Strip(content), "\n") {
		marked := strings.Contains(line, strings.TrimSpace(settingsEditMark))
		cycled := strings.Contains(line, "‹")
		if marked && cycled {
			t.Errorf("a row cannot both cycle and open an editor: %q", line)
		}
	}
	for _, tc := range []struct {
		row  int
		want bool
	}{
		{settingSyncServer, true},
		{settingServerListen, true},
		{settingStages, true},
		{settingTheme, false},
		{settingSyncNow, false},
		{settingVersion, false},
	} {
		if got := settingsEditsText(tc.row); got != tc.want {
			t.Errorf("settingsEditsText(%d) = %v, want %v", tc.row, got, tc.want)
		}
	}
}

// → and enter are one table now. They were two hand-kept chains, and a toggle
// that answered one key but not the other is what that cost.
func TestSettingsEnterAndRightAgreeOnEveryToggleRow(t *testing.T) {
	// A few of these rows write package-level globals; put them back so the
	// rest of the suite sees the state it started with.
	base := settingsModel(t)
	lang, themeBefore := activeLang, base.themeName
	t.Cleanup(func() {
		applyLang(string(lang))
		applyTheme(themeByName(themeBefore))
	})

	for _, row := range []int{
		settingAutoCloseParent, settingAutoCloseSubtasks, settingShowBoard,
		settingTheme, settingLanguage, settingAging,
		settingBiasDeadline, settingBiasPriority, settingBiasMomentum,
	} {
		byEnter := settingsModel(t)
		byEnter.settingsCursor = row
		byEnter = sendKey(t, byEnter, "enter")

		byArrow := settingsModel(t)
		byArrow.settingsCursor = row
		byArrow = sendKey(t, byArrow, "right")

		enterPane, _ := byEnter.renderSettingsSection(60)
		arrowPane, _ := byArrow.renderSettingsSection(60)
		if enterPane != arrowPane {
			t.Errorf("row %d: enter and → left different values:\nenter:\n%s\nright:\n%s",
				row, enterPane, arrowPane)
		}
	}
}

// Theme and Language open the pane. They are the settings someone changes on
// day one, and they sat six rows down behind the auto-close toggles.
func TestSettingsOpensWithAppearance(t *testing.T) {
	first := settingsGroups[0]
	// Appearance may grow more rows; what is pinned is that it leads the pane
	// and opens on Theme, which is where the cursor starts.
	if len(first.rows) < 2 || first.rows[0] != settingTheme || first.rows[1] != settingLanguage {
		t.Fatalf("the first settings group is %q %v, want Theme then Language", first.title, first.rows)
	}
	m := settingsModel(t)
	m.settingsCursor = settingTheme
	content, selected := m.renderSettingsSection(60)
	if selected != 1 {
		t.Errorf("Theme renders on pane line %d, want 1 (the line under the first heading)", selected)
	}
	// And the pane's first heading is the one the cursor starts under, not a
	// group the reader has to scroll past.
	lines := strings.Split(ansi.Strip(content), "\n")
	if !strings.Contains(lines[0], tr("Appearance")) {
		t.Errorf("first pane line is %q, want the Appearance heading", lines[0])
	}
}

// ── The persisted search filter ─────────────────────────────────────────────

// A committed `/` filter is a view preference like the sort modes: it has to
// reach settings.json when it settles, come back on the next start with the
// esc that clears it wired up, and be gone from disk once cleared.
func TestSearchFilterPersistsAndRestores(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))

	m = script(t, m, "/", "rent", "enter")
	if m.searchQuery != "rent" {
		t.Fatalf("setup: searchQuery = %q", m.searchQuery)
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.Search != "rent" {
		t.Errorf("settings.json Search = %q, want %q", got.Search, "rent")
	}

	// The restart: same HOME, a fresh model off the same settings file.
	restarted := initialModel(&fakeRepo{todos: []todo.Todo{todo.New("pay rent"), todo.New("water plants")}})
	if restarted.searchQuery != "rent" {
		t.Fatalf("restored searchQuery = %q, want %q", restarted.searchQuery, "rent")
	}
	restarted.termWidth, restarted.termHeight = 120, 40
	restarted.ensureCache()
	if n := len(restarted.cache.active); n != 1 {
		t.Errorf("restored filter matched %d tasks, want 1", n)
	}

	// esc clears it on screen — and on disk, or the next start would bring
	// back a filter the user just dismissed.
	restarted = sendKey(t, restarted, "esc")
	if restarted.searchQuery != "" {
		t.Fatalf("esc left searchQuery = %q", restarted.searchQuery)
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.Search != "" {
		t.Errorf("settings.json Search = %q after esc, want empty", got.Search)
	}
}

// The query is shared by Tasks, Board and Stats but stored per tab, so a
// search typed on another tab must not overwrite the Tasks-tab filter that
// startup restores.
func TestPersistedSearchIsTheTasksTabQuery(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))

	m = script(t, m, "/", "rent", "enter")
	m.switchTab(tabBoard)
	m = script(t, m, "/", "plants", "enter")
	if m.searchQuery != "plants" {
		t.Fatalf("board search = %q", m.searchQuery)
	}
	if got, err := loadSettings(); err != nil {
		t.Fatal(err)
	} else if got.Search != "rent" {
		t.Errorf("settings.json Search = %q, want the Tasks-tab query %q", got.Search, "rent")
	}
}

// ── The detail pane placement ───────────────────────────────────────────────

// The row cycles right → left → bottom and back, and the choice has to be on
// disk before the next start reads it: a layout that resets every launch is
// the setting not working at all.
func TestDetailPositionCyclesAndPersists(t *testing.T) {
	m := settingsModel(t)
	if m.detailPos != detailRight {
		t.Fatalf("default detailPos = %v, want right", m.detailPos)
	}

	m.settingsCursor = settingDetailPos
	for _, want := range []detailPos{detailLeft, detailBottom, detailRight} {
		m = sendKey(t, m, "right")
		if m.detailPos != want {
			t.Fatalf("→ gave %v, want %v", m.detailPos, want)
		}
		if got, err := loadSettings(); err != nil {
			t.Fatal(err)
		} else if got.DetailPosition != want.String() {
			t.Errorf("settings.json detail_position = %q, want %q", got.DetailPosition, want.String())
		}
	}

	// ← walks back the other way, and enter means the same as → (settingsAdjust).
	m = sendKey(t, m, "left")
	if m.detailPos != detailBottom {
		t.Fatalf("← gave %v, want bottom", m.detailPos)
	}
	m = sendKey(t, m, "enter")
	if m.detailPos != detailRight {
		t.Fatalf("enter gave %v, want right", m.detailPos)
	}

	// The restart: same HOME, the placement read back off the file.
	m = sendKey(t, m, "left")
	restarted := initialModel(&fakeRepo{})
	if restarted.detailPos != detailBottom {
		t.Errorf("restored detailPos = %v, want bottom", restarted.detailPos)
	}
}

// settings.json is hand-editable, so the words are the API — and a word it
// does not know costs the setting, not the start.
func TestDetailPositionReadsTheSettingsWord(t *testing.T) {
	for word, want := range map[string]detailPos{
		"right": detailRight, "left": detailLeft, "bottom": detailBottom,
		"Bottom": detailBottom, " left ": detailLeft,
		"": detailRight, "sideways": detailRight,
	} {
		if got := detailPosFromSettings(word); got != want {
			t.Errorf("detailPosFromSettings(%q) = %v, want %v", word, got, want)
		}
	}
}
