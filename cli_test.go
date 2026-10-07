package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/todo"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. CLI verbs print to os.Stdout directly (fmt.Println / emitJSON), so
// this lets tests assert the machine-readable output modes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	// Drain while fn runs. Reading only afterwards deadlocks as soon as the
	// output exceeds the pipe buffer — which `tjek completion fish` does, and
	// which showed up as a ten-minute test timeout on Windows, where the buffer
	// is far smaller than Linux's 64 KB.
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()

	fn()
	w.Close()
	if err := <-done; err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return buf.String()
}

func TestFindByPrefix(t *testing.T) {
	a := todo.New("a")
	a.ID = "abc12345-aaaa"
	b := todo.New("b")
	b.ID = "abcdef99-bbbb"
	c := todo.New("c")
	c.ID = "fedcba00-cccc"
	todos := []todo.Todo{a, b, c}

	t.Run("unique short prefix", func(t *testing.T) {
		got, err := findByPrefix(todoPtrs(todos), "fed")
		if err != nil || got.ID != c.ID {
			t.Fatalf("got=%v err=%v, want %s", got, err, c.ID)
		}
	})

	t.Run("ambiguous prefix errors", func(t *testing.T) {
		if _, err := findByPrefix(todoPtrs(todos), "abc"); err == nil {
			t.Error("expected ambiguity error, got nil")
		}
	})

	t.Run("no match errors", func(t *testing.T) {
		if _, err := findByPrefix(todoPtrs(todos), "zzz"); err == nil {
			t.Error("expected no-match error, got nil")
		}
	})

	t.Run("empty prefix rejected", func(t *testing.T) {
		if _, err := findByPrefix(todoPtrs(todos), ""); err == nil {
			t.Error("expected empty-prefix error, got nil")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got, err := findByPrefix(todoPtrs(todos), "FED")
		if err != nil || got.ID != c.ID {
			t.Fatalf("got=%v err=%v, want %s", got, err, c.ID)
		}
	})

	t.Run("full id matches one", func(t *testing.T) {
		got, err := findByPrefix(todoPtrs(todos), a.ID)
		if err != nil || got.ID != a.ID {
			t.Fatalf("got=%v err=%v, want %s", got, err, a.ID)
		}
	})
}

func TestSplitFlagsAndPositionals(t *testing.T) {
	// Value-vs-bool is now derived from the FlagSet itself: string flags
	// consume the next bare arg, bool flags don't.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.String("due", "", "")
		fs.String("p", "", "")
		fs.String("size", "", "")
		fs.Bool("json", false, "")
		return fs
	}

	cases := []struct {
		name      string
		in        []string
		wantFlags []string
		wantPos   []string
	}{
		{
			name:      "title first then flags",
			in:        []string{"Buy", "milk", "--size=s", "--p=h"},
			wantFlags: []string{"--size=s", "--p=h"},
			wantPos:   []string{"Buy", "milk"},
		},
		{
			name:      "interspersed flags consume next arg",
			in:        []string{"--p", "high", "Title", "here", "--size=s"},
			wantFlags: []string{"--p", "high", "--size=s"},
			wantPos:   []string{"Title", "here"},
		},
		{
			name:      "double-dash freezes the rest as positional",
			in:        []string{"--size=s", "--", "--p=h", "still", "title"},
			wantFlags: []string{"--size=s"},
			wantPos:   []string{"--p=h", "still", "title"},
		},
		{
			name:      "no flags",
			in:        []string{"Just", "a", "title"},
			wantFlags: nil,
			wantPos:   []string{"Just", "a", "title"},
		},
		{
			// `tjek comment <ref> -` uses bare dash for stdin; if the splitter
			// classifies it as a flag, the stdin path never fires.
			name:      "bare dash is positional, not a flag",
			in:        []string{"abc123", "-"},
			wantFlags: nil,
			wantPos:   []string{"abc123", "-"},
		},
		{
			// A bool flag must NOT swallow the next bare token — that token is
			// a positional. Guards the IsBoolFlag-based derivation.
			name:      "bool flag does not consume the next arg",
			in:        []string{"--json", "title", "words"},
			wantFlags: []string{"--json"},
			wantPos:   []string{"title", "words"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlags, gotPos := splitFlagsAndPositionals(newFS(), c.in)
			if !sliceEq(gotFlags, c.wantFlags) {
				t.Errorf("flags = %v, want %v", gotFlags, c.wantFlags)
			}
			if !sliceEq(gotPos, c.wantPos) {
				t.Errorf("positionals = %v, want %v", gotPos, c.wantPos)
			}
		})
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFindTaskByRefFallsBackToTitleSubstring covers the new ergonomic path:
// when no task ID matches the query, fall back to a case-insensitive title
// substring search. Without this, every CLI mutation requires looking up
// a UUID prefix from `tjek list` first — too much friction for daily use.
func TestFindTaskByRefFallsBackToTitleSubstring(t *testing.T) {
	a := todo.New("Buy milk")
	a.ID = "1aaaaa00-aaaa"
	b := todo.New("Call landlord")
	b.ID = "2bbbbb00-bbbb"
	c := todo.New("Read RFC")
	c.ID = "3ccccc00-cccc"
	todos := []todo.Todo{a, b, c}

	t.Run("substring matches one", func(t *testing.T) {
		got, err := findTaskByRef(todoPtrs(todos), "milk")
		if err != nil || got.ID != a.ID {
			t.Fatalf("got=%v err=%v, want a", got, err)
		}
	})

	t.Run("substring case-insensitive", func(t *testing.T) {
		got, err := findTaskByRef(todoPtrs(todos), "LANDLORD")
		if err != nil || got.ID != b.ID {
			t.Fatalf("got=%v err=%v, want b", got, err)
		}
	})

	t.Run("substring matches multiple errors", func(t *testing.T) {
		// Add a second task containing "milk" to force ambiguity.
		more := append(todos, todo.New("Buy more milk"))
		if _, err := findTaskByRef(todoPtrs(more), "milk"); err == nil {
			t.Error("expected ambiguity error")
		}
	})

	t.Run("id-prefix wins over title containing same chars", func(t *testing.T) {
		// "1aa" matches a's id prefix; if a title contained "1aa" too, the
		// id path should still win for determinism. Construct that scenario.
		shared := todo.New("contains 1aa in title")
		shared.ID = "4ddddd00-dddd"
		got, err := findTaskByRef(todoPtrs(append(todos, shared)), "1aa")
		if err != nil || got.ID != a.ID {
			t.Fatalf("got=%v err=%v, want a (id-prefix path wins)", got, err)
		}
	})

	t.Run("no match anywhere", func(t *testing.T) {
		if _, err := findTaskByRef(todoPtrs(todos), "wombat"); err == nil {
			t.Error("expected no-match error")
		}
	})

	t.Run("whitespace-only ref rejected", func(t *testing.T) {
		if _, err := findTaskByRef(todoPtrs(todos), "   "); err == nil {
			t.Error("expected empty-ref error")
		}
	})
}

// TestShowAcceptsTrailingJSONFlag locks in the fix for the bug where
// `tjek show <ref> --json` failed because stdlib flag.Parse stops at the
// first non-flag token, leaving --json as an unexpected second positional.
// cliShow now routes through splitFlagsAndPositionals; this test guards that
// path at the helper level so a future refactor can't silently revert it.
func TestShowAcceptsTrailingJSONFlag(t *testing.T) {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.Bool("json", false, "")
	flags, positionals := splitFlagsAndPositionals(fs, []string{"1ecdcc90", "--json"})
	if !sliceEq(flags, []string{"--json"}) {
		t.Errorf("flags = %v, want [--json]", flags)
	}
	if !sliceEq(positionals, []string{"1ecdcc90"}) {
		t.Errorf("positionals = %v, want [1ecdcc90]", positionals)
	}
}

// TestStartTimerOnRunningTaskRotatesEntry pins down the underlying todo
// behavior the cliStart guard relies on: an unguarded re-StartTimer call on
// an already-running task closes the existing entry and opens a new zero-gap
// one. The CLI now short-circuits before reaching this path; if todo's
// semantics ever change, the cliStart fix can be relaxed.
func TestStartTimerOnRunningTaskRotatesEntry(t *testing.T) {
	x := todo.New("track")
	x.StartTimer()
	if len(x.TimeEntries) != 1 {
		t.Fatalf("setup: want 1 entry, got %d", len(x.TimeEntries))
	}
	first := x.TimeEntries[0].ID
	x.StartTimer()
	if len(x.TimeEntries) != 2 {
		t.Fatalf("want 2 entries after re-start (proves the footgun), got %d", len(x.TimeEntries))
	}
	if x.TimeEntries[0].ID != first {
		t.Errorf("first entry id changed: was %s, now %s", first, x.TimeEntries[0].ID)
	}
	if x.TimeEntries[0].StoppedAt.IsZero() {
		t.Error("first entry should be stopped after second StartTimer")
	}
	if !x.TimeEntries[1].IsRunning() {
		t.Error("second entry should be the running one")
	}
}

func TestCliAddDependsLinksExistingTask(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"prereq task"}); code != 0 {
		t.Fatalf("add prerequisite: exit %d", code)
	}
	if code := cliAdd([]string{"dependent task", "--depends", "prereq"}); code != 0 {
		t.Fatalf("add dependent with --depends: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var dependent, prereq *todo.Todo
	for i := range todos {
		if len(todos[i].Dependencies) > 0 {
			dependent = &todos[i]
		} else {
			prereq = &todos[i]
		}
	}
	if dependent == nil || prereq == nil {
		t.Fatalf("want one task with a dependency and one without; got %d tasks", len(todos))
	}
	if len(dependent.Dependencies) != 1 || dependent.Dependencies[0] != prereq.ID {
		t.Fatalf("dependent.Dependencies = %v, want [%s]", dependent.Dependencies, prereq.ID)
	}
}

func TestCliAddNoteAndComment(t *testing.T) {
	setTestHome(t, t.TempDir())
	title := "atomic-note-comment-check"
	if code := cliAdd([]string{title, "--note", "the freeform body", "--comment", "first log entry"}); code != 0 {
		t.Fatalf("add with --note/--comment: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), title)
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Notes != "the freeform body" {
		t.Errorf("Notes = %q, want %q", got.Notes, "the freeform body")
	}
	if len(got.Comments) != 1 || got.Comments[0].Text != "first log entry" {
		t.Errorf("Comments = %+v, want a single comment %q", got.Comments, "first log entry")
	}
}

func TestCliAddQuietIDPrintsOnlyID(t *testing.T) {
	setTestHome(t, t.TempDir())
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"quiet-id-check", "--quiet-id"}); code != 0 {
			t.Fatalf("add --quiet-id: exit %d", code)
		}
	})
	id := strings.TrimSpace(out)
	if strings.ContainsAny(id, " \n") || strings.Contains(out, "added") {
		t.Fatalf("--quiet-id must print only the bare id, got %q", out)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), id)
	if err != nil {
		t.Fatalf("printed id %q does not resolve: %v", id, err)
	}
	if got.ID != id {
		t.Errorf("resolved id = %q, want printed id %q", got.ID, id)
	}
}

