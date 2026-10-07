package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// loadForCLI opens the store with the user's persisted biases and stage list
// applied so any score-based or stage-aware output matches the TUI. The
// returned repository carries the ranker (repo.ranker()) the command scores with.
func loadForCLI() (*sqliteRepo, []todo.Todo, error) {
	settings, sErr := loadSettings()
	if sErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (using defaults)\n", sErr)
	}
	repo := newSQLiteRepo()
	repo.SetRanker(rank.Ranker{Biases: biasesFromSettings(settings)})
	todos, err := repo.Load()
	if err == nil {
		// Momentum reads recent activity; snapshot it so CLI output ranks
		// the same way the TUI does after its cache refresh.
		repo.SetRanker(repo.ranker().Refreshed(time.Now(), todoPtrs(todos)))
	}
	return repo, todos, err
}

// resolveDepRef resolves a dependency ref, expanding the `^` shorthand — the
// most recently added task, per the last-added sidecar — before the usual
// id-prefix/title lookup. Cheap dependency capture: a decomposed plan gets
// typed as "step one", "step two dep:^", ... without ever looking up an id.
func resolveDepRef(todos []*todo.Todo, ref string) (*todo.Todo, error) {
	if strings.TrimSpace(ref) == "^" {
		id := loadLastAddedID()
		if id == "" {
			return nil, fmt.Errorf("^: no last-added task recorded yet")
		}
		t, err := findTaskByRef(todos, id)
		if err != nil {
			return nil, fmt.Errorf("^: last-added task %.8s no longer resolves", id)
		}
		return t, nil
	}
	return findTaskByRef(todos, ref)
}

// findTaskByRef matches a task by either an id-prefix or a title substring,
// in that order. The id path takes precedence so scripts and aliases remain
// deterministic: a hex-shaped query that happens to appear in a task title
// won't ambiguously swap which task you operate on.
//
// Matching rules:
//   - ID prefix exactly matches one task            → return it
//   - ID prefix matches multiple tasks              → error with candidates
//     (no fallback — ambiguity is the user's call)
//   - ID prefix matches zero, title substring …
//     … matches one task                         → return it
//     … matches multiple                         → error with candidates
//     … matches zero                             → error "no task matches"
//
// Both comparisons are case-insensitive.
func findTaskByRef(todos []*todo.Todo, ref string) (*todo.Todo, error) {
	t, _, err := findTaskByRefKind(todos, ref)
	return t, err
}

// refMatch reports which pass of findTaskByRefKind resolved the ref. Verbs
// with destructive semantics (delete) use it to require confirmation on the
// fuzzy title path while keeping exact id/prefix refs script-fast.
type refMatch int

const (
	refMatchID refMatch = iota
	refMatchTitle
)

func findTaskByRefKind(todos []*todo.Todo, ref string) (*todo.Todo, refMatch, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, refMatchID, fmt.Errorf("empty task reference (need id-prefix or title substring)")
	}
	q := strings.ToLower(ref)

	// Pass 1: id-prefix.
	var idMatches []int
	for i := range todos {
		if strings.HasPrefix(strings.ToLower(todos[i].ID), q) {
			idMatches = append(idMatches, i)
		}
	}
	switch len(idMatches) {
	case 1:
		return todos[idMatches[0]], refMatchID, nil
	case 0:
		// fall through to title-substring
	default:
		return nil, refMatchID, ambiguousMatchError("id prefix", ref, todos, idMatches)
	}

	// Pass 2: title substring (case-insensitive).
	var titleMatches []int
	for i := range todos {
		if strings.Contains(strings.ToLower(todos[i].Title), q) {
			titleMatches = append(titleMatches, i)
		}
	}
	switch len(titleMatches) {
	case 0:
		return nil, refMatchTitle, fmt.Errorf("no task matches %q (tried id-prefix and title substring)", ref)
	case 1:
		return todos[titleMatches[0]], refMatchTitle, nil
	default:
		return nil, refMatchTitle, ambiguousMatchError("title", ref, todos, titleMatches)
	}
}

