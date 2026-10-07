package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── pickerWindowStart unit tests ──────────────────────────────────────────────

func TestPickerWindowStartNoScroll(t *testing.T) {
	// Fewer results than the window: no indicators, start=0.
	for _, total := range []int{0, 1, 3, 5} {
		start, hasAbove, hasBelow := pickerWindowStart(0, total, 5)
		if start != 0 || hasAbove || hasBelow {
			t.Errorf("total=%d cursor=0: got start=%d above=%v below=%v, want 0 false false",
				total, start, hasAbove, hasBelow)
		}
	}
}

func TestPickerWindowStartCursorAlwaysVisible(t *testing.T) {
	// For every (total, cursor, max) triple the cursor must land on a result
	// slot — not the above-indicator (slot 0 when hasAbove) or the
	// below-indicator (slot max-1 when hasBelow).
	max := 5
	for total := 0; total <= 12; total++ {
		for cursor := 0; cursor < total; cursor++ {
			start, hasAbove, hasBelow := pickerWindowStart(cursor, total, max)

			// cursor must be in the window.
			if cursor < start || cursor >= start+max {
				t.Errorf("total=%d cursor=%d: start=%d → cursor outside [start, start+max)",
					total, cursor, start)
				continue
			}

			slot := cursor - start

			if hasAbove && slot == 0 {
				t.Errorf("total=%d cursor=%d: start=%d hasAbove — cursor is on the above-indicator slot",
					total, cursor, start)
			}
			if hasBelow && slot == max-1 {
				t.Errorf("total=%d cursor=%d: start=%d hasBelow — cursor is on the below-indicator slot",
					total, cursor, start)
			}
		}
	}
}

func TestPickerWindowStartIndicatorsCorrect(t *testing.T) {
	max := 5
	cases := []struct {
		total, cursor        int
		wantAbove, wantBelow bool
	}{
		// Exactly fits: no indicators.
		{5, 0, false, false},
		{5, 4, false, false},
		// Below only (cursor near start, items trail off the bottom).
		{7, 0, false, true},
		{7, 1, false, true},
		// Both (cursor in the middle of a long list).
		{10, 5, true, true},
		{10, 6, true, true},
		// Above only (cursor near end).
		{10, 9, true, false},
		{10, 8, true, false},
	}
	for _, c := range cases {
		_, hasAbove, hasBelow := pickerWindowStart(c.cursor, c.total, max)
		if hasAbove != c.wantAbove || hasBelow != c.wantBelow {
			t.Errorf("total=%d cursor=%d: hasAbove=%v hasBelow=%v, want %v %v",
				c.total, c.cursor, hasAbove, hasBelow, c.wantAbove, c.wantBelow)
		}
	}
}

// ── Tag picker integration tests ──────────────────────────────────────────────

// makeTagPickerModel builds a model in modeSearchTag with tasks that carry
// enough distinct tags to overflow the 5-row picker. The cursor is set to
// the given cursor position.
func makeTagPickerModel(t *testing.T, tagNames []string, cursor int) model {
	t.Helper()
	// Create one task per tag so all tags appear in the picker.
	tasks := make([]todo.Todo, len(tagNames))
	for i, tag := range tagNames {
		tasks[i] = todo.New(fmt.Sprintf("Task %d", i))
		tasks[i].AddTag(tag)
	}
	// One extra task to be the "current" task we're adding a tag to.
	target := todo.New("Current task")
	tasks = append(tasks, target)

	m := modelWithTasks(t, tasks...)
	// Set cursor on the target task (last added — ensureCache/refreshCaches
	// may reorder by score, so find it by ID).
	m.ensureCache()
	for i, td := range m.cache.active {
		if td.ID == target.ID {
			m.cursor = i
			break
		}
	}
	m.mode = modeSearchTag
	m.tagSearch = searchState{query: "", cursor: cursor}
	return m
}

// buildFooterForTagSearch renders only the footer section of View so we can
// inspect the picker rows without the full TUI frame.
func buildFooterForTagSearch(m model) string {
	w := m.termWidth - 6
	return m.buildFooterContent(w)
}

func TestTagPickerCursorBeyond5IsVisible(t *testing.T) {
	// 8 distinct tags → picker has 8 rows, cursor at row 6 (0-based).
	// The selected row must appear in the rendered footer, marked the way
	// every list in the app marks the row you are on.
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	const cursor = 6 // 7th tag: "eta"

	m := makeTagPickerModel(t, tags, cursor)
	results := m.tagSearchResults()
	if len(results) < cursor+1 {
		t.Fatalf("expected at least %d tag results, got %d", cursor+1, len(results))
	}
	selectedTag := results[cursor]

	out := buildFooterForTagSearch(m)
	if !strings.Contains(out, cursorMark+"#"+selectedTag) {
		t.Errorf("cursor=%d: selected tag %q not visible in picker output:\n%s",
			cursor, selectedTag, out)
	}
}

func TestTagPickerHeightConstant(t *testing.T) {
	// The rendered footer height must be identical for every cursor position:
	// scrolling the picker must not change the total number of output lines.
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}

	// Establish the baseline line count using cursor=0.
	baselineOut := buildFooterForTagSearch(makeTagPickerModel(t, tags, 0))
	baselineLines := len(strings.Split(baselineOut, "\n"))

	for cursor := 1; cursor < len(tags); cursor++ {
		out := buildFooterForTagSearch(makeTagPickerModel(t, tags, cursor))
		got := len(strings.Split(out, "\n"))
		if got != baselineLines {
			t.Errorf("cursor=%d: output has %d lines, baseline (cursor=0) has %d:\n%s",
				cursor, got, baselineLines, out)
		}
	}
}