// Closing a task with `done -m` must attach the comment, and a filtered list
// whose matches are all done must say so instead of a bare "(no tasks)" —
// otherwise a finished project is indistinguishable from a typo'd filter.
func TestCliDoneShortCommentAndListHintsHiddenDone(t *testing.T) {
	setTestHome(t, t.TempDir())
	const project = "done-hint-check"
	var id string
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"finish me", "--project", project, "--quiet-id"}); code != 0 {
			t.Fatalf("add: exit %d", code)
		}
	})
	id = strings.TrimSpace(out)

	captureStdout(t, func() {
		if code := cliDone([]string{id, "-m", "closed via short flag"}); code != 0 {
			t.Fatalf("done -m: exit %d", code)
		}
	})
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), id)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Status != todo.Done {
		t.Fatalf("status = %v, want Done", got.Status)
	}
	if len(got.Comments) != 1 || got.Comments[0].Text != "closed via short flag" {
		t.Errorf("comments = %+v, want the -m comment attached", got.Comments)
	}

	out = captureStdout(t, func() {
		if code := cliList([]string{"--project", project}); code != 0 {
			t.Fatalf("list: exit %d", code)
		}
	})
	if !strings.Contains(out, "1 done match") || !strings.Contains(out, "--all") {
		t.Errorf("list output = %q, want a hint that 1 done task is hidden behind --all", out)
	}
	out = captureStdout(t, func() {
		if code := cliList([]string{"--project", project, "--all"}); code != 0 {
			t.Fatalf("list --all: exit %d", code)
		}
	})
	if !strings.Contains(out, "Finish me") { // add title-cases the first letter
		t.Errorf("list --all output = %q, want the done task shown", out)
	}
	// A filter matching nothing at all keeps the plain marker (no false hint).
	out = captureStdout(t, func() {
		if code := cliList([]string{"--project", "no-such-project-zzz"}); code != 0 {
			t.Fatalf("list: exit %d", code)
		}
	})
	if !strings.Contains(out, "(no tasks)") {
		t.Errorf("list output = %q, want plain '(no tasks)' when nothing matches at all", out)
	}
}

// A hub host has server_listen/server_token in sync.json but no client URL;
// sync --status must report the server role instead of looking unconfigured.
func TestPrintSyncStatusReportsServerRole(t *testing.T) {
	out := captureStdout(t, func() {
		printSyncStatus(syncConfig{ServerListen: "100.64.0.1:8765", ServerToken: "tok"})
	})
	if !strings.Contains(out, "sync server (100.64.0.1:8765)") {
		t.Errorf("status = %q, want the serving role with its listen address", out)
	}
	out = captureStdout(t, func() {
		printSyncStatus(syncConfig{URL: "http://hub:8765"})
	})
	if strings.Contains(out, "sync server") {
		t.Errorf("status = %q, must not claim server role on a plain client", out)
	}
}

func TestCliAddJSONEmitsCreatedTask(t *testing.T) {
	setTestHome(t, t.TempDir())
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"json-add-check", "--json"}); code != 0 {
			t.Fatalf("add --json: exit %d", code)
		}
	})
	var got todo.Todo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.ID == "" {
		t.Error("json output missing id")
	}
	if got.Title != "Json-add-check" {
		t.Errorf("json title = %q, want %q", got.Title, "Json-add-check")
	}
}

