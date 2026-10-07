package rank

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// explain_test.go guards the promise the explain view makes: that what
// it says about a score is the score. An explanation that drifts from the
// arithmetic it describes is worse than no explanation — it teaches a model of
// the ranking that is wrong.

func balancedBiases() Biases {
	return Biases{Deadline: Balanced, Priority: Balanced, Momentum: Balanced, Aging: true}
}

// The five factors must add up to the number the sort used. Any future
// dimension that forgets to appear here shows up as a gap.
func TestExplainFactorsSumToTheScore(t *testing.T) {
	for _, b := range []Biases{
		balancedBiases(),
		{Deadline: Intense, Priority: Relaxed, Momentum: Intense, Aging: true},
		{Deadline: Relaxed, Priority: Relaxed, Momentum: Relaxed, Aging: false},
	} {
		tt := todo.New("sum check")
		tt.Priority = todo.PriorityHigh
		tt.Size = todo.SizeSmall
		tt.Project = "hot"
		tt.DueDate = fixedNow.AddDate(0, 0, 3)
		tt.CreatedAt = fixedNow.AddDate(0, 0, -40)
		heat := hotHeat(fixedNow, nil, []string{"hot"}, nil)

		want := ComponentsAt(fixedNow, &tt, b, heat).Total
		var sum float64
		for _, f := range FactorsAt(fixedNow, &tt, b, heat) {
			sum += f.Weighted
		}
		if !approxEq(sum, want) {
			t.Errorf("biases %+v: factors sum to %v, score is %v", b, sum, want)
		}
	}
}

// Each factor's sentence must describe the branch the number came from. The
// deadline axis has the most branches and the most room to disagree.
func TestExplainDeadlineReasonMatchesTheNumber(t *testing.T) {
	cases := []struct {
		name   string
		due    time.Time
		reason Reason
		n      int
	}{
		{"no date", time.Time{}, ReasonNoDue, 0},
		{"overdue", startOfDay(fixedNow).AddDate(0, 0, -4), ReasonOverdue, 4},
		{"today", fixedNow.Add(2 * time.Hour), ReasonDueToday, 0},
		{"tomorrow", startOfDay(fixedNow).AddDate(0, 0, 1), ReasonDueTomorrow, 1},
		{"inside the ramp", startOfDay(fixedNow).AddDate(0, 0, 5), ReasonDueInDays, 5},
		{"past the ramp", startOfDay(fixedNow).AddDate(0, 0, 20), ReasonDueBeyondRamp, 20},
	}
	for _, c := range cases {
		tt := todo.New(c.name)
		tt.DueDate = c.due
		f := FactorsAt(fixedNow, &tt, balancedBiases(), Heat{})[0]
		if f.Reason != c.reason || f.N != c.n {
			t.Errorf("%s: reason %v/%d, want %v/%d", c.name, f.Reason, f.N, c.reason, c.n)
		}
		// The sentence and the points have to agree about whether the deadline
		// contributed anything at all.
		scores := f.Weighted > 0
		says := c.reason != ReasonNoDue && c.reason != ReasonDueBeyondRamp
		if scores != says {
			t.Errorf("%s: scored %v but the reason (%v) reads otherwise", c.name, f.Weighted, c.reason)
		}
	}
}

func TestExplainMomentumNamesItsSource(t *testing.T) {
	heat := hotHeat(fixedNow, []string{"self"}, []string{"alpha"}, []string{"go"})
	mk := func(id, project string, tags ...string) *todo.Todo {
		tt := todo.New("x")
		tt.ID, tt.Project, tt.Tags = id, project, tags
		return &tt
	}
	cases := []struct {
		name   string
		t      *todo.Todo
		reason Reason
		word   string
	}{
		{"own work", mk("self", ""), ReasonMomentumTask, ""},
		{"project", mk("other", "alpha"), ReasonMomentumProj, "alpha"},
		{"tag", mk("other", "cold", "go"), ReasonMomentumTag, "go"},
		{"cold", mk("other", "cold", "rust"), ReasonMomentumCold, ""},
	}
	for _, c := range cases {
		f := FactorsAt(fixedNow, c.t, balancedBiases(), heat)[2]
		if f.Reason != c.reason || f.Word != c.word {
			t.Errorf("%s: got %v/%q, want %v/%q", c.name, f.Reason, f.Word, c.reason, c.word)
		}
	}
}