func ambiguousMatchError(kind, ref string, todos []*todo.Todo, matches []int) error {
	lines := make([]string, len(matches))
	for i, m := range matches {
		lines[i] = fmt.Sprintf("    %s  %s", todos[m].ID[:8], todos[m].Title)
	}
	return fmt.Errorf("%s %q matches %d tasks:\n%s", kind, ref, len(matches), strings.Join(lines, "\n"))
}

// findByPrefix is preserved as a thin alias so existing call-sites (and
// tests) keep working while the name catches up everywhere.
func findByPrefix(todos []*todo.Todo, ref string) (*todo.Todo, error) {
	return findTaskByRef(todos, ref)
}

// resolveRefs resolves N refs at once, failing fast on the first unresolvable
// or ambiguous one. Used by batch verbs (`done a b c`) so we can validate the
// whole set before mutating anything. Duplicate refs collapse to the first
// match — `done abc abc` is a single done, not an error.
func resolveRefs(todos []*todo.Todo, refs []string) ([]*todo.Todo, error) {
	out := make([]*todo.Todo, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		t, err := findTaskByRef(todos, ref)
		if err != nil {
			return nil, err
		}
		if seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, nil
}

// listFilterOpts collects every filter `list` and `search` apply. Lifted into
// a struct so the filter logic is unit-testable separately from CLI parsing.
type listFilterOpts struct {
	includeDone bool
	focus       bool
	tag         string // matched case-insensitively after NormalizeTag
	project     string // matched case-insensitively for equality
	search      string // case-insensitive substring of title
	searchWord  string // like search, but only whole-word matches
	searchRe    *regexp.Regexp
	onlyReady   bool // only actionable pending tasks (not blocked by an unfinished dependency)
	onlyBlocked bool // only tasks blocked by at least one unfinished dependency
	// staleFor keeps only tasks untouched for at least this long — the
	// "what has been sitting here" question a backlog review is made of.
	staleFor time.Duration
	// unblockedFor keeps only tasks that became actionable within this window:
	// they have dependencies, none of them is still pending, and the last one
	// to close did so recently. Nothing else surfaces "you can start this now"
	// — a freed task looks exactly like one that never had a blocker.
	unblockedFor time.Duration
	now          time.Time // injectable clock for the windows above; zero = time.Now()
}

// matchesText applies whichever of the three text filters are set. They are
// separate fields rather than one string with a mode, because `list --search
// RAM --search-word RAM` is a contradiction worth catching at the flag layer,
// not a precedence puzzle here.
func (o listFilterOpts) matchesText(t *todo.Todo) bool {
	if q := strings.ToLower(strings.TrimSpace(o.search)); q != "" {
		if !strings.Contains(strings.ToLower(t.Title), q) && !strings.Contains(strings.ToLower(t.Notes), q) {
			return false
		}
	}
	if o.searchWord != "" && !containsWord(t.Title, o.searchWord) && !containsWord(t.Notes, o.searchWord) {
		return false
	}
	if o.searchRe != nil && !o.searchRe.MatchString(t.Title) && !o.searchRe.MatchString(t.Notes) {
		return false
	}
	return true
}

// containsWord reports whether word occurs in s delimited by non-word runes on
// both sides, case-insensitively. Plain substring search answers "RAM" with
// "Ramte", which is right for recall and wrong for the question "is anything
// about RAM still open" — the one you ask when clearing a backlog.
//
// A "word rune" here is a letter or digit, so the Danish, German and other
// non-ASCII titles in this store behave like the English ones: 'å' is part of
// a word, '-' and ':' are not.
func containsWord(s, word string) bool {
	word = strings.TrimSpace(word)
	if word == "" {
		return true
	}
	hay := []rune(strings.ToLower(s))
	needle := []rune(strings.ToLower(word))
	isWordRune := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) != string(needle) {
			continue
		}
		if i > 0 && isWordRune(hay[i-1]) {
			continue
		}
		if j := i + len(needle); j < len(hay) && isWordRune(hay[j]) {
			continue
		}
		return true
	}
	return false
}