func TestReadTitlesFromStdin(t *testing.T) {
	in := "  first task \n\n second task\n\t\nthird\n"
	got, err := readTitlesFromStdin(strings.NewReader(in))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []string{"first task", "second task", "third"}
	if len(got) != len(want) {
		t.Fatalf("got %d titles %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("title[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCliAddBatchFromStdinSharesFlags(t *testing.T) {
	setTestHome(t, t.TempDir())
	// Redirect stdin so `add -` reads our lines; shared flags must land on every
	// created task, and all should be written (one save).
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		if _, err := io.WriteString(w, "batch-alpha\nbatch-beta\nbatch-gamma\n"); err != nil {
			t.Errorf("write stdin: %v", err)
		}
		w.Close()
	}()
	code := cliAdd([]string{"-", "--project", "batchproj", "--p", "h"})
	os.Stdin = orig
	if code != 0 {
		t.Fatalf("batch add: exit %d", code)
	}

	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	seen := map[string]*todo.Todo{}
	for i := range todos {
		seen[todos[i].Title] = &todos[i]
	}
	for _, want := range []string{"Batch-alpha", "Batch-beta", "Batch-gamma"} {
		got, ok := seen[want]
		if !ok {
			t.Fatalf("missing batch task %q", want)
		}
		if got.Project != "batchproj" {
			t.Errorf("%q project = %q, want batchproj", want, got.Project)
		}
		if got.Priority != todo.PriorityHigh {
			t.Errorf("%q priority = %v, want high", want, got.Priority)
		}
	}
}

func TestCliAddBatchRejectsStart(t *testing.T) {
	setTestHome(t, t.TempDir())
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		if _, err := io.WriteString(w, "x\n"); err != nil {
			t.Errorf("write stdin: %v", err)
		}
		w.Close()
	}()
	code := cliAdd([]string{"-", "--start"})
	os.Stdin = orig
	if code != 2 {
		t.Errorf("batch add --start: want exit 2, got %d", code)
	}
}

func TestCliAddDependsUnknownRefFails(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"orphan", "--depends", "no-such-task-zzz"}); code != 2 {
		t.Errorf("want exit 2 for unknown --depends ref, got %d", code)
	}
}

// findByTitle is a small lookup for the chain/caret tests; titles are stored
// capitalized by todo.New.
func findByTitle(t *testing.T, todos []todo.Todo, title string) *todo.Todo {
	t.Helper()
	for i := range todos {
		if todos[i].Title == title {
			return &todos[i]
		}
	}
	t.Fatalf("task %q not found", title)
	return nil
}

func TestCliAddDependsCaretChainsOnLastAdded(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"caret-root"}); code != 0 {
		t.Fatalf("add root: exit %d", code)
	}
	if code := cliAdd([]string{"caret-next", "--depends", "^"}); code != 0 {
		t.Fatalf("add next --depends ^: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	root := findByTitle(t, todos, "Caret-root")
	next := findByTitle(t, todos, "Caret-next")
	if len(next.Dependencies) != 1 || next.Dependencies[0] != root.ID {
		t.Errorf("next.Dependencies = %v, want [%s]", next.Dependencies, root.ID)
	}
}

func TestCliAddDependsCaretWithoutHistoryFails(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := os.Remove(lastAddedPath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear last-added: %v", err)
	}
	if code := cliAdd([]string{"caret-orphan", "--depends", "^"}); code != 2 {
		t.Errorf("want exit 2 for ^ with no last-added recorded, got %d", code)
	}
}

func TestCliAddBatchChain(t *testing.T) {
	setTestHome(t, t.TempDir())
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		if _, err := io.WriteString(w, "chain-one\nchain-two\nchain-three\n"); err != nil {
			t.Errorf("write stdin: %v", err)
		}
		w.Close()
	}()
	code := cliAdd([]string{"-", "--chain"})
	os.Stdin = orig
	if code != 0 {
		t.Fatalf("batch chain add: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	one := findByTitle(t, todos, "Chain-one")
	two := findByTitle(t, todos, "Chain-two")
	three := findByTitle(t, todos, "Chain-three")
	if len(one.Dependencies) != 0 {
		t.Errorf("first line got dependencies %v, want none", one.Dependencies)
	}
	if len(two.Dependencies) != 1 || two.Dependencies[0] != one.ID {
		t.Errorf("two.Dependencies = %v, want [%s]", two.Dependencies, one.ID)
	}
	if len(three.Dependencies) != 1 || three.Dependencies[0] != two.ID {
		t.Errorf("three.Dependencies = %v, want [%s]", three.Dependencies, two.ID)
	}
	// The sidecar now points at the last line, ready for a follow-up ^.
	if got := loadLastAddedID(); got != three.ID {
		t.Errorf("last-added = %q, want %s", got, three.ID)
	}
}

func TestCliAddChainRequiresBatch(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"solo", "--chain"}); code != 2 {
		t.Errorf("want exit 2 for --chain without batch stdin, got %d", code)
	}
}