// The margins have to be the real ones: the gap to the row above is what the
// task would have to gain to pass it, measured on the scores the sort used.
func TestExplainQuotesRealMargins(t *testing.T) {
	high := todo.New("high priority")
	high.ID = "high"
	high.Priority = todo.PriorityHigh
	mid := todo.New("medium priority")
	mid.ID = "mid"
	mid.Priority = todo.PriorityMedium
	low := todo.New("low priority")
	low.ID = "low"
	low.Priority = todo.PriorityLow
	for _, tt := range []*todo.Todo{&high, &mid, &low} {
		tt.CreatedAt = fixedNow
	}
	all := []*todo.Todo{&high, &mid, &low}

	e := ExplainAt(fixedNow, &mid, all, balancedBiases(), Heat{})
	if e.Pos != 2 || e.Of != 3 {
		t.Fatalf("rank = #%d of %d, want #2 of 3", e.Pos, e.Of)
	}
	if e.Above == nil || e.Above.Title != high.Title || !approxEq(e.Above.Gap, 5) {
		t.Errorf("above = %+v, want the high-priority task 5.0 ahead", e.Above)
	}
	if e.Below == nil || e.Below.Title != low.Title || !approxEq(e.Below.Gap, 5) {
		t.Errorf("below = %+v, want the low-priority task 5.0 behind", e.Below)
	}
}

// A parent ranked on a subtask's score must say so rather than showing its own
// number next to a position that number does not explain.
func TestExplainFlagsARollupBoost(t *testing.T) {
	parent := todo.New("calm parent")
	parent.ID = "parent"
	parent.Priority = todo.PriorityLow
	parent.CreatedAt = fixedNow
	child := todo.New("urgent child")
	child.ID = "child"
	child.ParentID = "parent"
	child.Priority = todo.PriorityHigh
	child.CreatedAt = fixedNow
	all := []*todo.Todo{&parent, &child}

	e := ExplainAt(fixedNow, &parent, all, balancedBiases(), Heat{})
	if !e.Boosted {
		t.Fatalf("parent lifted by its subtask did not report a boost (own %.1f, ranked %.1f)", e.Total, e.Ranked)
	}
	if e.Ranked <= e.Total {
		t.Errorf("ranked on %.1f, own score %.1f — a boost must rank higher", e.Ranked, e.Total)
	}
}

// The forecast is the whole point: the two things that reshuffle a list with
// nobody touching it are the midnight deadline step and momentum expiring.
func TestForecastReportsTheMidnightDeadlineStep(t *testing.T) {
	tt := todo.New("due in three days")
	tt.ID = "due"
	tt.CreatedAt = fixedNow
	tt.DueDate = startOfDay(fixedNow).AddDate(0, 0, 3)

	shifts := Forecast(fixedNow, &tt, []*todo.Todo{&tt}, balancedBiases(), Heat{})
	if len(shifts) == 0 {
		t.Fatal("a task inside the deadline ramp reported no upcoming change")
	}
	first := shifts[0]
	if first.Cause != ShiftMidnight {
		t.Errorf("first shift cause = %v, want the midnight step", first.Cause)
	}
	if !first.At.Equal(startOfDay(fixedNow).AddDate(0, 0, 1)) {
		t.Errorf("first shift at %v, want the next midnight", first.At)
	}
	if first.Delta <= 0 {
		t.Errorf("a day closer to the deadline scored %+.2f, want a gain", first.Delta)
	}
}

func TestForecastReportsMomentumRunningOut(t *testing.T) {
	// Warm only through the project, with the signal 40h old: it expires 8h
	// from now, well before any deadline effect could explain the drop.
	signal := fixedNow.Add(-40 * time.Hour)
	tt := todo.New("carried by a warm project")
	tt.ID = "warm"
	tt.Project = "alpha"
	tt.CreatedAt = fixedNow
	heat := hotHeat(signal, nil, []string{"alpha"}, nil)

	shifts := Forecast(fixedNow, &tt, []*todo.Todo{&tt}, balancedBiases(), heat)
	var found *Shift
	for i := range shifts {
		if shifts[i].Cause == ShiftMomentum {
			found = &shifts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("momentum expiry was not forecast; shifts = %+v", shifts)
	}
	if want := signal.Add(MomentumWindow); !found.At.Equal(want) {
		t.Errorf("momentum expiry at %v, want %v (signal + 48h)", found.At, want)
	}
	// Age keeps accruing across the eight hours, so the drop is the whole
	// Balanced momentum axis less that day's fraction — not exactly -10.
	if found.Delta > -9.9 || found.Delta < -10.1 {
		t.Errorf("momentum expiry delta = %+.2f, want ~-10 (the whole Balanced axis)", found.Delta)
	}
}

// A stable task must say it is stable rather than inventing a change: age
// drift alone moves 0.1/day and reorders nothing.
func TestForecastStaysQuietForAStableTask(t *testing.T) {
	tt := todo.New("no deadline, no heat")
	tt.ID = "quiet"
	tt.CreatedAt = fixedNow.AddDate(0, 0, -3)

	if shifts := Forecast(fixedNow, &tt, []*todo.Todo{&tt}, balancedBiases(), Heat{}); len(shifts) != 0 {
		t.Errorf("a task with no deadline and no heat forecast %d change(s): %+v", len(shifts), shifts)
	}
}

// ── The overlay ───────────────────────────────────────────────────────────────