// unblockedSet returns the tasks whose last pending dependency completed within
// the window: still-pending tasks that have dependencies, all of them done, and
// the most recent completion no older than `within`. A task whose blocker was
// deleted rather than completed is not included — there is no moment to date it
// from, so it would be either always or never recent.
func unblockedSet(todos []todo.Todo, within time.Duration, now time.Time) map[string]bool {
	byID := make(map[string]*todo.Todo, len(todos))
	for i := range todos {
		byID[todos[i].ID] = &todos[i]
	}
	cutoff := now.Add(-within)
	out := make(map[string]bool)
	for i := range todos {
		t := &todos[i]
		if t.Status != todo.Pending || t.Deleted || len(t.Dependencies) == 0 {
			continue
		}
		var newest time.Time
		ok := true
		for _, depID := range t.Dependencies {
			dep := byID[depID]
			if dep == nil || dep.Status != todo.Done || dep.CompletedAt.IsZero() {
				ok = false
				break
			}
			if dep.CompletedAt.After(newest) {
				newest = dep.CompletedAt
			}
		}
		if ok && newest.After(cutoff) {
			out[t.ID] = true
		}
	}
	return out
}

// buildBlockedSet is the CLI's view of rank.DependencySets: same rule, over the
// value slice the CLI load path hands out.
func buildBlockedSet(todos []todo.Todo) map[string]bool {
	blocked, _ := rank.DependencySets(todoPtrs(todos))
	return blocked
}