func TestCliDoneStampsSeqRank(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"seq-rank-probe", "--p", "h", "--due", "today"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	if code := cliDone([]string{"seq-rank-probe"}); code != 0 {
		t.Fatalf("done: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	probe := findByTitle(t, todos, "Seq-rank-probe")
	if probe.SeqRankAtDone < 1 {
		t.Errorf("SeqRankAtDone = %d, want >= 1 (stamped at completion)", probe.SeqRankAtDone)
	}
	// And the stat pipeline sees it.
	if hits, rated := rank.HitStats(todoPtrs(todos), rank.HitWindow); rated < 1 || hits < 1 {
		t.Errorf("hit stats = %d/%d, want at least 1/1 (high-pri due-today closes as a top-5 hit)", hits, rated)
	}
}

func TestCliEditAddDepLinksAndRefusesLoop(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"edit-dep-base"}); code != 0 {
		t.Fatalf("add base: exit %d", code)
	}
	if code := cliAdd([]string{"edit-dep-leaf"}); code != 0 {
		t.Fatalf("add leaf: exit %d", code)
	}
	// leaf depends on base — fine.
	if code := cliEdit([]string{"edit-dep-leaf", "--add-dep", "edit-dep-base"}); code != 0 {
		t.Fatalf("add-dep leaf->base: exit %d", code)
	}
	// base depending on leaf would close a loop — must be refused (exit 2).
	if code := cliEdit([]string{"edit-dep-base", "--add-dep", "edit-dep-leaf"}); code != 2 {
		t.Errorf("loop-forming add-dep: want exit 2, got %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	leaf, err := findTaskByRef(todoPtrs(todos), "edit-dep-leaf")
	if err != nil {
		t.Fatalf("find leaf: %v", err)
	}
	base, err := findTaskByRef(todoPtrs(todos), "edit-dep-base")
	if err != nil {
		t.Fatalf("find base: %v", err)
	}
	if len(leaf.Dependencies) != 1 || leaf.Dependencies[0] != base.ID {
		t.Errorf("leaf.Dependencies = %v, want [%s]", leaf.Dependencies, base.ID)
	}
	if len(base.Dependencies) != 0 {
		t.Errorf("base.Dependencies = %v, want none (loop add refused)", base.Dependencies)
	}
}

func TestCliEditRemoveDep(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"rmdep-base"}); code != 0 {
		t.Fatalf("add base: exit %d", code)
	}
	if code := cliAdd([]string{"rmdep-leaf"}); code != 0 {
		t.Fatalf("add leaf: exit %d", code)
	}
	if code := cliEdit([]string{"rmdep-leaf", "--add-dep", "rmdep-base"}); code != 0 {
		t.Fatalf("add-dep: exit %d", code)
	}
	if code := cliEdit([]string{"rmdep-leaf", "--remove-dep", "rmdep-base"}); code != 0 {
		t.Fatalf("remove-dep: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	leaf, err := findTaskByRef(todoPtrs(todos), "rmdep-leaf")
	if err != nil {
		t.Fatalf("find leaf: %v", err)
	}
	if len(leaf.Dependencies) != 0 {
		t.Errorf("leaf.Dependencies = %v, want none after remove", leaf.Dependencies)
	}
}

func TestIsCLICommand(t *testing.T) {
	cases := map[string]bool{
		"add": true, "list": true, "ls": true, "done": true, "top": true,
		"show": true, "edit": true, "delete": true, "rm": true, "comment": true,
		"search": true, "tags": true, "projects": true,
		"help": true, "-h": true, "--help": true, "--version": true,
		"foo": false, "": false, "Add": false,
	}
	for in, want := range cases {
		if got := isCLICommand(in); got != want {
			t.Errorf("isCLICommand(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestRankTopBySequenceLiftsBlockerAboveDependent(t *testing.T) {
	// A low-priority, no-due-date blocker has a low base score; the urgent task
	// that depends on it has a high base score. The critical-path rollup must
	// lift the blocker above the work it holds up, so `tjek top` matches the
	// TUI's Sequence ordering rather than the plain base-score sort.
	blocker := todo.New("blocker")
	blocker.Priority = todo.PriorityLow

	dependent := todo.New("urgent dependent")
	dependent.Priority = todo.PriorityHigh
	dependent.DueDate = time.Now()
	dependent.AddDependency(blocker.ID)

	ranked := rank.Default().Top(todoPtrs([]todo.Todo{dependent, blocker}))

	pos := make(map[string]int, len(ranked))
	for i := range ranked {
		pos[ranked[i].ID] = i
	}
	if pos[blocker.ID] >= pos[dependent.ID] {
		t.Fatalf("blocker should rank above the urgent task it blocks; got blocker=%d dependent=%d",
			pos[blocker.ID], pos[dependent.ID])
	}
}

// TestFilterTopLevel exercises every combination of filter the list / search
// verbs expose, so a future refactor of filterTopLevel can't silently change
// inclusion semantics.
func TestFilterTopLevel(t *testing.T) {
	mk := func(title, project string, tags []string, status todo.Status, due time.Time) todo.Todo {
		x := todo.New(title)
		x.Project = project
		for _, tag := range tags {
			x.AddTag(tag)
		}
		x.Status = status
		x.DueDate = due
		if status == todo.Done {
			x.CompletedAt = time.Now()
		}
		return x
	}
	yesterday := startOfDay(time.Now()).AddDate(0, 0, -1)
	today := startOfDay(time.Now())
	nextWeek := startOfDay(time.Now()).AddDate(0, 0, 7)

	a := mk("Buy milk", "groceries", []string{"shop", "urgent"}, todo.Pending, time.Time{})
	b := mk("Read RFC", "work", []string{"reading"}, todo.Pending, nextWeek)
	c := mk("Call landlord", "", []string{"urgent"}, todo.Pending, today)
	d := mk("Pay rent", "", []string{"urgent"}, todo.Pending, yesterday) // overdue
	e := mk("Old shopping", "groceries", []string{"shop"}, todo.Done, time.Time{})
	sub := mk("subtask", "", nil, todo.Pending, time.Time{})
	sub.ParentID = a.ID
	todos := []todo.Todo{a, b, c, d, e, sub}

	titlesOf := func(rows []todo.Todo) []string {
		out := make([]string, len(rows))
		for i := range rows {
			out[i] = rows[i].Title
		}
		return out
	}

	t.Run("default excludes done + subtasks", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{})
		if len(got) != 4 {
			t.Fatalf("want 4 rows (a,b,c,d), got %d: %v", len(got), titlesOf(got))
		}
	})
	t.Run("includeDone surfaces completed", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{includeDone: true})
		if len(got) != 5 {
			t.Errorf("want 5 with done included, got %d", len(got))
		}
	})
	t.Run("focus = overdue + today only", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{focus: true})
		names := titlesOf(got)
		want := map[string]bool{"Call landlord": true, "Pay rent": true}
		if len(names) != len(want) {
			t.Fatalf("want 2 focus rows, got %d: %v", len(names), names)
		}
		for _, n := range names {
			if !want[n] {
				t.Errorf("unexpected focus row: %s", n)
			}
		}
	})
	t.Run("tag filter case-insensitive", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{tag: "URGENT"})
		if len(got) != 3 {
			t.Errorf("want 3 urgent rows, got %d: %v", len(got), titlesOf(got))
		}
	})
	t.Run("project filter case-insensitive equality", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{project: "Groceries"})
		if len(got) != 1 || got[0].Title != "Buy milk" {
			t.Errorf("want only 'Buy milk', got %v", titlesOf(got))
		}
	})
	t.Run("search filter case-insensitive substring", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{search: "MILK"})
		if len(got) != 1 || got[0].Title != "Buy milk" {
			t.Errorf("want only 'Buy milk', got %v", titlesOf(got))
		}
	})
	t.Run("search also matches the notes", func(t *testing.T) {
		// Learnings were folded into notes by migration 011, so the CLI has to
		// reach into notes or that text became unfindable from the shell.
		withNote := todo.New("Fix the boiler")
		withNote.Notes = "## Learnings\n- bleed the radiators first"
		got := filterTopLevel(append(todos, withNote), listFilterOpts{search: "RADIATORS"})
		if len(got) != 1 || got[0].Title != "Fix the boiler" {
			t.Errorf("want the task carrying the note, got %v", titlesOf(got))
		}
	})
	t.Run("combined filters are AND", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{tag: "urgent", project: "groceries"})
		if len(got) != 1 || got[0].Title != "Buy milk" {
			t.Errorf("want only 'Buy milk' (urgent ∩ groceries), got %v", titlesOf(got))
		}
	})
}

// TestBuildBlockedSet verifies the pure blocked-set helper matches the same
// semantics as model.rebuildDependencySets: a task is blocked iff at least one
// of its dependencies is still pending; a dependency that is done does not block.
func TestBuildBlockedSet(t *testing.T) {
	blocker := todo.New("Prerequisite")
	blocked := todo.New("Depends on prerequisite")
	blocked.AddDependency(blocker.ID)
	donePrereq := todo.New("Already done prereq")
	donePrereq.Status = todo.Done
	notBlocked := todo.New("Depends only on done task")
	notBlocked.AddDependency(donePrereq.ID)
	independent := todo.New("No dependencies")

	todos := []todo.Todo{blocker, blocked, donePrereq, notBlocked, independent}
	bs := buildBlockedSet(todos)

	if !bs[blocked.ID] {
		t.Errorf("task with pending dep should be blocked")
	}
	if bs[blocker.ID] {
		t.Errorf("task with no deps should not be in blocked set")
	}
	if bs[notBlocked.ID] {
		t.Errorf("task whose dep is done should not be blocked")
	}
	if bs[independent.ID] {
		t.Errorf("independent task should not be blocked")
	}
}

