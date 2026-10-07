// Package rank is the sequencing engine: the rule that decides the "Sequence"
// sort order and the value persisted in the todos.sequence column on every save.
//
// The score is the design's Normalized Power Scale: three 0–10 dimensions,
// each multiplied by a user-tunable bias (Relaxed=0.5, Balanced=1.0,
// Intense=2.0), plus two small unweighted terms — Size and Age — so quick
// wins edge ahead of equal peers and old tasks always eventually surface for
// cleanup or completion.
//
//	Score = U·Wd + I·Wp + M·Wm + Size + Age
//
//	U  Urgency    closeness to deadline (0..10+)
//	I  Importance priority bucket (0/5/10)
//	M  Momentum   activity heat: 10 when the task or its project saw activity
//	              (completion, timer, comment) inside MomentumWindow, 5 when
//	              only one of its tags did, 0 cold
//	Size          quick-win nudge (S=2, M=1, L=0)
//	Age           rot-guard: +0.1/day, +0.2/day past 30, counted from the
//	              start date when that is later than creation
//	Wd Wp Wm      Deadline / Priority / Momentum bias multipliers
//
// Done tasks score 0.
package rank

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── Bias level ────────────────────────────────────────────────────────────────

// Level is the three-state user-facing knob exposed in Settings. The
// numbers are deliberately symmetric around Balanced so cycling left/right
// doubles or halves the dimension's voting weight.
type Level int

const (
	Balanced Level = iota // weight 1.0 — the design's "neutral middleground"
	Relaxed               // weight 0.5
	Intense               // weight 2.0
)

func (b Level) Weight() float64 {
	switch b {
	case Relaxed:
		return 0.5
	case Intense:
		return 2.0
	default:
		return 1.0
	}
}

func (b Level) String() string {
	switch b {
	case Relaxed:
		return "relaxed"
	case Intense:
		return "intense"
	default:
		return "balanced"
	}
}

// Next cycles Relaxed → Balanced → Intense → Relaxed.
func (b Level) Next() Level {
	switch b {
	case Relaxed:
		return Balanced
	case Balanced:
		return Intense
	default:
		return Relaxed
	}
}

// Prev cycles in the opposite direction so ←/→ in Settings are symmetric.
func (b Level) Prev() Level {
	switch b {
	case Intense:
		return Balanced
	case Balanced:
		return Relaxed
	default:
		return Intense
	}
}

// ── Biases (the user setting) ─────────────────────────────────────────────────

type Biases struct {
	Deadline Level
	Priority Level
	Momentum Level
	// Aging gates the per-day Age contribution. true (default) keeps the rot
	// guard on; toggling off zeros the Age term so a brand-new task and a
	// year-old task with the same Deadline/Priority/Momentum score identically.
	Aging bool
}

// DefaultBiases is the all-Balanced, aging-on configuration that the engine
// boots into before settings.json is read.
func DefaultBiases() Biases {
	return Biases{Aging: true}
}

// CycleLevel is the user-facing knob bound to ←/→ on a Settings bias row.
// Direction +1 cycles Relaxed→Balanced→Intense (next), -1 the other way. After
// changing a bias the caller is responsible for invalidating the task cache so
// the new weights take effect on the next render.
func CycleLevel(b Level, direction int) Level {
	if direction < 0 {
		return b.Prev()
	}
	return b.Next()
}

// ── Activity heat (the Momentum signal) ──────────────────────────────────────

// MomentumWindow is how far back an activity signal still counts as "recent".
const MomentumWindow = 48 * time.Hour

// Heat is the recent-activity snapshot the Momentum dimension reads:
// which tasks, projects, and tags saw a completion, a time entry, or a comment
// inside MomentumWindow. The zero value means everything is cold (momentum 0),
// which is what a process that never computes heat — the sync server
// persisting merged rows — correctly falls back to; every user-facing surface
// recomputes it on load or cache refresh.
//
// A key's presence is the hot/cold answer scoring needs; the value it maps to
// is the newest signal behind it, which is what says *when* that heat runs out.
// Momentum is the one dimension that changes with no user action at all — it
// expires MomentumWindow after the last signal — so a list can reshuffle
// overnight with nothing to point at. Recording the instant lets the explain
// view name it in advance (see expire / HeatExpiries).
type Heat struct {
	tasks    map[string]time.Time
	projects map[string]time.Time
	tags     map[string]time.Time
}

