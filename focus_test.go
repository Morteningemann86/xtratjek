package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// focus_test.go covers the unified focus/esc stack: esc always backs out of
// the most recently entered state on the current tab, replacing the old
// hardcoded filter→search→history priority order.

// Search then history: esc must pop history (entered last) and keep the
// search. The pre-stack code popped the search first regardless of order.
func TestEscPopsMostRecentState_SearchThenHistory(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))

	m = script(t, m, "/", "rent", "enter", "h")
	if m.searchQuery != "rent" || !m.showHistory {
		t.Fatalf("setup: searchQuery=%q showHistory=%v", m.searchQuery, m.showHistory)
	}

	m = sendKey(t, m, "esc")
	if m.showHistory {
		t.Error("first esc should close history (entered last)")
	}
	if m.searchQuery != "rent" {
		t.Errorf("first esc should keep the search, got %q", m.searchQuery)
	}

	m = sendKey(t, m, "esc")
	if m.searchQuery != "" {
		t.Errorf("second esc should clear the search, got %q", m.searchQuery)
	}
}

// History then search: the same two states in the opposite order must pop in
// the opposite order.
func TestEscPopsMostRecentState_HistoryThenSearch(t *testing.T) {
	done := todo.New("shipped thing")
	done.Toggle()
	m := modelWithTasks(t, todo.New("pay rent"), done)

	m = script(t, m, "h", "/", "shipped", "enter")
	if !m.showHistory || m.searchQuery != "shipped" {
		t.Fatalf("setup: showHistory=%v searchQuery=%q", m.showHistory, m.searchQuery)
	}

	m = sendKey(t, m, "esc")
	if m.searchQuery != "" {
		t.Errorf("first esc should clear the search (entered last), got %q", m.searchQuery)
	}
	if !m.showHistory {
		t.Error("first esc should keep history open")
	}

	m = sendKey(t, m, "esc")
	if m.showHistory {
		t.Error("second esc should close history")
	}
}

// Esc only pops states belonging to the current tab: a Projects drill-in esc
// must not eat the search left behind on Tasks.
func TestEscOnlyPopsCurrentTabState(t *testing.T) {
	withProj := todo.New("build the shed")
	withProj.Project = "garden"
	m := modelWithTasks(t, withProj, todo.New("pay rent"))

	m = script(t, m, "/", "rent", "enter", "4", "enter")
	if m.tab != tabProjects || !m.projectTaskMode {
		t.Fatalf("setup: tab=%v projectTaskMode=%v", m.tab, m.projectTaskMode)
	}

	m = sendKey(t, m, "esc")
	if m.projectTaskMode {
		t.Error("esc on Projects should exit the drill-in")
	}

	m = sendKey(t, m, "1")
	if m.searchQuery != "rent" {
		t.Fatalf("search should survive the Projects detour, got %q", m.searchQuery)
	}
	m = sendKey(t, m, "esc")
	if m.searchQuery != "" {
		t.Errorf("esc back on Tasks should clear the search, got %q", m.searchQuery)
	}
}

// A state exited through its own toggle key must not leave a stack entry
// that eats a later esc.
func TestToggledOffStateDoesNotEatEsc(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))

	m = script(t, m, "h", "h", "/", "rent", "enter")
	m = sendKey(t, m, "esc")
	if m.searchQuery != "" {
		t.Errorf("esc should clear the search, not a stale history entry; got %q", m.searchQuery)
	}
}

// Entering the detail pane on top of a search: esc leaves the pane first and
// keeps the filter, then a second esc clears it.
func TestDetailEscKeepsSearch(t *testing.T) {
	m := modelWithTasks(t, todo.New("pay rent"), todo.New("water plants"))

	m = script(t, m, "/", "rent", "enter", "enter")
	if m.pane != paneDetail {
		t.Fatalf("setup: pane=%v, want paneDetail", m.pane)
	}

	m = sendKey(t, m, "esc")
	if m.pane != paneList {
		t.Error("first esc should leave the detail pane")
	}
	if m.searchQuery != "rent" {
		t.Errorf("first esc should keep the search, got %q", m.searchQuery)
	}

	m = sendKey(t, m, "esc")
	if m.searchQuery != "" {
		t.Errorf("second esc should clear the search, got %q", m.searchQuery)
	}
}

// Calendar timeline focus participates in the same stack.
func TestCalendarTimelineEsc(t *testing.T) {
	tracked := todo.New("tracked work")
	// Anchor the entry to noon so it stays on today's calendar day no matter
	// when the test runs — Now()-1h crosses midnight and lands on yesterday.
	y, mo, d := time.Now().Date()
	noon := time.Date(y, mo, d, 12, 0, 0, 0, time.Local)
	tracked.TimeEntries = []todo.TimeEntry{{
		StartedAt: noon,
		StoppedAt: noon.Add(30 * time.Minute),
	}}
	m := modelWithTasks(t, tracked)

	m = script(t, m, "2", "enter")
	if !m.calendar.focusTimeline {
		t.Fatal("enter on a day with activity should focus the timeline")
	}
	m = sendKey(t, m, "esc")
	if m.calendar.focusTimeline {
		t.Error("esc should return focus to the calendar grid")
	}
}