// TestFilterTopLevelReadyBlocked exercises the --ready and --blocked filters.
// They are the only way to select on blockedness now that the ST column has no
// glyph for it, so a refactor must not silently break them.
func TestFilterTopLevelReadyBlocked(t *testing.T) {
	prereq := todo.New("Prerequisite step")
	dependent := todo.New("Depends on step")
	dependent.AddDependency(prereq.ID)
	free := todo.New("No dependencies")
	todos := []todo.Todo{prereq, dependent, free}

	titlesOf := func(rows []todo.Todo) []string {
		out := make([]string, len(rows))
		for i := range rows {
			out[i] = rows[i].Title
		}
		return out
	}

	t.Run("--ready excludes blocked", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{onlyReady: true})
		for _, r := range got {
			if r.ID == dependent.ID {
				t.Errorf("--ready should exclude blocked task, got %v", titlesOf(got))
			}
		}
		if len(got) != 2 {
			t.Errorf("want 2 ready tasks (prereq + free), got %d: %v", len(got), titlesOf(got))
		}
	})

	t.Run("--blocked returns only blocked", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{onlyBlocked: true})
		if len(got) != 1 || got[0].ID != dependent.ID {
			t.Errorf("want only the blocked task, got %v", titlesOf(got))
		}
	})

	t.Run("default includes all pending (blocked + unblocked)", func(t *testing.T) {
		got := filterTopLevel(todos, listFilterOpts{})
		if len(got) != 3 {
			t.Errorf("want 3 rows (all pending), got %d: %v", len(got), titlesOf(got))
		}
	})
}

// The ST column carries one fact — where the task stands — in the same four
// states the TUI uses, with overdue ahead of started. Blockedness is not among
// them: it is expressed by sort position, so a blocked task that is otherwise
// untouched reads [ ] like any other task you have not started.
func TestPrintTaskTableStatusGlyphs(t *testing.T) {
	plain := todo.New("Plain task")
	started := todo.New("Started task")
	started.TimeEntries = []todo.TimeEntry{{StartedAt: time.Now().Add(-time.Hour), StoppedAt: time.Now()}}
	late := todo.New("Late task")
	late.DueDate = time.Now().Add(-48 * time.Hour)
	lateAndStarted := todo.New("Late started task")
	lateAndStarted.DueDate = late.DueDate
	lateAndStarted.TimeEntries = started.TimeEntries
	finished := todo.New("Finished task")
	finished.Status = todo.Done
	blocked := todo.New("Blocked task")
	blocked.AddDependency(plain.ID)

	out := captureStdout(t, func() {
		printTaskTable([]todo.Todo{plain, started, late, lateAndStarted, finished, blocked},
			map[string]bool{blocked.ID: true})
	})

	want := map[string]string{
		"Plain task":        "[ ]",
		"Started task":      "[>]",
		"Late task":         "[!]",
		"Late started task": "[!]", // overdue outranks started
		"Finished task":     "[✓]",
		"Blocked task":      "[ ]", // position says it, not a glyph
	}
	for _, line := range strings.Split(out, "\n") {
		for title, glyph := range want {
			if !strings.Contains(line, title) {
				continue
			}
			if !strings.Contains(line, glyph) {
				t.Errorf("%q should show %s, got: %s", title, glyph, line)
			}
		}
	}
	if strings.Contains(out, "[~]") {
		t.Errorf("the blocked glyph is gone from the status column; output still has one:\n%s", out)
	}
	// It moved in front of the title, where it explains the sort position.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Blocked task") && !strings.Contains(line, "↧ Blocked task") {
			t.Errorf("blocked row should carry ↧ before the title: %s", line)
		}
	}
}

// With column icons, tjek list's ST column is the TUI's box: the task's
// column mark, overdue or not.
func TestListStatusColumnShowsColumnIcons(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := saveSettings(appSettings{Stages: []string{"Todo", "Review", "Done"}, StageIcons: map[string]string{"review": "R"}}); err != nil {
		t.Fatal(err)
	}
	late := todo.New("Late in review")
	late.DueDate = time.Now().Add(-48 * time.Hour)
	late.SetStage("Review")
	out := captureStdout(t, func() { printTaskTable([]todo.Todo{late}, nil) })
	if !strings.Contains(out, "[R]") || strings.Contains(out, "[!]") {
		t.Errorf("ST should show the column icon:\n%s", out)
	}
}

// TestResolveRefs covers the batch verb's ref-resolution contract: succeed on
// all refs or fail before any mutation; collapse duplicates silently so
// `done abc abc` is one done, not an error.
func TestResolveRefs(t *testing.T) {
	a := todo.New("Buy milk")
	a.ID = "aaaa1111-aaaa"
	b := todo.New("Call landlord")
	b.ID = "bbbb2222-bbbb"
	todos := []todo.Todo{a, b}

	t.Run("two refs", func(t *testing.T) {
		got, err := resolveRefs(todoPtrs(todos), []string{"aaaa", "bbbb"})
		if err != nil || len(got) != 2 {
			t.Fatalf("want 2 targets, got %d err=%v", len(got), err)
		}
	})
	t.Run("duplicate refs collapse", func(t *testing.T) {
		got, err := resolveRefs(todoPtrs(todos), []string{"aaaa", "aaaa", "milk"})
		if err != nil || len(got) != 1 {
			t.Fatalf("want 1 target after dedup, got %d err=%v", len(got), err)
		}
	})
	t.Run("first failure aborts the batch", func(t *testing.T) {
		if _, err := resolveRefs(todoPtrs(todos), []string{"aaaa", "nope"}); err == nil {
			t.Error("want error from missing ref")
		}
	})
}

// TestTrackedTodayDuration covers the four overlap cases the stats one-liner
// has to get right: fully today, ends today (started yesterday), starts today
// (still running), and entirely outside today.
func TestTrackedTodayDuration(t *testing.T) {
	now := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)
	today := startOfDay(now)
	yesterday := today.AddDate(0, 0, -1)

	make := func(start, stop time.Time) todo.Todo {
		x := todo.New("t")
		x.TimeEntries = []todo.TimeEntry{{ID: "e", StartedAt: start, StoppedAt: stop}}
		return x
	}

	cases := []struct {
		name string
		t    todo.Todo
		want time.Duration
	}{
		{"fully today", make(today.Add(9*time.Hour), today.Add(11*time.Hour)), 2 * time.Hour},
		{"crosses midnight into today", make(yesterday.Add(23*time.Hour), today.Add(1*time.Hour)), 1 * time.Hour},
		{"running entry counts up to now", make(today.Add(12*time.Hour), time.Time{}), 2 * time.Hour},
		{"entirely yesterday", make(yesterday.Add(8*time.Hour), yesterday.Add(9*time.Hour)), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := trackedTodayDuration([]todo.Todo{c.t}, now)
			if got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}

	t.Run("sums across multiple todos and entries", func(t *testing.T) {
		x := todo.New("x")
		x.TimeEntries = []todo.TimeEntry{
			{StartedAt: today.Add(9 * time.Hour), StoppedAt: today.Add(10 * time.Hour)},
			{StartedAt: today.Add(13 * time.Hour), StoppedAt: time.Time{}}, // running, 1h so far
		}
		y := todo.New("y")
		y.TimeEntries = []todo.TimeEntry{
			{StartedAt: today.Add(11 * time.Hour), StoppedAt: today.Add(11*time.Hour + 30*time.Minute)},
		}
		got := trackedTodayDuration([]todo.Todo{x, y}, now)
		want := 1*time.Hour + 1*time.Hour + 30*time.Minute
		if got != want {
			t.Errorf("got %v want %v", got, want)
		}
	})
}

