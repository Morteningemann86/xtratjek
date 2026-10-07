package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── list ─────────────────────────────────────────────────────────────────────

func cliList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	all := fs.Bool("all", false, "include completed tasks")
	focus := fs.Bool("focus", false, "only today + overdue")
	limit := fs.Int("limit", 0, "cap rows (0 = no cap)")
	tag := fs.String("tag", "", "only tasks carrying this tag (case-insensitive)")
	project := fs.String("project", "", "only tasks in this project")
	search := fs.String("search", "", "only tasks whose title contains this substring")
	searchWord := fs.String("search-word", "", "like --search, but only whole-word matches ('RAM' won't match 'Ramte')")
	searchRe := fs.String("search-re", "", "only tasks whose title or notes match this regular expression")
	ready := fs.Bool("ready", false, "only actionable pending tasks (no unfinished dependencies)")
	blocked := fs.Bool("blocked", false, "only tasks blocked by at least one unfinished dependency")
	stale := fs.String("stale", "", "only tasks untouched for at least this long (30d, 2w, 12h; a bare number means days)")
	unblocked := fs.String("unblocked-since", "", "only tasks freed within this window: every dependency done, the last one recently")
	sortBy := fs.String("sort", "", "order rows: "+strings.Join(cliSortNames(), "|")+" (default seq)")
	wide := fs.Bool("wide", false, "add AGE and IDLE columns (days since creation / last change)")
	flagArgs, _ := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	opts, code := listOptsFromFlags(*all, *focus, *tag, *project, *search, *searchWord, *searchRe, *stale, *unblocked)
	if code != 0 {
		return code
	}
	opts.onlyReady = *ready
	opts.onlyBlocked = *blocked
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	blockedSet := buildBlockedSet(todos)
	rows := filterTopLevel(todos, opts)
	if err := sortTodosByCLIMode(rows, *sortBy, blockedSet, repo.ranker()); err != nil {
		fmt.Fprintf(os.Stderr, "tjek list: %v\n", err)
		return 2
	}
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}
	if *asJSON {
		return emitJSON(rows)
	}
	// A bare "(no tasks)" after finishing a project is indistinguishable from
	// a typo'd filter. When completed tasks match the same filters, say so —
	// the project exists, the work is just done.
	if len(rows) == 0 && !*all {
		withDone := opts
		withDone.includeDone = true
		if hidden := len(filterTopLevel(todos, withDone)); hidden > 0 {
			fmt.Printf("(no pending tasks; %d done match, use --all to see them)\n", hidden)
			return 0
		}
	}
	printTaskTableWide(rows, blockedSet, *wide)
	return 0
}

// listOptsFromFlags builds the shared filter from the text/window flags, so
// `list` and `search` can't drift on what --search-word or --stale mean. It
// reports its own usage errors and returns the exit code to propagate.
func listOptsFromFlags(all, focus bool, tag, project, search, searchWord, searchRe, stale, unblocked string) (listFilterOpts, int) {
	opts := listFilterOpts{
		includeDone: all,
		focus:       focus,
		tag:         tag,
		project:     project,
		search:      search,
		searchWord:  searchWord,
	}
	if search != "" && searchWord != "" {
		fmt.Fprintln(os.Stderr, "tjek: --search and --search-word both narrow the same fields; pass one")
		return opts, 2
	}
	if searchRe != "" {
		// Case-insensitive by default: every other text filter here is, and a
		// regex that misses "RAM" because the note says "ram" would be a trap
		// rather than a feature.
		re, err := regexp.Compile("(?i)" + searchRe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek: invalid --search-re: %v\n", err)
			return opts, 2
		}
		opts.searchRe = re
	}
	if stale != "" {
		d, err := parseAgeSpec(stale)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek: --stale: %v\n", err)
			return opts, 2
		}
		opts.staleFor = d
	}
	if unblocked != "" {
		d, err := parseAgeSpec(unblocked)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek: --unblocked-since: %v\n", err)
			return opts, 2
		}
		opts.unblockedFor = d
	}
	return opts, 0
}