// hot reports whether a heat map carries a live signal for key. Presence is the
// answer: both builders only record signals already inside the window, and
// Expire drops the ones that have aged out.
func hot(m map[string]time.Time, key string) bool {
	if key == "" {
		return false
	}
	_, ok := m[key]
	return ok
}

// ComputeHeat scans the full task set (done tasks included — their
// completions are the strongest signal) and marks the task, its project, and
// its tags hot when any signal lands inside the window ending at `now`, with
// the newest such signal as the value.
func ComputeHeat(now time.Time, todos []*todo.Todo) Heat {
	cutoff := now.Add(-MomentumWindow)
	return scanHeat(todos, func(t *todo.Todo) time.Time {
		var newest time.Time
		bump := func(ts time.Time) {
			if !ts.IsZero() && ts.After(cutoff) && ts.After(newest) {
				newest = ts
			}
		}
		bump(t.CompletedAt)
		for _, c := range t.Comments {
			if c.DeletedAt.IsZero() {
				bump(c.CreatedAt)
				bump(c.ModifiedAt)
			}
		}
		for _, e := range t.TimeEntries {
			if e.IsRunning() {
				// A running timer cannot go cold while it runs, so its signal
				// is always "now" — a full window out, refreshed every refresh.
				bump(now)
				continue
			}
			bump(e.StartedAt)
			bump(e.StoppedAt)
		}
		return newest
	})
}

// ComputeHeatAt reconstructs the heat snapshot as it stood at a past
// moment `at` — used by the stats --seq miss analysis to re-score a completion
// with the momentum signal its rank stamp actually saw. Unlike the live
// ComputeHeat it bounds signals STRICTLY before `at`: the completion
// being analyzed lands at exactly `at`, and CaptureRankAtDone stamps the
// rank before Toggle flips the status, so the task's own completion must not
// count toward its own momentum. The live path keeps its open upper edge on
// purpose — cross-device clock skew after a sync can put a legitimate hot
// signal slightly in the future, and dropping it there would be wrong.
func ComputeHeatAt(at time.Time, todos []*todo.Todo) Heat {
	cutoff := at.Add(-MomentumWindow)
	return scanHeat(todos, func(t *todo.Todo) time.Time {
		var newest time.Time
		bump := func(ts time.Time) {
			if !ts.IsZero() && ts.After(cutoff) && ts.Before(at) && ts.After(newest) {
				newest = ts
			}
		}
		bump(t.CompletedAt)
		for _, c := range t.Comments {
			if c.DeletedAt.IsZero() {
				bump(c.CreatedAt)
				bump(c.ModifiedAt)
			}
		}
		for _, e := range t.TimeEntries {
			// A time entry is a signal if it overlapped the window at all:
			// started before `at` and not stopped before the window opened.
			// (IsRunning is a *current* fact, meaningless for a past moment.)
			if !e.StartedAt.IsZero() && e.StartedAt.Before(at) &&
				(e.StoppedAt.IsZero() || e.StoppedAt.After(cutoff)) {
				// The signal counted until it stopped, or until `at` for an
				// entry still open then.
				ts := e.StoppedAt
				if ts.IsZero() || ts.After(at) {
					ts = at
				}
				if ts.After(newest) {
					newest = ts
				}
			}
		}
		return newest
	})
}

// scanHeat builds a Heat by asking `latest` for each live task's newest
// in-window signal and marking the task, its project, and its tags with it. A
// zero time means cold and records nothing. The two heat builders above share
// it; only their notion of "recent" differs.
func scanHeat(todos []*todo.Todo, latest func(*todo.Todo) time.Time) Heat {
	h := Heat{
		tasks:    make(map[string]time.Time),
		projects: make(map[string]time.Time),
		tags:     make(map[string]time.Time),
	}
	mark := func(m map[string]time.Time, key string, ts time.Time) {
		if key == "" {
			return
		}
		if prev, ok := m[key]; !ok || ts.After(prev) {
			m[key] = ts
		}
	}
	for _, t := range todos {
		if t.Deleted {
			continue
		}
		ts := latest(t)
		if ts.IsZero() {
			continue
		}
		mark(h.tasks, t.ID, ts)
		mark(h.projects, t.Project, ts)
		for _, tag := range t.Tags {
			mark(h.tags, tag, ts)
		}
	}
	return h
}