func filterTopLevel(todos []todo.Todo, opts listFilterOpts) []todo.Todo {
	tagQ := todo.NormalizeTag(opts.tag)
	projQ := strings.ToLower(strings.TrimSpace(opts.project))

	// Build the blocked set only when one of the readiness filters is active —
	// it requires scanning the full task list and is unnecessary otherwise.
	var blockedSet map[string]bool
	if opts.onlyReady || opts.onlyBlocked {
		blockedSet = buildBlockedSet(todos)
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	var freed map[string]bool
	if opts.unblockedFor > 0 {
		freed = unblockedSet(todos, opts.unblockedFor, now)
	}

	rows := make([]todo.Todo, 0, len(todos))
	for _, t := range todos {
		if t.ParentID != "" {
			continue
		}
		if !opts.includeDone && t.Status != todo.Pending {
			continue
		}
		if opts.focus && !(t.IsOverdue() || t.IsDueToday()) {
			continue
		}
		if tagQ != "" {
			found := false
			for _, tag := range t.Tags {
				if tag == tagQ {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if projQ != "" && strings.ToLower(t.Project) != projQ {
			continue
		}
		// Title or notes. The CLI matches both as plain substrings (the TUI's
		// compileSearch adds fuzzy title matching and the token grammar on
		// top); notes have to be reachable here because migration 011 folded
		// the old per-task learnings into them. --search-word and --search-re
		// narrow the same two fields.
		if !opts.matchesText(&t) {
			continue
		}
		if opts.staleFor > 0 && now.Sub(t.ModifiedAt) < opts.staleFor {
			continue
		}
		if opts.unblockedFor > 0 && !freed[t.ID] {
			continue
		}
		if opts.onlyReady && blockedSet[t.ID] {
			continue
		}
		if opts.onlyBlocked && !blockedSet[t.ID] {
			continue
		}
		rows = append(rows, t)
	}
	return rows
}

// parseAgeSpec parses the duration accepted by --stale and --unblocked-since:
// "30d", "2w", "12h", "90m", or a bare number taken as days ("--stale=30").
//
// Days and weeks are the units a backlog is actually discussed in, and Go's
// time.ParseDuration knows neither. Note that 'm' keeps its Go meaning of
// minutes — the same as `tjek log 45m` — so months have no shorthand; write
// them as days.
func parseAgeSpec(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	mult := time.Duration(0)
	switch {
	case strings.HasSuffix(s, "d"):
		mult = 24 * time.Hour
	case strings.HasSuffix(s, "w"):
		mult = 7 * 24 * time.Hour
	}
	if mult > 0 {
		n, err := strconv.ParseFloat(strings.TrimRight(s, "dw"), 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q (try 30d, 2w, 12h)", s)
		}
		return time.Duration(n * float64(mult)), nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("invalid duration %q (try 30d, 2w, 12h)", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q (try 30d, 2w, 12h)", s)
	}
	return d, nil
}

// cliSortSpec names the orders `list`/`search` can be asked for. Three of them
// delegate to the shared comparators the TUI sorts by (so a listing can't
// disagree with the app about what "by due date" means); age and idle are
// CLI-only, because the TUI's sort modes are contracted to line up with a
// visible column and neither timestamp has one.
var cliSortSpec = []struct {
	name string
	desc string
}{
	{"seq", "sequence score, highest first (default)"},
	{"due", "due date, soonest first"},
	{"size", "size, small first"},
	{"age", "creation time, oldest first"},
	{"idle", "last modification, longest untouched first"},
	{"pri", "priority, highest first"},
}

func cliSortNames() []string {
	out := make([]string, 0, len(cliSortSpec))
	for _, s := range cliSortSpec {
		out = append(out, s.name)
	}
	return out
}

// sortTodosByCLIMode orders rows for a CLI listing. Every order ends at ID so
// it is total and the output is reproducible between runs, matching the
// house rule for the comparators in storage.go.
// blocked (nil when the caller has none on hand) sinks work waiting on an
// unfinished dependency below work that can be started, and so does a start
// date on a later day, in the sequence order only — an explicit --sort=due or --sort=size is an instruction to order by
// that key alone.
func sortTodosByCLIMode(rows []todo.Todo, mode string, blocked map[string]bool, rk rank.Ranker) error {
	switch mode {
	case "", "seq":
		now := time.Now()
		rank.SortValues(rows, nil, rank.Sunk(blocked, todoPtrs(rows), now), rk.ScoreAt(now))
	case "due":
		sortTodosByMode(rows, taskSortDueDate, nil)
	case "size":
		sortTodosByMode(rows, taskSortSize, nil)
	case "age":
		sortTodoValues(rows, func(a, b *todo.Todo) bool {
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID < b.ID
		})
	case "idle":
		sortTodoValues(rows, func(a, b *todo.Todo) bool {
			if !a.ModifiedAt.Equal(b.ModifiedAt) {
				return a.ModifiedAt.Before(b.ModifiedAt)
			}
			return a.ID < b.ID
		})
	case "pri":
		sortTodoValues(rows, func(a, b *todo.Todo) bool {
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			return a.ID < b.ID
		})
	default:
		return fmt.Errorf("unknown sort %q (use %s)", mode, strings.Join(cliSortNames(), "|"))
	}
	return nil
}

// trackedToday sums the portion of every TimeEntry across all todos that falls
// within today's local window. Running entries count up to `now`. Used by the
// stats one-liner.
func trackedTodayDuration(todos []todo.Todo, now time.Time) time.Duration {
	today := startOfDay(now)
	tomorrow := today.AddDate(0, 0, 1)
	var total time.Duration
	for _, t := range todos {
		for _, e := range t.TimeEntries {
			start := e.StartedAt
			end := e.StoppedAt
			if end.IsZero() {
				end = now
			}
			if !end.After(today) || !start.Before(tomorrow) {
				continue
			}
			if start.Before(today) {
				start = today
			}
			if end.After(tomorrow) {
				end = tomorrow
			}
			total += end.Sub(start)
		}
	}
	return total
}
