package rank

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}

// fixedNow anchors every dimension test on a deterministic clock so the
// "today" / "overdue" / "future" branches don't flake at midnight.
var fixedNow = time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)

func TestUrgencyDimDueToday(t *testing.T) {
	due := time.Date(2026, 6, 17, 23, 30, 0, 0, time.UTC)
	got := urgencyDim(fixedNow, due)
	if !approxEq(got, 10.0) {
		t.Errorf("due today (later in the same day) = %v, want 10.0", got)
	}
}

func TestUrgencyDimOverdue(t *testing.T) {
	cases := []struct {
		daysOverdue int
		want        float64
	}{
		{1, 10.5},
		{4, 12.0},
		{10, 15.0},
	}
	for _, c := range cases {
		due := startOfDay(fixedNow).Add(-time.Duration(c.daysOverdue) * 24 * time.Hour)
		got := urgencyDim(fixedNow, due)
		if !approxEq(got, c.want) {
			t.Errorf("%d days overdue = %v, want %v", c.daysOverdue, got, c.want)
		}
	}
}

func TestUrgencyDimWithinWeek(t *testing.T) {
	cases := []struct {
		daysUntil int
		want      float64
	}{
		{1, 10.0 - 8.0/7.0},
		{4, 10.0 - 32.0/7.0},
		{7, 2.0},
	}
	for _, c := range cases {
		due := startOfDay(fixedNow).Add(time.Duration(c.daysUntil) * 24 * time.Hour)
		got := urgencyDim(fixedNow, due)
		if !approxEq(got, c.want) {
			t.Errorf("%d days out = %v, want %v", c.daysUntil, got, c.want)
		}
	}
}

func TestUrgencyDimBeyondWeek(t *testing.T) {
	due := startOfDay(fixedNow).Add(8 * 24 * time.Hour)
	if got := urgencyDim(fixedNow, due); got != 0 {
		t.Errorf("beyond 7 days = %v, want 0 (Age takes over)", got)
	}
}

func TestUrgencyDimNoDate(t *testing.T) {
	if got := urgencyDim(fixedNow, time.Time{}); got != 0 {
		t.Errorf("no due date = %v, want 0", got)
	}
}

func TestImportanceDim(t *testing.T) {
	cases := []struct {
		p    todo.Priority
		want float64
	}{
		{todo.PriorityHigh, 10},
		{todo.PriorityMedium, 5},
		{todo.PriorityLow, 0},
	}
	for _, c := range cases {
		if got := importanceDim(c.p); got != c.want {
			t.Errorf("priority %v = %v, want %v", c.p, got, c.want)
		}
	}
}

// hotHeat builds a heat snapshot whose every listed key carries a signal that
// landed just now — the shape tests want when they only care about hot/cold.
// The heat maps hold the signal instant (see Heat), so a literal of
// bare keys needs a timestamp per entry.
func hotHeat(at time.Time, tasks, projects, tags []string) Heat {
	fill := func(keys []string) map[string]time.Time {
		m := make(map[string]time.Time, len(keys))
		for _, k := range keys {
			m[k] = at
		}
		return m
	}
	return Heat{tasks: fill(tasks), projects: fill(projects), tags: fill(tags)}
}

