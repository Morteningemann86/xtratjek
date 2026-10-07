package main

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/todo"
)

// benchTodos builds a realistic task set: a mix of pending/done, varied tags
// and projects, due dates spread around now, plus some subtasks — the kind of
// shape that exercises the active/done split, tag stats, and project rollups.
func benchTodos(n int) []todo.Todo {
	now := time.Now()
	tags := []string{"work", "home", "urgent", "later", "errand", "health", "reading", "code"}
	projects := []string{"alpha", "beta", "gamma", "", "delta", ""}
	out := make([]todo.Todo, 0, n)
	for i := 0; i < n; i++ {
		t := todo.New(fmt.Sprintf("Task number %d with a reasonably long title", i))
		t.ID = fmt.Sprintf("t%d", i)
		if i%3 == 0 {
			t.Status = todo.Done
		}
		t.Priority = todo.Priority(i % 3)
		t.Project = projects[i%len(projects)]
		// 0–2 tags per task.
		t.Tags = append(t.Tags, tags[i%len(tags)])
		if i%2 == 0 {
			t.Tags = append(t.Tags, tags[(i+3)%len(tags)])
		}
		t.DueDate = now.AddDate(0, 0, (i%14)-5)
		out = append(out, t)
	}
	// Make every 10th task a subtask of the one before it (link lives on the
	// child as ParentID).
	for i := 10; i < len(out); i += 10 {
		out[i].ParentID = out[i-1].ID
	}
	return out
}

func benchModel(n int) model {
	m := initialModel(&fakeRepo{todos: benchTodos(n)})
	m.termWidth = 120
	m.termHeight = 40
	m.ensureCache()
	return m
}

// BenchmarkView measures a full frame render with caches already warm — the
// common case (a keypress that doesn't change data, e.g. moving the cursor).
func BenchmarkView(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		m := benchModel(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = m.View()
			}
		})
	}
}

// BenchmarkSearchKeystroke measures the per-keystroke search path: a filter
// change followed by the frame that renders it.
func BenchmarkSearchKeystroke(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		m := benchModel(n)
		queries := []string{"task", "task number 1", "#work", "#urgent", ""}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.searchQuery = queries[i%len(queries)]
				m.markFilterDirty()
				_ = m.View()
			}
		})
	}
}

// BenchmarkRefreshCaches measures a full derived-state rebuild — what every
// data mutation pays for.
func BenchmarkRefreshCaches(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		m := benchModel(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.refreshCaches()
			}
		})
	}
}

// BenchmarkBoardView measures a full Board-tab frame with warm caches. It
// guards the boardColumns cache: the column split copies task values, so
// doing it per frame would reintroduce exactly the per-frame O(active) scans
// cacheState exists to prevent.
func BenchmarkBoardView(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		m := benchModel(n)
		m.tab = tabBoard
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = m.View()
			}
		})
	}
}

// BenchmarkCursorMove measures one cursor step and the frame it produces on
// every surface the cursor walks — what a held arrow key costs. The drill-in
// lists and the calendar derive their rows on demand, so they are where a
// per-key rebuild of the whole set shows up first.
func BenchmarkCursorMove(b *testing.B) {
	const n = 2000
	tasks := benchTodos(n)
	now := time.Now()
	for i := range tasks {
		if i%4 == 0 { // time entries across the last month, for the calendar
			start := now.AddDate(0, 0, -(i % 30)).Add(-time.Hour)
			tasks[i].TimeEntries = []todo.TimeEntry{{ID: fmt.Sprintf("e%d", i), StartedAt: start, StoppedAt: start.Add(40 * time.Minute)}}
		}
	}
	surfaces := []struct {
		name string
		open func(m model) model
		key  string
	}{
		{"tasks", func(m model) model { return m }, "down"},
		{"tasks detail", func(m model) model { return benchKey(m, "enter") }, "down"},
		{"calendar", func(m model) model { m.switchTab(tabCalendar); return m }, "right"},
		{"projects", func(m model) model { m.switchTab(tabProjects); return m }, "down"},
		{"inside a project", func(m model) model { m.switchTab(tabProjects); return benchKey(m, "enter") }, "down"},
		{"tags", func(m model) model { m.switchTab(tabTags); return m }, "down"},
		{"inside a tag", func(m model) model { m.switchTab(tabTags); return benchKey(m, "enter") }, "down"},
		{"board", func(m model) model { m.switchTab(tabBoard); return m }, "down"},
		{"tab switch", func(m model) model { return m }, "tab"},
	}
	for _, s := range surfaces {
		m := initialModel(&fakeRepo{todos: tasks})
		m.termWidth, m.termHeight = 120, 40
		m.ensureCache()
		m = s.open(m)
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			back := map[string]string{"down": "up", "right": "left", "tab": "shift+tab"}[s.key]
			for i := 0; i < b.N; i++ {
				k := s.key
				if i%20 >= 10 { // walk back and forth so a list never runs out
					k = back
				}
				m = benchKey(m, k)
				_ = m.View()
			}
		})
	}
}

// benchKey is sendKey without a *testing.T, for benchmarks.
func benchKey(m model, k string) model {
	var msg tea.KeyMsg
	switch k {
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyMsg{Type: tea.KeyShiftTab}
	}
	next, _ := m.Update(msg)
	return next.(model)
}