// Expire returns the snapshot as it will stand at `future`: every signal that
// will have aged out of MomentumWindow by then is dropped. Scoring a task
// against an expired snapshot is how the explain view forecasts the moment
// momentum stops holding a task up — the reshuffle nobody triggered.
func (h Heat) Expire(future time.Time) Heat {
	cutoff := future.Add(-MomentumWindow)
	keep := func(m map[string]time.Time) map[string]time.Time {
		out := make(map[string]time.Time, len(m))
		for k, ts := range m {
			if ts.After(cutoff) {
				out[k] = ts
			}
		}
		return out
	}
	return Heat{tasks: keep(h.tasks), projects: keep(h.projects), tags: keep(h.tags)}
}

// HeatExpiries returns the distinct future instants at which a signal feeding
// this task's momentum ages out — the candidate moments its Momentum term can
// drop. Signals already expired (or with no bearing on this task) are skipped.
func HeatExpiries(t *todo.Todo, h Heat, now time.Time) []time.Time {
	var out []time.Time
	seen := map[time.Time]bool{}
	add := func(m map[string]time.Time, key string) {
		ts, ok := m[key]
		if !ok {
			return
		}
		at := ts.Add(MomentumWindow)
		if !at.After(now) || seen[at] {
			return
		}
		seen[at] = true
		out = append(out, at)
	}
	add(h.tasks, t.ID)
	add(h.projects, t.Project)
	for _, tag := range t.Tags {
		add(h.tags, tag)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// ── Per-dimension contributions ──────────────────────────────────────────────

// dimensionsAt is the pure core of the formula: given `now`, a task, and the
// activity-heat snapshot, return the five un-weighted dimension scores.
// Splitting `now` and `heat` out lets tests pin both without monkey-patching.
func dimensionsAt(now time.Time, t *todo.Todo, heat Heat) (u, i, m, size, age float64) {
	if t == nil || t.Status == todo.Done {
		return 0, 0, 0, 0, 0
	}
	return rawDimensionsAt(now, t, heat)
}

// rawDimensionsAt is dimensionsAt without the Done guard. The stats --seq
// miss analysis re-scores *completed* tasks as of their completion moment,
// where the live guard (Done scores 0) would erase exactly the data it needs.
// Live scoring must keep going through dimensionsAt.
func rawDimensionsAt(now time.Time, t *todo.Todo, heat Heat) (u, i, m, size, age float64) {
	u = urgencyDim(now, t.DueDate)
	i = importanceDim(t.Priority)
	m = momentumDim(t, heat)
	size = sizeDim(t.Size)
	age = ageDim(now, ageFrom(t))
	return
}

// ageFrom is when a task starts to age: its creation, or its start date when
// that is later. A task parked until a start date was not being neglected
// while it waited, so the rot guard counts from the day it could be picked up.
func ageFrom(t *todo.Todo) time.Time {
	if !t.StartDate.IsZero() {
		if start := startOfDay(t.StartDate); start.After(t.CreatedAt) {
			return start
		}
	}
	return t.CreatedAt
}

// urgencyDim implements the Deadline rule from the design:
//
//   - No due date          → 0
//   - Overdue or due today → 10 + 0.5 × (whole days past)
//   - 1..7 days out        → linear decay 10 (day 0) → 2 (day 7)
//   - >7 days out          → 0 (Age takes over for the long tail)
//
// Days are measured between start-of-day(now) and start-of-day(due), so a task
// due any time today scores exactly 10 regardless of the wall clock.
func urgencyDim(now, due time.Time) float64 {
	if due.IsZero() {
		return 0
	}
	today := startOfDay(now)
	dueDay := startOfDay(due)
	days := int(dueDay.Sub(today).Hours() / 24)
	switch {
	case days <= 0:
		return 10.0 + 0.5*float64(-days)
	case days <= 7:
		return 10.0 - (8.0*float64(days))/7.0
	default:
		return 0
	}
}

// importanceDim is the flat priority lookup: High=10, Medium=5, Low=0.
func importanceDim(p todo.Priority) float64 {
	switch p {
	case todo.PriorityHigh:
		return 10
	case todo.PriorityMedium:
		return 5
	default:
		return 0
	}
}

// momentumDim is the activity-heat lookup: 10 when the task itself or its
// project was touched (completion, timer, comment) inside MomentumWindow,
// 5 when only one of its tags was, 0 cold. "The thing you're already deep in
// comes next" — the informal ordering that dependency edges encode explicitly,
// available even when no edges were recorded.
func momentumDim(t *todo.Todo, heat Heat) float64 {
	if hot(heat.tasks, t.ID) || hot(heat.projects, t.Project) {
		return 10
	}
	for _, tag := range t.Tags {
		if hot(heat.tags, tag) {
			return 5
		}
	}
	return 0
}

// sizeDim is the quick-win nudge that survived the momentum rework: Small=2,
// Medium=1, Large=0, added unweighted. Size was never momentum — it's a
// static property — so it stopped claiming that axis and became a small
// tie-flavored bonus instead: quick wins still edge ahead of equal peers
// without drowning the real signals.
func sizeDim(s todo.Size) float64 {
	switch s {
	case todo.SizeSmall:
		return 2
	case todo.SizeLarge:
		return 0
	default:
		return 1
	}
}

// ageDim is the unbounded rot-guard: 0.1/day until day 30, then 0.2/day. A
// 30-day-old task with no other signals scores 3.0; after 60 days, 9.0. The
// rule is intentionally unbounded so anything truly forgotten eventually
// floats to the top to be finished or deleted.
func ageDim(now, created time.Time) float64 {
	if created.IsZero() {
		return 0
	}
	days := now.Sub(created).Hours() / 24
	if days <= 0 {
		return 0
	}
	if days <= 30 {
		return 0.1 * days
	}
	return 0.1*30 + 0.2*(days-30)
}

// ── Score assembly ────────────────────────────────────────────────────────────

// Components is the breakdown shown in the detail view. Each field is
// already weighted (i.e. multiplied by its bias) so the five values sum to
// Total — the user sees the actual contributions, not the raw 0..10 axes.
type Components struct {
	Urgency    float64
	Importance float64
	Momentum   float64
	Size       float64
	Age        float64
	Total      float64
}

// ComponentsAt is the testable assembly: pure, takes `now`, biases,
// and the heat snapshot explicitly.
func ComponentsAt(now time.Time, t *todo.Todo, b Biases, heat Heat) Components {
	u, i, m, size, age := dimensionsAt(now, t, heat)
	if !b.Aging {
		age = 0
	}
	out := Components{
		Urgency:    u * b.Deadline.Weight(),
		Importance: i * b.Priority.Weight(),
		Momentum:   m * b.Momentum.Weight(),
		Size:       size,
		Age:        age,
	}
	out.Total = out.Urgency + out.Importance + out.Momentum + out.Size + out.Age
	return out
}

// Ranker is everything a live score depends on besides the task and the clock:
// the user's bias knobs, the activity-heat snapshot behind Momentum, and the
// top of the current field that percentages are measured against. It is a
// value, handed to whoever scores — the model holds one (refreshed in
// refreshCaches), the CLI builds one in loadForCLI, and the save path copies it
// into the goroutine that writes the sequence column — so a score never reads
// state another goroutine may be changing.
type Ranker struct {
	Biases Biases
	Heat   Heat
	Max    float64
}

// Default is the neutral ranker: default biases, no activity heat, no
// field yet. It is what a surface without settings scores with — the headless
// server, a first-run import.
func Default() Ranker { return Ranker{Biases: DefaultBiases()} }

// Components is the per-dimension breakdown at time.Now, for display.
func (r Ranker) Components(t *todo.Todo) Components {
	return ComponentsAt(time.Now(), t, r.Biases, r.Heat)
}

// Score is the total persisted score: written into todos.sequence on every
// save and printed next to a task.
func (r Ranker) Score(t *todo.Todo) float64 {
	return ComponentsAt(time.Now(), t, r.Biases, r.Heat).Total
}

// ScoreNow returns Score bound to a single instant, and is what every *sort*
// and *ranking* must use — never Score itself.
//
// Age contributes 0.2/day continuously, so score reads its own clock and two
// tasks created at the same moment score differently by ~1e-11 purely because
// their scores were computed microseconds apart. The comparator then separates
// them on that float and never reaches the ID tie-break, so the order of equal
// tasks is decided by whatever order they were scored in — which sort.Slice
// does not preserve. Freezing the clock for the duration of one sort makes
// equal tasks actually tie, so LessTie runs and the order ends at ID,
// like every other comparator in this repo.
func (r Ranker) ScoreNow() func(*todo.Todo) float64 {
	return r.ScoreAt(time.Now())
}

// ScoreAt is Score against a caller-chosen instant.
func (r Ranker) ScoreAt(now time.Time) func(*todo.Todo) float64 {
	return func(t *todo.Todo) float64 {
		return ComponentsAt(now, t, r.Biases, r.Heat).Total
	}
}

// Refreshed returns r with the heat snapshot and the 100% mark recomputed from
// the task set at `now`: the step both surfaces take after loading or changing
// tasks, so they rank identically.
func (r Ranker) Refreshed(now time.Time, todos []*todo.Todo) Ranker {
	r.Heat = ComputeHeat(now, todos)
	r.Max = MaxRanked(todos, Lifts(todos, r.ScoreAt(now)), r.ScoreAt(now), now)
	return r
}

// ── The percentage scale ─────────────────────────────────────────────────────
//
// The raw score is unbounded upward — Age alone adds 0.2/day forever — so a
// bare "24.4" tells the user nothing about whether that is a lot. On screen the
// score therefore reads as a percentage of the current field: 100% is the
// highest-scoring pending task right now, so the number answers "how close to
// the top is this" rather than asking to be calibrated against a scale nobody
// published. The points survive where the arithmetic is being explained (the
// w overlay, `tjek why`), which is the one place a raw magnitude is the point.
//
// Normalizing against the live field rather than a fixed theoretical maximum is
// a deliberate trade: the scale uses its whole range, at the cost of 100%
// moving when the top task is finished. The explain overlay states what 100%
// currently equals, so the move is visible rather than mysterious.

// MaxScore is the top of the raw field, lifts not counted, against the given
// score function — so the explain view can establish the same 100% mark
// against its own clock and biases. One definition of "the field" keeps the
// overlay's percentage and the list column's from disagreeing about the same
// task.
func MaxScore(todos []*todo.Todo, score func(*todo.Todo) float64, now time.Time) float64 {
	return MaxRanked(todos, nil, score, now)
}

// MaxRanked is the top of the field with lifts counted — the highest
// score anything is actually ranked by. That is the 100% mark because the
// ranked score is what the list prints: measuring it against the best *raw*
// score lets a blocker carrying a fan-out bonus land above the top of the
// scale, where the clamp would print 100% for it and for the task it inherited
// from, hiding a difference the ranking still makes. The caller supplies the
// lift map (nil for the raw field) and the score function, so the explain view
// can establish the mark against its own clock and biases.
//
// A task that starts on a later day is not in the field: it shows its start
// instead of a percentage, and a waiting task at 100% would push every task
// that can be picked up today below the top of the scale.
func MaxRanked(todos []*todo.Todo, rollup map[string]float64, score func(*todo.Todo) float64, now time.Time) float64 {
	max := 0.0
	for _, t := range todos {
		if t.Deleted || t.Status != todo.Pending || StartsLater(t, now) {
			continue
		}
		if s := ScoreOf(t, rollup, score); s > max {
			max = s
		}
	}
	return max
}

// PercentOfField converts a score to its share of `max`, rounded to a whole
// percent and clamped to 0..100. Callers with a hypothetical field (the
// Settings preview ranks with knob values that are not live yet) pass their own
// maximum so the preview's numbers are internally consistent.
func PercentOfField(score, max float64) int {
	if max <= 0 || score <= 0 {
		return 0
	}
	p := int(math.Round(100 * score / max))
	if p > 100 {
		return 100
	}
	return p
}

// Percent is PercentOfField against the ranker's field.
func (r Ranker) Percent(score float64) int { return PercentOfField(score, r.Max) }

// FormatPercent is the on-screen form, four columns wide at most ("100%").
func (r Ranker) FormatPercent(score float64) string {
	return strconv.Itoa(r.Percent(score)) + "%"
}

// TopWith is the pure, testable form of rankTopBySequence: it
// accepts explicit biases, a heat snapshot, and a clock so callers can compute
// a preview ranking with knob values that are not live yet.
// The result is the same critical-path ordering (subtask + dependency rollups
// applied) as the live path — only the scoring inputs differ.
func TopWith(todos []*todo.Todo, b Biases, heat Heat, now time.Time) []todo.Todo {
	return TopBy(todos, now, func(t *todo.Todo) float64 {
		return ComponentsAt(now, t, b, heat).Total
	})
}

// ── Sequence hit rate ─────────────────────────────────────────────────────────

const (
	HitWindow = 50 // completions the hit-rate stat looks back over
	HitTopN   = 5  // a "hit" closed while ranked in the top N
)

// CaptureRankAtDone stamps t.SeqRankAtDone with the task's 1-based
// position in the ranking `tjek top` would have shown at this moment. The
// user-initiated close paths (CLI done, TUI toggle, confirm-close-parent)
// call it just before Toggle flips the status; auto-closed parents and
// recurrence spawns don't, so the metric only reads deliberate picks —
// "when you finished something, was it what the engine suggested".
func CaptureRankAtDone(r Ranker, todos []*todo.Todo, t *todo.Todo) {
	t.SeqRankAtDone = 0
	if t.ParentID != "" {
		return
	}
	now := time.Now()
	for i, row := range TopBy(todos, now, r.ScoreAt(now)) {
		if row.ID == t.ID {
			t.SeqRankAtDone = i + 1
			return
		}
	}
}

// RatedCompletions returns the rank-stamped completions the hit-rate metric
// reads — done, top-level, stamped, timestamped — most recent first, truncated
// to `window`. Shared by HitStats and AnalyzeMisses so the two
// always agree on which completions count.
func RatedCompletions(todos []*todo.Todo, window int) []*todo.Todo {
	var recent []*todo.Todo
	for _, t := range todos {
		if t.Status != todo.Done || t.ParentID != "" || t.SeqRankAtDone <= 0 || t.CompletedAt.IsZero() {
			continue
		}
		recent = append(recent, t)
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].CompletedAt.After(recent[j].CompletedAt) })
	if len(recent) > window {
		recent = recent[:window]
	}
	return recent
}