func TestMomentumDim(t *testing.T) {
	heat := hotHeat(fixedNow, []string{"self"}, []string{"hot-proj"}, []string{"hot-tag"})
	mk := func(id, project string, tags ...string) *todo.Todo {
		tt := todo.New("x")
		tt.ID = id
		tt.Project = project
		tt.Tags = tags
		return &tt
	}
	cases := []struct {
		name string
		t    *todo.Todo
		want float64
	}{
		{"own activity", mk("self", ""), 10},
		{"hot project", mk("other", "hot-proj"), 10},
		{"hot tag only", mk("other", "cold-proj", "hot-tag"), 5},
		{"cold", mk("other", "cold-proj", "cold-tag"), 0},
		{"no project no tags", mk("other", ""), 0},
	}
	for _, c := range cases {
		if got := momentumDim(c.t, heat); got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
	// Zero-value heat (no snapshot computed) must read as all-cold, not panic.
	if got := momentumDim(mk("any", "p", "t"), Heat{}); got != 0 {
		t.Errorf("zero-value heat = %v, want 0", got)
	}
}

func TestSizeDim(t *testing.T) {
	cases := []struct {
		s    todo.Size
		want float64
	}{
		{todo.SizeSmall, 2},
		{todo.SizeMedium, 1},
		{todo.SizeLarge, 0},
	}
	for _, c := range cases {
		if got := sizeDim(c.s); got != c.want {
			t.Errorf("size %v = %v, want %v", c.s, got, c.want)
		}
	}
}

// TestComputeActivityHeat pins which signals make a task/project/tag hot and
// which don't: recency window, deleted tasks, tombstoned comments, running
// timers.
func TestComputeActivityHeat(t *testing.T) {
	now := fixedNow
	inWindow := now.Add(-24 * time.Hour)
	stale := now.Add(-3 * 24 * time.Hour)

	fresh := todo.New("finished yesterday")
	fresh.ID = "fresh"
	fresh.Status = todo.Done
	fresh.CompletedAt = inWindow
	fresh.Project = "alpha"
	fresh.Tags = []string{"go"}

	old := todo.New("finished last week")
	old.ID = "old"
	old.Status = todo.Done
	old.CompletedAt = stale
	old.Project = "beta"

	commented := todo.New("discussed this morning")
	commented.ID = "commented"
	commented.Comments = []todo.Comment{{CreatedAt: inWindow}}
	commented.Project = "gamma"

	tracked := todo.New("timer running")
	tracked.ID = "tracked"
	tracked.TimeEntries = []todo.TimeEntry{{StartedAt: now.Add(-10 * time.Minute)}}
	tracked.Project = "delta"

	ghost := todo.New("deleted but recently touched")
	ghost.ID = "ghost"
	ghost.CompletedAt = inWindow
	ghost.Deleted = true
	ghost.Project = "epsilon"

	h := ComputeHeat(now, todoPtrs([]todo.Todo{fresh, old, commented, tracked, ghost}))

	for _, want := range []struct {
		kind string
		m    map[string]time.Time
		key  string
		hot  bool
	}{
		{"task", h.tasks, "fresh", true},
		{"task", h.tasks, "old", false},
		{"task", h.tasks, "commented", true},
		{"task", h.tasks, "tracked", true},
		{"task", h.tasks, "ghost", false},
		{"project", h.projects, "alpha", true},
		{"project", h.projects, "beta", false},
		{"project", h.projects, "epsilon", false},
		{"tag", h.tags, "go", true},
	} {
		if got := hot(want.m, want.key); got != want.hot {
			t.Errorf("%s %q hot = %v, want %v", want.kind, want.key, got, want.hot)
		}
	}

	// The recorded instant is the signal's own, not the scan's: it is what the
	// explain view's momentum forecast counts down from.
	if got := h.tasks["fresh"]; !got.Equal(inWindow) {
		t.Errorf("fresh signal stamped %v, want %v", got, inWindow)
	}
	if got := h.tasks["tracked"]; !got.Equal(now) {
		t.Errorf("running timer stamped %v, want %v (a running timer never goes cold)", got, now)
	}
}

func TestAgeDimLinear(t *testing.T) {
	created := fixedNow.Add(-10 * 24 * time.Hour)
	if got := ageDim(fixedNow, created); !approxEq(got, 1.0) {
		t.Errorf("10-day-old = %v, want 1.0", got)
	}
}

func TestAgeDimPastThirtyDays(t *testing.T) {
	created := fixedNow.Add(-60 * 24 * time.Hour)
	// 30 days * 0.1 + 30 days * 0.2 = 3 + 6 = 9
	if got := ageDim(fixedNow, created); !approxEq(got, 9.0) {
		t.Errorf("60-day-old = %v, want 9.0", got)
	}
}

func TestDoneTaskScoresZero(t *testing.T) {
	tt := todo.New("done")
	tt.Status = todo.Done
	tt.Priority = todo.PriorityHigh
	tt.Size = todo.SizeSmall
	got := ComponentsAt(fixedNow, &tt, Biases{Deadline: Intense, Priority: Intense, Momentum: Intense, Aging: true}, Heat{})
	if got.Total != 0 {
		t.Errorf("done task scored %v, want 0", got.Total)
	}
}

func TestBalancedBiasGivesUnweightedSum(t *testing.T) {
	tt := todo.New("balanced")
	tt.Priority = todo.PriorityHigh // 10
	tt.Size = todo.SizeSmall        // size nudge 2
	tt.Project = "hot"
	tt.CreatedAt = fixedNow.Add(-1 * 24 * time.Hour)
	heat := hotHeat(fixedNow, nil, []string{"hot"}, nil)
	got := ComponentsAt(fixedNow, &tt, Biases{Deadline: Balanced, Priority: Balanced, Momentum: Balanced, Aging: true}, heat)
	// no due, so urgency=0. importance 10 + momentum 10 + size 2 + age 0.1 = 22.1
	if !approxEq(got.Total, 22.1) {
		t.Errorf("Balanced score = %v, want ~22.1; components=%+v", got.Total, got)
	}
}

func TestIntenseBiasDoublesEachAxis(t *testing.T) {
	tt := todo.New("intense")
	tt.Priority = todo.PriorityHigh
	tt.Size = todo.SizeSmall
	tt.Project = "hot"
	tt.DueDate = fixedNow.Add(48 * time.Hour) // 2 days out
	heat := hotHeat(fixedNow, nil, []string{"hot"}, nil)
	got := ComponentsAt(fixedNow, &tt, Biases{Deadline: Intense, Priority: Intense, Momentum: Intense, Aging: true}, heat)
	// urgency dim = 10 - 16/7 ≈ 7.714; weighted ×2 ≈ 15.428
	// importance dim = 10; weighted ×2 = 20
	// momentum dim = 10 (hot project); weighted ×2 = 20
	// size nudge = 2 (unweighted — the bias must not touch it)
	// age ≈ 0
	wantTotal := 2*(10.0-16.0/7.0) + 20 + 20 + 2
	if !approxEq(got.Total, wantTotal) {
		t.Errorf("Intense score = %v, want ~%v; components=%+v", got.Total, wantTotal, got)
	}
}

func TestRelaxedBiasHalvesEachAxis(t *testing.T) {
	tt := todo.New("relaxed")
	tt.Priority = todo.PriorityHigh
	tt.Size = todo.SizeLarge
	got := ComponentsAt(fixedNow, &tt, Biases{Deadline: Relaxed, Priority: Relaxed, Momentum: Relaxed, Aging: true}, Heat{})
	// urgency 0, importance 10 * 0.5 = 5, momentum 0 (cold), size 0, age 0
	if !approxEq(got.Total, 5.0) {
		t.Errorf("Relaxed score = %v, want 5.0; components=%+v", got.Total, got)
	}
}

// TestHotProjectOutranksColdPeer is the new Momentum contract: two otherwise
// identical tasks — the one whose project saw activity in the window ranks a
// full axis (10 points, Balanced) above the cold one.
func TestHotProjectOutranksColdPeer(t *testing.T) {
	bal := Biases{Deadline: Balanced, Priority: Balanced, Momentum: Balanced, Aging: true}
	heat := hotHeat(fixedNow, nil, []string{"active"}, nil)

	inFlow := todo.New("next step in the active project")
	inFlow.Project = "active"
	cold := todo.New("same shape, dormant project")
	cold.Project = "dormant"

	s := ComponentsAt(fixedNow, &inFlow, bal, heat).Total
	p := ComponentsAt(fixedNow, &cold, bal, heat).Total
	if s-p < 9.99 {
		t.Errorf("hot project lead = %v, want 10 (in-flow=%v cold=%v)", s-p, s, p)
	}
}

// TestAgingToggle proves the Aging flag fully zeros the Age contribution.
func TestAgingToggle(t *testing.T) {
	tt := todo.New("aged task")
	tt.Priority = todo.PriorityMedium
	tt.Size = todo.SizeMedium
	tt.CreatedAt = fixedNow.Add(-60 * 24 * time.Hour) // 60d old → 30·0.1 + 30·0.2 = 9.0 of Age

	with := Biases{Deadline: Balanced, Priority: Balanced, Momentum: Balanced, Aging: true}
	without := Biases{Deadline: Balanced, Priority: Balanced, Momentum: Balanced, Aging: false}

	gotWith := ComponentsAt(fixedNow, &tt, with, Heat{})
	gotWithout := ComponentsAt(fixedNow, &tt, without, Heat{})

	if !approxEq(gotWith.Age, 9.0) {
		t.Errorf("aging on: Age = %v, want ~9.0 (60d)", gotWith.Age)
	}
	if gotWithout.Age != 0 {
		t.Errorf("aging off: Age = %v, want 0", gotWithout.Age)
	}
	// Urgency/Importance/Momentum/Size are unaffected by the toggle.
	if gotWith.Urgency != gotWithout.Urgency ||
		gotWith.Importance != gotWithout.Importance ||
		gotWith.Momentum != gotWithout.Momentum ||
		gotWith.Size != gotWithout.Size {
		t.Errorf("non-Age dimensions diverged across toggle: with=%+v without=%+v", gotWith, gotWithout)
	}
}

func TestCaptureSeqRankAtDone(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	mk := func(id string, p todo.Priority) todo.Todo {
		tt := todo.New(id)
		tt.ID = id
		tt.Priority = p
		tt.CreatedAt = created
		return tt
	}
	top := mk("top", todo.PriorityHigh)
	mid := mk("mid", todo.PriorityMedium)
	low := mk("low", todo.PriorityLow)
	sub := mk("sub", todo.PriorityHigh)
	sub.ParentID = "top"
	todos := []todo.Todo{low, mid, top, sub}

	CaptureRankAtDone(Default(), todoPtrs(todos), &top)
	if top.SeqRankAtDone != 1 {
		t.Errorf("top rank = %d, want 1", top.SeqRankAtDone)
	}
	CaptureRankAtDone(Default(), todoPtrs(todos), &low)
	if low.SeqRankAtDone != 3 {
		t.Errorf("low rank = %d, want 3", low.SeqRankAtDone)
	}
	CaptureRankAtDone(Default(), todoPtrs(todos), &sub)
	if sub.SeqRankAtDone != 0 {
		t.Errorf("subtask rank = %d, want 0 (not recorded)", sub.SeqRankAtDone)
	}
}

func TestToggleReopenClearsSeqRank(t *testing.T) {
	tt := todo.New("x")
	tt.SeqRankAtDone = 0
	tt.Toggle() // pending → done
	tt.SeqRankAtDone = 3
	tt.Toggle() // done → pending: the reading is void
	if tt.SeqRankAtDone != 0 {
		t.Errorf("reopened task rank = %d, want 0", tt.SeqRankAtDone)
	}
}

func TestSequenceHitStats(t *testing.T) {
	now := time.Now()
	mkDone := func(id string, rank int, doneAgo time.Duration) todo.Todo {
		tt := todo.New(id)
		tt.ID = id
		tt.Status = todo.Done
		tt.CompletedAt = now.Add(-doneAgo)
		tt.SeqRankAtDone = rank
		return tt
	}
	todos := []todo.Todo{
		mkDone("hit1", 1, 1*time.Hour),
		mkDone("hit2", 5, 2*time.Hour),
		mkDone("miss", 9, 3*time.Hour),
		mkDone("old-hit", 2, 4*time.Hour),   // pushed out by window=3
		mkDone("legacy", 0, 30*time.Minute), // no rank recorded — not rated
	}
	pendingNoise := todo.New("pending")
	todos = append(todos, pendingNoise)

	hits, rated := HitStats(todoPtrs(todos), 3)
	if rated != 3 || hits != 2 {
		t.Errorf("hits/rated = %d/%d, want 2/3 (window keeps the 3 newest rated)", hits, rated)
	}
}

// ── stats --seq: historical heat + miss analysis ─────────────────────────────

func TestComputeActivityHeatAtBounds(t *testing.T) {
	at := fixedNow

	self := todo.New("the completion being analyzed")
	self.ID = "self"
	self.Status = todo.Done
	self.CompletedAt = at // lands exactly at `at` — its own completion must not count

	prior := todo.New("finished an hour earlier")
	prior.ID = "prior"
	prior.Status = todo.Done
	prior.CompletedAt = at.Add(-time.Hour)

	future := todo.New("finished after the analyzed moment")
	future.ID = "future"
	future.Status = todo.Done
	future.CompletedAt = at.Add(time.Hour)

	spanning := todo.New("timer active across the analyzed moment")
	spanning.ID = "spanning"
	spanning.TimeEntries = []todo.TimeEntry{{
		StartedAt: at.Add(-3 * 24 * time.Hour),
		StoppedAt: at.Add(24 * time.Hour),
	}}

	stale := todo.New("timer stopped before the window opened")
	stale.ID = "stale"
	stale.TimeEntries = []todo.TimeEntry{{
		StartedAt: at.Add(-4 * 24 * time.Hour),
		StoppedAt: at.Add(-3 * 24 * time.Hour),
	}}

	h := ComputeHeatAt(at, todoPtrs([]todo.Todo{self, prior, future, spanning, stale}))

	for id, want := range map[string]bool{
		"self":     false,
		"prior":    true,
		"future":   false,
		"spanning": true,
		"stale":    false,
	} {
		if got := hot(h.tasks, id); got != want {
			t.Errorf("task %q hot = %v, want %v", id, got, want)
		}
	}
}

// seqDone builds a rank-stamped completion for the miss-analysis tests:
// CreatedAt pinned to CompletedAt so the Age dimension reads 0 and the
// remaining dims are fully controlled by the caller.
func seqDone(id string, rank int, completedAt time.Time) todo.Todo {
	tt := todo.New(id)
	tt.ID = id
	tt.Status = todo.Done
	tt.CompletedAt = completedAt
	tt.CreatedAt = completedAt
	tt.SeqRankAtDone = rank
	return tt
}

func TestAnalyzeSeqMissesDeadlineGap(t *testing.T) {
	base := fixedNow
	// Two hits, both due on their completion day → Deadline 10 at completion.
	hit1 := seqDone("hit1", 1, base.Add(-30*24*time.Hour))
	hit1.DueDate = hit1.CompletedAt
	hit2 := seqDone("hit2", 4, base.Add(-20*24*time.Hour))
	hit2.DueDate = hit2.CompletedAt
	// Three misses, no due date → Deadline 0. Distinct timestamps so the
	// most-recent-first ordering is observable.
	miss1 := seqDone("miss1", 9, base.Add(-3*24*time.Hour))
	miss2 := seqDone("miss2", 12, base.Add(-2*24*time.Hour))
	miss3 := seqDone("miss3", 8, base.Add(-1*24*time.Hour))

	todos := []todo.Todo{hit1, miss1, hit2, miss2, miss3}
	a := AnalyzeMisses(todoPtrs(todos), todoPtrs(todos), HitWindow, DefaultBiases())

	if a.Hits != 2 || a.Rated != 5 {
		t.Fatalf("hits/rated = %d/%d, want 2/5", a.Hits, a.Rated)
	}
	if !approxEq(a.HitAvg[0], 10.0) || !approxEq(a.MissAvg[0], 0.0) || !approxEq(a.Gap[0], -10.0) {
		t.Errorf("Deadline hit/miss/gap = %v/%v/%v, want 10/0/-10", a.HitAvg[0], a.MissAvg[0], a.Gap[0])
	}
	// Priority (medium=5) and Size (medium=1) identical on both sides.
	if !approxEq(a.Gap[1], 0) || !approxEq(a.Gap[3], 0) {
		t.Errorf("Priority/Size gaps = %v/%v, want 0/0", a.Gap[1], a.Gap[3])
	}
	if len(a.Misses) != 3 {
		t.Fatalf("misses = %d, want 3", len(a.Misses))
	}
	if a.Misses[0].ID != "miss3" || a.Misses[2].ID != "miss1" {
		t.Errorf("miss order = %s..%s, want miss3..miss1 (most recent first)", a.Misses[0].ID, a.Misses[2].ID)
	}
	for _, r := range a.Misses {
		if r.Weakest != "Deadline" {
			t.Errorf("miss %s weakest = %q, want Deadline", r.ID, r.Weakest)
		}
	}
	if s := Suggestion(a, DefaultBiases()); !strings.Contains(s, "Deadline: relaxed") {
		t.Errorf("suggestion = %q, want a Deadline: relaxed hint", s)
	}
	already := DefaultBiases()
	already.Deadline = Relaxed
	if s := Suggestion(a, already); !strings.Contains(s, "already leans") {
		t.Errorf("suggestion with Deadline already relaxed = %q, want an 'already leans' note", s)
	}
}

func TestAnalyzeSeqMissesMomentumIntenseHint(t *testing.T) {
	base := fixedNow
	// Hits: cold, no due dates.
	hit1 := seqDone("hit1", 2, base.Add(-30*24*time.Hour))
	// Misses: each commented on an hour before completion → task heat →
	// Momentum 10 at their completion moments, while the hit stays cold.
	mkMiss := func(id string, rank int, at time.Time) todo.Todo {
		m := seqDone(id, rank, at)
		m.Comments = []todo.Comment{{CreatedAt: at.Add(-time.Hour)}}
		return m
	}
	miss1 := mkMiss("miss1", 7, base.Add(-3*24*time.Hour))
	miss2 := mkMiss("miss2", 11, base.Add(-2*24*time.Hour))
	miss3 := mkMiss("miss3", 9, base.Add(-1*24*time.Hour))

	a := AnalyzeMisses(todoPtrs([]todo.Todo{hit1, miss1, miss2, miss3}), todoPtrs([]todo.Todo{hit1, miss1, miss2, miss3}), HitWindow, DefaultBiases())

	if !approxEq(a.Gap[2], 10.0) {
		t.Fatalf("Momentum gap = %v, want +10", a.Gap[2])
	}
	if s := Suggestion(a, DefaultBiases()); !strings.Contains(s, "Momentum: intense") {
		t.Errorf("suggestion = %q, want a Momentum: intense hint", s)
	}
}

func TestSeqSuggestionGates(t *testing.T) {
	base := fixedNow
	// Two misses only — below the floor, no matter how clear the pattern.
	fewSet := []todo.Todo{
		seqDone("h", 1, base.Add(-3*24*time.Hour)),
		seqDone("m1", 9, base.Add(-2*24*time.Hour)),
		seqDone("m2", 8, base.Add(-1*24*time.Hour)),
	}
	few := AnalyzeMisses(todoPtrs(fewSet), todoPtrs(fewSet), HitWindow, DefaultBiases())
	if s := Suggestion(few, DefaultBiases()); s != "" {
		t.Errorf("suggestion with 2 misses = %q, want empty", s)
	}
	// Three misses with dims identical to the hit → calibrated message.
	flatSet := []todo.Todo{
		seqDone("h", 1, base.Add(-4*24*time.Hour)),
		seqDone("m1", 9, base.Add(-3*24*time.Hour)),
		seqDone("m2", 8, base.Add(-2*24*time.Hour)),
		seqDone("m3", 7, base.Add(-1*24*time.Hour)),
	}
	flat := AnalyzeMisses(todoPtrs(flatSet), todoPtrs(flatSet), HitWindow, DefaultBiases())
	if s := Suggestion(flat, DefaultBiases()); !strings.Contains(s, "calibrated") {
		t.Errorf("suggestion with flat gaps = %q, want the calibrated note", s)
	}
}

// A sort must score every task against ONE instant. Ranker.Score reads
// time.Now() per call and Age accrues continuously, so scoring tasks one at a
// time makes identical tasks differ by ~1e-11 — enough for the comparator to
// separate them on the float and never reach the ID tie-break, leaving the
// order of equal tasks up to sort.Slice. Guard the property directly, at the
// engine, rather than only through the CLI listing that exposed it.
func TestSequenceSortIsDeterministicForIdenticalTasks(t *testing.T) {
	created := time.Now().AddDate(0, 0, -5)
	build := func(ids []string) []*todo.Todo {
		out := make([]*todo.Todo, 0, len(ids))
		for _, id := range ids {
			x := todo.New("identical")
			x.ID = id
			x.CreatedAt, x.ModifiedAt = created, created
			cp := x
			out = append(out, &cp)
		}
		return out
	}
	forward := build([]string{"aaaa", "bbbb", "cccc"})
	shuffled := build([]string{"cccc", "aaaa", "bbbb"})
	SortPtrs(forward, nil, nil, Default().ScoreNow())
	SortPtrs(shuffled, nil, nil, Default().ScoreNow())
	for i := range forward {
		if forward[i].ID != shuffled[i].ID {
			t.Fatalf("same tasks in a different input order sorted differently: %s vs %s at %d",
				forward[i].ID, shuffled[i].ID, i)
		}
	}
	if forward[0].ID != "aaaa" {
		t.Errorf("tie-break did not fall through to ID: got %s first, want aaaa", forward[0].ID)
	}
}

// A task parked until a start date ages from that day, not from its creation:
// it was not being neglected while it waited.
func TestAgeCountsFromTheStartDate(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	parked := todo.New("parked")
	parked.CreatedAt = now.AddDate(0, 0, -40)
	parked.StartDate = now.AddDate(0, 0, -10)
	plain := todo.New("plain")
	plain.CreatedAt = startOfDay(now.AddDate(0, 0, -10)) // start dates are days
	b := DefaultBiases()
	if got, want := ComponentsAt(now, &parked, b, Heat{}).Age, ComponentsAt(now, &plain, b, Heat{}).Age; !approxEq(got, want) {
		t.Errorf("a task started 10 days ago ages like one created then: age %.3f, want %.3f", got, want)
	}
	early := todo.New("start before creation")
	early.CreatedAt = plain.CreatedAt
	early.StartDate = now.AddDate(0, 0, -30)
	if got, want := ComponentsAt(now, &early, b, Heat{}).Age, ComponentsAt(now, &plain, b, Heat{}).Age; !approxEq(got, want) {
		t.Errorf("a start date before creation should not add age: %.3f, want %.3f", got, want)
	}
}

// The 100% mark is the best task that can be picked up today: one waiting for
// a later start date is not in the field, however high it would score.
func TestWaitingTasksAreNotTheTopOfTheScale(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	waiting := todo.New("waiting")
	waiting.Priority = todo.PriorityHigh
	waiting.StartDate = now.AddDate(0, 0, 3)
	ready := todo.New("ready")
	ready.Priority = todo.PriorityLow
	all := []*todo.Todo{&waiting, &ready}
	score := func(x *todo.Todo) float64 { return ComponentsAt(now, x, DefaultBiases(), Heat{}).Total }
	if got, want := MaxScore(all, score, now), score(&ready); !approxEq(got, want) {
		t.Errorf("100%% mark = %.3f, want the ready task's %.3f", got, want)
	}
}
