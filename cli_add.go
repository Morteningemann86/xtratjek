package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── add ──────────────────────────────────────────────────────────────────────

func cliAdd(args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	due := fs.String("due", "", "due date (today|tomorrow|+3d|dd-mm-yy|...)")
	// Defaults stay empty so --like can fill them; without --like, todo.New
	// already sets Medium for both, so the user-visible behavior is unchanged.
	// --priority is an alias for --p; both share the same destination.
	var priorityVal string
	fs.StringVar(&priorityVal, "p", "", "priority: h|m|l (default m, or copied from --like)")
	fs.StringVar(&priorityVal, "priority", "", "priority: h|m|l (alias for --p)")
	size := fs.String("size", "", "size: s|m|l (default m, or copied from --like)")
	project := fs.String("project", "", "project name")
	tags := fs.String("tag", "", "comma-separated tags")
	recur := fs.String("recur", "", "recurrence rule: daily|weekly|monthly|yearly|weekdays|Nd|Nw|Nm|Ny")
	stage := fs.String("stage", "", "board stage (a name from settings.json \"stages\"; default = first stage)")
	depends := fs.String("depends", "", "make the new task depend on an existing task ref, or ^ for the last-added task")
	chain := fs.Bool("chain", false, "batch add (-) only: each line depends on the previous line's task")
	like := fs.String("like", "", "clone priority/size/project/tags from an existing task ref")
	note := fs.String("note", "", "set the task's notes field (freeform body; '-' reads from stdin)")
	comment := fs.String("comment", "", "add an initial timestamped comment to the task")
	startNow := fs.Bool("start", false, "start the time tracker on the new task (stops any other running timer first)")
	asJSON := fs.Bool("json", false, "emit the created task as JSON (includes its id) instead of the human line")
	quietID := fs.Bool("quiet-id", false, "print only the new task's full id (for scripting / shell capture)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tjek add \"title\" [flags]   (or `tjek add -` to read one title per line from stdin)")
		fmt.Fprintln(os.Stderr, "  the title accepts the same quick-add tokens as the TUI: #tag @project due:friday p:high s:l r:weekly dep:^  (flags override tokens)")
		fs.PrintDefaults()
	}
	priority := &priorityVal
	flagArgs, titleParts := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(titleParts) == 0 {
		fmt.Fprintln(os.Stderr, "tjek add: title required")
		return 2
	}
	settings, sErr := loadSettings()
	if sErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (using defaults)\n", sErr)
	}
	board := boardConfigFromSettings(settings)
	repo := newSQLiteRepo()
	repo.SetRanker(rank.Ranker{Biases: biasesFromSettings(settings)})

	// Resolve everything that's shared across all created tasks exactly once
	// (parsing and ref lookups), so batch add doesn't re-do it per line.
	var dueDate time.Time
	if *due != "" {
		d, err := parseDueDate(*due)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid due date %q: %v\n", *due, err)
			return 2
		}
		dueDate = d
	}
	var recurRule string
	if *recur != "" {
		canonical, ok := todo.ParseRecurrence(*recur)
		if !ok {
			fmt.Fprintf(os.Stderr, "invalid recurrence %q: use daily|weekly|monthly|yearly|weekdays|Nd|Nw|Nm|Ny\n", *recur)
			return 2
		}
		recurRule = canonical
	}

	// Titles: the joined positionals, or — when the sole positional is "-" —
	// one per non-empty stdin line (batch add). Batch shares all flags across
	// every task and writes them in a single transaction (one save, one sync).
	batch := len(titleParts) == 1 && titleParts[0] == "-"
	titles := []string{strings.Join(titleParts, " ")}
	if *chain && !batch {
		fmt.Fprintln(os.Stderr, "tjek add: --chain only applies to batch stdin add (-); for a single task use --depends ^")
		return 2
	}
	// Detect the stdin conflict: `add -` (batch titles from stdin) + `--note -`
	// (note body from stdin) can't both read the same stream.
	if batch && *note == "-" {
		fmt.Fprintln(os.Stderr, "tjek add: --note - and batch stdin add (-) both need stdin; use --note=TEXT or drop one")
		return 2
	}
	if batch {
		if *startNow {
			fmt.Fprintln(os.Stderr, "tjek add: --start can't be combined with batch stdin add (-)")
			return 2
		}
		lines, err := readTitlesFromStdin(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			return 1
		}
		if len(lines) == 0 {
			fmt.Fprintln(os.Stderr, "tjek add: no titles on stdin")
			return 2
		}
		titles = lines
	}
	// Resolve --stage once; an unknown name fails before anything is written.
	var stageName string
	if *stage != "" {
		name, ok := board.canonicalStage(*stage)
		if !ok {
			fmt.Fprintf(os.Stderr, "tjek add: unknown stage %q (configured: %s)\n", *stage, strings.Join(board.pending(), ", "))
			return 2
		}
		stageName = name
	}

	// Resolve --note value once (before buildTask, so stdin is only consumed
	// once even though buildTask is called in a loop). For '-', read from stdin
	// — same helper as `edit --note -` / `edit --append-note -`.
	var noteText string
	if *note != "" {
		text, terr := noteFlagText(*note, os.Stdin)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "stdin: %v\n", terr)
			return 1
		}
		noteText = text
	}

	// Parse quick-add tokens (#tag @project due: p: s: r: dep:) out of every
	// title so the CLI understands the same grammar as the TUI — a line is
	// copy-pasteable between them. Explicit flags below still win; a dep: token
	// needs the existing set, exactly like --depends.
	parsedTitles := make([]parsedTask, len(titles))
	anyTokenDeps := false
	for i, ti := range titles {
		parsedTitles[i] = parseQuickAdd(ti)
		if parsedTitles[i].title == "" {
			fmt.Fprintf(os.Stderr, "tjek add: title required (%q has nothing but tokens)\n", strings.TrimSpace(ti))
			return 2
		}
		if len(parsedTitles[i].deps) > 0 {
			anyTokenDeps = true
		}
		for _, tok := range parsedTitles[i].unparsed {
			fmt.Fprintf(os.Stderr, "tjek add: warning: %q not understood, kept in the title\n", tok)
		}
	}

	// --like / --depends / --start (and any dep: token) need the existing set;
	// load it once.
	var (
		existing []todo.Todo
		likeSrc  *todo.Todo
		depID    string
		depTask  *todo.Todo // kept for confirmation output after save
	)
	if *like != "" || *depends != "" || *startNow || anyTokenDeps {
		loaded, err := repo.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "load: %v\n", err)
			return 1
		}
		existing = loaded
	}
	if *like != "" {
		src, err := findTaskByRef(todoPtrs(existing), *like)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		likeSrc = src
	}
	if *depends != "" {
		dep, err := resolveDepRef(todoPtrs(existing), *depends)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		depID = dep.ID
		depTask = dep
	}
	// Resolve any dep: token refs up front (mirroring --depends), so a bad ref
	// fails cleanly before anything is written.
	tokenDepIDs := make([][]string, len(titles))
	for i := range parsedTitles {
		for _, ref := range parsedTitles[i].deps {
			dep, err := resolveDepRef(todoPtrs(existing), ref)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			tokenDepIDs[i] = append(tokenDepIDs[i], dep.ID)
		}
	}

	// buildTask stamps one task, layering precedence low→high: --like clone,
	// then the title's quick-add tokens, then explicit flags. --note is the
	// freeform body and --comment a timestamped log entry (distinct purposes).
	buildTask := func(i int) todo.Todo {
		parsed := parsedTitles[i]
		t := todo.New(parsed.title)
		if likeSrc != nil {
			t.Priority = likeSrc.Priority
			t.Size = likeSrc.Size
			t.Project = likeSrc.Project
			for _, tag := range likeSrc.Tags {
				t.AddTag(tag)
			}
		}
		// Quick-add tokens override the --like clone.
		if parsed.hasPriority {
			t.Priority = parsed.priority
		}
		if parsed.hasSize {
			t.Size = parsed.size
		}
		if !parsed.dueDate.IsZero() {
			t.DueDate = parsed.dueDate
		}
		if parsed.project != "" {
			t.Project = parsed.project
		}
		for _, tag := range parsed.tags {
			t.AddTag(tag)
		}
		if parsed.recurrence != "" {
			t.Recurrence = parsed.recurrence
		}
		for _, id := range tokenDepIDs[i] {
			t.AddDependency(id)
		}
		// Explicit flags win over both the clone and the tokens.
		if *priority != "" {
			t.Priority = parsePriorityFlag(*priority)
		}
		if *size != "" {
			t.Size = parseSizeFlag(*size)
		}
		if *due != "" {
			t.DueDate = dueDate
		}
		if *project != "" {
			t.Project = *project
		}
		if *tags != "" {
			for _, tag := range strings.Split(*tags, ",") {
				t.AddTag(tag)
			}
		}
		if recurRule != "" {
			t.Recurrence = recurRule
		}
		if depID != "" {
			t.AddDependency(depID)
		}
		if stageName != "" {
			t.Stage = stageName
		}
		if noteText != "" {
			t.SetNotes(noteText)
		}
		if *comment != "" {
			t.AddComment(*comment)
		}
		return t
	}

	created := make([]todo.Todo, len(titles))
	for i := range titles {
		created[i] = buildTask(i)
	}
	// --chain turns a batch brain-dump into a recorded sequence: a decomposed
	// plan is usually typed in execution order, so each line blocks on the
	// one before it (--depends, if also given, still applies to every line).
	if *chain {
		for i := 1; i < len(created); i++ {
			created[i].AddDependency(created[i-1].ID)
		}
	}
	dirty := make([]*todo.Todo, 0, len(created)+1)
	for i := range created {
		dirty = append(dirty, &created[i])
	}

	// --start collapses the common "add then start tracking" two-call dance
	// into one (single-task only; rejected above for batch). Stop any other
	// running timer to keep the single-timer invariant, then start + save.
	started := false
	if *startNow {
		for _, x := range stopOtherRunningTimers(existing, created[0].ID) {
			dirty = append(dirty, x)
			fmt.Fprintf(os.Stderr, "stopped: %s  %s\n", x.ID[:8], x.Title)
		}
		created[0].StartTimer()
		started = true
	}

	if err := repo.Save(dirty, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	// Record the newest task so the next add can chain onto it via ^.
	saveLastAddedID(created[len(created)-1].ID)
	if batch {
		return emitAddResultsBatch(created, *asJSON, *quietID)
	}
	return emitAddResult(&created[0], started, depTask, *asJSON, *quietID)
}

