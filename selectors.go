package main

import (
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// Selectors are pure functions that derive a view from the source of truth (the
// task set) plus the relevant UI parameters (search text, sort mode, …). They
// hold no state and touch no model fields, which makes each one independently
// testable and impossible to leave "stale". The cache currently memoizes their
// results; the selectors are the single definition of each derivation.

// ── Predicates ────────────────────────────────────────────────────────────────

func todoMatchesSearch(t todo.Todo, search string) bool {
	return compileSearch(search)(t)
}

// compileSearch lowers the query once and returns a per-task predicate, so the
// active/done scan doesn't re-lower the (constant) search string for every task.
// todoMatchesSearch is the single-shot form; both share this one definition.
//
// The query is tokenised on whitespace and the tokens are ANDed together,
// reusing the quick-add vocabulary: `#tag` (tag substring), `@project` (project
// substring), `p:high|med|low`, `due:<date` / `due:>date` / `due:date`
// (comparison, "<"/">"/"<="/">=" or exact day), and the bare keyword `overdue`.
// Any leftover bare words are joined and fuzzy-matched against the title
// (subsequence, so "grcrs" finds "Buy groceries") or matched as a substring of
// the notes. The two click-driven sentinels
// — empty (match all) and untaggedKey (no tags) — keep their exact-string meaning.
func compileSearch(search string) func(todo.Todo) bool {
	switch search {
	case "":
		return func(todo.Todo) bool { return true }
	case untaggedKey:
		return func(t todo.Todo) bool { return len(t.Tags) == 0 }
	}

	var preds []func(todo.Todo) bool
	var titleWords []string

	for _, tok := range strings.Fields(search) {
		// Same two-step as parseQuickAdd: lower once, then fold a localized
		// field prefix back to English so the branches know one spelling.
		lower := canonicalInputToken(strings.ToLower(tok))
		switch {
		case strings.HasPrefix(tok, "#") && len(tok) > 1:
			q := strings.ToLower(tok[1:])
			preds = append(preds, func(t todo.Todo) bool {
				for _, tag := range t.Tags {
					if strings.Contains(strings.ToLower(tag), q) {
						return true
					}
				}
				return false
			})
		case strings.HasPrefix(tok, "@") && len(tok) > 1:
			q := strings.ToLower(tok[1:])
			preds = append(preds, func(t todo.Todo) bool {
				return strings.Contains(strings.ToLower(t.Project), q)
			})
		case strings.HasPrefix(lower, "p:"):
			if p, ok := parsePriorityFilter(strings.TrimPrefix(lower, "p:")); ok {
				preds = append(preds, func(t todo.Todo) bool { return t.Priority == p })
			} else {
				titleWords = append(titleWords, tok)
			}
		case strings.HasPrefix(lower, "due:"):
			if f, ok := parseDueFilter(strings.TrimPrefix(lower, "due:")); ok {
				preds = append(preds, f)
			} else {
				titleWords = append(titleWords, tok)
			}
		case canonicalInputWord(lower) == "overdue":
			preds = append(preds, func(t todo.Todo) bool { return t.IsOverdue() })
		default:
			titleWords = append(titleWords, tok)
		}
	}

	if len(titleWords) > 0 {
		titleQuery := strings.ToLower(strings.Join(titleWords, " "))
		preds = append(preds, func(t todo.Todo) bool {
			if subsequenceFold(t.Title, titleQuery) {
				return true
			}
			// Notes are matched as a plain substring, not a subsequence: a
			// note is long enough that a fuzzy match over it would hit
			// almost anything. This is what keeps written-down detail
			// findable — migration 011 folded the old per-task learnings
			// into notes, and recall was the point of having them.
			return strings.Contains(strings.ToLower(t.Notes), titleQuery)
		})
	}

	if len(preds) == 0 {
		return func(todo.Todo) bool { return true }
	}
	return func(t todo.Todo) bool {
		for _, p := range preds {
			if !p(t) {
				return false
			}
		}
		return true
	}
}

// subsequenceFold reports whether every rune of needle appears in haystack in
// order (not necessarily contiguous), case-insensitively. Empty needle matches.
// This is the fuzzy form of strings.Contains: "grcrs" matches "Buy groceries"
// (every rune must appear, in order — "grcry" does not, there is no trailing y).
func subsequenceFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	n := []rune(strings.ToLower(needle))
	i := 0
	for _, r := range strings.ToLower(haystack) {
		if r == n[i] {
			if i++; i == len(n) {
				return true
			}
		}
	}
	return false
}

// parsePriorityFilter maps a `p:` filter value to a priority, reusing the
// quick-add spellings. The bool is false for anything unrecognised so the caller
// can fall back to treating the token as a literal title word.
func parsePriorityFilter(s string) (todo.Priority, bool) {
	switch canonicalInputWord(s) {
	case "high", "h":
		return todo.PriorityHigh, true
	case "medium", "med", "m":
		return todo.PriorityMedium, true
	case "low", "l":
		return todo.PriorityLow, true
	}
	return 0, false
}