// HitStats reports, over the `window` most recent rank-stamped
// completions, how many closed inside the top HitTopN. rated counts the
// completions considered, so callers can render "39/50" and hide the stat
// entirely while no history exists.
func HitStats(todos []*todo.Todo, window int) (hits, rated int) {
	for _, t := range RatedCompletions(todos, window) {
		rated++
		if t.SeqRankAtDone <= HitTopN {
			hits++
		}
	}
	return hits, rated
}

// ── Sequence miss analysis (stats --seq) ─────────────────────────────────────
//
// The hit rate says how often a finished task was a top-N pick; this section
// says WHY the misses weren't. For every rated completion the five score
// dimensions are recomputed as of its CompletedAt — activity heat rebuilt from
// the historical record via ComputeHeatAt — then averaged separately
// for hits and misses. A dimension where misses lag hits is one the engine
// values more than the user's actual picking behaviour does (they finished
// those tasks anyway), so its bias knob is a Relaxed candidate; a dimension
// where misses *beat* hits is followed more than the engine weights it —
// an Intense candidate.
//
// Known approximation, deliberate: dimensions are recomputed from each task's
// CURRENT fields (a due date or priority edited after completion skews that
// reading) and weighted by the CURRENT biases. Exact readings would need the
// components stamped at done-time — a schema migration not worth taking until
// this reconstruction proves its keep.

