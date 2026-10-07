package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── edit ─────────────────────────────────────────────────────────────────────

func cliEdit(args []string) int {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	title := fs.String("title", "", "new title")
	// --priority is an alias for --p; both share the same destination.
	var editPriorityVal string
	fs.StringVar(&editPriorityVal, "p", "", "new priority: h|m|l")
	fs.StringVar(&editPriorityVal, "priority", "", "new priority: h|m|l (alias for --p)")
	priority := &editPriorityVal
	size := fs.String("size", "", "new size: s|m|l")
	due := fs.String("due", "", "set due date (today|tomorrow|+3d|dd-mm-yy|...)")
	clearDue := fs.Bool("clear-due", false, "drop the due date")
	start := fs.String("start", "", "set start date")
	clearStart := fs.Bool("clear-start", false, "drop the start date")
	project := fs.String("project", "", "set project name")
	clearProject := fs.Bool("clear-project", false, "drop the project")
	stage := fs.String("stage", "", "move to a board stage (a name from settings.json \"stages\")")
	addTag := fs.String("add-tag", "", "comma-separated tags to add")
	removeTag := fs.String("remove-tag", "", "comma-separated tags to remove")
	addDep := fs.String("add-dep", "", "add a dependency (ref to an existing task; refused if it would loop)")
	removeDep := fs.String("remove-dep", "", "remove a dependency (ref to a currently-depended-on task)")
	note := fs.String("note", "", "replace the task's notes ('-' reads from stdin)")
	appendNote := fs.String("append-note", "", "append a paragraph to the task's notes ('-' reads from stdin)")
	clearNote := fs.Bool("clear-note", false, "drop the notes")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tjek edit <ref> [<ref>...] [flags]")
		fs.PrintDefaults()
	}
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "tjek edit: at least one ref required")
		return 2
	}
	// A title is one task's identity — applying the same one to several is
	// always a mistake, so it's refused rather than obeyed. Every other flag
	// here is a property several tasks can genuinely share.
	if *title != "" && len(positionals) > 1 {
		fmt.Fprintln(os.Stderr, "tjek edit: --title takes one ref (it would give every task the same title)")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	// Resolve every ref before mutating anything, like `done` — an ambiguity
	// in the third ref shouldn't leave the first two edited.
	targets, err := resolveRefs(todoPtrs(todos), positionals)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// Read stdin ONCE, ahead of the loop: '-' can only be consumed a single
	// time, so resolving it per task would give the second task an empty note.
	noteText, appendText := *note, *appendNote
	if *note == "-" || *appendNote == "-" {
		text, terr := noteFlagText("-", os.Stdin)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "stdin: %v\n", terr)
			return 1
		}
		if *note == "-" {
			noteText = text
		} else {
			appendText = text
		}
	}
	var saveSet []*todo.Todo
	var edited []*todo.Todo
	var allPropagated, allBumped, allCapped, allMoved []*todo.Todo
	for _, t := range targets {
		changed, code := editOneTask(t, todos, editFields{
			title: *title, priority: *priority, size: *size, stage: *stage,
			due: *due, clearDue: *clearDue, start: *start, clearStart: *clearStart,
			project: *project, clearProject: *clearProject,
			addTag: *addTag, removeTag: *removeTag,
			addDep: *addDep, removeDep: *removeDep,
			note: noteText, appendNote: appendText, clearNote: *clearNote,
		}, &saveSet, &allPropagated, &allBumped, &allCapped, &allMoved)
		if code != 0 {
			return code
		}
		if changed {
			edited = append(edited, t)
		}
	}
	if len(saveSet) == 0 {
		fmt.Fprintln(os.Stderr, "tjek edit: no fields changed (nothing to save)")
		return 0
	}
	if err := repo.Save(saveSet, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	for _, t := range edited {
		fmt.Printf("edited  %s  %s\n", t.ID[:8], t.Title)
	}
	// Propagation and ancestor bumps are side-effects of the edit, not its
	// result — stderr keeps scripting on stdout clean.
	for _, child := range allPropagated {
		if child.DueDate.IsZero() {
			fmt.Fprintf(os.Stderr, "cleared  %s  %s  due\n", child.ID[:8], child.Title)
		} else {
			fmt.Fprintf(os.Stderr, "updated  %s  %s  due → %s\n", child.ID[:8], child.Title, child.DueDate.Format("02-01-06"))
		}
	}
	for _, a := range allBumped {
		fmt.Fprintf(os.Stderr, "bumped  %s  %s  due → %s\n", a.ID[:8], a.Title, a.DueDate.Format("02-01-06"))
	}
	for _, c := range allCapped {
		fmt.Fprintf(os.Stderr, "capped  %s  %s  priority → %s\n", c.ID[:8], c.Title, c.Priority.String())
	}
	for _, c := range allMoved {
		if c.Project == "" {
			fmt.Fprintf(os.Stderr, "cleared  %s  %s  project\n", c.ID[:8], c.Title)
		} else {
			fmt.Fprintf(os.Stderr, "moved  %s  %s  project → %s\n", c.ID[:8], c.Title, c.Project)
		}
	}
	return 0
}