// TestDoneCommentSplitter verifies that `done <ref>... --comment="why"` keeps
// the refs as positionals and the comment as a flag value, with the comment
// surviving spaces. Guards the splitFlagsAndPositionals wiring on cliDone.
func TestDoneCommentSplitter(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("done", flag.ContinueOnError)
		fs.String("comment", "", "")
		return fs
	}
	flags, positionals := splitFlagsAndPositionals(
		newFS(), []string{"abc", "def", "--comment=finished sprint"},
	)
	if !sliceEq(flags, []string{"--comment=finished sprint"}) {
		t.Errorf("flags = %v, want [--comment=finished sprint]", flags)
	}
	if !sliceEq(positionals, []string{"abc", "def"}) {
		t.Errorf("positionals = %v, want [abc def]", positionals)
	}
	// Spaced form: --comment "why" — value consumes next arg.
	flags, positionals = splitFlagsAndPositionals(
		newFS(), []string{"abc", "--comment", "finished sprint", "def"},
	)
	if !sliceEq(flags, []string{"--comment", "finished sprint"}) {
		t.Errorf("spaced form flags = %v", flags)
	}
	if !sliceEq(positionals, []string{"abc", "def"}) {
		t.Errorf("spaced form positionals = %v", positionals)
	}
}

// The slice-backed descendant walk must return rootID and every transitive
// subtask, so cliDelete can cascade the tombstone set and not strand subtasks
// with a parent_id pointing at a deleted parent (the DOGFOOD-child orphan bug).
// Now exercises the shared descendantIDsFrom over sliceTaskLookups.
func TestDescendantIDsInSliceCascadesSubtree(t *testing.T) {
	root := todo.New("root")
	root.ID = "root"
	child := todo.NewSubtask("child", "root")
	child.ID = "child"
	grand := todo.NewSubtask("grand", "child")
	grand.ID = "grand"
	sibling := todo.New("sibling") // unrelated, must not appear
	sibling.ID = "sibling"

	children, _ := sliceTaskLookups([]todo.Todo{root, child, grand, sibling})
	got := descendantIDsFrom(children, "root")
	want := map[string]bool{"root": true, "child": true, "grand": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected id %q in descendants", id)
		}
	}
}

// the "-" sentinel reads stdin and trims one trailing newline (heredocs
// typically include one).
func TestCommentTextFromPositionals(t *testing.T) {
	got, err := commentTextFromPositionals([]string{"hello", "world"}, strings.NewReader(""))
	if err != nil || got != "hello world" {
		t.Errorf("literal path: got=%q err=%v", got, err)
	}
	got, err = commentTextFromPositionals([]string{"-"}, strings.NewReader("piped text\n"))
	if err != nil || got != "piped text" {
		t.Errorf("stdin path: got=%q err=%v", got, err)
	}
	got, err = commentTextFromPositionals([]string{"-"}, strings.NewReader("line one\nline two\n"))
	if err != nil || got != "line one\nline two" {
		t.Errorf("multiline stdin: got=%q err=%v", got, err)
	}
}

// findTaskByRefKind must report WHICH pass matched: cliDelete only prompts
// for confirmation on the fuzzy title path, so a kind mix-up either nags on
// exact ids or (worse) deletes a fuzzy match silently.
func TestFindTaskByRefKindReportsMatchPath(t *testing.T) {
	a := todo.New("Buy milk")
	a.ID = "1aaaaa00-aaaa"
	b := todo.New("Call landlord")
	b.ID = "2bbbbb00-bbbb"
	todos := []todo.Todo{a, b}

	if _, kind, err := findTaskByRefKind(todoPtrs(todos), "1aaa"); err != nil || kind != refMatchID {
		t.Errorf("id prefix: kind=%v err=%v, want refMatchID", kind, err)
	}
	if _, kind, err := findTaskByRefKind(todoPtrs(todos), "landlord"); err != nil || kind != refMatchTitle {
		t.Errorf("title substring: kind=%v err=%v, want refMatchTitle", kind, err)
	}
}

// `tjek add` parses the same quick-add tokens as the TUI, so a line is
// copy-pasteable between them. Backlog item 52089090.
func TestCliAddParsesQuickAddTokens(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Buy milk #shopping #errand due:friday p:high s:l @home"}); code != 0 {
		t.Fatalf("add with tokens: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Buy milk")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Title != "Buy milk" {
		t.Errorf("Title = %q, want %q (tokens stripped)", got.Title, "Buy milk")
	}
	if got.Priority != todo.PriorityHigh {
		t.Errorf("Priority = %v, want High (p:high)", got.Priority)
	}
	if got.Size != todo.SizeLarge {
		t.Errorf("Size = %v, want Large (s:l)", got.Size)
	}
	if got.Project != "home" {
		t.Errorf("Project = %q, want home (@home)", got.Project)
	}
	if got.DueDate.IsZero() {
		t.Error("DueDate should be set from due:friday")
	}
	wantTags := map[string]bool{"shopping": true, "errand": true}
	for _, tg := range got.Tags {
		delete(wantTags, tg)
	}
	if len(wantTags) != 0 {
		t.Errorf("missing tags %v; got %v", wantTags, got.Tags)
	}
}

