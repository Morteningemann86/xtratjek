package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// TestComputeStatsBuckets covers the four pending buckets + the two done
// buckets simultaneously. Uses a fixed `now` so the date math is
// deterministic regardless of when CI runs.
func TestComputeStatsBuckets(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	today := startOfDay(now)

	build := func(title string, st todo.Status, due time.Time, completedAt time.Time, parentID string) todo.Todo {
		x := todo.New(title)
		x.Status = st
		x.DueDate = due
		x.CompletedAt = completedAt
		x.ParentID = parentID
		return x
	}

	todos := []todo.Todo{
		build("overdue 1", todo.Pending, today.AddDate(0, 0, -5), time.Time{}, ""),
		build("overdue 2", todo.Pending, today.AddDate(0, 0, -1), time.Time{}, ""),
		build("due today", todo.Pending, today.Add(8*time.Hour), time.Time{}, ""),
		build("due this week", todo.Pending, today.AddDate(0, 0, 3), time.Time{}, ""),
		build("far future, no bucket", todo.Pending, today.AddDate(0, 0, 30), time.Time{}, ""),
		build("no date pending", todo.Pending, time.Time{}, time.Time{}, ""),
		build("done today", todo.Done, time.Time{}, today.Add(2*time.Hour), ""),
		build("done yesterday", todo.Done, time.Time{}, today.AddDate(0, 0, -1), ""),
		// Subtasks should be ignored by the top-level filter.
		build("subtask of something", todo.Pending, today, time.Time{}, "some-parent-id"),
	}

	got := computeStats(todos, now)

	// Active = every non-subtask pending row, regardless of due bucket:
	//   2 overdue + 1 due today + 1 due this week + 1 far-future + 1 no-date
	want := statsSummary{
		Active:       6,
		Overdue:      2,
		DueToday:     1,
		DueThisWeek:  1,
		DoneToday:    1,
		DoneThisWeek: 2,
	}
	if got != want {
		t.Errorf("computeStats() = %+v\nwant %+v", got, want)
	}
}

// TestComputeStatsIgnoresSubtasksAndCompletedWithoutTimestamp ensures the
// filter is robust to malformed input — a Done task with a zero CompletedAt
// shouldn't count toward "done today".
func TestComputeStatsIgnoresEdgeCases(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	todos := []todo.Todo{
		{ParentID: "p", Status: todo.Pending},         // subtask → skipped
		{Status: todo.Done, CompletedAt: time.Time{}}, // done with no timestamp → skipped from done buckets
		{Status: todo.Done, CompletedAt: time.Time{}}, // ditto
	}
	got := computeStats(todos, now)
	if got.Active != 0 || got.DoneToday != 0 || got.DoneThisWeek != 0 {
		t.Errorf("expected all zero, got %+v", got)
	}
}

func TestScopeForStats(t *testing.T) {
	parent := todo.New("tagged parent")
	parent.ID = "parent"
	parent.Tags = []string{"work"}
	sub := todo.New("subtask, no tag of its own")
	sub.ID = "sub"
	sub.ParentID = "parent"
	other := todo.New("unrelated")
	other.ID = "other"
	doneTagged := todo.New("tagged and finished")
	doneTagged.ID = "done"
	doneTagged.Tags = []string{"work"}
	doneTagged.Status = todo.Done
	doneTagged.CompletedAt = time.Now()

	scoped := scopeForStats([]todo.Todo{parent, sub, other, doneTagged}, listFilterOpts{includeDone: true, tag: "work"})

	got := map[string]bool{}
	for _, x := range scoped {
		got[x.ID] = true
	}
	for id, want := range map[string]bool{"parent": true, "sub": true, "done": true, "other": false} {
		if got[id] != want {
			t.Errorf("scoped[%s] = %v, want %v", id, got[id], want)
		}
	}
}

func TestRenderSeqAnalysisTextEdges(t *testing.T) {
	if s := renderSeqAnalysisText(rank.Analysis{}, rank.DefaultBiases()); !strings.Contains(s, "no rank-stamped completions") {
		t.Errorf("empty analysis = %q, want the no-history message", s)
	}
	allHits := rank.Analysis{Hits: 4, Rated: 4, TopN: rank.HitTopN}
	if s := renderSeqAnalysisText(allHits, rank.DefaultBiases()); !strings.Contains(s, "no misses") {
		t.Errorf("all-hits analysis = %q, want the no-misses message", s)
	}
}

func TestRenderSeqAnalysisTextTable(t *testing.T) {
	base := time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)
	mk := func(id string, rank int, at time.Time, due bool) todo.Todo {
		x := todo.New(id)
		x.ID = id
		x.Status = todo.Done
		x.CompletedAt = at
		x.CreatedAt = at
		x.SeqRankAtDone = rank
		if due {
			x.DueDate = at
		}
		return x
	}
	todos := []todo.Todo{
		mk("h1", 1, base.Add(-40*24*time.Hour), true),
		mk("m1", 9, base.Add(-3*24*time.Hour), false),
		mk("m2", 12, base.Add(-2*24*time.Hour), false),
		mk("m3", 8, base.Add(-1*24*time.Hour), false),
	}
	a := rank.AnalyzeMisses(todoPtrs(todos), todoPtrs(todos), rank.HitWindow, rank.DefaultBiases())
	out := renderSeqAnalysisText(a, rank.DefaultBiases())
	for _, want := range []string{"largest gap", "Deadline: relaxed", "recent misses:", "rank   9"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered analysis missing %q:\n%s", want, out)
		}
	}
}
