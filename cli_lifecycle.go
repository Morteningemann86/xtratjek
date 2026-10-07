package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// ── done ─────────────────────────────────────────────────────────────────────

// pendingDescendants returns every still-pending, non-deleted subtask beneath
// rootID (transitive). `done` uses it to avoid silently orphaning open subtasks
// when their parent is closed — they'd otherwise vanish from every list and the
// stats until only `export` could see them (e71788f0).
func pendingDescendants(children func(string) []string, get func(string) *todo.Todo, rootID string) []*todo.Todo {
	var out []*todo.Todo
	for _, id := range descendantIDsFrom(children, rootID)[1:] { // [0] is rootID itself
		if s := get(id); s != nil && s.Status == todo.Pending && !s.Deleted {
			out = append(out, s)
		}
	}
	return out
}

func cliDone(args []string) int {
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var comment string
	fs.StringVar(&comment, "comment", "", "append this comment to each task transitioned to done")
	fs.StringVar(&comment, "m", "", "shorthand for --comment (git muscle memory)")
	cascade := fs.Bool("cascade", false, "also close pending subtasks of each target (default: prompt on a TTY, else warn and leave them open)")
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek done <ref> [<ref>...] [-m \"why\"] [--cascade]")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	// Resolve every ref before mutating anything — that way an ambiguity in
	// position 3 doesn't leave the first two already toggled.
	targets, err := resolveRefs(todoPtrs(todos), positionals)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// The auto-close-subtasks preference makes cascade the default, matching
	// the TUI toggle; --cascade forces it regardless.
	settings, _ := loadSettings()
	autoCascade := *cascade || settings.AutoCloseSubtasks
	children, get := sliceTaskLookups(todos)
	var dirty, cascaded, skipped, stopped []*todo.Todo
	var spawned []todo.Todo
	closed := make(map[string]bool)
	var stdinScan *bufio.Scanner
	for _, t := range targets {
		if t.Status == todo.Done || closed[t.ID] {
			skipped = append(skipped, t)
			continue
		}
		// Guard (e71788f0): closing a parent with open subtasks hides them
		// everywhere but `export`. Decide whether to cascade, close the parent
		// only, or abort — mirroring the TUI's confirm-close-parent prompt.
		pending := pendingDescendants(children, get, t.ID)
		doCascade := autoCascade
		if len(pending) > 0 && !autoCascade {
			if stdinIsTTY() {
				if stdinScan == nil {
					stdinScan = bufio.NewScanner(os.Stdin)
				}
				fmt.Fprintf(os.Stderr, "%s %q has %d pending subtask(s). Close them too? [y]es / [n]o parent only / [a]bort: ",
					t.ID[:8], t.Title, len(pending))
				ans := ""
				if stdinScan.Scan() {
					ans = strings.ToLower(strings.TrimSpace(stdinScan.Text()))
				}
				switch ans {
				case "y", "yes":
					doCascade = true
				case "n", "no":
					// close the parent only
				default:
					fmt.Fprintf(os.Stderr, "aborted: %s left open\n", t.ID[:8])
					continue
				}
			} else {
				// Non-interactive: don't break scripts, but surface it loudly
				// so the subtasks aren't silently orphaned.
				fmt.Fprintf(os.Stderr, "warning: closing %s %q leaves %d pending subtask(s) hidden under it; rerun with --cascade to close them, or 'tjek done <subtask>'\n",
					t.ID[:8], t.Title, len(pending))
			}
		}
		// Comment lands BEFORE Toggle so the timeline reads "added the why,
		// then closed it" — and the comment timestamp matches the close.
		if comment != "" {
			t.AddComment(comment)
		}
		// Closing a task while its timer is running would leave a dangling
		// open entry; the TUI auto-stops, so the CLI matches.
		if t.IsTimerRunning() {
			t.StopTimer()
			stopped = append(stopped, t)
		}
		rank.CaptureRankAtDone(repo.ranker(), todoPtrs(todos), t)
		t.Toggle()
		closed[t.ID] = true
		dirty = append(dirty, t)
		if t.IsRecurring() {
			if next, ok := buildNextRecurrence(*t); ok {
				spawned = append(spawned, next)
				// Clone the subtree onto the new parent so a recurring
				// "weekly review" keeps its checklist on each spawn. Same
				// delta-shifted semantics as the TUI path.
				var delta time.Duration
				if !t.DueDate.IsZero() && !next.DueDate.IsZero() {
					delta = next.DueDate.Sub(t.DueDate)
				}
				spawned = append(spawned, cloneSubtreeResetFrom(children, get, t.ID, next.ID, delta)...)
			}
		}
		if doCascade {
			for _, s := range pending {
				if s.Status == todo.Done || closed[s.ID] {
					continue
				}
				if s.IsTimerRunning() {
					s.StopTimer()
					stopped = append(stopped, s)
				}
				rank.CaptureRankAtDone(repo.ranker(), todoPtrs(todos), s)
				s.Toggle()
				closed[s.ID] = true
				cascaded = append(cascaded, s)
			}
		}
	}
	saveSet := make([]*todo.Todo, 0, len(dirty)+len(cascaded)+len(spawned))
	saveSet = append(saveSet, dirty...)
	saveSet = append(saveSet, cascaded...)
	for i := range spawned {
		saveSet = append(saveSet, &spawned[i])
	}
	if len(saveSet) > 0 {
		if err := repo.Save(saveSet, nil); err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return 1
		}
	}
	for _, t := range stopped {
		fmt.Fprintf(os.Stderr, "stopped: %s  %s\n", t.ID[:8], t.Title)
	}
	for _, t := range dirty {
		fmt.Printf("done  %s  %s\n", t.ID[:8], t.Title)
	}
	for _, t := range cascaded {
		fmt.Printf("done  %s  %s  (subtask)\n", t.ID[:8], t.Title)
	}
	for _, t := range spawned {
		due := ""
		if !t.DueDate.IsZero() {
			due = "  due " + t.DueDate.Format("02-01-06")
		}
		fmt.Printf("recur %s  %s%s\n", t.ID[:8], t.Title, due)
	}
	for _, t := range skipped {
		fmt.Fprintf(os.Stderr, "already done: %s\n", t.Title)
	}
	return 0
}