// editFields carries the resolved --edit flags into editOneTask. It exists so
// the per-task mutation is one body rather than one per ref: `tjek edit a b c
// --project hoth` and `tjek edit a --project hoth` must mean the same thing to
// each task they touch.
type editFields struct {
	title, priority, size, stage       string
	due, start, project                string
	clearDue, clearStart, clearProject bool
	addTag, removeTag                  string
	addDep, removeDep                  string
	note, appendNote                   string
	clearNote                          bool
}

// editOneTask applies f to t, appending anything that needs saving to saveSet
// (and any due-date propagation to the two side-effect lists). It returns a
// non-zero exit code on a usage error, having reported it — the caller returns
// before any Save, so a failure on the third ref leaves nothing persisted.
func editOneTask(t *todo.Todo, todos []todo.Todo, f editFields, saveSet, propagatedOut, bumpedOut, cappedOut, movedOut *[]*todo.Todo) (bool, int) {
	changed := false
	priorityEdited := false
	title, priority, size, stage := &f.title, &f.priority, &f.size, &f.stage
	due, start, project := &f.due, &f.start, &f.project
	clearDue, clearStart, clearProject := &f.clearDue, &f.clearStart, &f.clearProject
	addTag, removeTag, addDep, removeDep := &f.addTag, &f.removeTag, &f.addDep, &f.removeDep
	note, appendNote, clearNote := &f.note, &f.appendNote, &f.clearNote
	if *title != "" {
		t.Title = todo.CapitalizeTitle(*title)
		t.ModifiedAt = todo.StampModified(t.ModifiedAt)
		changed = true
	}
	if *priority != "" {
		t.SetPriority(parsePriorityFlag(*priority))
		changed = true
		priorityEdited = true
	}
	if *size != "" {
		t.SetSize(parseSizeFlag(*size))
		changed = true
	}
	if *stage != "" {
		board := storedBoard()
		name, ok := board.canonicalStage(*stage)
		if !ok {
			fmt.Fprintf(os.Stderr, "tjek edit: unknown stage %q (configured: %s)\n", *stage, strings.Join(board.pending(), ", "))
			return false, 2
		}
		t.SetStage(name)
		changed = true
	}
	if *clearDue {
		t.DueDate = time.Time{}
		t.ModifiedAt = todo.StampModified(t.ModifiedAt)
		changed = true
	} else if *due != "" {
		d, err := parseDueDate(*due)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid due date %q: %v\n", *due, err)
			return false, 2
		}
		t.SetDueDate(d)
		changed = true
	}
	if *clearStart {
		t.StartDate = time.Time{}
		t.ModifiedAt = todo.StampModified(t.ModifiedAt)
		changed = true
	} else if *start != "" {
		d, err := parseDueDate(*start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid start date %q: %v\n", *start, err)
			return false, 2
		}
		t.SetStartDate(d)
		changed = true
	}
	if *clearProject {
		t.SetProject("")
		changed = true
	} else if *project != "" {
		t.SetProject(*project)
		changed = true
	}
	if *clearProject || *project != "" {
		children, get := sliceTaskLookups(todos)
		moved := propagateDescendantsProject(children, get, t)
		*saveSet = append(*saveSet, moved...)
		*movedOut = append(*movedOut, moved...)
	}
	if *addTag != "" {
		for _, tag := range strings.Split(*addTag, ",") {
			t.AddTag(tag)
		}
		changed = true
	}
	if *removeTag != "" {
		for _, tag := range strings.Split(*removeTag, ",") {
			t.RemoveTag(tag)
		}
		changed = true
	}
	if *addDep != "" {
		dep, err := findTaskByRef(todoPtrs(todos), *addDep)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return false, 2
		}
		// Refuse a dependency that would close a loop: dep already (transitively)
		// depends on t, or is t itself. Same rule the TUI picker filters by.
		byID := make(map[string]*todo.Todo, len(todos))
		for i := range todos {
			byID[todos[i].ID] = &todos[i]
		}
		if loopingDepCandidates(byID, t.ID)[dep.ID] {
			fmt.Fprintf(os.Stderr, "tjek edit: %q can't depend on %q: it would create a dependency loop\n", t.Title, dep.Title)
			return false, 2
		}
		t.AddDependency(dep.ID)
		changed = true
	}
	if *removeDep != "" {
		dep, err := findTaskByRef(todoPtrs(todos), *removeDep)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return false, 2
		}
		t.RemoveDependency(dep.ID)
		changed = true
	}
	// Notes: clear wins, then replace, then append (a new paragraph). The '-' stdin form is resolved by the caller, once.
	switch {
	case *clearNote:
		t.SetNotes("")
		changed = true
	case *note != "":
		t.SetNotes(*note)
		changed = true
	case *appendNote != "":
		if t.Notes == "" {
			t.SetNotes(*appendNote)
		} else {
			t.SetNotes(t.Notes + "\n\n" + *appendNote)
		}
		changed = true
	}
	if !changed {
		return changed, 0
	}
	*saveSet = append(*saveSet, t)
	if priorityEdited {
		children, get := sliceTaskLookups(todos)
		// Cap the task itself against its parent first — a subtask never lifts
		// the parent (see the note in taskops.go) — then push the value that
		// survived down the subtree, so a parent moved to low takes its
		// children with it. In that order each task is clamped once.
		if clampPriorityToParent(get, t) {
			*cappedOut = append(*cappedOut, t) // already in saveSet
		}
		capped := clampDescendantsPriority(children, get, t)
		*saveSet = append(*saveSet, capped...)
		*cappedOut = append(*cappedOut, capped...)
	}
	if *clearDue || *due != "" {
		children, get := sliceTaskLookups(todos)
		// A parent deadline applies to the full subtree, including clearing it.
		propagated := propagateDescendantsDue(children, get, t)
		*saveSet = append(*saveSet, propagated...)
		*propagatedOut = append(*propagatedOut, propagated...)
		// If a subtask's due moved later, also extend every ancestor whose due
		// falls short of the child — mirrors the TUI flow.
		if *due != "" && t.ParentID != "" {
			bumped := extendAncestorsDue(get, t)
			*saveSet = append(*saveSet, bumped...)
			*bumpedOut = append(*bumpedOut, bumped...)
		}
	}
	return changed, 0
}

