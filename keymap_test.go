package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// A canonical action must use the same key in every context it appears in —
// delete is always x, edit is always r, sort is always s. This is the
// cross-page control-consistency contract: bind an existing action to a new
// key on some tab and this fails, forcing the divergence to be deliberate.
func TestKeymapActionsAreConsistent(t *testing.T) {
	keysFor := map[string]map[string]bool{}
	for _, b := range keymap {
		if keysFor[b.action] == nil {
			keysFor[b.action] = map[string]bool{}
		}
		keysFor[b.action][b.key] = true
	}
	for action, keys := range keysFor {
		if len(keys) > 1 {
			var ks []string
			for k := range keys {
				ks = append(ks, k)
			}
			t.Errorf("action %q is bound to multiple keys %v — one action must use one key everywhere", action, ks)
		}
	}
}

// No two actions may share a key within the same context (a real collision the
// user would experience), which is what forced h off the vim-left slot.
func TestKeymapNoIntraContextCollision(t *testing.T) {
	ctxs := []keyCtx{
		ctxTasksList, ctxTasksDetail, ctxProjects, ctxTags, ctxBoard,
		ctxStats, ctxCalendar, ctxCalendarTimeline, ctxSettings,
		ctxTagDrill, ctxProjectDrill,
	}
	for _, ctx := range ctxs {
		seen := map[string]string{} // key -> action
		for _, b := range keymap {
			if b.ctx&ctx == 0 {
				continue
			}
			if prev, ok := seen[b.key]; ok && prev != b.action {
				t.Errorf("ctx %d: key %q maps to both %q and %q", ctx, b.key, prev, b.action)
			}
			seen[b.key] = b.action
		}
	}
}

// Regression guard for the exact drift this registry was built to kill: T
// (manual time entry) and m (merge tags) both dispatch but were missing from
// the help. Since help is generated from the registry, asserting they're
// registered guarantees they show up.
func TestKeymapCoversPreviouslyMissingKeys(t *testing.T) {
	has := func(key, action string) bool {
		for _, b := range keymap {
			if b.key == key && b.action == action {
				return true
			}
		}
		return false
	}
	if !has("T", "timeentry") {
		t.Error("T (manual time entry) missing from the keymap registry")
	}
	if !has("m", "merge") {
		t.Error("m (merge tags) missing from the keymap registry")
	}
}

// Every context must produce a non-empty footer hint, and every registered
// section must render into the help overlay — the two generated surfaces.
func TestKeymapGeneratesHintsAndHelp(t *testing.T) {
	ctxs := map[string]keyCtx{
		"tasks": ctxTasksList, "detail": ctxTasksDetail, "projects": ctxProjects,
		"tags": ctxTags, "board": ctxBoard, "calendar": ctxCalendar,
		"calendarTimeline": ctxCalendarTimeline, "settings": ctxSettings,
		"tagDrill": ctxTagDrill, "projectDrill": ctxProjectDrill,
	}
	for name, ctx := range ctxs {
		if hintString(ctx, false, nil) == "" {
			t.Errorf("ctx %s produced an empty footer hint", name)
		}
	}
	// Stats has a single binding; assert it at least appears.
	if !strings.Contains(hintString(ctxStats, false, nil), "cycle activity range") {
		t.Error("stats hint should mention cycling the activity range")
	}
}

// The tabs binding advertises the digit shortcuts as a range. It used to
// promise "1-8" against seven tabs, so assert the advertised range, numTabs,
// and the digits dispatch actually accepts all agree.
func TestKeymapDigitRangeMatchesTabCount(t *testing.T) {
	var key string
	for _, b := range keymap {
		if b.action == "tabs" {
			key = b.key
		}
	}
	if want := fmt.Sprintf("1-%d", numTabs); !strings.Contains(key, want) {
		t.Errorf("tabs binding key = %q, want it to advertise %q", key, want)
	}
	if !strings.Contains(key, "shift+tab") {
		t.Errorf("tabs binding key = %q, want it to advertise shift+tab", key)
	}
	for d := 1; d <= numTabs; d++ {
		if _, ok := paletteModel().tabForNumberKey(strconv.Itoa(d)); !ok {
			t.Errorf("digit %d is advertised but not handled by tabForNumberKey", d)
		}
	}
	if _, ok := paletteModel().tabForNumberKey(strconv.Itoa(numTabs + 1)); ok {
		t.Errorf("digit %d is handled but there are only %d tabs", numTabs+1, numTabs)
	}
}