// cliSearch is sugar for `list --all --search=...` — kept as its own verb for
// discoverability. Includes done by default since searching is usually for
// recall, not focus.
func cliSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	pendingOnly := fs.Bool("pending", false, "exclude completed tasks (default: include)")
	limit := fs.Int("limit", 0, "cap rows (0 = no cap)")
	word := fs.Bool("word", false, "match the term as a whole word ('RAM' won't match 'Ramte')")
	asRegexp := fs.Bool("re", false, "treat the term as a regular expression")
	sortBy := fs.String("sort", "", "order rows: "+strings.Join(cliSortNames(), "|")+" (default seq)")
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) == 0 {
		fmt.Fprintln(os.Stderr, `usage: tjek search "term" [--json] [--pending] [--word] [--re] [--sort=…] [--limit=N]`)
		return 2
	}
	if *word && *asRegexp {
		fmt.Fprintln(os.Stderr, "tjek search: --word and --re are two ways to read the same term; pass one")
		return 2
	}
	term := strings.Join(positionals, " ")
	// search takes its term as a positional, so the mode is a flag here while
	// list spells it out per filter; both land in the same listFilterOpts.
	opts := listFilterOpts{includeDone: !*pendingOnly}
	switch {
	case *word:
		opts.searchWord = term
	case *asRegexp:
		re, err := regexp.Compile("(?i)" + term)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek search: invalid regular expression: %v\n", err)
			return 2
		}
		opts.searchRe = re
	default:
		opts.search = term
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	blockedSet := buildBlockedSet(todos)
	rows := filterTopLevel(todos, opts)
	if err := sortTodosByCLIMode(rows, *sortBy, blockedSet, repo.ranker()); err != nil {
		fmt.Fprintf(os.Stderr, "tjek search: %v\n", err)
		return 2
	}
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}
	if *asJSON {
		return emitJSON(rows)
	}
	printTaskTable(rows, blockedSet)
	return 0
}

// cliTags / cliProjects: discovery commands. Counts only count pending
// top-level tasks so the listing reflects what's in flight, not historical
// fragments. Sort by count desc, then name asc, for stable output.

type nameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func sortNameCounts(rows []nameCount) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
}