// readTitlesFromStdin returns one trimmed, non-empty title per line of r. Used
// by `tjek add -` for batch creation from a pipe or heredoc.
func readTitlesFromStdin(r io.Reader) ([]string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var titles []string
	for _, line := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			titles = append(titles, s)
		}
	}
	return titles, nil
}

// emitAddResultsBatch renders the outcome of a batch `tjek add -`: a JSON array
// under --json, one bare id per line under --quiet-id, else one human line each.
func emitAddResultsBatch(tasks []todo.Todo, asJSON, quietID bool) int {
	switch {
	case asJSON:
		return emitJSON(tasks)
	case quietID:
		for i := range tasks {
			fmt.Println(tasks[i].ID)
		}
	default:
		for i := range tasks {
			fmt.Printf("added %s  %s\n", tasks[i].ID[:8], tasks[i].Title)
		}
	}
	return 0
}

// emitAddResult renders the outcome of `tjek add`. --json emits the full
// created task so a script can read .id (or any other field); --quiet-id prints
// only the full UUID for shell capture (id=$(tjek add … --quiet-id)); otherwise
// the usual human line. started selects the --start variant's message. dep, when
// non-nil, triggers a follow-up confirmation line so the user can see that the
// dependency link took effect. Any --start "stopped:" notices already went to
// stderr, so stdout stays clean for the machine-readable modes.
func emitAddResult(t *todo.Todo, started bool, dep *todo.Todo, asJSON, quietID bool) int {
	switch {
	case asJSON:
		return emitJSON(t)
	case quietID:
		fmt.Println(t.ID)
	case started:
		fmt.Printf("added + started: %s  %s\n", t.ID[:8], t.Title)
	default:
		fmt.Printf("added %s  %s\n", t.ID[:8], t.Title)
	}
	if dep != nil && !asJSON && !quietID {
		fmt.Printf("blocked on %s  %s\n", dep.ID[:8], dep.Title)
	}
	return 0
}