// The jump/page keys only drive tabs with a linear list (listNavTarget). The
// registry claimed them globally, so the help promised them on Calendar,
// Stats, Settings and Board where they do nothing. Assert the claim tracks
// dispatch in both directions.
func TestKeymapListpageClaimedOnlyWhereItWorks(t *testing.T) {
	var claimed keyCtx
	for _, b := range keymap {
		if b.action == "listpage" {
			claimed = b.ctx
		}
	}
	for _, c := range []struct {
		name string
		ctx  keyCtx
		tab  tab
	}{
		{"tasks", ctxTasksList, tabTasks},
		{"projects", ctxProjects, tabProjects},
		{"tags", ctxTags, tabTags},
		{"board", ctxBoard, tabBoard},
		{"stats", ctxStats, tabStats},
		{"calendar", ctxCalendar, tabCalendar},
		{"settings", ctxSettings, tabSettings},
	} {
		m := modelWithTasks(t, todo.New("a task"))
		m.tab = c.tab
		cursor, _ := m.listNavTarget()
		works, advertised := cursor != nil, claimed&c.ctx != 0
		if works != advertised {
			t.Errorf("%s: home/end·pgup/pgdn advertised = %v but dispatch handles it = %v",
				c.name, advertised, works)
		}
	}
}

// ↑/↓ is registered globally, which is only honest because every context but
// Stats moves a cursor. Guard the exception so re-adding a Stats cursor (or
// dropping one elsewhere) forces the registry to be updated with it.
func TestKeymapNavigateSkipsStats(t *testing.T) {
	for _, b := range keymap {
		if b.action == "navigate" && b.section == secNavigation && b.ctx&ctxStats != 0 {
			t.Error("navigate is advertised on the Stats tab, which has no list cursor")
		}
	}
	m := modelWithTasks(t, todo.New("a task"))
	m.tab = tabStats
	before := m.cursor
	m.moveCursorDown()
	if m.cursor != before {
		t.Errorf("Stats grew a cursor (%d → %d) — re-register ↑/↓ for ctxStats", before, m.cursor)
	}
}

// The drill-in contexts are only honest if the keys they advertise reach a
// task. Assert the live context follows the drill state and that the row-level
// keys dispatch there, both of which the registry now promises.
func TestKeymapDrillContextsMatchState(t *testing.T) {
	tagged := todo.New("Alpha")
	tagged.AddTag("home")
	m := modelWithTasks(t, tagged)

	m.tab = tabTags
	if got := m.currentKeyCtx(); got != ctxTags {
		t.Errorf("tag list ctx = %d, want ctxTags", got)
	}
	m.tagTaskMode = true
	if got := m.currentKeyCtx(); got != ctxTagDrill {
		t.Errorf("drilled-in ctx = %d, want ctxTagDrill", got)
	}
	if m.currentTodo() == nil {
		t.Error("the drill context is live but no task is under the cursor")
	}
	cursor, n := m.listNavTarget()
	if cursor == nil || n == 0 {
		t.Error("home/end·pgup/pgdn is advertised in the drill but has no list to move")
	}

	m = modelWithTasks(t, todo.New("Alpha"))
	m.tab = tabProjects
	m.projectTaskMode = true
	if got := m.currentKeyCtx(); got != ctxProjectDrill {
		t.Errorf("project drill ctx = %d, want ctxProjectDrill", got)
	}
}
