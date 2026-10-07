package rank

import (
	"math"
	"sort"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// explain.go answers the two questions the score column raises but
// cannot answer on its own: why is this task *here*, and what is going to move
// it. A ranking nobody can predict is a ranking nobody trusts — and this engine
// has two ways of reordering a list with no edit from the user at all: the
// Deadline term steps every midnight, and the Momentum term expires exactly
// MomentumWindow after the last signal. Both are computable in advance, so the
// explain view states them as facts rather than leaving the reshuffle to be
// discovered the next morning.
//
// Everything here is pure and locale-free, like the rest of the scoring code:
// reasons are returned as codes (Reason) and become sentences at the view
// layer (trSeqReason, in the app's view_explain.go).

// ── Reasons ───────────────────────────────────────────────────────────────────

// Reason identifies the sentence behind one factor's value.
type Reason int

const (
	ReasonNone Reason = iota
	ReasonNoDue
	ReasonOverdue       // N = whole days past the due date
	ReasonDueToday      //
	ReasonDueTomorrow   //
	ReasonDueInDays     // N = days out, inside the 7-day ramp
	ReasonDueBeyondRamp // N = days out, past the ramp (scores 0)
	ReasonPriority      // Word = the English priority word
	ReasonMomentumTask  // this task itself was worked on
	ReasonMomentumProj  // Word = the project that saw the activity
	ReasonMomentumTag   // Word = the tag that saw the activity
	ReasonMomentumCold  //
	ReasonSize          // Word = the English size word
	ReasonAgeDays       // N = days since creation
	ReasonAgeToday      //
	ReasonAgeOff        // aging switched off in Settings
)

// Factor is one row of the breakdown: the raw 0–10 axis reading, the bias it
// was multiplied by, the points that reached the total, and why the axis reads
// what it does.
type Factor struct {
	Name     string // one of DimNames
	Raw      float64
	Weight   float64 // the bias multiplier; 1 for the unweighted terms
	Weighted float64 // Raw × Weight — the points in Total
	Bias     Level
	Knobbed  bool // Deadline/Priority/Momentum have a Settings knob; Size/Age don't
	Reason   Reason
	N        int
	Word     string
}

// ── Shifts (what moves on its own) ────────────────────────────────────────────

type ShiftCause int

const (
	ShiftMidnight ShiftCause = iota // the Deadline ramp steps a day closer
	ShiftMomentum                   // an activity signal ages out of the window
)

// Shift is a predicted future state of this task: at `At` the score becomes
// Total and the task sits at rank Pos, with nothing required from the user.
type Shift struct {
	At    time.Time
	Cause ShiftCause
	Total float64
	Delta float64 // change from the previously reported state
	Pos   int     // rank at that moment, 0 if it drops out of the ranking
}

// Neighbour is the row directly above or below in the ranking, with the
// margin that separates it. Gap is always positive: the points this task would
// have to gain to pass Above, or the cushion it holds over Below.
type Neighbour struct {
	Pos   int
	Title string
	Score float64
	Gap   float64
}

// Explanation is the whole answer for one task.
type Explanation struct {
	Now     time.Time
	Title   string
	Done    bool    // done tasks score 0 by rule; the breakdown would be zeros
	Total   float64 // this task's own score
	Ranked  float64 // the score the ranking actually sorted it by
	Boosted bool    // Ranked > Total — a subtask or a blocked dependent lifted it
	Pos     int     // 1-based rank among top-level pending tasks, 0 if unranked
	Of      int
	// FieldMax is the highest score in the field at this moment — the 100%
	// mark the displayed percentage is relative to. Carried on the explanation
	// so the overlay can state what 100% currently costs in points, which is
	// what keeps a moving scale from being a mysterious one.
	FieldMax float64
	Factors  []Factor
	Above    *Neighbour
	Below    *Neighbour
	Shifts   []Shift
	// StartsOn is the start date when it falls on a later day (StartsLater):
	// the ranking sinks the task below today's work whatever it scores.
	StartsOn time.Time
}

// ── Ranking with its effective scores ─────────────────────────────────────────

// Ranking is the ordering `tjek top` and the Sequence sort produce, paired
// with the score each row was actually sorted by. That effective score is
// max(own score, rollup) — a parent lifted by a subtask, or a blocker lifted by
// what it blocks, ranks on the inherited number, so quoting the own-score as
// the margin to a neighbour would be quoting the wrong one.
func Ranking(todos []*todo.Todo, now time.Time, score func(*todo.Todo) float64) ([]todo.Todo, map[string]float64) {
	rollup := DescendantRollup(todos, score)
	rollup = DependencyRollup(todos, rollup, score)
	rows := make([]todo.Todo, 0, len(todos))
	for _, t := range todos {
		if t.ParentID == "" && t.Status == todo.Pending {
			rows = append(rows, *t)
		}
	}
	// Same partition the live list applies, or `tjek top` and the explain
	// view would rank a blocked task the list has already pushed to the bottom.
	blocked, _ := DependencySets(todos)
	SortValues(rows, rollup, Sunk(blocked, todos, now), score)
	eff := make(map[string]float64, len(rows))
	for i := range rows {
		eff[rows[i].ID] = ScoreOf(&rows[i], rollup, score)
	}
	return rows, eff
}

// ── Factors ───────────────────────────────────────────────────────────────────

// FactorsAt breaks the score into its five rows for the given moment. The
// arithmetic is deliberately the same call the live scorer makes, so what the
// explanation shows and what the sort used cannot drift.
func FactorsAt(now time.Time, t *todo.Todo, b Biases, heat Heat) []Factor {
	u, i, m, size, age := dimensionsAt(now, t, heat)
	if !b.Aging {
		age = 0
	}
	dueReason, dueN := deadlineReason(now, t.DueDate)
	momReason, momWord := momentumReason(t, heat)
	ageReason, ageN := agingReason(now, t.CreatedAt, b.Aging)

	knob := func(name string, raw float64, level Level, reason Reason, n int, word string) Factor {
		return Factor{
			Name: name, Raw: raw, Weight: level.Weight(), Weighted: raw * level.Weight(),
			Bias: level, Knobbed: true, Reason: reason, N: n, Word: word,
		}
	}
	flat := func(name string, v float64, reason Reason, n int, word string) Factor {
		return Factor{Name: name, Raw: v, Weight: 1, Weighted: v, Bias: Balanced, Reason: reason, N: n, Word: word}
	}
	return []Factor{
		knob(DimNames[0], u, b.Deadline, dueReason, dueN, ""),
		knob(DimNames[1], i, b.Priority, ReasonPriority, 0, t.Priority.String()),
		knob(DimNames[2], m, b.Momentum, momReason, 0, momWord),
		flat(DimNames[3], size, ReasonSize, 0, t.Size.String()),
		flat(DimNames[4], age, ageReason, ageN, ""),
	}
}

// deadlineReason names the branch of urgencyDim the task landed in. It counts
// days exactly the way urgencyDim does — start-of-day to start-of-day — so the
// sentence and the number can never disagree.
func deadlineReason(now, due time.Time) (Reason, int) {
	if due.IsZero() {
		return ReasonNoDue, 0
	}
	days := int(startOfDay(due).Sub(startOfDay(now)).Hours() / 24)
	switch {
	case days < 0:
		return ReasonOverdue, -days
	case days == 0:
		return ReasonDueToday, 0
	case days == 1:
		return ReasonDueTomorrow, 1
	case days <= 7:
		return ReasonDueInDays, days
	default:
		return ReasonDueBeyondRamp, days
	}
}

// momentumReason names which tier of momentumDim fired, and what warmed it —
// "the project you were in" is a far more useful answer than "10".
func momentumReason(t *todo.Todo, heat Heat) (Reason, string) {
	if hot(heat.tasks, t.ID) {
		return ReasonMomentumTask, ""
	}
	if hot(heat.projects, t.Project) {
		return ReasonMomentumProj, t.Project
	}
	for _, tag := range t.Tags {
		if hot(heat.tags, tag) {
			return ReasonMomentumTag, tag
		}
	}
	return ReasonMomentumCold, ""
}

func agingReason(now, created time.Time, aging bool) (Reason, int) {
	if !aging {
		return ReasonAgeOff, 0
	}
	days := int(now.Sub(created).Hours() / 24)
	if created.IsZero() || days < 1 {
		return ReasonAgeToday, 0
	}
	return ReasonAgeDays, days
}

// ── Forecast ──────────────────────────────────────────────────────────────────

// seqShiftHorizon bounds how far ahead the forecast looks. Both self-moving
// terms act inside two days (the next midnight, and MomentumWindow from the
// newest signal), so nothing useful lives past it.
const seqShiftHorizon = 3 * 24 * time.Hour

// seqShiftMinDelta is the score change worth a line on its own. Age alone
// drifts 0.1–0.2 a day, which moves nothing anyone can see; a rank change is
// reported regardless of size.
const seqShiftMinDelta = 0.5

// seqShiftMax caps the forecast so the overlay stays a screen.
const seqShiftMax = 3

// Forecast predicts the moments this task's standing changes with no
// user action: each midnight inside the horizon (the Deadline ramp steps) and
// each instant a signal feeding its Momentum ages out. Every candidate is
// scored by re-running the real scorer against the heat snapshot as it will
// stand then, and re-ranked against the real ranking, so a predicted position
// is the position the list will actually show.
func Forecast(now time.Time, t *todo.Todo, all []*todo.Todo, b Biases, heat Heat) []Shift {
	if t == nil || t.Status == todo.Done || t.ParentID != "" {
		return nil
	}
	type candidate struct {
		at    time.Time
		cause ShiftCause
	}
	var cands []candidate
	for day := startOfDay(now).AddDate(0, 0, 1); day.Sub(now) < seqShiftHorizon; day = day.AddDate(0, 0, 1) {
		cands = append(cands, candidate{day, ShiftMidnight})
	}
	for _, at := range HeatExpiries(t, heat, now) {
		if at.Sub(now) < seqShiftHorizon {
			cands = append(cands, candidate{at, ShiftMomentum})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].at.Before(cands[j].at) })

	prevTotal := ComponentsAt(now, t, b, heat).Total
	prevPos := rankOf(RankingAt(now, all, b, heat))(t.ID)
	var out []Shift
	for _, c := range cands {
		aged := heat.Expire(c.at)
		total := ComponentsAt(c.at, t, b, aged).Total
		pos := rankOf(RankingAt(c.at, all, b, aged))(t.ID)
		delta := total - prevTotal
		if pos == prevPos && math.Abs(delta) < seqShiftMinDelta {
			continue
		}
		out = append(out, Shift{At: c.at, Cause: c.cause, Total: total, Delta: delta, Pos: pos})
		prevTotal, prevPos = total, pos
		if len(out) == seqShiftMax {
			break
		}
	}
	return out
}