func TestCliEditParentDuePropagatesAndClearsSubtree(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"CLI due propagation parent"}); code != 0 {
		t.Fatalf("add parent: exit %d", code)
	}
	_, tasks, err := loadForCLI()
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	parent, err := findTaskByRef(todoPtrs(tasks), "CLI due propagation parent")
	if err != nil {
		t.Fatalf("find parent: %v", err)
	}
	if code := cliSubtask([]string{parent.ID[:8], "CLI due propagation child"}); code != 0 {
		t.Fatalf("add child: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	child, err := findTaskByRef(todoPtrs(tasks), "CLI due propagation child")
	if err != nil {
		t.Fatalf("find child: %v", err)
	}
	if code := cliSubtask([]string{child.ID[:8], "CLI due propagation grandchild"}); code != 0 {
		t.Fatalf("add grandchild: exit %d", code)
	}

	if code := cliEdit([]string{parent.ID[:8], "--due", "+10d"}); code != 0 {
		t.Fatalf("edit parent due: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	parent, _ = findTaskByRef(todoPtrs(tasks), "CLI due propagation parent")
	child, _ = findTaskByRef(todoPtrs(tasks), "CLI due propagation child")
	grandchild, _ := findTaskByRef(todoPtrs(tasks), "CLI due propagation grandchild")
	for _, task := range []*todo.Todo{child, grandchild} {
		if !task.DueDate.Equal(parent.DueDate) {
			t.Errorf("%s due = %v, want parent due %v", task.Title, task.DueDate, parent.DueDate)
		}
	}

	if code := cliEdit([]string{parent.ID[:8], "--clear-due"}); code != 0 {
		t.Fatalf("clear parent due: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	for _, title := range []string{"CLI due propagation parent", "CLI due propagation child", "CLI due propagation grandchild"} {
		task, findErr := findTaskByRef(todoPtrs(tasks), title)
		if findErr != nil {
			t.Fatalf("find %q after clear: %v", title, findErr)
		}
		if !task.DueDate.IsZero() {
			t.Errorf("%s due = %v after parent clear, want zero", task.Title, task.DueDate)
		}
	}
}

func TestCliEditParentPriorityClampsTheSubtree(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"CLI priority clamp parent", "--p", "h"}); code != 0 {
		t.Fatalf("add parent: exit %d", code)
	}
	_, tasks, err := loadForCLI()
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	parent, err := findTaskByRef(todoPtrs(tasks), "CLI priority clamp parent")
	if err != nil {
		t.Fatalf("find parent: %v", err)
	}
	if code := cliSubtask([]string{parent.ID[:8], "CLI priority clamp child"}); code != 0 {
		t.Fatalf("add child: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	child, err := findTaskByRef(todoPtrs(tasks), "CLI priority clamp child")
	if err != nil {
		t.Fatalf("find child: %v", err)
	}
	if code := cliSubtask([]string{child.ID[:8], "CLI priority clamp grandchild"}); code != 0 {
		t.Fatalf("add grandchild: exit %d", code)
	}
	// The whole subtree is high, then the parent is parked at low.
	for _, ref := range []string{child.ID[:8], "CLI priority clamp grandchild"} {
		if code := cliEdit([]string{ref, "--p", "h"}); code != 0 {
			t.Fatalf("raise %s: exit %d", ref, code)
		}
	}
	if code := cliEdit([]string{parent.ID[:8], "--p", "l"}); code != 0 {
		t.Fatalf("lower parent: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	for _, title := range []string{"CLI priority clamp parent", "CLI priority clamp child", "CLI priority clamp grandchild"} {
		task, findErr := findTaskByRef(todoPtrs(tasks), title)
		if findErr != nil {
			t.Fatalf("find %q: %v", title, findErr)
		}
		if task.Priority != todo.PriorityLow {
			t.Errorf("%s priority = %v after the parent went low, want low", task.Title, task.Priority)
		}
	}

	// And a subtask raised above the parent is capped, not granted.
	if code := cliEdit([]string{child.ID[:8], "--p", "h"}); code != 0 {
		t.Fatalf("raise child: exit %d", code)
	}
	_, tasks, _ = loadForCLI()
	child, _ = findTaskByRef(todoPtrs(tasks), "CLI priority clamp child")
	parent, _ = findTaskByRef(todoPtrs(tasks), "CLI priority clamp parent")
	if child.Priority != todo.PriorityLow {
		t.Errorf("child priority = %v, want low (capped at the parent)", child.Priority)
	}
	if parent.Priority != todo.PriorityLow {
		t.Errorf("parent priority = %v, want low — a subtask never lifts its parent", parent.Priority)
	}
}

// An explicit flag overrides the matching token in the title.
func TestCliAddFlagOverridesToken(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Flag wins p:low", "-p", "high"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Flag wins")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Priority != todo.PriorityHigh {
		t.Errorf("Priority = %v, want High (-p high overrides p:low token)", got.Priority)
	}
}

// A token-free title must not clobber a --like clone: the Medium defaults from
// parseQuickAdd only apply when a p:/s: token was actually present.
func TestCliAddLikeNotClobberedByTokenlessTitle(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Source task p:high s:l"}); code != 0 {
		t.Fatalf("add source: exit %d", code)
	}
	if code := cliAdd([]string{"Cloned task", "--like", "Source task"}); code != 0 {
		t.Fatalf("add clone: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Cloned task")
	if err != nil {
		t.Fatalf("find clone: %v", err)
	}
	if got.Priority != todo.PriorityHigh || got.Size != todo.SizeLarge {
		t.Errorf("clone = (%v, %v), want (High, Large) from --like", got.Priority, got.Size)
	}
}

// `tjek undelete <ref>` restores a soft-deleted task from the tombstones.
// Backlog item e80b30ed (pivoted from a redo stack to undelete-by-ref).
func TestCliUndeleteRestoresTask(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Undelete restore target"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Undelete restore target")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	id := got.ID

	if code := cliDelete([]string{id[:8]}); code != 0 { // id ref → no confirm
		t.Fatalf("delete: exit %d", code)
	}
	_, live, _ := loadForCLI()
	if _, err := findTaskByRef(todoPtrs(live), "Undelete restore target"); err == nil {
		t.Fatal("task should be gone from the live set after delete")
	}

	if code := cliUndelete([]string{id[:8]}); code != 0 {
		t.Fatalf("undelete: exit %d", code)
	}
	_, back, _ := loadForCLI()
	restored, err := findTaskByRef(todoPtrs(back), "Undelete restore target")
	if err != nil {
		t.Fatalf("task should be restored to the live set: %v", err)
	}
	if restored.Deleted {
		t.Error("restored task should not be marked Deleted")
	}
}

// A restore must be stamped strictly later than the tombstone it has to beat.
// After a slow-clock delete the tombstone's deleted_at is clamped to the row's
// modified_at+1ms (StampModified); if the restore only stamped
// StampModified(pre-delete ModifiedAt) it would land on exactly that same
// instant, and the merge breaks an event-time tie by content hash (laterWins)
// — a coin flip the restore can lose, silently re-deleting the task on the
// next sync. cliUndo therefore clamps against the live tombstone's deleted_at.
// The slow clock is simulated by pushing the task's ModifiedAt into the future
// before deleting, so the real clock is "behind" it for the whole test.
func TestCliUndoRestoreBeatsSlowClockTombstone(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Undo tie target"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Undo tie target")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	future := time.Now().Add(30 * time.Second)
	got.ModifiedAt = future
	if err := repo.Save([]*todo.Todo{got}, nil); err != nil {
		t.Fatalf("save future stamp: %v", err)
	}

	if code := cliDelete([]string{got.ID[:8]}); code != 0 { // id ref → no confirm
		t.Fatalf("delete: exit %d", code)
	}
	tombAt := tombstoneDeletedAt(testStore(t), got.ID)
	if !tombAt.After(future) {
		t.Fatalf("precondition: tombstone deleted_at = %v, want clamped past modified_at %v", tombAt, future)
	}

	if code := cliUndo(nil); code != 0 {
		t.Fatalf("undo: exit %d", code)
	}
	_, back, _ := loadForCLI()
	restored, err := findTaskByRef(todoPtrs(back), "Undo tie target")
	if err != nil {
		t.Fatalf("task should be restored: %v", err)
	}
	if !restored.ModifiedAt.After(tombAt) {
		t.Errorf("restore ModifiedAt = %v, want strictly after tombstone deleted_at %v — an exact tie is a hash coin flip in the merge",
			restored.ModifiedAt, tombAt)
	}
}

// `tjek undelete --list` browses the deleted tasks.
func TestCliUndeleteListShowsDeleted(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Undelete list target"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "Undelete list target")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if code := cliDelete([]string{got.ID[:8]}); code != 0 {
		t.Fatalf("delete: exit %d", code)
	}
	out := captureStdout(t, func() {
		if code := cliUndelete([]string{"--list"}); code != 0 {
			t.Fatalf("undelete --list: exit %d", code)
		}
	})
	if !strings.Contains(out, "Undelete list target") {
		t.Errorf("--list should show the deleted task; got %q", out)
	}
}

// Deleting a parent cascades to its subtasks; undelete brings the whole subtree
// back with the parent link intact.
func TestCliUndeleteRestoresSubtree(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"Undelete parent target"}); code != 0 {
		t.Fatalf("add parent: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	parent, err := findTaskByRef(todoPtrs(todos), "Undelete parent target")
	if err != nil {
		t.Fatalf("find parent: %v", err)
	}
	if code := cliSubtask([]string{parent.ID[:8], "child of undelete parent"}); code != 0 {
		t.Fatalf("subtask: exit %d", code)
	}
	if code := cliDelete([]string{parent.ID[:8]}); code != 0 {
		t.Fatalf("delete parent: exit %d", code)
	}
	_, live, _ := loadForCLI()
	if _, err := findTaskByRef(todoPtrs(live), "child of undelete parent"); err == nil {
		t.Fatal("child should be cascade-deleted with the parent")
	}

	if code := cliUndelete([]string{parent.ID[:8]}); code != 0 {
		t.Fatalf("undelete: exit %d", code)
	}
	_, back, _ := loadForCLI()
	p, err := findTaskByRef(todoPtrs(back), "Undelete parent target")
	if err != nil {
		t.Fatalf("parent should be restored: %v", err)
	}
	c, err := findTaskByRef(todoPtrs(back), "child of undelete parent")
	if err != nil {
		t.Fatalf("child should be restored with the subtree: %v", err)
	}
	if c.ParentID != p.ID {
		t.Errorf("restored child ParentID = %q, want parent %q", c.ParentID, p.ID)
	}
}

// ── Change 1: --depends confirmation + loud bad-ref failure ──────────────────

// TestCliAddDependsConfirmationOutput verifies that a successful add with
// --depends prints a "blocked on" line so the user can see the link took effect.
func TestCliAddDependsConfirmationOutput(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"dep-confirm-prereq"}); code != 0 {
		t.Fatalf("add prereq: exit %d", code)
	}
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"dep-confirm-task", "--depends", "dep-confirm-prereq"}); code != 0 {
			t.Fatalf("add with --depends: exit %d", code)
		}
	})
	if !strings.Contains(out, "added") {
		t.Errorf("output should contain 'added'; got %q", out)
	}
	if !strings.Contains(out, "blocked on") {
		t.Errorf("output should contain 'blocked on'; got %q", out)
	}
	if !strings.Contains(out, "Dep-confirm-prereq") {
		t.Errorf("'blocked on' line should show dep title; got %q", out)
	}
}