// ── subtask ──────────────────────────────────────────────────────────────────

func cliSubtask(args []string) int {
	fs := flag.NewFlagSet("subtask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	each := fs.Bool("each", false, "treat each remaining positional as a separate subtask title")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage:
  tjek subtask <parent-ref> "title"                   one subtask (args after parent are joined)
  tjek subtask <parent-ref> --each "title1" "title2"  one subtask per remaining positional`)
		fs.PrintDefaults()
	}
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) < 2 {
		fs.Usage()
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	parent, err := findTaskByRef(todoPtrs(todos), positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var titles []string
	if *each {
		titles = positionals[1:]
	} else {
		// Default mode preserves the historical behavior of joining everything
		// after the parent ref into one title — that's how unquoted
		// `subtask P Buy milk` has always worked.
		titles = []string{strings.Join(positionals[1:], " ")}
	}
	subs := make([]*todo.Todo, 0, len(titles))
	withTags := storedSubtaskTags()
	for _, title := range titles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		s := todo.NewSubtask(title, parent.ID)
		s.InheritContextFrom(parent, withTags)
		subs = append(subs, &s)
	}
	if len(subs) == 0 {
		fmt.Fprintln(os.Stderr, "tjek subtask: no non-empty titles")
		return 2
	}
	if err := repo.Save(subs, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	for _, s := range subs {
		fmt.Printf("subtask of %s: %s  %s\n", parent.ID[:8], s.ID[:8], s.Title)
	}
	return 0
}
