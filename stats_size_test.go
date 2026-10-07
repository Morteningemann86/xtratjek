package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// The stats page's "Cycle time by size" and "Projected backlog clear" blocks:
// median created→completed time bucketed by size, and pending-count × that
// median summed into a rough ETA.
func TestStatsCycleTimeAndProjection(t *testing.T) {
	now := time.Now()
	// Cycle time is measured start date→completion, so a completed task only
	// contributes when it has a start date (ageStarted >= 0).
	mk := func(id string, sz todo.Size, done bool, ageStarted, ageCompleted int) todo.Todo {
		td := todo.Todo{
			ID:        id,
			Title:     id,
			Size:      sz,
			Priority:  todo.PriorityMedium,
			CreatedAt: now.AddDate(0, 0, -60), // created long ago; must not affect cycle time
		}
		if ageStarted >= 0 {
			td.StartDate = now.AddDate(0, 0, -ageStarted)
		}
		if done {
			td.Status = todo.Done
			td.CompletedAt = now.AddDate(0, 0, -ageCompleted)
		}
		return td
	}

	var todos []todo.Todo
	// Completed small tasks: started 4d/10d ago, completed 2d/6d ago → cycle
	// times 2d and 4d → median 3.0d.
	todos = append(todos, mk("s-done-a", todo.SizeSmall, true, 4, 2))
	todos = append(todos, mk("s-done-b", todo.SizeSmall, true, 10, 6))
	// One completed large task: started 40d ago, done 10d ago → cycle 30d.
	todos = append(todos, mk("l-done", todo.SizeLarge, true, 40, 10))
	// A completed small task with NO start date must be excluded from the median.
	todos = append(todos, mk("s-done-nostart", todo.SizeSmall, true, -1, 1))
	// Pending: 3 small, 1 large. Medium has neither history nor pending.
	for i := 0; i < 3; i++ {
		todos = append(todos, mk(fmt.Sprintf("s-pend-%d", i), todo.SizeSmall, false, 3, 0))
	}
	todos = append(todos, mk("l-pend", todo.SizeLarge, false, 3, 0))

	m := newTagModel(todos...)
	m.tab = tabStats
	m.termWidth = 60 // single column: every section stacks, easy to scan
	m.frameTime = now

	out := ansi.Strip(m.renderStatsList())
	line := func(prefix string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(strings.TrimSpace(l), prefix) {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		return ""
	}

	// Small median = 3.0d over 2 samples.
	if got := line("Small"); !strings.Contains(got, "3.0d (n=2)") {
		t.Errorf("cycle-time Small row = %q, want it to contain %q", got, "3.0d (n=2)")
	}
	// Projection sums 3×3.0d (small) + 1×30d (large) = 9 + 30 = ~39d.
	if got := line("Projected clear"); !strings.Contains(got, "39d") {
		t.Errorf("projected clear = %q, want ~39d", got)
	}
	// Medium has no completed samples, so its cycle row reads "none yet".
	if got := line("Medium"); !strings.Contains(got, "none yet") {
		t.Errorf("cycle-time Medium row = %q, want %q (no samples)", got, "none yet")
	}
}
