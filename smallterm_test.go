package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// smallterm_test.go is the guard for the class of bug that made tjek crash
// when the terminal got small: a width budget computed from the window size
// (termWidth-6 and its friends) goes negative, and the next rune slice or
// strings.Repeat panics. The fix is clamping in the shared helpers, so the
// guard has to be a render sweep — any new call site inherits the same trap.
//
// The sweep asserts two things at every size: View must not panic, and no line
// may be wider than the window (the no-wrap contract, which the Calendar tab
// broke by joining a fixed-width month grid to a floored timeline).

func smallTermModel(t *testing.T) model {
	t.Helper()
	overdue := todo.New("Fix the boiler in the basement")
	overdue.AddTag("home")
	overdue.AddTag("urgent")
	overdue.Project = "House"
	overdue.DueDate = time.Now().Add(-24 * time.Hour)
	done := todo.New("Write the quarterly memo")
	done.AddTag("work")
	done.Project = "Q3"
	done.Status = todo.Done
	plain := todo.New("Buy filters")
	plain.AddTag("home")
	return modelWithTasks(t, overdue, done, plain)
}

var smallTermTabs = []tab{
	tabTasks, tabCalendar, tabProjects, tabTags, tabBoard, tabStats, tabSettings,
}

// smallTermStates are the on-screen states that add their own geometry on top
// of the tab: an open pane, a modal, a picker, a drill-in.
var smallTermStates = []struct {
	name  string
	apply func(m *model)
}{
	{"plain", func(m *model) {}},
	{"detail", func(m *model) {
		m.pane = paneDetail
		if t := m.currentTodo(); t != nil {
			m.detailTaskID = t.ID
		}
	}},
	{"drill", func(m *model) { m.tagTaskMode, m.projectTaskMode = true, true }},
	{"history", func(m *model) { m.showHistory = true }},
	{"calendar timeline", func(m *model) { m.calendar.focusTimeline = true }},
	{"help", func(m *model) { m.mode = modeHelp }},
	{"why this rank", func(m *model) {
		m.mode = modeExplain
		if t := m.currentTodo(); t != nil {
			m.explainTaskID = t.ID
		}
	}},
	{"quick-add", func(m *model) {
		m.mode = modeInput
		m.textInput.SetValue("buy milk #ho")
		m.textInput.SetCursor(12)
	}},
	{"search", func(m *model) { m.mode = modeSearch; m.searchInput.SetValue("#home p:high") }},
	{"confirm", func(m *model) {
		m.mode = modeConfirm
		m.confirmMsg = "Delete 'Fix the boiler in the basement'? (y/n)"
	}},
	{"stage editor", func(m *model) { m.mode = modeEditStages; m.textInput.SetValue(m.boardCfg.stagesDisplay()) }},
	{"tag picker", func(m *model) { m.mode = modeSearchTag; m.tagSearchInput.SetValue("h") }},
	{"project picker", func(m *model) { m.mode = modeSearchProject; m.projSearchInput.SetValue("h") }},
	{"dep picker", func(m *model) { m.mode = modeSearchDep; m.depSearchInput.SetValue("b") }},
	{"board carry", func(m *model) {
		if m.tab == tabBoard {
			m.startBoardCarry()
			m.board.carryCol = m.boardCfg.doneColumn()
		}
	}},
	{"board card view", func(m *model) {
		if m.tab == tabBoard && m.boardSelectedTask() != nil {
			m.mode = modeBoardCard
		}
	}},
	{"toast", func(m *model) { m.err = "Something went wrong somewhere far away" }},
}

// Sizes span the degenerate end (a terminal reporting 0 during startup, a
// couple of columns mid-drag) up to the widths where the side-by-side layouts
// switch on, since the thresholds are where the arithmetic goes negative.
var smallTermWidths = []int{0, 1, 2, 3, 5, 8, 12, 20, 30, 45, 46, 60, 100}

// noWrapFloor is the narrowest window whose lines must still fit inside it.
const noWrapFloor = 8

var smallTermHeights = []int{0, 1, 2, 3, 5, 10, 24}

