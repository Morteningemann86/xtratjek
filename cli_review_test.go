package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// cli_review_test.go covers the flags a backlog review needs — the ones added
// after clearing a 47-task backlog was only possible by exporting to JSON and
// doing the arithmetic outside tjek: age/idle ordering, --stale, whole-word
// and regexp matching, "what did closing that unblock", multi-ref edit and
// reopen.

func TestContainsWordRespectsWordBoundaries(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		// The case that started it: a substring search for RAM answers with
		// the Danish word "Ramte" ("hit"), which is not about memory at all.
		{"Ramte t480 2026-08-08", "RAM", false},
		{"Opgrader hoth til 32GB RAM", "ram", true},
		{"RAM-opgradering", "RAM", true},   // '-' is not a word rune
		{"(RAM)", "ram", true},             // nor is a bracket
		{"32GB", "gb", false},              // a digit is, so this is one word
		{"vm-win11 mod OOM", "oom", true},  // end of string
		{"OOM crash", "oom", true},         // start of string
		{"kørsel på hoth", "hoth", true},   // non-ASCII neighbours
		{"gennemhothsyret", "hoth", false}, // buried inside a longer word
		{"anything", "", true},             // an empty needle filters nothing
	}
	for _, c := range cases {
		if got := containsWord(c.hay, c.needle); got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", c.hay, c.needle, got, c.want)
		}
	}
}

func TestParseAgeSpecUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"12h", 12 * time.Hour},
		{"90m", 90 * time.Minute}, // 'm' stays minutes, as in `tjek log 45m`
		{"30", 30 * 24 * time.Hour},
		{" 1W ", 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseAgeSpec(c.in)
		if err != nil {
			t.Errorf("parseAgeSpec(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAgeSpec(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "soon", "-3d", "0d", "3x"} {
		if _, err := parseAgeSpec(bad); err == nil {
			t.Errorf("parseAgeSpec(%q) accepted a bad spec", bad)
		}
	}
}

// buildReviewSet returns a fixed little backlog: one fresh task, one that has
// been sitting untouched for 40 days, and one that was blocked until its only
// dependency closed two days ago.
func buildReviewSet(now time.Time) []todo.Todo {
	fresh := todo.New("Fresh task about RAM")
	fresh.ID = "aaaaaaaa-1111"
	fresh.CreatedAt = now.AddDate(0, 0, -2)
	fresh.ModifiedAt = now.Add(-time.Hour)

	stale := todo.New("Ramte an old wall")
	stale.ID = "bbbbbbbb-2222"
	stale.CreatedAt = now.AddDate(0, 0, -60)
	stale.ModifiedAt = now.AddDate(0, 0, -40)

	blocker := todo.New("The blocker")
	blocker.ID = "cccccccc-3333"
	blocker.Status = todo.Done
	blocker.CompletedAt = now.AddDate(0, 0, -2)
	blocker.CreatedAt = now.AddDate(0, 0, -30)
	blocker.ModifiedAt = now.AddDate(0, 0, -2)

	freed := todo.New("Freed by the blocker")
	freed.ID = "dddddddd-4444"
	freed.Dependencies = []string{blocker.ID}
	freed.CreatedAt = now.AddDate(0, 0, -30)
	freed.ModifiedAt = now.AddDate(0, 0, -30)

	return []todo.Todo{fresh, stale, blocker, freed}
}

func titlesOf(rows []todo.Todo) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func TestFilterStaleKeepsOnlyUntouchedTasks(t *testing.T) {
	now := time.Now()
	todos := buildReviewSet(now)
	rows := filterTopLevel(todos, listFilterOpts{staleFor: 30 * 24 * time.Hour, now: now})
	got := titlesOf(rows)
	if len(got) != 2 {
		t.Fatalf("--stale=30d returned %v, want the two tasks untouched for 30+ days", got)
	}
	for _, title := range got {
		if strings.HasPrefix(title, "Fresh") {
			t.Errorf("--stale=30d returned %q, which was modified an hour ago", title)
		}
	}
}

func TestFilterUnblockedSinceFindsFreedTasks(t *testing.T) {
	now := time.Now()
	todos := buildReviewSet(now)

	rows := filterTopLevel(todos, listFilterOpts{unblockedFor: 14 * 24 * time.Hour, now: now})
	if got := titlesOf(rows); len(got) != 1 || got[0] != "Freed by the blocker" {
		t.Fatalf("--unblocked-since=14d = %v, want only the freed task", got)
	}

	// A window that closes before the blocker did must find nothing: the point
	// of the flag is "recently", not "ever".
	rows = filterTopLevel(todos, listFilterOpts{unblockedFor: time.Hour, now: now})
	if got := titlesOf(rows); len(got) != 0 {
		t.Fatalf("--unblocked-since=1h = %v, want nothing (the blocker closed 2 days ago)", got)
	}

	// A task still waiting on a pending dependency is not "freed".
	todos[2].Status = todo.Pending
	rows = filterTopLevel(todos, listFilterOpts{unblockedFor: 14 * 24 * time.Hour, now: now})
	if got := titlesOf(rows); len(got) != 0 {
		t.Fatalf("--unblocked-since with the blocker reopened = %v, want nothing", got)
	}
}

func TestFilterSearchWordAndRegexpNarrowTheSameFields(t *testing.T) {
	now := time.Now()
	todos := buildReviewSet(now)

	plain := titlesOf(filterTopLevel(todos, listFilterOpts{search: "ram", now: now}))
	if len(plain) != 2 {
		t.Fatalf("--search=ram = %v, want both the RAM task and the 'Ramte' one", plain)
	}
	word := titlesOf(filterTopLevel(todos, listFilterOpts{searchWord: "ram", now: now}))
	if len(word) != 1 || !strings.Contains(word[0], "RAM") {
		t.Fatalf("--search-word=ram = %v, want only the real RAM task", word)
	}
	re := titlesOf(filterTopLevel(todos, listFilterOpts{searchRe: regexp.MustCompile(`(?i)^fresh`), now: now}))
	if len(re) != 1 || !strings.HasPrefix(re[0], "Fresh") {
		t.Fatalf("--search-re='^fresh' = %v, want only the fresh task", re)
	}
	// Notes are searched too, by all three.
	todos[1].Notes = "mentions kryptonite"
	if got := titlesOf(filterTopLevel(todos, listFilterOpts{searchWord: "kryptonite", now: now})); len(got) != 1 {
		t.Fatalf("--search-word against notes = %v, want the one task whose notes match", got)
	}
}

func TestSortTodosByCLIModeOrdersAndRejectsUnknown(t *testing.T) {
	now := time.Now()
	base := buildReviewSet(now)

	rows := append([]todo.Todo(nil), base...)
	if err := sortTodosByCLIMode(rows, "age", nil, rank.Default()); err != nil {
		t.Fatalf("sort age: %v", err)
	}
	if rows[0].Title != "Ramte an old wall" {
		t.Errorf("age sort put %q first, want the oldest task", rows[0].Title)
	}

	rows = append([]todo.Todo(nil), base...)
	if err := sortTodosByCLIMode(rows, "idle", nil, rank.Default()); err != nil {
		t.Fatalf("sort idle: %v", err)
	}
	// Untouched for 40 days beats the freed task's 30 — idle reads ModifiedAt,
	// not CreatedAt, so it is a different order from age above (where the
	// 60-day-old task also happens to lead).
	if rows[0].Title != "Ramte an old wall" {
		t.Errorf("idle sort put %q first, want the longest-untouched task", rows[0].Title)
	}
	if rows[len(rows)-1].Title != "Fresh task about RAM" {
		t.Errorf("idle sort put %q last, want the most recently touched task", rows[len(rows)-1].Title)
	}

	rows = append([]todo.Todo(nil), base...)
	rows[1].Priority = todo.PriorityHigh
	if err := sortTodosByCLIMode(rows, "pri", nil, rank.Default()); err != nil {
		t.Fatalf("sort pri: %v", err)
	}
	if rows[0].Priority != todo.PriorityHigh {
		t.Errorf("pri sort put %v first, want high", rows[0].Priority)
	}

	if err := sortTodosByCLIMode(rows, "colour", nil, rank.Default()); err == nil {
		t.Error("an unknown --sort was accepted; it should name the valid modes instead")
	}
}

// Every mode must be a total order, or two runs over the same data can print
// different tables. Ties on the sort key are broken by ID everywhere.
func TestCLISortsAreTotalOrders(t *testing.T) {
	now := time.Now()
	same := now.AddDate(0, 0, -5)
	var rows []todo.Todo
	for _, id := range []string{"cccccccc", "aaaaaaaa", "bbbbbbbb"} {
		x := todo.New("identical " + id)
		x.ID = id + "-9999"
		x.CreatedAt, x.ModifiedAt = same, same
		rows = append(rows, x)
	}
	for _, mode := range cliSortNames() {
		first := append([]todo.Todo(nil), rows...)
		second := append([]todo.Todo(nil), rows[1], rows[2], rows[0])
		if err := sortTodosByCLIMode(first, mode, nil, rank.Default()); err != nil {
			t.Fatalf("sort %s: %v", mode, err)
		}
		if err := sortTodosByCLIMode(second, mode, nil, rank.Default()); err != nil {
			t.Fatalf("sort %s: %v", mode, err)
		}
		for i := range first {
			if first[i].ID != second[i].ID {
				t.Fatalf("sort %s is not a total order: %v vs %v", mode, titlesOf(first), titlesOf(second))
			}
		}
	}
}

func TestCliEditAppliesOneFlagToSeveralTasks(t *testing.T) {
	const project = "bulk-edit-check"
	var ids []string
	for _, title := range []string{"bulk one", "bulk two", "bulk three"} {
		out := captureStdout(t, func() {
			if code := cliAdd([]string{title, "--quiet-id"}); code != 0 {
				t.Fatalf("add %q: exit %d", title, code)
			}
		})
		ids = append(ids, strings.TrimSpace(out))
	}

	captureStdout(t, func() {
		if code := cliEdit(append(append([]string{}, ids...), "--project", project, "--add-tag", "sweep")); code != 0 {
			t.Fatalf("edit three refs: exit %d", code)
		}
	})
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, id := range ids {
		got, err := findTaskByRef(todoPtrs(todos), id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if got.Project != project {
			t.Errorf("%s project = %q, want %q", id[:8], got.Project, project)
		}
		if len(got.Tags) != 1 || got.Tags[0] != "sweep" {
			t.Errorf("%s tags = %v, want [sweep]", id[:8], got.Tags)
		}
	}

	// --title is identity, not a property: refuse it for more than one ref
	// rather than giving three tasks the same name.
	if code := cliEdit([]string{ids[0], ids[1], "--title", "same name"}); code != 2 {
		t.Errorf("edit --title with two refs: exit %d, want 2", code)
	}
}

// A bad ref anywhere in the list must leave the whole edit unapplied — the same
// contract `done` has.
func TestCliEditIsAllOrNothingOnABadRef(t *testing.T) {
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"atomic edit target", "--quiet-id"}); code != 0 {
			t.Fatalf("add: exit %d", code)
		}
	})
	id := strings.TrimSpace(out)

	if code := cliEdit([]string{id, "zzzzzzzz-nope", "--project", "should-not-land"}); code == 0 {
		t.Fatal("edit with an unresolvable second ref succeeded; want a usage error")
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), id)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Project == "should-not-land" {
		t.Error("the first task was edited even though a later ref failed to resolve")
	}
}