// DimCount / DimNames fix the dimension order used by every [DimCount]
// array below: Deadline, Priority, Momentum, Size, Age. The first three are
// the knobbed dimensions (they have a Settings bias); Size and Age are shown
// in the table but never suggested on.
const DimCount = 5

var DimNames = [DimCount]string{"Deadline", "Priority", "Momentum", "Size", "Age"}

type MissRow struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Rank        int               `json:"rank"`
	CompletedAt time.Time         `json:"completed_at"`
	Dims        [DimCount]float64 `json:"dims"`
	// Weakest is the dimension where this miss fell furthest below the hit
	// average — the single best answer to "what buried it". Empty when there
	// are no hits to compare against.
	Weakest string `json:"weakest,omitempty"`
}

type Analysis struct {
	Hits       int               `json:"hits"`
	Rated      int               `json:"rated"`
	TopN       int               `json:"top_n"`
	Window     int               `json:"window"`
	Dimensions [DimCount]string  `json:"dimensions"` // names the array order for JSON consumers
	HitAvg     [DimCount]float64 `json:"hit_avg"`
	MissAvg    [DimCount]float64 `json:"miss_avg"`
	Gap        [DimCount]float64 `json:"gap"` // MissAvg − HitAvg
	Misses     []MissRow         `json:"misses"`
}

