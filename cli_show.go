package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── show ─────────────────────────────────────────────────────────────────────

func cliShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON instead of a formatted view")
	// Route through splitFlagsAndPositionals so `tjek show <ref> --json` works
	// the same as `tjek show --json <ref>`. Stdlib flag.Parse stops at the
	// first non-flag token, which otherwise turns a trailing --json into a
	// second positional and trips the usage check below.
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek show <ref>")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	t, err := findTaskByRef(todoPtrs(todos), positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *asJSON {
		return emitJSON(t)
	}
	// Gather subtasks from the loaded slice (parent→child via ParentID).
	// Sorted by CreatedAt to match TUI ordering.
	var subs []todo.Todo
	for _, s := range todos {
		if s.ParentID == t.ID {
			subs = append(subs, s)
		}
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].CreatedAt.Before(subs[j].CreatedAt) })
	printTaskDetail(t, subs, todos, repo.ranker())
	return 0
}

// cliWhy prints the same answer the TUI's w overlay gives: the score broken
// into its causes, the margins to the tasks either side, and the moments the
// ranking moves on its own. `tjek top` says what is next; this says why, which
// is the difference between a ranking you follow and one you argue with.
func cliWhy(args []string) int {
	fs := flag.NewFlagSet("why", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the breakdown as JSON")
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek why <ref>")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	ptrs := todoPtrs(todos)
	t, err := findTaskByRef(ptrs, positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	e := repo.ranker().Explain(t, ptrs)
	if *asJSON {
		return emitJSON(seqExplainJSONOf(e))
	}
	for _, line := range explainPlainLines(e) {
		fmt.Println(line)
	}
	return 0
}

// seqExplainJSON is the wire shape of `tjek why --json`. The internal
// rank.Explanation carries reason codes and a rank.Level, which mean nothing outside
// the binary, so the JSON form resolves them to the same sentences the text
// form prints.
type seqFactorJSON struct {
	Name     string  `json:"name"`
	Raw      float64 `json:"raw"`
	Weight   float64 `json:"weight"`
	Weighted float64 `json:"weighted"`
	Reason   string  `json:"reason"`
}

type seqShiftJSON struct {
	At     time.Time `json:"at"`
	Score  float64   `json:"score"`
	Delta  float64   `json:"delta"`
	Rank   int       `json:"rank"`
	Reason string    `json:"reason"`
}

type seqExplainJSON struct {
	Title   string          `json:"title"`
	Rank    int             `json:"rank"`
	Of      int             `json:"of"`
	Score   float64         `json:"score"`
	Ranked  float64         `json:"ranked_on"`
	Boosted bool            `json:"boosted"`
	Factors []seqFactorJSON `json:"factors"`
	Shifts  []seqShiftJSON  `json:"shifts,omitempty"`
}

func seqExplainJSONOf(e rank.Explanation) seqExplainJSON {
	out := seqExplainJSON{
		Title: e.Title, Rank: e.Pos, Of: e.Of,
		Score: e.Total, Ranked: e.Ranked, Boosted: e.Boosted,
	}
	for _, f := range e.Factors {
		out.Factors = append(out.Factors, seqFactorJSON{f.Name, f.Raw, f.Weight, f.Weighted, trSeqReason(f)})
	}
	for _, s := range e.Shifts {
		out.Shifts = append(out.Shifts, seqShiftJSON{s.At, s.Total, s.Delta, s.Pos, trShiftCause(s.Cause)})
	}
	return out
}

func printTaskDetail(t *todo.Todo, subs []todo.Todo, todos []todo.Todo, rk rank.Ranker) {
	fmt.Printf("ID:       %s\n", t.ID)
	fmt.Printf("Title:    %s\n", t.Title)
	status := "pending"
	if t.Status == todo.Done {
		status = "done"
	}
	fmt.Printf("Status:   %s\n", status)
	if t.Status == todo.Pending && t.ParentID == "" {
		fmt.Printf("Stage:    %s\n", storedBoard().stageDisplay(t.Stage))
	}
	fmt.Printf("Priority: %s\n", t.Priority.String())
	fmt.Printf("Size:     %s\n", t.Size.String())
	if !t.StartDate.IsZero() {
		layout := "2006-01-02"
		if !t.StartDate.Equal(startOfDay(t.StartDate)) {
			layout = "2006-01-02 15:04"
		}
		fmt.Printf("Start:    %s\n", t.StartDate.Format(layout))
	}
	if !t.DueDate.IsZero() {
		fmt.Printf("Due:      %s\n", t.DueDate.Format("2006-01-02"))
	}
	if t.Project != "" {
		fmt.Printf("Project:  %s\n", t.Project)
	}
	if len(t.Tags) > 0 {
		fmt.Printf("Tags:     %s\n", strings.Join(t.Tags, ", "))
	}
	if !t.CompletedAt.IsZero() {
		fmt.Printf("Done at:  %s\n", t.CompletedAt.Format("2006-01-02 15:04"))
	}
	fmt.Printf("Created:  %s\n", t.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Printf("Modified: %s\n", t.ModifiedAt.Format("2006-01-02 15:04"))

	if t.Status == todo.Pending && rank.StartsLater(t, time.Now()) {
		fmt.Printf("Score:    %s (ranked below the tasks that can start today until then)\n", startsLong(t.StartDate))
	} else if t.Status == todo.Pending {
		sc := rk.Components(t)
		// Spelled-out component names instead of single letters — the previous
		// `D/P/M/A` was a stat-readout cliff for anyone not already steeped in
		// the sequencing engine's terminology.
		// Percent of the current field, with the points that produced it —
		// `tjek why` spells out where each of them came from.
		fmt.Printf("Score:    %s  (%.1f pts: Deadline %.1f · Priority %.1f · Momentum %.1f · Size %.1f · Age %.1f)\n",
			rk.FormatPercent(sc.Total), sc.Total,
			sc.Urgency, sc.Importance, sc.Momentum, sc.Size, sc.Age)
	}
	if len(subs) > 0 {
		fmt.Printf("\nSubtasks (%d):\n", len(subs))
		for _, s := range subs {
			marker := "[ ]"
			if s.Status == todo.Done {
				marker = "[✓]"
			}
			fmt.Printf("  %s  %s  %s\n", s.ID[:8], marker, s.Title)
		}
	}
	// One merged dependency list, direction carried by the glyph: ↧ = this
	// task waits on it (outbound, stored on t), ↥ = it waits on this task
	// (inbound, derived). Mirrors the TUI detail pane.
	inbound := dependentsOf(todoPtrs(todos), t.ID)
	if len(t.Dependencies) > 0 || len(inbound) > 0 {
		fmt.Printf("\nDependencies (%d):\n", len(t.Dependencies)+len(inbound))
		for _, dep := range t.Dependencies {
			// Resolve to a title where possible; fall back to the raw id for
			// dangling references.
			line := "  - ↧ " + dep
			for i := range todos {
				if todos[i].ID == dep {
					marker := "[ ]"
					if todos[i].Status == todo.Done {
						marker = "[✓]"
					}
					line = fmt.Sprintf("  %.8s  %s ↧ %s", dep, marker, todos[i].Title)
					break
				}
			}
			fmt.Println(line)
		}
		for _, d := range inbound {
			fmt.Printf("  %.8s      ↥ %s\n", d.ID, d.Title)
		}
	}
	if len(t.Comments) > 0 {
		// 1-based indices so the user can pass them directly to
		// `tjek comment <ref> --edit=N` / `--delete=N`. Timestamp includes
		// HH:MM so multiple comments on the same day stay ordered/readable.
		fmt.Printf("\nComments (%d):\n", len(t.Comments))
		for i, c := range t.Comments {
			fmt.Printf("  %d. [%s] %s\n", i+1, c.CreatedAt.Format("2006-01-02 15:04"), c.Text)
		}
	}
	if t.Notes != "" {
		fmt.Printf("\nNotes:\n%s\n", t.Notes)
	}
}

// ── stats ────────────────────────────────────────────────────────────────────

// statsSummary is the structured shape `stats --format=json` writes and that
// the waybar formatter renders into its expected schema.
type statsSummary struct {
	Active              int `json:"active"`
	Overdue             int `json:"overdue"`
	DueToday            int `json:"due_today"`
	DueThisWeek         int `json:"due_this_week"`
	DoneToday           int `json:"done_today"`
	DoneThisWeek        int `json:"done_this_week"`
	TrackedTodayMinutes int `json:"tracked_today_minutes"`
	// Sequence hit rate inputs: of the last rank.HitWindow rank-stamped
	// completions, how many closed while in the engine's top rank.HitTopN.
	SeqHitsRecent  int `json:"seq_hits_recent"`
	SeqRatedRecent int `json:"seq_rated_recent"`
	// Seq carries the --seq miss analysis in JSON output; nil (and omitted)
	// unless the flag was passed.
	Seq *rank.Analysis `json:"seq,omitempty"`
}

func computeStats(todos []todo.Todo, now time.Time) statsSummary {
	today := startOfDay(now)
	tomorrow := today.AddDate(0, 0, 1)
	weekAhead := today.AddDate(0, 0, 7)
	weekAgo := today.AddDate(0, 0, -7)
	var s statsSummary
	for _, t := range todos {
		if t.ParentID != "" {
			continue
		}
		if t.Status == todo.Done {
			if !t.CompletedAt.IsZero() {
				if !t.CompletedAt.Before(today) {
					s.DoneToday++
				}
				if !t.CompletedAt.Before(weekAgo) {
					s.DoneThisWeek++
				}
			}
			continue
		}
		s.Active++
		if t.IsOverdueAt(now) {
			s.Overdue++
		} else if !t.DueDate.IsZero() && t.DueDate.Before(tomorrow) {
			s.DueToday++
		} else if !t.DueDate.IsZero() && t.DueDate.Before(weekAhead) {
			s.DueThisWeek++
		}
	}
	// Time tracking spans all todos (including subtasks and completed) since
	// time entries are work that happened today regardless of the parent
	// task's lifecycle. Minutes as an int keeps the JSON shape boring; the
	// text renderer formats it for humans.
	s.TrackedTodayMinutes = int(trackedTodayDuration(todos, now).Minutes())
	return s
}

// scopeForStats narrows the full task set to the top-level tasks matching the
// filters plus every descendant of a match — descendants carry the time
// entries the tracked-today scan reads, and computeStats skips them for the
// top-level counts anyway.
func scopeForStats(todos []todo.Todo, opts listFilterOpts) []todo.Todo {
	matched := make(map[string]bool)
	for _, t := range filterTopLevel(todos, opts) {
		matched[t.ID] = true
	}
	parentOf := make(map[string]string, len(todos))
	for i := range todos {
		parentOf[todos[i].ID] = todos[i].ParentID
	}
	inScope := func(id string) bool {
		for cur := id; cur != ""; cur = parentOf[cur] {
			if matched[cur] {
				return true
			}
		}
		return false
	}
	out := make([]todo.Todo, 0, len(todos))
	for i := range todos {
		if inScope(todos[i].ID) {
			out = append(out, todos[i])
		}
	}
	return out
}

func cliStats(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	format := fs.String("format", "text", "output format: text | json | waybar")
	seq := fs.Bool("seq", false, "append the sequence miss analysis (why completions closed outside the top-5)")
	tag := fs.String("tag", "", "restrict stats to tasks carrying this tag (case-insensitive)")
	project := fs.String("project", "", "restrict stats to tasks in this project")
	search := fs.String("search", "", "restrict stats to tasks whose title contains this substring")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch strings.ToLower(*format) {
	case "text", "json", "waybar":
	default:
		// A status-bar script that misspells the format would otherwise get
		// the text line and fail to parse it, far from the cause.
		fmt.Fprintf(os.Stderr, "tjek stats: unknown --format %q (use text|json|waybar)\n", *format)
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	scoped := todos
	if *tag != "" || *project != "" || *search != "" {
		scoped = scopeForStats(todos, listFilterOpts{
			includeDone: true, // the done/tracked buckets need completed rows
			tag:         *tag,
			project:     *project,
			search:      *search,
		})
	}
	s := computeStats(scoped, time.Now())
	s.SeqHitsRecent, s.SeqRatedRecent = rank.HitStats(todoPtrs(scoped), rank.HitWindow)
	if *seq {
		// Heat always reconstructs from the full set: completions outside the
		// filter still warmed their projects/tags at the time.
		a := rank.AnalyzeMisses(todoPtrs(scoped), todoPtrs(todos), rank.HitWindow, repo.ranker().Biases)
		s.Seq = &a
	}
	switch strings.ToLower(*format) {
	case "json":
		return emitJSON(s)
	case "waybar":
		// Waybar custom modules expect this JSON shape on stdout. `class`
		// drives the CSS state — warning when there's anything overdue or
		// due today, ok otherwise. Tooltip carries the breakdown.
		class := "ok"
		switch {
		case s.Overdue > 0:
			class = "critical"
		case s.DueToday > 0:
			class = "warning"
		}
		text := fmt.Sprintf("%d active", s.Active)
		if s.Overdue > 0 {
			text = fmt.Sprintf("%d overdue · %d active", s.Overdue, s.Active)
		} else if s.DueToday > 0 {
			text = fmt.Sprintf("%d due today · %d active", s.DueToday, s.Active)
		}
		tracked := formatDurationCompact(time.Duration(s.TrackedTodayMinutes) * time.Minute)
		tooltip := fmt.Sprintf("active %d · overdue %d · due today %d · due this week %d · done today %d · tracked today %s",
			s.Active, s.Overdue, s.DueToday, s.DueThisWeek, s.DoneToday, tracked)
		return emitJSON(map[string]string{"text": text, "tooltip": tooltip, "class": class})
	default:
		tracked := formatDurationCompact(time.Duration(s.TrackedTodayMinutes) * time.Minute)
		line := fmt.Sprintf("active %d · overdue %d · due today %d · due this week %d · done today %d · tracked today %s",
			s.Active, s.Overdue, s.DueToday, s.DueThisWeek, s.DoneToday, tracked)
		// Hidden until rank-stamped completions exist, so a fresh install
		// doesn't advertise a 0/0 metric.
		if s.SeqRatedRecent > 0 {
			line += fmt.Sprintf(" · seq hit %d%% (%d/%d top-%d)",
				100*s.SeqHitsRecent/s.SeqRatedRecent, s.SeqHitsRecent, s.SeqRatedRecent, rank.HitTopN)
		}
		fmt.Println(line)
		if s.Seq != nil {
			fmt.Print("\n" + renderSeqAnalysisText(*s.Seq, repo.ranker().Biases))
		}
		return 0
	}
}

// seqMissDisplayCap bounds the per-miss listing in the --seq text output; the
// full set is always in the JSON form.
const seqMissDisplayCap = 5

// renderSeqAnalysisText renders the stats --seq block: the hit/miss dimension
// table, the bias suggestion, and the most recent misses. The hit rate itself
// is already on the stats summary line above it, so it isn't repeated here.
func renderSeqAnalysisText(a rank.Analysis, b rank.Biases) string {
	if a.Rated == 0 {
		return "no rank-stamped completions yet; the analysis needs a few finished tasks\n"
	}
	misses := a.Rated - a.Hits
	if misses == 0 {
		return fmt.Sprintf("no misses in the last %d rated completions: every one closed as a top-%d pick\n", a.Rated, a.TopN)
	}
	var sb strings.Builder
	if a.Hits == 0 {
		fmt.Fprintf(&sb, "all %d rated completions closed outside the top-%d, so there are no hits to compare against\n\n", a.Rated, a.TopN)
	}
	largest, largestAbs := -1, 0.0
	for d := range a.Gap {
		if abs := math.Abs(a.Gap[d]); abs > largestAbs {
			largest, largestAbs = d, abs
		}
	}
	sb.WriteString("             avg contribution at completion\n")
	fmt.Fprintf(&sb, "%-10s  %6s  %6s  %6s\n", "dimension", "hits", "misses", "gap")
	for d := range rank.DimNames {
		marker := ""
		if d == largest && largestAbs >= 0.05 {
			marker = "  ◂ largest gap"
		}
		fmt.Fprintf(&sb, "%-10s  %6.1f  %6.1f  %+6.1f%s\n",
			rank.DimNames[d], a.HitAvg[d], a.MissAvg[d], a.Gap[d], marker)
	}
	if hint := rank.Suggestion(a, b); hint != "" {
		sb.WriteString("\n" + hint + "\n")
	}
	sb.WriteString("\nrecent misses:\n")
	for i, r := range a.Misses {
		if i == seqMissDisplayCap {
			fmt.Fprintf(&sb, "  … %d more (use --format=json for all)\n", len(a.Misses)-i)
			break
		}
		title := r.Title
		if len([]rune(title)) > 44 {
			title = string([]rune(title)[:43]) + "…"
		}
		line := fmt.Sprintf("  rank %3d  %-44s", r.Rank, title)
		if r.Weakest != "" {
			line += "  weakest: " + r.Weakest
		}
		sb.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	sb.WriteString("\n(dimensions recomputed at each completion's timestamp from current task fields and rank.Biases)\n")
	return sb.String()
}
