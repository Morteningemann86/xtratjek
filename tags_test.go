package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// newTagModel returns a model with a known, hermetic set of todos (initialModel
// loads from disk, so we replace its tasks wholesale). initialModel also applies
// the developer's stored language; pin English so these tests, which assert
// English labels, are deterministic regardless of ~/.tjek/settings.json.
func newTagModel(todos ...todo.Todo) model {
	m := newTestModel()
	applyLang(string(langEN))
	for _, td := range todos {
		m.add(td)
	}
	m.termWidth = 80
	m.termHeight = 30
	m.refreshCaches()
	return m
}

func TestRenameTagMerges(t *testing.T) {
	task := todo.New("merge")
	task.Tags = []string{"a", "b"}
	m := newTagModel(task)

	m.renameTagGlobally("a", "b")

	got := m.get(task.ID).Tags
	if len(got) != 1 || got[0] != "b" {
		t.Errorf("after merge rename, tags = %v, want [b]", got)
	}
}

func TestRenameTagMergesMixedCase(t *testing.T) {
	// Legacy data: a capitalized tag stored before normalization, alongside
	// the lowercase form. Merging the two must collapse to one.
	capped := todo.New("capped")
	capped.Tags = []string{"Work"}
	lower := todo.New("lower")
	lower.Tags = []string{"work"}
	m := newTagModel(capped, lower)

	m.renameTagGlobally("Work", "work")

	if got := m.get(capped.ID).Tags; len(got) != 1 || got[0] != "work" {
		t.Errorf("capped task tags = %v, want [work]", got)
	}
	if got := m.get(lower.ID).Tags; len(got) != 1 || got[0] != "work" {
		t.Errorf("lower task tags = %v, want [work]", got)
	}
}

func TestRenameTagNonColliding(t *testing.T) {
	task := todo.New("rename")
	task.Tags = []string{"a", "b"}
	m := newTagModel(task)

	m.renameTagGlobally("a", "C") // also exercises normalization

	got := m.get(task.ID).Tags
	if len(got) != 2 {
		t.Fatalf("tags = %v, want 2 tags", got)
	}
	has := map[string]bool{}
	for _, tg := range got {
		has[tg] = true
	}
	if !has["b"] || !has["c"] {
		t.Errorf("tags = %v, want b and normalized c", got)
	}
}

func TestUntaggedRowAndFilter(t *testing.T) {
	tagged := todo.New("tagged")
	tagged.Tags = []string{"x"}
	m := newTagModel(tagged, todo.New("bare one"), todo.New("bare two"))

	if g := m.cache.tagGroups[untaggedKey]; g == nil || g.open != 2 {
		t.Fatalf("untagged group = %+v, want 2 open", g)
	}

	tags := m.getFilteredTagsForTab()
	if len(tags) == 0 || tags[0] != untaggedKey {
		t.Fatalf("expected untagged row first, got %v", tags)
	}

	m.searchQuery = untaggedKey
	matched := 0
	for _, td := range m.tasks {
		if m.matchesSearch(*td) {
			matched++
		}
	}
	if matched != 2 {
		t.Errorf("untagged filter matched %d tasks, want 2", matched)
	}
}

func TestUntaggedRowHiddenWhenAllTagged(t *testing.T) {
	tagged := todo.New("tagged")
	tagged.Tags = []string{"x"}
	m := newTagModel(tagged)

	for _, tg := range m.getFilteredTagsForTab() {
		if tg == untaggedKey {
			t.Fatal("untagged row should not appear when no tasks are untagged")
		}
	}
}