// AnalyzeMisses is the pure fold behind stats --seq: re-score every rated
// completion at its own CompletedAt, split hits from misses, and aggregate the
// weighted per-dimension contributions. Misses come back most recent first
// (RatedCompletions' order). heatSource is the task set momentum is
// reconstructed from — pass the FULL set even when `todos` is a filtered
// stats scope, or completions outside the filter stop warming their
// project/tags and the momentum readings go colder than the rank stamp saw.
func AnalyzeMisses(todos, heatSource []*todo.Todo, window int, b Biases) Analysis {
	a := Analysis{TopN: HitTopN, Window: window, Dimensions: DimNames}
	type scored struct {
		t    *todo.Todo
		dims [DimCount]float64
	}
	var missRows []scored
	var hitSum, missSum [DimCount]float64
	for _, t := range RatedCompletions(todos, window) {
		heat := ComputeHeatAt(t.CompletedAt, heatSource)
		u, i, m, size, age := rawDimensionsAt(t.CompletedAt, t, heat)
		if !b.Aging {
			age = 0
		}
		dims := [DimCount]float64{
			u * b.Deadline.Weight(),
			i * b.Priority.Weight(),
			m * b.Momentum.Weight(),
			size,
			age,
		}
		a.Rated++
		if t.SeqRankAtDone <= HitTopN {
			a.Hits++
			for d := range dims {
				hitSum[d] += dims[d]
			}
			continue
		}
		missRows = append(missRows, scored{t, dims})
		for d := range dims {
			missSum[d] += dims[d]
		}
	}
	misses := a.Rated - a.Hits
	for d := 0; d < DimCount; d++ {
		if a.Hits > 0 {
			a.HitAvg[d] = hitSum[d] / float64(a.Hits)
		}
		if misses > 0 {
			a.MissAvg[d] = missSum[d] / float64(misses)
		}
		a.Gap[d] = a.MissAvg[d] - a.HitAvg[d]
	}
	for _, r := range missRows {
		row := MissRow{
			ID:          r.t.ID,
			Title:       r.t.Title,
			Rank:        r.t.SeqRankAtDone,
			CompletedAt: r.t.CompletedAt,
			Dims:        r.dims,
		}
		if a.Hits > 0 {
			worst, worstIdx := 0.0, -1
			for d := range r.dims {
				if deficit := a.HitAvg[d] - r.dims[d]; deficit > worst {
					worst, worstIdx = deficit, d
				}
			}
			if worstIdx >= 0 {
				row.Weakest = DimNames[worstIdx]
			}
		}
		a.Misses = append(a.Misses, row)
	}
	return a
}

