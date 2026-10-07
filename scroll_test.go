package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// The selected row must be on screen. Each list draws a window of its rows and
// a scroll clamp keeps the cursor inside it; when the two measure the window
// differently the cursor sits a row or two below the panel's edge, which is
// what the Tasks list did once it scrolled, at every window size. These walk
// every list the cursor can be in and look for the selected row in the frame.

// scrollTasks is a set long enough to scroll at any window size, spread over
// many tags and projects plus one tag and one project that hold a lot of it.
func scrollTasks(n int) []todo.Todo {
	var tasks []todo.Todo
	for i := range n {
		td := todo.New(fmt.Sprintf("Row%02d", i))
		td.AddTag(fmt.Sprintf("tg%02d", i))
		td.AddTag("all")
		td.Project = fmt.Sprintf("pj%02d", i)
		if i%3 == 0 {
			td.Project = "big"
		}
		tasks = append(tasks, td)
	}
	return tasks
}

// selectedTaskShown reports whether the cursor's task row is in the frame. A
// task row reads "] Title", or "] ⧗ Title" while its timer runs.
func selectedTaskShown(m model, out string) bool {
	cur := m.currentTodo()
	return cur == nil || strings.Contains(out, "] "+cur.Title) || strings.Contains(out, "] ⧗ "+cur.Title)
}

func TestSelectedRowStaysOnScreen(t *testing.T) {
	done := scrollTasks(40)
	for i := range done {
		done[i].Title = fmt.Sprintf("Done%02d", i)
		done[i].Status = todo.Done
		done[i].Tags, done[i].Project = nil, ""
	}
	enterGroup := func(t *testing.T, m model, cursorOn func(model) string, name string) model {
		for cursorOn(m) != name {
			m = sendKey(t, m, "down")
		}
		return sendKey(t, m, "enter")
	}
	tagUnderCursor := func(m model) string { return m.getFilteredTagsForTab()[m.tagTabCursor] }
	projectUnderCursor := func(m model) string { return m.allProjectsForList()[m.projectCursor] }

	views := []struct {
		name  string
		setup func(*testing.T, model) model
		shown func(model, string) bool
	}{
		{"tasks", func(_ *testing.T, m model) model { return m }, selectedTaskShown},
		{"tasks, detail open", func(t *testing.T, m model) model { return m }, nil},
		{"history", func(t *testing.T, m model) model { return sendKey(t, m, "h") }, selectedTaskShown},
		{"tag list", func(_ *testing.T, m model) model { m.switchTab(tabTags); return m },
			func(m model, out string) bool { return strings.Contains(out, "▶ #"+tagUnderCursor(m)) }},
		{"project list", func(_ *testing.T, m model) model { m.switchTab(tabProjects); return m },
			func(m model, out string) bool { return strings.Contains(out, "▶ "+projectUnderCursor(m)) }},
		{"inside a tag", func(t *testing.T, m model) model {
			m.switchTab(tabTags)
			return enterGroup(t, m, tagUnderCursor, "all")
		}, selectedTaskShown},
		{"inside a project", func(t *testing.T, m model) model {
			m.switchTab(tabProjects)
			return enterGroup(t, m, projectUnderCursor, "big")
		}, selectedTaskShown},
	}
	for _, v := range views {
		for _, w := range []int{80, 130} {
			for _, h := range []int{20, 32} {
				for _, pos := range []detailPos{detailRight, detailBottom} {
					m := modelWithTasks(t, append(scrollTasks(40), done...)...)
					m.termWidth, m.termHeight, m.detailPos = w, h, pos
					m = v.setup(t, m)
					check := func(dir string) {
						shown, frame := v.shown, m
						if shown == nil { // the detail opened on the row
							shown, frame = selectedTaskShown, sendKey(t, m, "enter")
						}
						if out := ansi.Strip(frame.View()); !shown(frame, out) {
							t.Fatalf("%s w=%d h=%d detail=%s: selected row off screen after %s:\n%s",
								v.name, w, h, pos, dir, out)
						}
					}
					for range 30 {
						check("down")
						m = sendKey(t, m, "down")
					}
					for range 30 {
						m = sendKey(t, m, "up")
						check("up")
					}
				}
			}
		}
	}
}

// Random keys over every tab and window shape: no panic, no line wider than
// the window, and the selected row on screen wherever the cursor is in a list.
func TestRandomKeysKeepTheSelectionOnScreen(t *testing.T) {
	keys := []string{"down", "down", "down", "up", "up", "right", "left", "enter", "esc", "esc",
		"h", "pgdown", "pgup", "end", "home", "1", "3", "4", "5", "6", "tab", "shift+tab",
		"s", "f", "d", "u", "t", "p", "x", "y", "w", "?", "H", "L"}
	for seed := int64(1); seed <= 6; seed++ {
		r := rand.New(rand.NewSource(seed))
		base := scrollTasks(30)
		for i := range 6 {
			sub := todo.NewSubtask(fmt.Sprintf("Sub%02d", i), base[i*3].ID)
			sub.InheritContextFrom(&base[i*3], true)
			base = append(base, sub)
		}
		m := modelWithTasks(t, base...)
		m.termWidth = []int{70, 90, 110, 140}[r.Intn(4)]
		m.termHeight = []int{18, 24, 32, 45}[r.Intn(4)]
		m.detailPos = []detailPos{detailRight, detailLeft, detailBottom}[r.Intn(3)]
		var trail []string
		for range 250 {
			k := keys[r.Intn(len(keys))]
			trail = append(trail, k)
			m = sendKey(t, m, k)
			out := ansi.Strip(m.View())
			fail := func(what string) {
				t.Fatalf("seed %d, %dx%d, detail=%s: %s after %v:\n%s",
					seed, m.termWidth, m.termHeight, m.detailPos, what, trail[max(len(trail)-8, 0):], out)
			}
			for _, line := range strings.Split(out, "\n") {
				if ansi.StringWidth(line) > m.termWidth {
					fail("a line wider than the window")
				}
			}
			if m.mode != modeNormal || m.pane != paneList {
				continue
			}
			if _, drilled := m.drillTaskList(); (m.tab == tabTasks || drilled) && !selectedTaskShown(m, out) {
				fail("the selected task off screen")
			}
		}
	}
}

// A detail stacked under the task list takes the rows a short list leaves
// over, and when both are full the two split the height evenly.
func TestStackedTaskDetailTakesWhatTheListLeaves(t *testing.T) {
	split := func(n int) (listRows, detail, area int) {
		var ts []todo.Todo
		for i := 0; i < n; i++ {
			ts = append(ts, todo.New(fmt.Sprintf("task %d", i)))
		}
		m := modelWithTasks(t, ts...)
		m.termWidth, m.termHeight = 100, 40
		m.detailPos = detailBottom
		m = script(t, m, "enter")
		if got := strings.Count(m.View(), "\n") + 1; got != m.termHeight {
			t.Fatalf("%d tasks: frame is %d lines, window is %d", n, got, m.termHeight)
		}
		return m.taskListRows(), m.stackedTaskDetailLines(), m.taskStackArea()
	}
	if rows, detail, area := split(3); rows != 3 || detail != area-3-taskListChromeLines {
		t.Errorf("3 tasks: list shows %d rows and detail has %d of %d lines; want 3 rows and the rest", rows, detail, area)
	}
	if _, detail, area := split(60); detail != area-area/2 {
		t.Errorf("a full list and a long detail: detail has %d of %d lines, want half", detail, area)
	}
}
