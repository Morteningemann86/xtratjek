package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// keys_vim_test.go covers the j/k aliases and the class of bug that hid their
// absence: a key the interface advertises that dispatch never answers. The
// registry is generated into the footer and the help overlay, so anything
// listed there is a promise — and nothing was checking that the promise was
// kept by a handler.

// vimModel is a model with enough tasks, tags and projects that every list has
// at least two rows to move between.
func vimModel(t *testing.T) model {
	t.Helper()
	var tasks []todo.Todo
	for _, spec := range []struct {
		title, tag, project string
	}{
		{"Fix the boiler", "home", "House"},
		{"Write the memo", "work", "Q3"},
		{"Buy filters", "home", "House"},
		{"Call the plumber", "work", "House"},
	} {
		task := todo.New(spec.title)
		task.AddTag(spec.tag)
		task.Project = spec.project
		tasks = append(tasks, task)
	}
	return modelWithTasks(t, tasks...)
}

// vimContexts are the on-screen states with a cursor to move, each named by the
// context its keys resolve to.
var vimContexts = []struct {
	name  string
	setup func(m *model)
}{
	{"tasks list", func(m *model) { m.switchTab(tabTasks) }},
	{"tasks detail", func(m *model) {
		m.switchTab(tabTasks)
		m.pane = paneDetail
		if t := m.currentTodo(); t != nil {
			m.detailTaskID = t.ID
		}
	}},
	{"tags", func(m *model) { m.switchTab(tabTags) }},
	{"tag drill", func(m *model) { m.switchTab(tabTags); m.tagTaskMode = true }},
	{"projects", func(m *model) { m.switchTab(tabProjects) }},
	{"project drill", func(m *model) { m.switchTab(tabProjects); m.projectTaskMode = true }},
	{"board", func(m *model) { m.switchTab(tabBoard) }},
	{"calendar", func(m *model) { m.switchTab(tabCalendar) }},
	{"settings", func(m *model) { m.switchTab(tabSettings) }},
}

// cursorFingerprint captures every cursor the contexts above move, so the test
// can compare "what j did" against "what ↓ did" without knowing which field a
// given tab happens to use.
func cursorFingerprint(m model) [6]int {
	return [6]int{m.cursor, m.tagTabCursor, m.projectCursor, m.settingsCursor,
		m.board.col*1000 + m.board.cursor, m.calendar.selected.Day()}
}

// j and k must land exactly where ↓ and ↑ land — in every context that has a
// cursor. An alias that works on the Tasks tab and nowhere else is the bug this
// pins, since that is what a per-switch implementation would have produced.
func TestVimKeysMatchTheArrowsEverywhere(t *testing.T) {
	for _, c := range vimContexts {
		for _, pair := range []struct{ vim, arrow string }{{"j", "down"}, {"k", "up"}} {
			withVim := vimModel(t)
			c.setup(&withVim)
			withArrow := vimModel(t)
			c.setup(&withArrow)

			withVim = sendKey(t, withVim, pair.vim)
			withArrow = sendKey(t, withArrow, pair.arrow)

			got, want := cursorFingerprint(withVim), cursorFingerprint(withArrow)
			if got != want {
				t.Errorf("%s: %q left cursors %v, %q left %v", c.name, pair.vim, got, pair.arrow, want)
			}
		}
	}
}

// The alias must actually move something, or the test above passes on two
// models that both did nothing.
func TestVimKeysActuallyMoveTheCursor(t *testing.T) {
	m := vimModel(t)
	m.switchTab(tabTasks)
	before := m.cursor
	m = sendKey(t, m, "j")
	if m.cursor == before {
		t.Fatalf("j did not move the Tasks cursor (still %d)", before)
	}
	m = sendKey(t, m, "k")
	if m.cursor != before {
		t.Errorf("k did not undo j: cursor %d, want %d", m.cursor, before)
	}
}

// A rebind onto j or k wins over the alias: the override pass runs first, so
// the user's binding is not shadowed by a vim convention they overrode.
func TestVimKeysYieldToARebind(t *testing.T) {
	defer applyKeys(nil)
	m := vimModel(t)
	m.switchTab(tabTasks)
	// applyKeys after the model: initialModel re-applies settings.json's
	// overrides, which would wipe one set before it.
	applyKeys(map[string]string{"done": "j"})
	task := m.currentTodo()
	if task == nil {
		t.Fatal("no task under the cursor")
	}
	before := m.cursor

	m = sendKey(t, m, "j")
	if m.cursor != before {
		t.Errorf("j moved the cursor despite being rebound to done")
	}
	if got := m.get(task.ID); got.Status != todo.Done {
		t.Errorf("j was rebound to done but the task is %v", got.Status)
	}
}

// j/k scroll the help overlay, which runs outside modeNormal and so never sees
// the dispatch alias.
func TestVimKeysScrollTheHelpOverlay(t *testing.T) {
	m := vimModel(t)
	m.termHeight = 12 // short enough that the body overflows and can scroll
	m.mode = modeHelp
	m = sendKey(t, m, "j")
	if m.helpScroll == 0 {
		t.Fatal("j did not scroll the help overlay")
	}
	down := m.helpScroll
	m = sendKey(t, m, "k")
	if m.helpScroll >= down {
		t.Errorf("k did not scroll back up: %d, want < %d", m.helpScroll, down)
	}
}

// ── The class guard ───────────────────────────────────────────────────────────

// Every single-key binding the registry advertises must be answered by a case
// clause in one of the dispatch switches. The registry generates the footer
// hints, the help overlay and the palette, so an entry no handler cases on is
// an interface that lies about itself — and nothing was checking.
//
// This is a source scan rather than a keypress sweep on purpose. What a key
// *does* is already driven end to end by update_keyscript_test.go; the gap this
// closes is narrower and structural — advertised, but not wired at all. Proving
// that behaviourally would need a bespoke probe state per key (a subtask under
// the cursor for the detail keys, a non-today day for the calendar's t), which
// is a table that would rot faster than the thing it guards.
func TestEveryAdvertisedKeyHasAHandler(t *testing.T) {
	handled := handledKeys(t)
	for i := range keymap {
		bd := &keymap[i]
		if !paletteSendable(bd.key) {
			continue // a pair or a range: several keys, no single case to find
		}
		if !handled[bd.key] {
			t.Errorf("the registry advertises %q (%s) but no dispatch switch cases on it",
				bd.key, bd.desc)
		}
	}
}

// caseKeys matches the string literals of a `case "a", "b":` clause.
var caseKeys = regexp.MustCompile(`(?m)^\s*case\s+("(?:[^"\\]|\\.)*"(?:\s*,\s*"(?:[^"\\]|\\.)*")*)\s*:`)
var caseLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// handledKeys collects every key string the Update path cases on.
func handledKeys(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	sources, err := filepath.Glob("update*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	sources = append(sources, "keys.go")
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, clause := range caseKeys.FindAllStringSubmatch(string(data), -1) {
			for _, lit := range caseLiteral.FindAllStringSubmatch(clause[1], -1) {
				out[lit[1]] = true
			}
		}
	}
	if len(out) < 20 {
		t.Fatalf("found only %d cased keys — the scan is broken, not the keymap", len(out))
	}
	return out
}
