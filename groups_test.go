package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

func TestFormatSince(t *testing.T) {
	applyLang(string(langEN))
	now := time.Date(2026, 9, 24, 15, 0, 0, 0, time.Local)
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-time.Hour), "today"},
		{now.AddDate(0, 0, -1), "1d"},
		{now.AddDate(0, 0, -13), "13d"},
		{now.AddDate(0, 0, -14), "2w"},
		{now.AddDate(0, 0, -69), "9w"},
		{now.AddDate(0, 0, -95), "3mo"},
	}
	for _, c := range cases {
		if got := formatSince(c.at, now); got != c.want {
			t.Errorf("formatSince(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

// One pass builds every group's counts. An untagged subtask belongs to its
// parent and so to no tag group, and next-up is the best-scored open task.
func TestSummarizeGroups(t *testing.T) {
	now := time.Now()
	parent := mkTodo("p", "parent", todo.Pending)
	parent.Tags = []string{"home"}
	child := mkTodo("c", "child", todo.Pending)
	child.ParentID = "p"
	late := mkTodo("l", "late", todo.Pending)
	late.Tags = []string{"home"}
	late.DueDate = now.AddDate(0, 0, -2)
	done := mkTodo("d", "done", todo.Done)
	done.Tags = []string{"home"}
	bare := mkTodo("b", "bare", todo.Pending)
	all := todoPtrs([]todo.Todo{parent, child, late, done, bare})

	score := map[string]float64{"p": 1, "l": 5, "c": 9, "b": 2}
	sums := summarizeGroups(all, tagGroupKeys, func(t *todo.Todo) float64 { return score[t.ID] })

	home := sums["home"]
	if home.open != 2 || home.overdue != 1 || home.done != 1 {
		t.Errorf("home = %d open, %d overdue, %d done; want 2, 1, 1", home.open, home.overdue, home.done)
	}
	if home.nextID != "l" {
		t.Errorf("home next = %q, want the best-scored open task l", home.nextID)
	}
	if u := sums[untaggedKey]; u == nil || u.open != 1 || u.nextID != "b" {
		t.Errorf("untagged = %+v, want only the top-level bare task", u)
	}
}

func TestVisibleGroupsHidesFinishedButKeepsThePinnedOne(t *testing.T) {
	now := time.Now()
	sums := map[string]*groupSummary{
		"busy":      {open: 3, last: now.Add(-time.Hour)},
		"quiet":     {open: 1, last: now},
		"finished":  {done: 4, last: now},
		untaggedKey: {open: 1},
	}
	all := func(string) bool { return true }

	if got, want := visibleGroups(sums, groupSortOpen, false, "", all), []string{"busy", "quiet", untaggedKey}; !reflect.DeepEqual(got, want) {
		t.Errorf("by open work = %v, want %v", got, want)
	}
	if got, want := visibleGroups(sums, groupSortRecent, false, "", all), []string{"quiet", "busy", untaggedKey}; !reflect.DeepEqual(got, want) {
		t.Errorf("by recent = %v, want %v", got, want)
	}
	if got, want := visibleGroups(sums, groupSortName, true, "", all), []string{untaggedKey, "busy", "finished", "quiet"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by name, all shown = %v, want %v", got, want)
	}
	if got := visibleGroups(sums, groupSortName, false, "finished", all); !reflect.DeepEqual(got, []string{untaggedKey, "busy", "finished", "quiet"}) {
		t.Errorf("the pinned group must stay listed, got %v", got)
	}
	if n := hiddenFinishedGroups(sums, false, all); n != 1 {
		t.Errorf("hidden = %d, want 1", n)
	}
}

func TestHasDatedOpenTask(t *testing.T) {
	undated := mkTodo("a", "a", todo.Pending)
	doneDated := mkTodo("b", "b", todo.Done)
	doneDated.DueDate = time.Now()
	if hasDatedOpenTask([]todo.Todo{undated, doneDated}) {
		t.Error("only a done task has a date; the timeline has nothing ahead to draw")
	}
	started := mkTodo("c", "c", todo.Pending)
	started.StartDate = time.Now()
	if !hasDatedOpenTask([]todo.Todo{undated, started}) {
		t.Error("an open task with a start date belongs on a timeline")
	}
}

// The pane under a list shows how much of the group is done as a small bar,
// groupBarWidth cells whatever the window, followed by the percentage.
func TestGroupPaneShowsAProgressBar(t *testing.T) {
	applyLang(string(langEN))
	for _, width := range []int{60, 120, 200} {
		m := newTagModel()
		m.termWidth = width
		head := m.groupPaneHead(&groupSummary{open: 1, done: 3}, width-8)
		if len(head) != 2 {
			t.Fatalf("width %d: pane head = %q, want counts then bar", width, head)
		}
		bar := ansi.Strip(head[1])
		if !strings.HasSuffix(bar, " 75%") {
			t.Errorf("width %d: bar line = %q, want it to end in the percentage", width, bar)
		}
		if got := ansi.StringWidth(bar); got != 2+groupBarWidth+len("  75%") {
			t.Errorf("width %d: bar line is %d cells, want a fixed %d-cell bar", width, got, groupBarWidth)
		}
	}
	if head := newTagModel().groupPaneHead(&groupSummary{}, 80); len(head) != 1 {
		t.Errorf("an empty group has no bar to draw, got %q", head)
	}
}