// ── reopen ───────────────────────────────────────────────────────────────────

// cliReopen is `done`'s counterpart: it moves tasks back to pending. Without
// it the CLI could close a task but never undo that, so a mistyped ref in a
// batch `tjek done a b c` could only be repaired by opening the TUI — which
// makes every scripted or remote close riskier than it needs to be.
//
// Deliberately not a "toggle": a verb that closes a pending task when you
// meant to reopen a done one is exactly the wrong thing to hand a script.
// Tasks that are already pending are reported and skipped.
func cliReopen(args []string) int {
	fs := flag.NewFlagSet("reopen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var comment string
	fs.StringVar(&comment, "comment", "", "append this comment to each task reopened")
	fs.StringVar(&comment, "m", "", "shorthand for --comment (git muscle memory)")
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek reopen <ref> [<ref>...] [-m \"why\"]")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	// A done task can only be found by a ref search that includes done tasks;
	// resolveRefs searches the whole set, so an id prefix or a title substring
	// both work here.
	targets, err := resolveRefs(todoPtrs(todos), positionals)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_, get := sliceTaskLookups(todos)
	var dirty, skipped []*todo.Todo
	for _, t := range targets {
		if t.Status != todo.Done {
			skipped = append(skipped, t)
			continue
		}
		if comment != "" {
			t.AddComment(comment)
		}
		// Toggle clears CompletedAt and SeqRankAtDone — the rank was a reading
		// taken at completion, and this task hasn't completed after all.
		t.Toggle()
		dirty = append(dirty, t)
	}
	if len(dirty) == 0 {
		for _, t := range skipped {
			fmt.Fprintf(os.Stderr, "already pending: %s\n", t.Title)
		}
		return 0
	}
	if err := repo.Save(dirty, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	for _, t := range dirty {
		fmt.Printf("reopened  %s  %s\n", t.ID[:8], t.Title)
		// A subtask under a closed parent is invisible in every list but
		// `export` — the same trap `done` warns about from the other side.
		if t.ParentID != "" {
			if p := get(t.ParentID); p != nil && p.Status == todo.Done {
				fmt.Fprintf(os.Stderr, "note: parent %s %q is still done; reopen it too, or this subtask stays hidden\n",
					p.ID[:8], p.Title)
			}
		}
	}
	for _, t := range skipped {
		fmt.Fprintf(os.Stderr, "already pending: %s\n", t.Title)
	}
	return 0
}

// ── delete ───────────────────────────────────────────────────────────────────

func cliDelete(args []string) int {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("f", false, "skip the confirmation prompt (title-substring matches only; id refs never prompt)")
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek delete <ref> [-f]")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	t, kind, err := findTaskByRefKind(todoPtrs(todos), positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	children, get := sliceTaskLookups(todos)
	ids := descendantIDsFrom(children, t.ID)
	// A title-substring ref is fuzzy — the match may not be the task the user
	// meant, and delete is the one verb where that's expensive. Confirm on the
	// fuzzy path only; exact id/prefix refs stay script-fast, and -f opts out.
	if kind == refMatchTitle && !*force {
		what := fmt.Sprintf("delete %s  %q", t.ID[:8], t.Title)
		if extra := len(ids) - 1; extra > 0 {
			what += fmt.Sprintf(" (+%d subtask(s))", extra)
		}
		if !stdinIsTTY() {
			fmt.Fprintf(os.Stderr, "tjek delete: %q matched by title substring and confirmation needs a terminal; use the id prefix %s, or -f\n",
				positionals[0], t.ID[:8])
			return 2
		}
		if !confirmStdin(what + "?") {
			fmt.Fprintln(os.Stderr, "aborted")
			return 1
		}
	}
	// Soft delete via the Repository contract — the row is tombstoned and
	// will not load again. Matches the TUI's delete semantics: cascade to
	// every descendant so subtasks don't get stranded with a parent_id
	// pointing at a tombstone.
	deletedAt := time.Now()
	tombstones := make(map[string]time.Time, len(ids))
	for _, id := range ids {
		tombstones[id] = deletedAt
	}
	if err := repo.Save(nil, tombstones); err != nil {
		fmt.Fprintf(os.Stderr, "delete: %v\n", err)
		return 1
	}
	// Record the pre-delete states in the undo sidecar so `tjek undo` (and
	// the TUI, which seeds its undo stack from the same file) can restore
	// this. Best-effort: a persist failure must not fail the delete — but the
	// "(recoverable…)" hint below is printed only when recording succeeded.
	entry := undoEntry{desc: undoDescDeleteTask, ids: ids}
	for _, id := range ids {
		if x := get(id); x != nil {
			entry.partial = append(entry.partial, copyTodo(*x))
		}
	}
	recorded := recordDeleteUndo(entry)
	if extra := len(ids) - 1; extra > 0 {
		noun := "subtask"
		if extra != 1 {
			noun = "subtasks"
		}
		fmt.Printf("deleted %s  %s  (+%d %s)\n", t.ID[:8], t.Title, extra, noun)
	} else {
		fmt.Printf("deleted %s  %s\n", t.ID[:8], t.Title)
	}
	if recorded {
		fmt.Fprintln(os.Stderr, "(recoverable with `tjek undo`)")
	}
	return 0
}

// ── undelete ─────────────────────────────────────────────────────────────────

// cliUndelete restores a soft-deleted task (and any deleted descendants) from
// the tombstones the delete verb leaves in SQLite. Where `undo` pops the most
// recent deletion LIFO, undelete targets a specific task by ref and `--list`
// browses what's recoverable. Tombstones live for tombstoneRetention (180 days),
// so anything inside that window can be brought back.
func cliUndelete(args []string) int {
	fs := flag.NewFlagSet("undelete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	list := fs.Bool("list", false, "list the deleted tasks that can be restored instead of restoring one")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tjek undelete <ref>   |   tjek undelete --list")
		fs.PrintDefaults()
	}
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}

	if err := openStore(); err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	all, err := loadTodosForSync(db) // includes tombstones (Deleted/DeletedAt set)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	var deleted []todo.Todo
	for i := range all {
		if all[i].Deleted {
			deleted = append(deleted, all[i])
		}
	}

	if *list || len(positionals) == 0 {
		if len(deleted) == 0 {
			fmt.Println("(no deleted tasks to restore)")
			return 0
		}
		sort.Slice(deleted, func(i, j int) bool { // newest deletions first
			return deleted[i].DeletedAt.After(deleted[j].DeletedAt)
		})
		for i := range deleted {
			when := "unknown"
			if !deleted[i].DeletedAt.IsZero() {
				when = deleted[i].DeletedAt.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s  %s  (deleted %s)\n", deleted[i].ID[:8], deleted[i].Title, when)
		}
		return 0
	}

	t, err := findTaskByRef(todoPtrs(deleted), positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// Restore the task and any of its deleted descendants — delete cascades, so a
	// subtree went down together and comes back together. Bump ModifiedAt so the
	// un-delete wins over the tombstone in a later sync merge. StampModified is
	// clamped against the tombstone's DeletedAt so a slow local clock can't stamp
	// an older ModifiedAt that loses to the deletion in the merge.
	children, get := sliceTaskLookups(all)
	restored := make([]todo.Todo, 0, 1)
	for _, id := range descendantIDsFrom(children, t.ID) {
		x := get(id)
		if x == nil || !x.Deleted {
			continue
		}
		c := copyTodo(*x)
		c.Deleted = false
		c.ModifiedAt = todo.StampModified(x.DeletedAt)
		c.DeletedAt = time.Time{}
		restored = append(restored, c)
	}
	// If the restored root's parent is itself still deleted, detach it so it
	// doesn't come back stranded under a tombstone (descendantIDsFrom yields the
	// root first, so restored[0] is it).
	if len(restored) > 0 && restored[0].ParentID != "" {
		if p := get(restored[0].ParentID); p == nil || p.Deleted {
			restored[0].ParentID = ""
		}
	}

	ptrs := make([]*todo.Todo, len(restored))
	for i := range restored {
		ptrs[i] = &restored[i]
	}
	repo := newSQLiteRepo()
	if err := repo.Save(ptrs, nil); err != nil {
		fmt.Fprintf(os.Stderr, "undelete: %v\n", err)
		return 1
	}
	if extra := len(restored) - 1; extra > 0 {
		noun := "subtask"
		if extra != 1 {
			noun = "subtasks"
		}
		fmt.Printf("restored %s  %s  (+%d %s)\n", t.ID[:8], t.Title, extra, noun)
	} else {
		fmt.Printf("restored %s  %s\n", t.ID[:8], t.Title)
	}
	return 0
}

// ── undo ─────────────────────────────────────────────────────────────────────

// cliUndo restores the most recent persisted deletion (task or subtask, with
// its captured descendants) from the undo sidecar — the same file the TUI
// seeds its cross-restart undo stack from, so a delete made in either surface
// is recoverable from either. Only deletions are persisted; everything else
// stays TUI-session undo.
func cliUndo(args []string) int {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	list := fs.Bool("list", false, "show the restorable deletions and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	entries, err := loadPersistedUndoEntries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek undo: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "tjek undo: nothing to restore (only deletions are undoable from the CLI, last 5 kept)")
		return 1
	}
	if *list {
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			title := "(no captured tasks)"
			if len(e.partial) > 0 {
				title = e.partial[0].Title
			}
			marker := " "
			if i == len(entries)-1 {
				marker = "*" // next `tjek undo` restores this one
			}
			fmt.Printf("%s %s  %q  (%d task(s))\n", marker, e.desc, title, len(e.partial))
		}
		return 0
	}

	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	live := make(map[string]bool, len(todos))
	for i := range todos {
		live[todos[i].ID] = true
	}
	entry := entries[len(entries)-1]
	var restore []*todo.Todo
	for i := range entry.partial {
		t := &entry.partial[i]
		if live[t.ID] {
			// Already back (restored in the TUI, or re-created): overwriting
			// the live row with the old snapshot would destroy newer edits.
			fmt.Fprintf(os.Stderr, "skipping %s  %s: already exists\n", t.ID[:8], t.Title)
			continue
		}
		// Stamp the restore as the latest write. The tombstone in the store
		// (and on other devices) carries a newer DeletedAt than the captured
		// pre-delete state, so without the bump the deletion would win the
		// next sync merge and quietly re-apply itself. Clamp against the live
		// tombstone's deleted_at, not just the snapshot's ModifiedAt: after a
		// slow-clock delete both the tombstone's stamp and
		// StampModified(pre-delete ModifiedAt) land on the same prev+1ms, and
		// an exact event-time tie resolves by content hash (laterWins) — a
		// coin flip the restore could lose.
		prev := t.ModifiedAt
		if d := tombstoneDeletedAt(db, t.ID); d.After(prev) {
			prev = d
		}
		t.ModifiedAt = todo.StampModified(prev)
		restore = append(restore, t)
	}
	if len(restore) == 0 {
		fmt.Fprintln(os.Stderr, "tjek undo: every task in the newest entry already exists; nothing to do")
		if perr := savePersistedUndoEntries(entries[:len(entries)-1]); perr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update undo history: %v\n", perr)
		}
		return 0
	}
	if err := repo.Save(restore, nil); err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		return 1
	}
	// Pop the consumed entry so the next undo reaches the one before it.
	if perr := savePersistedUndoEntries(entries[:len(entries)-1]); perr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update undo history: %v\n", perr)
	}
	for _, t := range restore {
		fmt.Printf("restored %s  %s\n", t.ID[:8], t.Title)
	}
	return 0
}

// stdinIsTTY reports whether stdin is an interactive terminal — the gate for
// asking a y/N question at all. A variable so a test can drive the prompting
// paths (suggest's link loop) with scripted answers on a pipe.
var stdinIsTTY = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// confirmStdin prompts on stderr (stdout stays machine-clean) and accepts
// y/yes case-insensitively; anything else, or EOF, declines.
func confirmStdin(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(sc.Text()))
	return ans == "y" || ans == "yes"
}
