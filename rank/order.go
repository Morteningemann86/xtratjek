package rank

import (
	"sort"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// order.go turns scores into an order: the lifts a task inherits from its
// subtasks and from the work waiting on it, the partition that sinks blocked
// work, and the sort itself.

// Lifts returns the lift map the sequence ranking sorts by: per task, the
// best score found among its subtasks and among the pending work waiting on it.
// It is the two rollups in the order they compose — a dependent's own lift has
// to be settled before it can be passed on to what blocks it.
//
// The map holds candidates, not final scores: a task appears only when
// something lifted it, and the value is applied with ScoreOf's max.
func Lifts(todos []*todo.Todo, score func(*todo.Todo) float64) map[string]float64 {
	return DependencyRollup(todos, DescendantRollup(todos, score), score)
}

// ScoreOf is the score the sequencer ranks t by, and — since a rank the
// list cannot explain reads as a bug — the score every surface shows next to
// its position. A task that unblocks urgent work is doing that work's job:
// ranking it high while printing its own low score put the two halves of one
// row at odds, and the row looked misplaced rather than promoted.
func ScoreOf(t *todo.Todo, rollup map[string]float64, score func(*todo.Todo) float64) float64 {
	s := score(t)
	if rollup != nil {
		if lift, ok := rollup[t.ID]; ok && lift > s {
			return lift
		}
	}
	return s
}

// DependencySets returns, from the full task set, the tasks waiting on an
// unfinished dependency and the tasks holding others up. A task is "blocked" if
// any task it depends on is still pending (not Done); that depended-on task is
// in turn a "blocker". Dependencies on a Done or deleted task don't count —
// they're already cleared — so a dangling/finished dep never blocks.
//
// One definition, two readers: the cache renders from it, and the sequence sort
// sinks the blocked half below the work that can actually be started.
func DependencySets(all []*todo.Todo) (blocked, blocker map[string]bool) {
	blocked = make(map[string]bool)
	blocker = make(map[string]bool)
	pending := make(map[string]bool, len(all))
	for i := range all {
		// The TUI's store never holds deleted tasks; the CLI's load path can,
		// so the guard is what lets both share this one rule.
		if all[i].Status != todo.Done && !all[i].Deleted {
			pending[all[i].ID] = true
		}
	}
	for i := range all {
		if all[i].Status == todo.Done {
			continue
		}
		for _, depID := range all[i].Dependencies {
			if pending[depID] {
				blocked[all[i].ID] = true
				blocker[depID] = true
			}
		}
	}
	return blocked, blocker
}

// StartsLater reports whether t is pending with a start date on a later day
// than now. Start dates are calendar days, like due dates, so a task set to
// start tomorrow comes back at midnight rather than at the time of day
// SetStartDate stamped on it.
func StartsLater(t *todo.Todo, now time.Time) bool {
	return t.Status != todo.Done && !t.StartDate.IsZero() &&
		startOfDay(t.StartDate).After(startOfDay(now))
}

// Sunk is the set the sequence sort ranks below the work that can be picked up
// today: blocked (from DependencySets), plus every task in todos that
// StartsLater. blocked is not modified; the cache still renders it as the
// dependency marker, which a start date is not.
func Sunk(blocked map[string]bool, todos []*todo.Todo, now time.Time) map[string]bool {
	out := make(map[string]bool, len(blocked))
	for id, b := range blocked {
		out[id] = b
	}
	for _, t := range todos {
		if StartsLater(t, now) {
			out[t.ID] = true
		}
	}
	return out
}

// DescendantRollup walks the full task slice and returns, per top-level
// ID, the max score observed across all of its transitive subtasks.
// Pure: builds its own parent index in one pass and follows ParentID chains
// instead of relying on the model's subtaskOf cache. Tasks without subtasks
// don't appear in the map.
func DescendantRollup(todos []*todo.Todo, score func(*todo.Todo) float64) map[string]float64 {
	if len(todos) == 0 {
		return nil
	}
	idx := make(map[string]int, len(todos))
	for i := range todos {
		idx[todos[i].ID] = i
	}
	rollup := make(map[string]float64, len(todos))
	for i := range todos {
		if todos[i].ParentID == "" {
			continue
		}
		// Walk up to the top-level ancestor, lifting the boost at every
		// level so a deeply-nested high-pri grandchild reaches the root.
		s := score(todos[i])
		cur := todos[i].ParentID
		for cur != "" {
			if rollup[cur] < s {
				rollup[cur] = s
			}
			pi, ok := idx[cur]
			if !ok {
				break
			}
			cur = todos[pi].ParentID
		}
	}
	return rollup
}

// DependencyRollup augments base (the subtask rollup) with dependency
// boosts: a still-pending task that another pending task depends on inherits
// that dependent's urgency, so a blocker can't sort below the work it's holding
// up — the prerequisite for an urgent task surfaces right above it (critical-path
// behaviour). Propagation is transitive (a chain lifts end-to-end) and cycle-safe.
// effBase is max(own score, subtask rollup), so subtask and dependency boosts
// compose. Returns base unchanged when no task depends on a pending one.
//
// On top of the max-inheritance, each blocker earns a fan-out bonus: +0.5 per
// distinct pending task it directly unblocks, capped at +2. Inheritance alone
// is max, not sum — unblocking four tasks would score the same as unblocking
// one — so the bonus is the leverage signal that prefers the wider blocker.
// It compounds mildly along a chain (each hop adds its own bonus), which reads
// as intended: a longer chain is more leverage. The cap keeps sheer task count
// from gaming the ranking.
//
// depBoostEpsilon is the per-edge nudge that keeps a boosted blocker strictly
// ahead of the dependent it inherited from.
const (
	depBoostEpsilon = 0.001
	fanOutBonusPer  = 0.5
	fanOutBonusCap  = 2.0
)

func DependencyRollup(todos []*todo.Todo, base map[string]float64, score func(*todo.Todo) float64) map[string]float64 {
	if len(todos) == 0 {
		return base
	}
	pending := make(map[string]bool, len(todos))
	for i := range todos {
		if todos[i].Status != todo.Done {
			pending[todos[i].ID] = true
		}
	}
	// dependents maps a pending task to the pending tasks that depend on it.
	dependents := make(map[string][]string)
	for i := range todos {
		if todos[i].Status == todo.Done {
			continue
		}
		for _, depID := range todos[i].Dependencies {
			if pending[depID] {
				dependents[depID] = append(dependents[depID], todos[i].ID)
			}
		}
	}
	if len(dependents) == 0 {
		return base
	}
	idx := make(map[string]int, len(todos))
	for i := range todos {
		idx[todos[i].ID] = i
	}
	effBase := func(id string) float64 {
		s := score(todos[idx[id]])
		if b, ok := base[id]; ok && b > s {
			s = b
		}
		return s
	}
	// eff(id) = max(effBase(id), max eff over its dependents). Memoised DFS;
	// visiting guards back-edges so a dependency cycle terminates.
	eff := make(map[string]float64, len(dependents))
	visiting := make(map[string]bool)
	var compute func(id string) float64
	compute = func(id string) float64 {
		if v, ok := eff[id]; ok {
			return v
		}
		best := effBase(id)
		if visiting[id] {
			return best
		}
		visiting[id] = true
		for _, dep := range dependents[id] {
			// + epsilon so a blocker sorts strictly above its dependent rather
			// than merely tying (the score-tie backstop is the tie-break chain,
			// which could otherwise place the dependent first). Compounds per
			// chain hop; far below the %.1f the score column rounds to, so
			// invisible.
			if s := compute(dep) + depBoostEpsilon; s > best {
				best = s
			}
		}
		visiting[id] = false
		if bonus := fanOutBonusPer * float64(len(dependents[id])); bonus > 0 {
			if bonus > fanOutBonusCap {
				bonus = fanOutBonusCap
			}
			best += bonus
		}
		eff[id] = best
		return best
	}
	out := make(map[string]float64, len(base)+len(dependents))
	for k, v := range base {
		out[k] = v
	}
	for id := range dependents {
		if s := compute(id); s > out[id] {
			out[id] = s
		}
	}
	return out
}

// LessTie is the tie-break chain applied once two tasks score equal,
// each key meaningful rather than arbitrary:
//  1. due proximity (a real due date beats none; sooner beats later)
//  2. size ascending (the quick win first)
//  3. CreatedAt ascending (tasks entered as a burst — a decomposed plan typed
//     in execution order — keep that entry order)
//  4. ID — the absolute backstop so tasks identical on every key (common in
//     the Done list, where score is uniformly 0) don't inherit the random
//     order they came out of Store.allTodos in, which would otherwise
//     reshuffle them on every cache rebuild (e.g. while the search-input
//     cursor blinks).
func LessTie(a, b *todo.Todo) bool {
	aZero, bZero := a.DueDate.IsZero(), b.DueDate.IsZero()
	if aZero != bZero {
		return bZero
	}
	if !aZero && !a.DueDate.Equal(b.DueDate) {
		return a.DueDate.Before(b.DueDate)
	}
	if ra, rb := a.Size.Rank(), b.Size.Rank(); ra != rb {
		return ra < rb
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// SortPtrs sorts by sequence score descending, then the tie-break
// chain. The score is computed once per task into the slice being sorted, so
// the comparator reads a float field instead of hashing an ID into a score map
// on every comparison.
//
// sunk (nil when the caller has no such set on hand; usually Sunk's result)
// partitions ahead of the score: work waiting on an unfinished dependency, or
// not due to start until a later day, sorts below work that can be started,
// however urgent it is. A list whose top is always something
// you can pick up right now is the whole point of the ranking — and it is what
// lets the row drop its blocked marker from the status column, since position
// now carries the fact. Within each half the ordering is unchanged, so a
// blocker still outranks what it holds up.
func SortPtrs(todos []*todo.Todo, rollup map[string]float64, sunk map[string]bool, score func(*todo.Todo) float64) {
	if len(todos) <= 1 {
		return
	}
	type scored struct {
		t     *todo.Todo
		score float64
		sunk  bool
	}
	rows := make([]scored, len(todos))
	for i, t := range todos {
		rows[i] = scored{t, ScoreOf(t, rollup, score), sunk[t.ID]}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].sunk != rows[j].sunk {
			return rows[j].sunk
		}
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return LessTie(rows[i].t, rows[j].t)
	})
	for i := range rows {
		todos[i] = rows[i].t
	}
}

// SortValues is the sequence-mode sort, with an optional
// per-ID rollup map that boosts each task's effective score to
// max(own, rollup[id]). The boost is how a parent inherits the urgency of
// its highest-priority subtask — so a "high" subtask buried under a "low"
// parent doesn't disappear into the bottom of the list. Passing nil
// preserves the original behaviour (used by callers that don't have the
// child set on hand, e.g. on-disk loads).
func SortValues(todos []todo.Todo, rollup map[string]float64, sunk map[string]bool, score func(*todo.Todo) float64) {
	if len(todos) <= 1 {
		return
	}
	// Sort the pointers, then permute the values once: the same ordering with
	// 8-byte swaps instead of 416-byte ones.
	ptrs := todoPtrs(todos)
	SortPtrs(ptrs, rollup, sunk, score)
	sorted := make([]todo.Todo, len(todos))
	for i, t := range ptrs {
		sorted[i] = *t
	}
	copy(todos, sorted)
}

// Top returns the top-level pending tasks ranked exactly as the TUI's
// Sequence sort ranks them: each task's base score
// lifted by the subtask and dependency critical-path rollups, so a parent
// inherits its subtasks' urgency and a blocker inherits the urgency of the work
// it holds up. The rollup is computed from the full set — it needs subtasks and
// dependency targets, not just the top-level rows. Pure; the caller applies any
// -n limit. `tjek top`'s displayed SCORE stays each task's own score (matching
// the TUI); only the ordering reflects the boost.
func (r Ranker) Top(todos []*todo.Todo) []todo.Todo {
	now := time.Now()
	return TopBy(todos, now, r.ScoreAt(now))
}

// TopBy is the shared implementation behind Top and TopWith. It accepts an
// arbitrary score function so callers can supply knob values that are not
// live yet (the preview path) or the live ranker (the CLI and TUI paths). The rollup and sort logic — subtask inheritance, critical-path
// dependency boost, fan-out bonus, cycle-safe DFS — is identical for both.
func TopBy(todos []*todo.Todo, now time.Time, score func(*todo.Todo) float64) []todo.Todo {
	// Ranking (explain.go) is the same fold; it also hands back the
	// effective score each row sorted by, which only the explain view needs.
	rows, _ := Ranking(todos, now, score)
	return rows
}

// todoPtrs views a value slice as pointers into it.
func todoPtrs(todos []todo.Todo) []*todo.Todo {
	out := make([]*todo.Todo, len(todos))
	for i := range todos {
		out[i] = &todos[i]
	}
	return out
}

// startOfDay is local midnight on t's date.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