// seqSuggestionMinMisses / seqSuggestionMinGap gate the bias hint: with fewer
// misses than the floor any pattern is noise, and a gap under the floor (in
// weighted score points) isn't worth moving a knob over.
const (
	seqSuggestionMinMisses = 3
	seqSuggestionMinGap    = 1.0
)

// Suggestion turns the gap table into at most one actionable line: the
// knobbed dimension (Deadline/Priority/Momentum) with the largest |gap|, and
// which way to move its bias. Empty when there isn't enough signal to say
// anything; an explicit "looks calibrated" when there is signal but no
// dominant pattern.
func Suggestion(a Analysis, b Biases) string {
	misses := a.Rated - a.Hits
	if a.Hits == 0 || misses < seqSuggestionMinMisses {
		return ""
	}
	knobs := [3]Level{b.Deadline, b.Priority, b.Momentum}
	best, bestGap := -1, 0.0
	for d := 0; d < len(knobs); d++ {
		if g := a.Gap[d]; math.Abs(g) > math.Abs(bestGap) {
			best, bestGap = d, g
		}
	}
	if best < 0 || math.Abs(bestGap) < seqSuggestionMinGap {
		return "No dominant pattern in the misses; the Biases look calibrated."
	}
	name := DimNames[best]
	if bestGap < 0 {
		if knobs[best] == Relaxed {
			return fmt.Sprintf("Misses were weakest on %s; your %s: relaxed setting already leans that way.", name, name)
		}
		return fmt.Sprintf("Misses were weakest on %s: you finish tasks the engine buried for scoring low there. Consider %s: relaxed (Settings).", name, name)
	}
	if knobs[best] == Intense {
		return fmt.Sprintf("Misses scored higher on %s than hits; your %s: intense setting already leans that way.", name, name)
	}
	return fmt.Sprintf("Misses scored higher on %s than hits: you follow it more than the engine weights it. Consider %s: intense (Settings).", name, name)
}
