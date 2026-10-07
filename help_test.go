package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// On a terminal too short to show the whole overlay, the last section ("Date
// input") must be hidden at the top of the scroll and revealed once scrolled
// to the bottom — the bug this feature fixes was that it silently vanished.
func TestHelpOverlayScrollRevealsLowerSections(t *testing.T) {
	m := modelWithTasks(t)
	m.mode = modeHelp
	m.termHeight = 14 // deliberately short: body can't fit in the viewport

	body := m.helpBodyLines()
	if len(body) <= m.helpViewportH() {
		t.Fatalf("test needs a body taller than the viewport: %d body vs %d viewport",
			len(body), m.helpViewportH())
	}

	// The final date-input row is the last content line — off-screen at the
	// top, reachable at the bottom.
	const lastRow = "+3d / +2w / +1m"

	atTop := m.renderHelpFullscreen()
	if strings.Contains(atTop, lastRow) {
		t.Fatal("last row should be below the fold at scroll=0")
	}
	if !strings.Contains(atTop, "↓ scroll down") {
		t.Fatal("footer should advertise that more content is below")
	}

	// Scroll to the bottom (clamped) and re-render.
	m.helpScroll = clampHelpScroll(len(body), len(body), m.helpViewportH())
	atBottom := m.renderHelpFullscreen()
	if !strings.Contains(atBottom, lastRow) {
		t.Fatal("last row should be visible after scrolling to the bottom")
	}
	if !strings.Contains(atBottom, "↑ scroll up") {
		t.Fatal("footer should advertise that content is above once at the bottom")
	}
}

// clampHelpScroll never lets the offset leave [0, total-viewport].
func TestClampHelpScrollBounds(t *testing.T) {
	if got := clampHelpScroll(-5, 100, 20); got != 0 {
		t.Fatalf("negative offset should clamp to 0, got %d", got)
	}
	if got := clampHelpScroll(999, 100, 20); got != 80 {
		t.Fatalf("offset past the end should clamp to total-viewport (80), got %d", got)
	}
	if got := clampHelpScroll(5, 10, 20); got != 0 {
		t.Fatalf("body shorter than viewport should pin to 0, got %d", got)
	}
}

// The height floor keeps a usable viewport even on absurdly short terminals.
func TestHelpViewportFloor(t *testing.T) {
	m := modelWithTasks(t)
	m.termHeight = 4
	if got := m.helpViewportH(); got < 3 {
		t.Fatalf("viewport should floor at 3, got %d", got)
	}
}

// The help overlay documents the task-row annotation glyphs so users don't have
// to read source to decode them. Backlog item d33d9e6e.
func TestHelpOverlayListsRowSymbols(t *testing.T) {
	m := modelWithTasks(t)
	body := strings.Join(m.helpBodyLines(), "\n")

	if !strings.Contains(body, "Row symbols") {
		t.Fatal("help body should contain a 'Row symbols' section")
	}
	for _, want := range []string{"⧗", "↥", "↧", "↻", "recurring task", "timer running"} {
		if !strings.Contains(body, want) {
			t.Errorf("Row symbols section missing %q", want)
		}
	}
}

// The overlay lists every binding in the app, which is more than fits on a
// screen — so it filters. A query matches the key, the description or the
// section title, and sections left empty disappear.
func TestHelpFilterNarrowsRows(t *testing.T) {
	m := modelWithTasks(t)
	m.mode = modeHelp

	m.helpFilter = "priority"
	body := strings.Join(m.helpBodyLines(), "\n")
	if !strings.Contains(body, "cycle priority") {
		t.Error("filtering by description dropped the row it describes")
	}
	if strings.Contains(body, "toggle history") {
		t.Error("filtering left an unrelated row in place")
	}

	// By key, not description.
	m.helpFilter = "pgup"
	if body := strings.Join(m.helpBodyLines(), "\n"); !strings.Contains(body, "page through list") {
		t.Error("filtering by key text did not find the binding")
	}

	// By section title, which keeps the whole section — "which keys does the
	// Board have?" is the question that asks for that.
	m.helpFilter = "board"
	body = strings.Join(m.helpBodyLines(), "\n")
	for _, want := range []string{"focus previous/next column", "move card between stages"} {
		if !strings.Contains(body, want) {
			t.Errorf("section-title filter dropped %q", want)
		}
	}

	m.helpFilter = "zzzznope"
	if body := strings.Join(m.helpBodyLines(), "\n"); !strings.Contains(body, "No shortcut matches") {
		t.Errorf("an unmatched filter should say so, got %q", body)
	}
}