func TestCliReopenRestoresPendingAndSkipsOpenTasks(t *testing.T) {
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"reopen me", "--quiet-id"}); code != 0 {
			t.Fatalf("add: exit %d", code)
		}
	})
	id := strings.TrimSpace(out)
	captureStdout(t, func() {
		if code := cliDone([]string{id}); code != 0 {
			t.Fatalf("done: exit %d", code)
		}
	})

	out = captureStdout(t, func() {
		if code := cliReopen([]string{id, "-m", "closed by mistake"}); code != 0 {
			t.Fatalf("reopen: exit %d", code)
		}
	})
	if !strings.Contains(out, "reopened") {
		t.Errorf("reopen output = %q, want a 'reopened' line", out)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), id)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Status != todo.Pending {
		t.Fatalf("status = %v, want Pending", got.Status)
	}
	if !got.CompletedAt.IsZero() {
		t.Errorf("CompletedAt = %v, want zero — the task hasn't completed after all", got.CompletedAt)
	}
	if got.SeqRankAtDone != 0 {
		t.Errorf("SeqRankAtDone = %d, want 0 — the rank was a completion-time reading", got.SeqRankAtDone)
	}
	if len(got.Comments) != 1 || got.Comments[0].Text != "closed by mistake" {
		t.Errorf("comments = %+v, want the -m comment attached", got.Comments)
	}

	// Reopening an already-pending task is a no-op, not a toggle back to done.
	if code := cliReopen([]string{id}); code != 0 {
		t.Fatalf("second reopen: exit %d, want 0", code)
	}
	_, todos, _ = loadForCLI()
	got, _ = findTaskByRef(todoPtrs(todos), id)
	if got.Status != todo.Pending {
		t.Fatalf("status after reopening a pending task = %v, want it left Pending", got.Status)
	}
}