// parseDueFilter builds a due-date predicate from a `due:` filter value. An
// optional leading "<", ">", "<=" or ">=" sets the comparison (else exact day);
// the rest is parsed with the same parseDueDate the quick-add path uses. Tasks
// with no due date never match a due: filter. Returns false if the date is
// unparseable so the caller can fall back to a literal title word.
func parseDueFilter(spec string) (func(todo.Todo) bool, bool) {
	op, rest := "=", spec
	switch {
	case strings.HasPrefix(spec, "<="):
		op, rest = "<=", spec[2:]
	case strings.HasPrefix(spec, ">="):
		op, rest = ">=", spec[2:]
	case strings.HasPrefix(spec, "<"):
		op, rest = "<", spec[1:]
	case strings.HasPrefix(spec, ">"):
		op, rest = ">", spec[1:]
	}
	d, err := parseDueDate(rest)
	if err != nil {
		return nil, false
	}
	day := startOfDay(d)
	return func(t todo.Todo) bool {
		if t.DueDate.IsZero() {
			return false
		}
		td := startOfDay(t.DueDate)
		switch op {
		case "<":
			return td.Before(day)
		case "<=":
			return !td.After(day)
		case ">":
			return td.After(day)
		case ">=":
			return !td.Before(day)
		default:
			return td.Equal(day)
		}
	}, true
}

func todoMatchesFocus(t todo.Todo, focus bool) bool {
	if !focus {
		return true
	}
	return t.IsOverdue() || t.IsDueToday()
}

// ── View selectors ────────────────────────────────────────────────────────────

// selectActiveDone splits the top-level (non-subtask) tasks into the active and
// done lists, applying the search filter to both and the focus filter to active
// only. The active list is sorted by sortMode; the done list by historyMode
// (its own dimension). In Sequence mode, parents inherit the max score of their
// descendants for ranking only — the displayed score stays the parent's own —
// so a high-priority subtask pulls its parent up rather than hiding beneath a
// calmer one.
func selectActiveDone(todos []*todo.Todo, now time.Time, score func(*todo.Todo) float64, search string, focus bool, sortMode taskSortMode, historyMode historySortMode) (active, done []todo.Todo) {
	var rollup map[string]float64
	if sortMode == taskSortSequence {
		rollup = rank.Lifts(todos, score)
	}
	return selectActiveDoneRanked(todos, rollup, now, score, search, focus, sortMode, historyMode)
}

// selectActiveDoneRanked takes the lift map from its caller. The model computes
// it once per data change and caches it: it depends on the task set, not on the
// filter, so recomputing it inside the per-keystroke search path walked every
// task twice for an answer that had not changed.
func selectActiveDoneRanked(todos []*todo.Todo, rollup map[string]float64, now time.Time, score func(*todo.Todo) float64, search string, focus bool, sortMode taskSortMode, historyMode historySortMode) (active, done []todo.Todo) {
	match := compileSearch(search)
	// Split and sort as pointers, then materialize once at the end. The caches
	// hold values — they outlive this call and are read while the store mutates
	// — but sorting them as values meant every swap moved a 416-byte struct,
	// which made this the most expensive step of a cache refresh.
	var activeP, doneP []*todo.Todo
	for _, t := range todos {
		if t.ParentID != "" {
			continue
		}
		switch {
		case t.Status == todo.Pending && match(*t) && todoMatchesFocus(*t, focus):
			activeP = append(activeP, t)
		case t.Status == todo.Done && match(*t):
			doneP = append(doneP, t)
		}
	}
	// Active tasks rank by taskSort; the done list has its own history sort,
	// since the active modes (score, size) carry no meaning once tasks close.
	// Blocked work, and work not due to start until a later day, sinks below
	// everything that can be started today (see rank.SortPtrs). The blocked set
	// is derived from the whole slice, not from activeP: the task holding one
	// up may be a subtask, or filtered out of view.
	switch sortMode {
	case taskSortSequence:
		blocked, _ := rank.DependencySets(todos)
		rank.SortPtrs(activeP, rollup, rank.Sunk(blocked, activeP, now), score)
	case taskSortDueDate:
		sortTodoPtrs(activeP, lessByDueDate)
	case taskSortSize:
		sortTodoPtrs(activeP, lessBySize)
	default:
		blocked, _ := rank.DependencySets(todos)
		rank.SortPtrs(activeP, nil, rank.Sunk(blocked, activeP, now), score)
	}
	sortTodoPtrs(doneP, historyLess(historyMode))
	return todoValues(activeP), todoValues(doneP)
}
