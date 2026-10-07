package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// TestMain (main_test.go) already redirects $HOME to a temp dir, so
// settingsPath() points into a sandbox and these tests can write/read
// settings.json freely without touching the user's real config.

func TestLoadSettingsMissingFileNoError(t *testing.T) {
	// Brand-new install: settings.json doesn't exist yet.
	os.Remove(settingsPath())
	s, err := loadSettings()
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if !reflect.DeepEqual(s, appSettings{}) {
		t.Errorf("expected zero appSettings on missing file, got %+v", s)
	}
}

func TestLoadSettingsCorruptFileErrors(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath(), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	defer os.Remove(settingsPath())

	_, err := loadSettings()
	if err == nil {
		t.Fatal("corrupt JSON should return an error, got nil")
	}
}

// TestLoadSettingsMigratesLegacyVersion0 mirrors the task-file pattern: a
// settings.json written before the Version field existed has Version=0 in
// Go's decoder, and migrateSettings must bring it up to current without
// dropping the user's preferences.
func TestLoadSettingsMigratesLegacyVersion0(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{"theme":"tokyonight","task_sort":1}`
	if err := os.WriteFile(settingsPath(), []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	defer os.Remove(settingsPath())

	s, err := loadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Version != currentSettingsVersion {
		t.Errorf("migrated Version = %d, want %d", s.Version, currentSettingsVersion)
	}
	if s.Theme != "tokyonight" {
		t.Errorf("Theme lost during migration: %q", s.Theme)
	}
	if s.TaskSort != taskSortDueDate {
		t.Errorf("TaskSort lost: %v", s.TaskSort)
	}
}

// v2 moved the Done column into the "stages" list as its last entry. A v1 file
// listed only the working columns, so reading one unchanged would promote its
// last one ("Review") to Done and file every card in it under completed work.
// The migration appends the column the v1 renderer drew — in the language it
// drew it in, since that is the heading the user was looking at.
func TestSettingsMigrationKeepsTheOldLastColumnWorking(t *testing.T) {
	cases := []struct {
		name    string
		version int
		lang    string
		in      []string
		want    []string
	}{
		{"v1 list gains the Done column", 1, "", []string{"Backlog", "In progress", "Review"},
			[]string{"Backlog", "In progress", "Review", "Done"}},
		{"legacy pre-version file too", 0, "", []string{"Todo", "Doing"},
			[]string{"Todo", "Doing", "Done"}},
		{"in the language the column was shown in", 1, "da", []string{"Todo"},
			[]string{"Todo", "Færdige"}},
		{"an unset list still means the defaults", 1, "", nil, nil},
	}
	for _, c := range cases {
		got := migrateSettings(c.version, appSettings{Version: c.version, Language: c.lang, Stages: c.in})
		if !reflect.DeepEqual(got.Stages, c.want) {
			t.Errorf("%s: stages = %v, want %v", c.name, got.Stages, c.want)
		}
		if got.Version != currentSettingsVersion {
			t.Errorf("%s: version = %d, want %d", c.name, got.Version, currentSettingsVersion)
		}
	}

	// End to end: the migrated list still has Review as a working column, so a
	// card sitting there stays pending work rather than joining the done pile.
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.Remove(settingsPath())
	v1 := `{"version":1,"stages":["Backlog","In progress","Review"]}`
	if err := os.WriteFile(settingsPath(), []byte(v1), 0644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	stages := stagesFromSettings(s)
	if want := []string{"Backlog", "In progress", "Review", "Done"}; !reflect.DeepEqual(stages, want) {
		t.Fatalf("migrated board = %v, want %v", stages, want)
	}
	if _, ok := testBoard(stages).canonicalStage("Review"); !ok {
		t.Error("Review stopped being a working column after the migration")
	}

	// A file already on v2 is left alone — migrating twice would append a
	// second Done column every launch.
	twice := migrateSettings(currentSettingsVersion, appSettings{Version: currentSettingsVersion, Stages: []string{"Todo", "Done"}})
	if want := []string{"Todo", "Done"}; !reflect.DeepEqual(twice.Stages, want) {
		t.Errorf("re-migrated stages = %v, want %v", twice.Stages, want)
	}
}

// TestSaveSettingsStampsVersion confirms saveSettings always writes the
// current schema version, even if the caller forgot to set it.
func TestSaveSettingsStampsVersion(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.Remove(settingsPath())

	in := appSettings{Theme: "test", SeqBiasDeadline: rank.Intense}
	if err := saveSettings(in); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Re-read the raw JSON to confirm the version is on disk (not just an
	// in-memory side effect of loadSettings).
	raw, err := os.ReadFile(settingsPath())
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	var onDisk appSettings
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if onDisk.Version != currentSettingsVersion {
		t.Errorf("on-disk version = %d, want %d", onDisk.Version, currentSettingsVersion)
	}
	if onDisk.Theme != "test" || onDisk.SeqBiasDeadline != rank.Intense {
		t.Errorf("payload lost on round-trip: %+v", onDisk)
	}
}

// TestSettingsTopPreviewAppearsInView checks that the bias-knob preview block
// is present when the Settings tab is rendered with at least one pending task.
func TestSettingsTopPreviewAppearsInView(t *testing.T) {
	tasks := []todo.Todo{
		mkTodo("t1", "Alpha task", todo.Pending),
		mkTodo("t2", "Beta task", todo.Pending),
	}
	m := modelWithTasks(t, tasks...)
	m.tab = tabSettings
	m.taskSort = taskSortSequence
	m.ensureCache()

	out := m.renderSettingsList()
	if !strings.Contains(out, "Top 5 with these weights:") {
		t.Errorf("Settings view should contain preview header, got:\n%s", out)
	}
}

// TestSettingsTopPreviewEmptyWhenNoTasks checks that the preview is absent
// (no header, no rows) when there are no pending tasks.
func TestSettingsTopPreviewEmptyWhenNoTasks(t *testing.T) {
	m := modelWithTasks(t)
	m.tab = tabSettings
	m.ensureCache()

	out := m.renderSettingsList()
	if strings.Contains(out, "Top 5 with these weights:") {
		t.Errorf("Settings view should not show preview header when there are no tasks:\n%s", out)
	}
}

// Settings is one pane. It was two, and the four ranking knobs did not earn a
// second border, a second scroll position and half the width of the tab.
func TestSettingsRendersOneGroupedPane(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ranked task"))
	m.tab = tabSettings
	m.termHeight = 40
	m.ensureCache()
	content, _ := m.renderSettingsSection(50)
	content = ansi.Strip(content)
	for _, want := range []string{tr("Appearance"), "Theme", tr("Sequencer"), "Deadline pressure", tr("Sync")} {
		if !strings.Contains(content, want) {
			t.Fatalf("the settings pane is missing %q:\n%s", want, content)
		}
	}
	// The Sequencer rows are a group inside the pane, under their heading —
	// not a separate document beside it.
	if strings.Index(content, tr("Sequencer")) > strings.Index(content, "Deadline pressure") {
		t.Errorf("the Sequencer heading should lead its rows:\n%s", content)
	}

	for _, width := range []int{60, 120} {
		m.termWidth = width
		out := ansi.Strip(m.View())
		if strings.Contains(out, "╭─ "+tr("Sequencer")+" ") {
			t.Errorf("width=%d: Settings should draw one pane, not a Sequencer pane:\n%s", width, out)
		}
		if !strings.Contains(out, "╭─ "+tr("Preferences")+" ") {
			t.Errorf("width=%d: Settings should draw its single pane:\n%s", width, out)
		}
		if !strings.Contains(out, "Deadline pressure") || !strings.Contains(out, "Theme") {
			t.Errorf("width=%d: both groups belong in the one pane:\n%s", width, out)
		}
	}
}

// The pane scrolls to the cursor, and "visible" has to mean visible in what
// the panel actually draws: its chrome is two borders *and* the padding row
// above the first line, so a height that counted only the borders pushed the
// selected row off the bottom on a short terminal.
func TestSettingsPaneKeepsTheSelectedRowOnScreen(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ranked task"))
	m.tab = tabSettings
	m.ensureCache()
	for _, h := range []int{12, 16, 20, 24, 30} {
		m.termWidth, m.termHeight = 70, h
		for _, row := range m.settingsNavOrder() {
			m.settingsCursor = row
			out := ansi.Strip(m.View())
			found := false
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, cursorMark) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("height=%d row=%d: the selected row is not on screen:\n%s", h, row, out)
			}
		}
	}
}

// TestSettingsTopPreviewRespectsWeights verifies that the preview ranks tasks
// in the order the rank.Biases imply: with Priority=Intense and Deadline=Relaxed,
// a high-priority task with no due date should rank above a medium-priority
// task with an imminent due date when the preview is computed directly via
// rank.TopWith.
func TestSettingsTopPreviewRespectsWeights(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	highPri := mkTodo("hp", "High priority, no deadline", todo.Pending)
	highPri.Priority = todo.PriorityHigh
	// No due date — Deadline dimension contributes 0.

	dueUrgent := mkTodo("du", "Medium priority, urgent deadline", todo.Pending)
	dueUrgent.Priority = todo.PriorityMedium
	dueUrgent.DueDate = now.AddDate(0, 0, 1) // due tomorrow (7 days window → ~8.9 urgency)

	todos := []todo.Todo{highPri, dueUrgent}
	heat := rank.Heat{}

	// With Priority=Intense (2×) and Deadline=Relaxed (0.5×):
	// highPri:  Priority(10) × 2.0 = 20 + small age
	// dueUrgent: Priority(5) × 2.0 + Urgency(~8.9) × 0.5 = 10 + ~4.45 ≈ 14.45
	// → highPri should rank first.
	intensePri := rank.Biases{
		Priority: rank.Intense,
		Deadline: rank.Relaxed,
		Momentum: rank.Balanced,
		Aging:    true,
	}
	ranked := rank.TopWith(todoPtrs(todos), intensePri, heat, now)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked tasks, got %d", len(ranked))
	}
	if ranked[0].ID != "hp" {
		t.Errorf("Priority=Intense ranking: expected high-priority task first, got %q (score order: %s, %s)",
			ranked[0].ID,
			fmt.Sprintf("%.2f", rank.ComponentsAt(now, &ranked[0], intensePri, heat).Total),
			fmt.Sprintf("%.2f", rank.ComponentsAt(now, &ranked[1], intensePri, heat).Total),
		)
	}

	// With Deadline=Intense (2×) and Priority=Relaxed (0.5×):
	// highPri:  Priority(10) × 0.5 = 5 + small age
	// dueUrgent: Priority(5) × 0.5 + Urgency(~8.9) × 2.0 = 2.5 + ~17.8 ≈ 20.3
	// → dueUrgent should rank first.
	intenseDeadline := rank.Biases{
		Priority: rank.Relaxed,
		Deadline: rank.Intense,
		Momentum: rank.Balanced,
		Aging:    true,
	}
	ranked2 := rank.TopWith(todoPtrs(todos), intenseDeadline, heat, now)
	if len(ranked2) != 2 {
		t.Fatalf("expected 2 ranked tasks, got %d", len(ranked2))
	}
	if ranked2[0].ID != "du" {
		t.Errorf("Deadline=Intense ranking: expected urgent-deadline task first, got %q (score order: %s, %s)",
			ranked2[0].ID,
			fmt.Sprintf("%.2f", rank.ComponentsAt(now, &ranked2[0], intenseDeadline, heat).Total),
			fmt.Sprintf("%.2f", rank.ComponentsAt(now, &ranked2[1], intenseDeadline, heat).Total),
		)
	}
}

// TestSettingsTopPreviewNoWrap guards the no-wrap contract: the Settings view
// output must not exceed termWidth on any line, even with the preview block.
func TestSettingsTopPreviewNoWrap(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 120} {
		tasks := []todo.Todo{
			mkTodo("t1", "Task one with a rather long name for testing layout", todo.Pending),
			mkTodo("t2", "Task two also somewhat verbose", todo.Pending),
			mkTodo("t3", "Task three", todo.Pending),
		}
		m := modelWithTasks(t, tasks...)
		m.tab = tabSettings
		m.termWidth = width
		m.termHeight = 40
		m.ensureCache()

		out := m.View()
		for n, line := range strings.Split(out, "\n") {
			if lw := ansi.StringWidth(line); lw > width {
				t.Errorf("width=%d: line %d is %d cells wide: %q", width, n, lw, line)
			}
		}
	}
}

// The sync footer wraps instead of being cut, and wrapping must not break the
// pane's no-wrap contract: a long server error is exactly the string that
// would otherwise run past the border.
func TestSettingsFooterWrapsTheSyncStatus(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ranked task"))
	m.tab = tabSettings
	m.termHeight = 40
	m.ensureCache()
	m.syncStatus = "Last sync failed: sync server runs tjek v1.25.0, this device runs v1.33.1 — restart the sync server (it answered 500 Internal Server Error: merge failed: no such table: task_learnings)"

	const paneW = 50
	preferences, _ := m.renderSettingsSection(paneW)
	preferences = ansi.Strip(preferences)

	// Only the footer's own lines: this function deliberately leaves the final
	// per-line width contract to the pane builder, so the settings rows above
	// are not this test's business.
	all := strings.Split(strings.TrimRight(preferences, "\n"), "\n")
	start := -1
	for i, line := range all {
		if strings.Contains(line, "Last sync failed:") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no sync status in the footer:\n%s", preferences)
	}
	footer := all[start:]
	if len(footer) < 2 {
		t.Errorf("the message should have wrapped, got one line: %q", footer[0])
	}
	if len(footer) > syncStatusMaxLines {
		t.Errorf("footer runs %d lines, over the %d-line cap", len(footer), syncStatusMaxLines)
	}
	for _, line := range footer {
		if w := ansi.StringWidth(line); w > paneW {
			t.Errorf("footer line is %d wide, over the %d-column pane: %q", w, paneW, line)
		}
	}
	// Read back across the wrap: the part a fixed-width truncation used to eat.
	flat := strings.Join(strings.Fields(strings.Join(footer, " ")), " ")
	if !strings.Contains(flat, "restart the sync server") {
		t.Errorf("footer lost the actionable half of the message:\n%s", preferences)
	}
}

// clampLines is the cap that keeps a pathological server message from pushing
// the settings rows off the pane, and a capped block has to be tellable from
// one that simply ended.
func TestClampLinesMarksTheCut(t *testing.T) {
	lines := []string{"one", "two", "three", "four"}
	got := clampLines(lines, 2)
	if len(got) != 2 {
		t.Fatalf("clampLines returned %d lines, want 2", len(got))
	}
	if !strings.HasSuffix(got[1], ellipsis) {
		t.Errorf("last line %q should carry the ellipsis", got[1])
	}
	if same := clampLines(lines, 9); len(same) != len(lines) || strings.HasSuffix(same[3], ellipsis) {
		t.Errorf("an uncut block must be returned untouched, got %q", same)
	}
	if clampLines(lines, 0) != nil {
		t.Error("clampLines with no room should return nothing")
	}
}

// The pane is one border with two columns of groups inside it — not one tall
// column that runs off the bottom of a laptop screen, which is what collapsing
// the old second pane into the first one first produced.
func TestSettingsPaneSplitsIntoTwoColumns(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ranked task"))
	m.tab = tabSettings
	m.termHeight = 40
	m.ensureCache()

	wide, _ := m.renderSettingsSection(settingsTwoColMinWidth + 20)
	paired := false
	for _, line := range strings.Split(ansi.Strip(wide), "\n") {
		if strings.Contains(line, "Theme") && strings.Contains(line, tr("Automatic")) {
			paired = true
		}
	}
	if !paired {
		t.Errorf("a wide pane should put Appearance and Sync side by side:\n%s", ansi.Strip(wide))
	}

	// Too narrow for two columns: the groups stack instead of being clipped
	// into each other.
	narrow, _ := m.renderSettingsSection(settingsTwoColMinWidth - 1)
	for _, line := range strings.Split(ansi.Strip(narrow), "\n") {
		if strings.Contains(line, "Theme") && strings.Contains(line, tr("Automatic")) {
			t.Errorf("a narrow pane should stack the groups:\n%s", ansi.Strip(narrow))
		}
	}
}

// The scroll is driven by the line number the renderer reports, so it has to
// hold for a cursor in the right-hand column too — there the row is drawn
// after the left column's text, not at the start of the line.
func TestSettingsTwoColumnPaneReportsTheCursorLine(t *testing.T) {
	m := settingsModel(t)
	const w = settingsTwoColMinWidth + 20
	for _, g := range settingsGroups {
		for _, row := range g.rows {
			if !settingsSelectable(row) || !m.settingsRowVisible(row) {
				continue
			}
			m.settingsCursor = row
			content, selected := m.renderSettingsSection(w)
			lines := strings.Split(strings.TrimRight(ansi.Strip(content), "\n"), "\n")
			drawn := -1
			for i, line := range lines {
				if strings.Contains(line, strings.TrimSpace(cursorMark)) {
					drawn = i
					break
				}
			}
			if drawn < 0 {
				t.Fatalf("row %d: no cursor mark in the rendered pane:\n%s", row, ansi.Strip(content))
			}
			if selected != drawn {
				t.Errorf("row %d: selected line = %d, drawn on line %d:\n%s", row, selected, drawn, ansi.Strip(content))
			}
		}
	}
}

// A value too long for its row is clipped before the ⏎ is added, so the row
// still says that enter edits it — the list of board columns is the one that
// runs long.
func TestSettingsEditMarkSurvivesAClippedValue(t *testing.T) {
	m := modelWithTasks(t, todo.New("Ranked task"))
	m.tab = tabSettings
	m.termHeight = 40
	m.boardCfg.setStages([]string{"Backlog", "Waiting on someone else", "In progress", "In review with the team", "Done"})
	m.ensureCache()
	for _, w := range []int{50, settingsTwoColMinWidth + 20} {
		content, _ := m.renderSettingsSection(w)
		found := false
		for _, line := range strings.Split(content, "\n") {
			plain := ansi.Strip(line)
			if !strings.Contains(plain, tr("Board columns")) {
				continue
			}
			found = true
			if !strings.Contains(plain, "…") || !strings.Contains(plain, "⏎") {
				t.Errorf("width %d: the clipped row should end in … and keep its ⏎: %q", w, plain)
			}
			if cw := ansi.StringWidth(line); cw > w {
				t.Errorf("width %d: the row is %d cells wide", w, cw)
			}
		}
		if !found {
			t.Fatalf("width %d: no Board columns row in:\n%s", w, ansi.Strip(content))
		}
	}
}