func cliTags(args []string) int {
	fs := flag.NewFlagSet("tags", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	counts := map[string]int{}
	for _, t := range todos {
		if t.ParentID != "" || t.Status != todo.Pending {
			continue
		}
		for _, tag := range t.Tags {
			counts[tag]++
		}
	}
	rows := make([]nameCount, 0, len(counts))
	for tag, c := range counts {
		rows = append(rows, nameCount{tag, c})
	}
	sortNameCounts(rows)
	if *asJSON {
		return emitJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("(no tags)")
		return 0
	}
	for _, r := range rows {
		fmt.Printf("%4d  %s\n", r.Count, r.Name)
	}
	return 0
}

func cliProjects(args []string) int {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	counts := map[string]int{}
	for _, t := range todos {
		if t.ParentID != "" || t.Status != todo.Pending {
			continue
		}
		if t.Project == "" {
			continue
		}
		counts[t.Project]++
	}
	rows := make([]nameCount, 0, len(counts))
	for p, c := range counts {
		rows = append(rows, nameCount{p, c})
	}
	sortNameCounts(rows)
	if *asJSON {
		return emitJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("(no projects)")
		return 0
	}
	for _, r := range rows {
		fmt.Printf("%4d  %s\n", r.Count, r.Name)
	}
	return 0
}

// ── top ──────────────────────────────────────────────────────────────────────

func cliTop(args []string) int {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	n := fs.Int("n", 10, "rows to show")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	wide := fs.Bool("wide", false, "include priority, due date, and tags columns")
	flagArgs, _ := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if *n < 1 {
		fmt.Fprintln(os.Stderr, "tjek top: -n must be at least 1")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	rk := repo.ranker()
	rows := rk.Top(todoPtrs(todos))
	now := time.Now()
	if *n > 0 && len(rows) > *n {
		rows = rows[:*n]
	}
	if *asJSON {
		// The raw score stays the `score` field — machine consumers want the
		// magnitude, and it is what the database stores. `percent` carries the
		// scale the table and the TUI show, so a status-bar widget can render
		// the same number the app does without knowing the field maximum.
		type scoredOut struct {
			ID       string   `json:"id"`
			Title    string   `json:"title"`
			Score    float64  `json:"score"`
			Percent  int      `json:"percent"`
			Priority string   `json:"priority"`
			Due      string   `json:"due,omitempty"`
			Tags     []string `json:"tags,omitempty"`
		}
		out := make([]scoredOut, len(rows))
		for i := range rows {
			due := ""
			if !rows[i].DueDate.IsZero() {
				due = rows[i].DueDate.Format("2006-01-02")
			}
			score := rk.Score(&rows[i])
			out[i] = scoredOut{rows[i].ID, rows[i].Title, score, rk.Percent(score),
				priorityLetter(rows[i].Priority), due, rows[i].Tags}
		}
		return emitJSON(out)
	}
	if *wide {
		// Adaptive tag-column width: hug the widest tag string in the result
		// set instead of locking to 20 chars and chopping anything over. Cap
		// at 40 so a wildly tagged task can't push the title off the screen.
		tagW := len("TAGS")
		tagStrings := make([]string, len(rows))
		for i := range rows {
			tagStrings[i] = strings.Join(rows[i].Tags, ",")
			if w := len(tagStrings[i]); w > tagW {
				tagW = w
			}
		}
		if tagW > 40 {
			tagW = 40
		}
		fmt.Printf("%-8s  %-5s  %-3s  %-10s  %-*s  %s\n", "ID", "SCORE", "PRI", "DUE", tagW, "TAGS", "TITLE")
		for i := range rows {
			due := ""
			if !rows[i].DueDate.IsZero() {
				due = rows[i].DueDate.Format("02-01-06")
			}
			tags := truncate(tagStrings[i], tagW)
			fmt.Printf("%-8s  %5s  %-3s  %-10s  %-*s  %s\n",
				rows[i].ID[:8], cliScoreCell(rk, &rows[i], now),
				priorityLetter(rows[i].Priority), due, tagW, tags,
				truncate(rows[i].Title, 60))
		}
		return 0
	}
	for i := range rows {
		fmt.Printf("%-8s %5s  %s\n", rows[i].ID[:8],
			cliScoreCell(rk, &rows[i], now), truncate(rows[i].Title, 60))
	}
	return 0
}

// ── remind ───────────────────────────────────────────────────────────────────

// remindClock is the CLI reminder's clock, injectable for tests.
var remindClock = time.Now

// cliRemind is the daily reminder for a scheduler to run: it does what the
// TUI's minute tick does, so running it every few minutes from cron or a
// systemd timer reminds once, at the time set in Settings, whether or not the
// TUI is open. --now shows the reminder immediately and leaves the daily one
// alone, which is also how to check notifications work on a machine.
func cliRemind(args []string) int {
	fs := flag.NewFlagSet("remind", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	immediate := fs.Bool("now", false, "remind now, whatever the time, the Settings switch or today's reminder")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: tjek remind [--now]")
		return 2
	}
	now := remindClock()
	if !*immediate {
		settings, _ := loadSettings()
		at, on := storedReminder(settings)
		if !on {
			at = reminderOff
		}
		if !reminderDue(now, at, loadRemindedOn()) {
			return 0
		}
	}
	_, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	if !*immediate {
		saveRemindedOn(now.Format(reminderDayLayout))
	}
	overdue, today := reminderTasks(todoPtrs(todos), now)
	if len(overdue)+len(today) == 0 {
		if *immediate {
			fmt.Println("Nothing is due today or overdue.")
		}
		return 0
	}
	title, body := reminderMessage(overdue, today)
	fmt.Println(title)
	fmt.Println(body)
	if err := sendDesktopNotification(title, body); err != nil {
		fmt.Fprintf(os.Stderr, "tjek: desktop notification unavailable: %v\n", err)
		return 1
	}
	return 0
}

// cliScoreCell is a task's percentage in tjek top, or the day it starts when
// that is a later day, as the TUI's Score column shows it.
func cliScoreCell(rk rank.Ranker, t *todo.Todo, now time.Time) string {
	if rank.StartsLater(t, now) {
		return startsCell(t.StartDate, now)
	}
	return rk.FormatPercent(rk.Score(t))
}