// TestTagDetailCapsAndOrders verifies the detail pane stays within its height
// cap (no opaque overflow), puts the most urgent open task first, and keeps
// done tasks out of the list until finished work is shown.
func TestTagDetailCapsAndOrders(t *testing.T) {
	var todos []todo.Todo
	for i := 0; i < 30; i++ {
		todos = append(todos, todo.New(fmt.Sprintf("active-%02d", i)))
	}
	late := todo.New("alpha-overdue")
	late.DueDate = time.Now().AddDate(0, 0, -3)
	finished := todo.New("finished-one")
	finished.Status = todo.Done
	todos = append(todos, late, finished)

	m := newTagModel(todos...)
	m.tab = tabTags
	m.termHeight = 16
	m.refreshCaches()
	m.tagTabCursor = 0 // the untagged row

	content := strings.TrimRight(strings.Join(m.buildTagDetailLines(), "\n"), "\n")
	got := len(strings.Split(content, "\n"))
	// The pane's own share of the height, which View windows it to: a line
	// past it is cut behind a scroll marker, and the notice with it.
	if _, paneLines := m.tagStackRows(); got > paneLines {
		t.Errorf("detail produced %d lines, exceeds the pane's %d:\n%s", got, paneLines, content)
	}
	if !strings.Contains(content, "more") {
		t.Errorf("expected an 'and N more' notice, got:\n%s", content)
	}
	if !strings.Contains(content, "Alpha-overdue") {
		t.Errorf("the overdue task ranks first and should be shown, missing from:\n%s", content)
	}
	if strings.Contains(content, "Finished-one") {
		t.Errorf("done tasks stay folded until h, but one is listed:\n%s", content)
	}
	if !strings.Contains(content, "1 done") {
		t.Errorf("the counts line should still count the done task:\n%s", content)
	}
}

// A row says how much is open in the tag and what to do next in it; a tag with
// nothing open is left out until finished work is shown.
func TestTagRowSaysWhatIsOpen(t *testing.T) {
	low := todo.New("routine chore")
	low.Tags = []string{"home"}
	high := todo.New("fix the boiler")
	high.Tags = []string{"home"}
	high.Priority = todo.PriorityHigh
	old := todo.New("paint the fence")
	old.Tags = []string{"home"}
	old.Status = todo.Done
	gone := todo.New("archived thing")
	gone.Tags = []string{"attic"}
	gone.Status = todo.Done

	m := newTagModel(low, high, old, gone)
	m.termWidth = 120
	m.tab = tabTags
	m.refreshCaches()

	out := ansi.Strip(m.renderTagList())
	for _, want := range []string{"Open", "Next up", "#home", "Fix the boiler"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in tag list, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#attic") {
		t.Errorf("a finished tag should be hidden by default:\n%s", out)
	}
	if !strings.Contains(m.listPanelTitle(), "+1") {
		t.Errorf("title should count the hidden tag, got %q", m.listPanelTitle())
	}

	m.showFinishedGroups = true
	if out := ansi.Strip(m.renderTagList()); !strings.Contains(out, "#attic") {
		t.Errorf("h should bring the finished tag back:\n%s", out)
	}
}

// TestTagListDropsNextUpWhenNarrow asserts the next-up column disappears whole
// on a narrow terminal instead of leaving a clipped stub of a title.
func TestTagListDropsNextUpWhenNarrow(t *testing.T) {
	a := todo.New("open task with a title")
	a.Tags = []string{"work"}
	m := newTagModel(a)
	m.termWidth = 30
	m.tab = tabTags
	m.refreshCaches()
	if out := ansi.Strip(m.renderTagList()); strings.Contains(out, "Next") || strings.Contains(out, "Open task") {
		t.Errorf("narrow tag list should drop Next up whole, got:\n%s", out)
	}
}

// TestTagsTabNarrowNoWrap renders the Tags tab (list + detail) across widths and
// for both a real tag and the untagged row, asserting nothing wraps.
func TestTagsTabNarrowNoWrap(t *testing.T) {
	for _, width := range []int{40, 50, 60, 80, 120, 200} {
		tagged := todo.New("A long task title that would overflow a slim detail pane easily")
		tagged.Tags = []string{"alpha"}
		tagged.Project = "someproject"
		tagged.DueDate = time.Now().AddDate(0, 0, -2)

		m := newTagModel(tagged, todo.New("An untagged task with a similarly long title to test wrapping"))
		m.termWidth = width
		m.tab = tabTags
		m.refreshCaches()

		for _, cursor := range []int{0, 1} { // untagged row, then #alpha
			m.tagTabCursor = cursor
			out := m.View()
			for n, line := range strings.Split(out, "\n") {
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("width=%d cursor=%d: line %d is %d cells: %q", width, cursor, n, w, line)
				}
			}
		}
	}
}