// ── comment ──────────────────────────────────────────────────────────────────

func cliComment(args []string) int {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	editIdx := fs.Int("edit", 0, "1-based comment index to edit (with new text as positional)")
	delIdx := fs.Int("delete", 0, "1-based comment index to delete")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage:
  tjek comment <ref> "text"              append a new comment
  tjek comment <ref> -                   read comment text from stdin
  tjek comment <ref> --edit=N "new text" edit comment N (1-based)
  tjek comment <ref> --delete=N          delete comment N (1-based)`)
		fs.PrintDefaults()
	}
	// comment supports interspersed flags so --edit / --delete can sit
	// before or after the ref, just like other mutation commands.
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) < 1 {
		fs.Usage()
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
	switch {
	case *delIdx > 0:
		// Delete: index is 1-based for humans, 0-based internally.
		i := *delIdx - 1
		if i < 0 || i >= len(t.Comments) {
			fmt.Fprintf(os.Stderr, "comment index %d out of range (task has %d comments)\n", *delIdx, len(t.Comments))
			return 2
		}
		t.DeleteComment(i)
		if err := repo.Save([]*todo.Todo{t}, nil); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return 1
		}
		fmt.Printf("deleted comment %d on %s\n", *delIdx, t.ID[:8])
		return 0
	case *editIdx > 0:
		i := *editIdx - 1
		if i < 0 || i >= len(t.Comments) {
			fmt.Fprintf(os.Stderr, "comment index %d out of range (task has %d comments)\n", *editIdx, len(t.Comments))
			return 2
		}
		if len(positionals) < 2 {
			fmt.Fprintln(os.Stderr, "tjek comment --edit: new comment text required")
			return 2
		}
		text, terr := commentTextFromPositionals(positionals[1:], os.Stdin)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "stdin: %v\n", terr)
			return 1
		}
		t.UpdateComment(i, text)
		if err := repo.Save([]*todo.Todo{t}, nil); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return 1
		}
		fmt.Printf("edited comment %d on %s\n", *editIdx, t.ID[:8])
		return 0
	default:
		// Append (default behavior).
		if len(positionals) < 2 {
			fs.Usage()
			return 2
		}
		text, terr := commentTextFromPositionals(positionals[1:], os.Stdin)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "stdin: %v\n", terr)
			return 1
		}
		t.AddComment(text)
		if err := repo.Save([]*todo.Todo{t}, nil); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return 1
		}
		fmt.Printf("commented on %s\n", t.ID[:8])
		return 0
	}
}

// commentTextFromPositionals resolves the user's comment text. If the single
// positional is "-", read everything from the given reader (lets `tjek
// comment <ref> -` accept piped or here-doc input for long comments instead
// of forcing shell-escape gymnastics). Trailing newline trimmed so a heredoc
// doesn't leave a blank line in the comment.
func commentTextFromPositionals(positionals []string, r io.Reader) (string, error) {
	if len(positionals) == 1 && positionals[0] == "-" {
		b, err := io.ReadAll(r)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return strings.Join(positionals, " "), nil
}