// The whole point of --wide: the two review columns appear only when asked for,
// so a daily `tjek list` keeps its width for the title.
func TestListWideAddsAgeAndIdleColumns(t *testing.T) {
	const project = "wide-column-check"
	captureStdout(t, func() {
		if code := cliAdd([]string{"wide column task", "--project", project, "--quiet-id"}); code != 0 {
			t.Fatalf("add: exit %d", code)
		}
	})
	narrow := captureStdout(t, func() {
		if code := cliList([]string{"--project", project}); code != 0 {
			t.Fatalf("list: exit %d", code)
		}
	})
	if strings.Contains(narrow, "AGE") || strings.Contains(narrow, "IDLE") {
		t.Errorf("plain list printed the review columns:\n%s", narrow)
	}
	wide := captureStdout(t, func() {
		if code := cliList([]string{"--project", project, "--wide"}); code != 0 {
			t.Fatalf("list --wide: exit %d", code)
		}
	})
	if !strings.Contains(wide, "AGE") || !strings.Contains(wide, "IDLE") {
		t.Errorf("list --wide missed the review columns:\n%s", wide)
	}
	if !strings.Contains(wide, "0d") {
		t.Errorf("list --wide didn't age a just-created task as 0d:\n%s", wide)
	}
}

// Contradictory text filters are a usage error, not a precedence puzzle.
func TestListRejectsContradictoryTextFilters(t *testing.T) {
	if code := cliList([]string{"--search", "ram", "--search-word", "ram"}); code != 2 {
		t.Errorf("--search with --search-word: exit %d, want 2", code)
	}
	if code := cliList([]string{"--search-re", "([unclosed"}); code != 2 {
		t.Errorf("invalid --search-re: exit %d, want 2", code)
	}
	if code := cliList([]string{"--stale", "soon"}); code != 2 {
		t.Errorf("invalid --stale: exit %d, want 2", code)
	}
	if code := cliSearch([]string{"term", "--word", "--re"}); code != 2 {
		t.Errorf("search --word with --re: exit %d, want 2", code)
	}
}