// The filter's keys: / starts typing, printable keys go into the query,
// backspace deletes, enter keeps the filter but hands the keys back, and esc
// peels one layer at a time (clear the filter, then close the overlay).
func TestHelpFilterKeyFlow(t *testing.T) {
	m := modelWithTasks(t)
	m = sendKey(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("? did not open the help overlay: mode %v", m.mode)
	}

	m = sendKey(t, m, "/")
	if !m.helpFiltering {
		t.Fatal("/ did not start the filter")
	}
	for _, k := range []string{"d", "o", "n", "e"} {
		m = sendKey(t, m, k)
	}
	if m.helpFilter != "done" {
		t.Fatalf("filter = %q, want %q", m.helpFilter, "done")
	}
	// q is a normal character while typing, not "quit the overlay".
	m = sendKey(t, m, "q")
	if m.mode != modeHelp || m.helpFilter != "doneq" {
		t.Fatalf("q while typing: mode %v filter %q — want it typed, not obeyed", m.mode, m.helpFilter)
	}
	m = sendKey(t, m, "backspace")
	if m.helpFilter != "done" {
		t.Fatalf("backspace left %q", m.helpFilter)
	}

	m = sendKey(t, m, "enter")
	if m.helpFiltering || m.helpFilter != "done" {
		t.Fatalf("enter should stop typing and keep the filter: typing %v filter %q", m.helpFiltering, m.helpFilter)
	}

	m = sendKey(t, m, "esc")
	if m.mode != modeHelp {
		t.Fatal("the first esc should clear the filter, not close the overlay")
	}
	if m.helpFilter != "" {
		t.Fatalf("filter = %q, want it cleared", m.helpFilter)
	}
	m = sendKey(t, m, "esc")
	if m.mode != modeNormal {
		t.Fatal("the second esc should close the overlay")
	}
}

// The token grammars are documented in the overlay, and the documentation has
// to track the parsers. For every token the help advertises, the parser must
// actually recognise it — and vice versa for the ones parseQuickAdd supports.
func TestHelpDocumentsEveryToken(t *testing.T) {
	m := modelWithTasks(t)
	body := strings.Join(m.helpBodyLines(), "\n")
	for _, section := range []string{"Quick-add syntax", "Filters"} {
		if !strings.Contains(body, section) {
			t.Fatalf("help is missing the %q section", section)
		}
	}

	// Quick-add: each documented token, parsed.
	quickAdd := []struct {
		doc  string // the token as the help spells it
		in   string
		want func(parsedTask) bool
	}{
		{"#tag", "x #home", func(p parsedTask) bool { return len(p.tags) == 1 && p.tags[0] == "home" }},
		{"@project", "x @House", func(p parsedTask) bool { return p.project == "House" }},
		{"due:", "x due:tomorrow", func(p parsedTask) bool { return !p.dueDate.IsZero() }},
		{"p:high", "x p:high", func(p parsedTask) bool { return p.hasPriority }},
		{"s:l", "x s:l", func(p parsedTask) bool { return p.hasSize }},
		{"r:weekly", "x r:weekly", func(p parsedTask) bool { return p.recurrence != "" }},
		{"dep:", "x dep:^", func(p parsedTask) bool { return len(p.deps) == 1 }},
	}
	for _, c := range quickAdd {
		if !strings.Contains(body, c.doc) {
			t.Errorf("quick-add token %q is not documented in the help", c.doc)
		}
		if !c.want(parseQuickAdd(c.in)) {
			t.Errorf("the help documents %q but parseQuickAdd(%q) did not accept it", c.doc, c.in)
		}
	}

	// Search: the documented token form, and a query in that form that must
	// actually match. The fuzzy example is here because the README's old one
	// ("grcry") silently did not match — every letter has to appear in order.
	search := []struct{ doc, query string }{
		{"#tag", "#home"},
		{"@project", "@House"},
		{"p:high", "p:high"},
		{"due:<friday", "due:>01-01-20"},
		{"overdue", "overdue"},
		{"grcrs", "grcrs"},
	}
	task := m.get(mustAddTask(t, &m, "Buy groceries #home p:high @House"))
	task.DueDate = time.Now().Add(-24 * time.Hour)
	for _, c := range search {
		if !strings.Contains(body, c.doc) {
			t.Errorf("search token %q is not documented in the help", c.doc)
		}
		if !compileSearch(c.query)(*task) {
			t.Errorf("the help documents search %q but %q does not match the task it should",
				c.doc, c.query)
		}
	}
}

// mustAddTask runs a quick-add string through the parser and store, returning
// the new task's ID.
func mustAddTask(t *testing.T, m *model, line string) string {
	t.Helper()
	p := parseQuickAdd(line)
	task := todo.New(p.title)
	task.Priority = p.priority
	task.Project = p.project
	for _, tag := range p.tags {
		task.AddTag(tag)
	}
	m.add(task)
	m.markCacheDirty()
	m.ensureCache()
	return task.ID
}