func TestTagPickerBelowIndicatorAppearsAndDisappears(t *testing.T) {
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

	// cursor=0: window is [0..4], item 5 is below → indicator must appear.
	m0 := makeTagPickerModel(t, tags, 0)
	out0 := buildFooterForTagSearch(m0)
	if !strings.Contains(out0, "more below") {
		t.Errorf("cursor=0: expected 'more below' indicator:\n%s", out0)
	}

	// cursor=5: all 6 items, cursor is the last one → indicator must disappear
	// (or appear for above) but 'more below' must NOT appear.
	m5 := makeTagPickerModel(t, tags, 5)
	out5 := buildFooterForTagSearch(m5)
	if strings.Contains(out5, "more below") {
		t.Errorf("cursor=5 (last): unexpected 'more below' indicator:\n%s", out5)
	}
}

func TestTagPickerAboveIndicatorAppearsWhenScrolled(t *testing.T) {
	// With 8 tags and cursor at 7 (last), the window must scroll so items are
	// above the viewport — the "more above" indicator must appear.
	tags := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	m := makeTagPickerModel(t, tags, 7)
	out := buildFooterForTagSearch(m)
	if !strings.Contains(out, "more above") {
		t.Errorf("cursor=7 (last of 8): expected 'more above' indicator:\n%s", out)
	}
}

func TestTagPickerCreateNewRowUnchanged(t *testing.T) {
	// When there are no matching tags and a query is typed, the picker must
	// show the "create new tag:" row (unchanged behavior).
	target := todo.New("Current task")
	m := modelWithTasks(t, target)
	m.ensureCache()
	m.mode = modeSearchTag
	m.tagSearch = searchState{query: "xyzzy", cursor: 0}
	// tagSearchResults with no matching tags returns empty → create-new row.
	results := m.tagSearchResults()
	if len(results) != 0 {
		t.Skipf("expected 0 results for query 'xyzzy', got %d — adjust test setup", len(results))
	}

	out := buildFooterForTagSearch(m)
	if !strings.Contains(out, "create new tag:") {
		t.Errorf("empty results with query: expected 'create new tag:' in picker:\n%s", out)
	}
	if strings.Contains(out, "more below") || strings.Contains(out, "more above") {
		t.Errorf("create-new-tag row must not show scroll indicators:\n%s", out)
	}
}

// ── Project picker integration tests ─────────────────────────────────────────

func makeProjectPickerModel(t *testing.T, projectNames []string, cursor int) model {
	t.Helper()
	tasks := make([]todo.Todo, len(projectNames))
	for i, p := range projectNames {
		tasks[i] = todo.New(fmt.Sprintf("Task for %s", p))
		tasks[i].Project = p
	}
	target := todo.New("Current task")
	tasks = append(tasks, target)

	m := modelWithTasks(t, tasks...)
	m.ensureCache()
	for i, td := range m.cache.active {
		if td.ID == target.ID {
			m.cursor = i
			break
		}
	}
	m.mode = modeSearchProject
	m.projSearch = searchState{query: "", cursor: cursor}
	return m
}

func buildFooterForProjSearch(m model) string {
	w := m.termWidth - 6
	return m.buildFooterContent(w)
}

func TestProjectPickerCursorBeyond5IsVisible(t *testing.T) {
	projects := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"}
	const cursor = 5 // "zeta"

	m := makeProjectPickerModel(t, projects, cursor)
	results := m.projSearchResults()
	if len(results) < cursor+1 {
		t.Fatalf("expected at least %d project results, got %d", cursor+1, len(results))
	}
	selectedProj := results[cursor]

	out := buildFooterForProjSearch(m)
	if !strings.Contains(out, cursorMark+selectedProj) {
		t.Errorf("cursor=%d: selected project %q not visible in picker:\n%s",
			cursor, selectedProj, out)
	}
}

func TestProjectPickerHeightConstant(t *testing.T) {
	// The rendered footer height must be identical for every cursor position.
	projects := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"}

	baselineOut := buildFooterForProjSearch(makeProjectPickerModel(t, projects, 0))
	baselineLines := len(strings.Split(baselineOut, "\n"))

	for cursor := 1; cursor < len(projects); cursor++ {
		out := buildFooterForProjSearch(makeProjectPickerModel(t, projects, cursor))
		got := len(strings.Split(out, "\n"))
		if got != baselineLines {
			t.Errorf("cursor=%d: output has %d lines, baseline (cursor=0) has %d:\n%s",
				cursor, got, baselineLines, out)
		}
	}
}

func TestProjectPickerCreateNewRowUnchanged(t *testing.T) {
	target := todo.New("Current task")
	m := modelWithTasks(t, target)
	m.ensureCache()
	m.mode = modeSearchProject
	m.projSearch = searchState{query: "newproject", cursor: 0}

	results := m.projSearchResults()
	if len(results) != 0 {
		t.Skipf("expected 0 results for 'newproject', got %d", len(results))
	}

	out := buildFooterForProjSearch(m)
	if !strings.Contains(out, "create new project:") {
		t.Errorf("expected 'create new project:' in picker:\n%s", out)
	}
}