// tagTaskList is what both the tag pane and the drill cursor read, so its
// order is a contract: open tasks ranked highest first, subtasks folded under
// their parent as on the Tasks tab (all of them once unfolded, tagged or
// not), and the done tasks only when shown, last.
func TestTagTaskListOrder(t *testing.T) {
	beta := todo.New("Beta urgent")
	beta.AddTag("home")
	beta.Priority = todo.PriorityHigh
	alpha := todo.New("alpha routine") // todo.New capitalizes: "Alpha routine"
	alpha.AddTag("home")
	finished := todo.New("Aardvark done")
	finished.AddTag("home")
	finished.Status = todo.Done
	elsewhere := todo.New("Not tagged")
	sub := todo.NewSubtask("Subtask", alpha.ID)
	sub.AddTag("home")
	bareSub := todo.NewSubtask("Bare subtask", elsewhere.ID)
	untaggedStep := todo.NewSubtask("Untagged step", alpha.ID)

	m := modelWithTasks(t, beta, alpha, finished, elsewhere, sub, bareSub, untaggedStep)

	titles := func(tasks []todo.Todo) []string {
		var out []string
		for _, task := range tasks {
			out = append(out, task.Title)
		}
		return out
	}
	want := []string{"Beta urgent", "Alpha routine"}
	if got := titles(m.tagTaskList("home")); !reflect.DeepEqual(got, want) {
		t.Errorf("folded, tagTaskList = %v, want %v", got, want)
	}

	m.setExpanded(alpha.ID, true)
	got := titles(m.tagTaskList("home"))
	if len(got) != 4 || got[0] != "Beta urgent" || got[1] != "Alpha routine" ||
		!slices.Contains(got[2:], "Subtask") || !slices.Contains(got[2:], "Untagged step") {
		t.Errorf("unfolded, tagTaskList = %v, want both of Alpha's subtasks under it", got)
	}
	if nested := m.groupNestedRows(m.tagTaskList("home")); !reflect.DeepEqual(nested, []bool{false, false, true, true}) {
		t.Errorf("nesting = %v, want the two subtasks indented", nested)
	}
	want = got

	m.showFinishedGroups = true
	want = append(want, "Aardvark done")
	if got := titles(m.tagTaskList("home")); !reflect.DeepEqual(got, want) {
		t.Errorf("with finished shown, tagTaskList = %v, want %v", got, want)
	}

	// An untagged subtask belongs to its parent, not to the triage row.
	if got := titles(m.tagTaskList(untaggedKey)); !reflect.DeepEqual(got, []string{"Not tagged"}) {
		t.Errorf("untagged list = %v, want just the untagged top-level task", got)
	}
}

// The Tags header is padded to the pane's inner width. It used to be padded
// two cells past it, so whenever the columns filled the pane exactly the pane
// clipped the header and its ellipsis took the last letter: "Tim…".
func TestTagHeaderIsNeverClipped(t *testing.T) {
	a := todo.New("alpha")
	a.Tags = []string{"code"}
	m := modelWithTasks(t, a)
	m.tab = tabTags
	for w := 40; w <= 160; w++ {
		m.termWidth, m.termHeight = w, 24
		header := strings.SplitN(ansi.Strip(m.renderTagList()), "\n", 2)[0]
		if got := ansi.StringWidth(header); got > w-8 {
			t.Fatalf("width %d: header is %d cells, pane holds %d: %q", w, got, w-8, header)
		}
	}
}