func TestSmallTerminalRendersWithoutPanic(t *testing.T) {
	// Render in colour: with the test binary's default profile every style is
	// a no-op, so a truncation that cuts an SGR sequence in half — the bug
	// assertRenderFits checks for — cannot even be expressed.
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	defer func() {
		lipgloss.SetColorProfile(before)
		applyTheme(themes[0])
	}()

	for _, tb := range smallTermTabs {
		for _, st := range smallTermStates {
			for _, w := range smallTermWidths {
				for _, h := range smallTermHeights {
					m := smallTermModel(t)
					m.tab = tb
					m.termWidth, m.termHeight = w, h
					st.apply(&m)
					m.ensureCache()
					assertRenderFits(t, m, fmt.Sprintf("tab %d / %s", tb, st.name), w)
				}
			}
		}
	}
}

// The keys run the same geometry helpers as the render (list windowing, scroll
// clamps), so drive them at the sizes where the budgets are negative too.
func TestSmallTerminalSurvivesKeys(t *testing.T) {
	// The sweep presses keys that persist preferences (s cycles the sort
	// order), and $HOME is shared by the whole test binary — so put
	// settings.json back exactly as it was, file or no file.
	restoreSettingsFile(t)
	// The same sweep presses enter and → on the Settings tab's Language row,
	// which switches the process-wide language, and a later test reading an
	// English hint then fails for no reason of its own — the order -shuffle
	// happened to pick. Put it back.
	lang := activeLang
	t.Cleanup(func() { applyLang(string(lang)) })
	keys := []string{"down", "enter", "right", "left", "d", "t", "p", "a", "esc",
		"end", "pgdown", "s", "h", "f", "/", "?", "T", "m", "r", "x", "tab"}
	for _, tb := range smallTermTabs {
		for _, w := range []int{0, 1, 4, 12, 30} {
			for _, h := range []int{0, 1, 3, 8} {
				m := smallTermModel(t)
				m.tab = tb
				next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m = next.(model)
				for _, k := range keys {
					m = sendKey(t, m, k)
					assertRenderFits(t, m, fmt.Sprintf("tab %d after %q", tb, k), w)
				}
			}
		}
	}
}

// assertRenderFits renders and checks the no-wrap contract. A panic inside
// View surfaces as a test failure naming the size and state, rather than
// taking the whole run down with an anonymous stack.
func assertRenderFits(t *testing.T, m model, what string, w int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s at %dx%d: View panicked: %v", what, m.termWidth, m.termHeight, r)
		}
	}()
	out := m.View()
	// A styled string cut by rune count loses the tail of an SGR sequence, and
	// the terminal then reads the following characters as sequence parameters
	// and never resets the style — the panel border was the visible casualty.
	// ansi.Strip removes complete sequences only, so a leftover ESC is exactly
	// the fingerprint of a truncation that should have gone through
	// truncateStyled. Checked at every size, including the degenerate ones,
	// since that is where the budgets are tightest.
	if stripped := ansi.Strip(out); strings.ContainsRune(stripped, 0x1b) {
		for _, line := range strings.Split(stripped, "\n") {
			if strings.ContainsRune(line, 0x1b) {
				t.Fatalf("%s at %dx%d: truncation cut an escape sequence in half: %q",
					what, m.termWidth, m.termHeight, line)
			}
		}
	}
	// The panels carry a 3-column lead-in (View's left pad plus the panel
	// margin) and two border columns, so below noWrapFloor there is no layout
	// left to shrink — only chrome. The contract is asserted from there up;
	// under it the sweep still guarantees the render does not panic, which is
	// the part that actually bit users.
	if w < noWrapFloor {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		if n := ansi.StringWidth(line); n > w {
			t.Fatalf("%s at %dx%d: line of width %d exceeds the window (%q)",
				what, m.termWidth, m.termHeight, n, ansi.Truncate(line, 60, "…"))
		}
	}
}

// restoreSettingsFile snapshots settings.json and restores it (or removes it
// again, if it did not exist) when the test ends.
func restoreSettingsFile(t *testing.T) {
	t.Helper()
	path := settingsPath()
	before, err := os.ReadFile(path)
	t.Cleanup(func() {
		if err != nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, before, 0o600)
	})
}