// TestCliAddDependsUnknownRefExitsNonZero verifies the command fails loudly
// (non-zero exit, no task created) when --depends names a ref that doesn't exist.
func TestCliAddDependsUnknownRefExitsNonZero(t *testing.T) {
	setTestHome(t, t.TempDir())
	countBefore := func() int {
		_, todos, _ := loadForCLI()
		return len(todos)
	}
	before := countBefore()
	code := cliAdd([]string{"should-not-be-created", "--depends", "totally-bogus-ref-zzz99"})
	if code == 0 {
		t.Fatalf("want non-zero exit for unknown --depends ref, got 0")
	}
	after := countBefore()
	if after != before {
		t.Errorf("no task should be created when --depends ref is bad; had %d, now %d", before, after)
	}
}

// TestCliAddDependsCaretConfirmationOutput checks that the ^ shorthand (depend
// on the last-added task) also prints the "blocked on" confirmation line.
func TestCliAddDependsCaretConfirmationOutput(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"caret-confirm-root"}); code != 0 {
		t.Fatalf("add root: exit %d", code)
	}
	out := captureStdout(t, func() {
		if code := cliAdd([]string{"caret-confirm-next", "--depends", "^"}); code != 0 {
			t.Fatalf("add next --depends ^: exit %d", code)
		}
	})
	if !strings.Contains(out, "blocked on") {
		t.Errorf("^ path: output should contain 'blocked on'; got %q", out)
	}
	if !strings.Contains(out, "Caret-confirm-root") {
		t.Errorf("^ path: 'blocked on' line should show dep title; got %q", out)
	}
}

// ── Change 2: --note - reads stdin ───────────────────────────────────────────

// TestCliAddNoteFromStdin verifies that --note=- reads the note body from stdin.
func TestCliAddNoteFromStdin(t *testing.T) {
	setTestHome(t, t.TempDir())
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		if _, err := io.WriteString(w, "line one\nline two\n"); err != nil {
			t.Errorf("write stdin: %v", err)
		}
		w.Close()
	}()
	code := cliAdd([]string{"note-stdin-task", "--note", "-"})
	os.Stdin = orig
	if code != 0 {
		t.Fatalf("add --note -: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "note-stdin-task")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	// noteFlagText trims trailing newlines (heredoc convention).
	if got.Notes != "line one\nline two" {
		t.Errorf("Notes = %q, want %q", got.Notes, "line one\nline two")
	}
}

// TestCliAddNoteStdinConflictWithBatchErrors verifies that combining `add -`
// (batch titles from stdin) and --note=- (note from stdin) is rejected clearly.
func TestCliAddNoteStdinConflictWithBatchErrors(t *testing.T) {
	setTestHome(t, t.TempDir())
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		if _, err := io.WriteString(w, "some title\n"); err != nil {
			t.Errorf("write stdin: %v", err)
		}
		w.Close()
	}()
	code := cliAdd([]string{"-", "--note", "-"})
	os.Stdin = orig
	if code != 2 {
		t.Errorf("add - --note -: want exit 2 (stdin conflict), got %d", code)
	}
}

// ── Change 3: --priority alias ───────────────────────────────────────────────

// TestCliAddPriorityAlias verifies --priority sets the same value as --p.
func TestCliAddPriorityAlias(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"priority-alias-task", "--priority", "h"}); code != 0 {
		t.Fatalf("add --priority h: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "priority-alias-task")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Priority != todo.PriorityHigh {
		t.Errorf("Priority = %v, want High (--priority h)", got.Priority)
	}
}

// TestCliAddPriorityShortFlagStillWorks verifies --p still works after the alias
// is added (backward compat).
func TestCliAddPriorityShortFlagStillWorks(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"priority-short-task", "--p", "l"}); code != 0 {
		t.Fatalf("add --p l: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "priority-short-task")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Priority != todo.PriorityLow {
		t.Errorf("Priority = %v, want Low (--p l)", got.Priority)
	}
}

// TestCliEditPriorityAlias verifies that `edit --priority` is also aliased on
// the edit verb (for parity with add).
func TestCliEditPriorityAlias(t *testing.T) {
	setTestHome(t, t.TempDir())
	if code := cliAdd([]string{"edit-priority-alias-task"}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	if code := cliEdit([]string{"edit-priority-alias-task", "--priority", "h"}); code != 0 {
		t.Fatalf("edit --priority h: exit %d", code)
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := findTaskByRef(todoPtrs(todos), "edit-priority-alias-task")
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if got.Priority != todo.PriorityHigh {
		t.Errorf("Priority = %v, want High (edit --priority h)", got.Priority)
	}
}