// RankingAt ranks the whole set with an explicit clock and heat snapshot —
// the same ordering the Tasks tab shows, evaluated at another moment.
func RankingAt(now time.Time, all []*todo.Todo, b Biases, heat Heat) []todo.Todo {
	rows, _ := Ranking(all, now, func(t *todo.Todo) float64 {
		return ComponentsAt(now, t, b, heat).Total
	})
	return rows
}

// rankOf returns a lookup from task ID to 1-based position (0 = not ranked).
func rankOf(rows []todo.Todo) func(string) int {
	return func(id string) int {
		for i := range rows {
			if rows[i].ID == id {
				return i + 1
			}
		}
		return 0
	}
}

// ── Assembly ──────────────────────────────────────────────────────────────────

// ExplainAt is the pure, testable whole: breakdown, position in the
// ranking, the margins to the neighbours on either side, and the forecast.
func ExplainAt(now time.Time, t *todo.Todo, all []*todo.Todo, b Biases, heat Heat) Explanation {
	if t == nil {
		return Explanation{Now: now}
	}
	e := Explanation{Now: now, Title: t.Title, Done: t.Status == todo.Done}
	e.Total = ComponentsAt(now, t, b, heat).Total
	e.Ranked = e.Total
	if e.Done {
		return e
	}
	e.Factors = FactorsAt(now, t, b, heat)
	if StartsLater(t, now) {
		e.StartsOn = t.StartDate
	}

	score := func(x *todo.Todo) float64 { return ComponentsAt(now, x, b, heat).Total }
	rows, eff := Ranking(all, now, score)
	e.Of = len(rows)
	// eff already carries the lifts, so the overlay's 100% is the list's 100%.
	e.FieldMax = MaxRanked(all, eff, score, now)
	for i := range rows {
		if rows[i].ID != t.ID {
			continue
		}
		e.Pos = i + 1
		e.Ranked = eff[t.ID]
		e.Boosted = e.Ranked > e.Total+0.0005
		if i > 0 {
			above := rows[i-1]
			e.Above = &Neighbour{Pos: i, Title: above.Title, Score: eff[above.ID], Gap: eff[above.ID] - e.Ranked}
		}
		if i+1 < len(rows) {
			below := rows[i+1]
			e.Below = &Neighbour{Pos: i + 2, Title: below.Title, Score: eff[below.ID], Gap: e.Ranked - eff[below.ID]}
		}
		break
	}
	e.Shifts = Forecast(now, t, all, b, heat)
	return e
}

// Explain is the live form: current clock and the ranker's biases and heat —
// what the TUI overlay and `tjek why` both call.
func (r Ranker) Explain(t *todo.Todo, all []*todo.Todo) Explanation {
	return ExplainAt(time.Now(), t, all, r.Biases, r.Heat)
}
