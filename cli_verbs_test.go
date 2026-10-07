package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/todo"
)

// These cover the CLI verbs that change state but had next to no tests: the
// timer pair, comment, the interactive half of suggest, and the plain-text
// `show`. Each asserts what landed in the store, not only the exit code —
// the exit code is the part a regression is least likely to change.

// reloadCLI reads the store back the way the next CLI invocation would.
func reloadCLI(t *testing.T) []todo.Todo {
	t.Helper()
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return todos
}

func cliTaskByID(t *testing.T, id string) todo.Todo {
	t.Helper()
	for _, x := range reloadCLI(t) {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("task %s not in the store", id)
	return todo.Todo{}
}

func runningIDs(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, x := range reloadCLI(t) {
		if x.IsTimerRunning() {
			ids = append(ids, x.ID)
		}
	}
	return ids
}

// stdinWith swaps os.Stdin for a pipe carrying input, for the paths that read
// answers or text from it.
func stdinWith(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })
}

func TestCLIStartTracksOneTaskAtATime(t *testing.T) {
	setTestHome(t, t.TempDir())
	a := addQuietID(t, "Write report")
	b := addQuietID(t, "Review PR")

	if code := cliStart(nil); code != 2 {
		t.Errorf("start with no ref: want usage exit 2, got %d", code)
	}
	if code := cliStart([]string{"no-such-task-zzz"}); code != 2 {
		t.Errorf("start with an unknown ref: want 2, got %d", code)
	}

	out := captureStdout(t, func() {
		if code := cliStart([]string{a}); code != 0 {
			t.Fatalf("start a: exit %d", code)
		}
	})
	if !strings.HasPrefix(out, "started: "+a[:8]) {
		t.Errorf("stdout = %q, want the started line", out)
	}
	if got := runningIDs(t); len(got) != 1 || got[0] != a {
		t.Fatalf("running = %v, want only %s", got, a[:8])
	}

	// A repeat start is a no-op, not a second entry: splitting one session
	// into two zero-gap entries is the drift the guard in cliStart exists for.
	errOut := captureStderr(t, func() {
		if code := cliStart([]string{a}); code != 0 {
			t.Errorf("re-start a: exit %d", code)
		}
	})
	if !strings.Contains(errOut, "already tracking") {
		t.Errorf("stderr = %q, want 'already tracking'", errOut)
	}
	if n := len(cliTaskByID(t, a).TimeEntries); n != 1 {
		t.Errorf("a has %d time entries after a repeat start, want 1", n)
	}

	// Starting another task stops the first — one timer at a time, the
	// invariant the TUI keeps too — and says so on stderr, keeping stdout to
	// the one line a script parses.
	var stdout string
	errOut = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if code := cliStart([]string{b}); code != 0 {
				t.Errorf("start b: exit %d", code)
			}
		})
	})
	if !strings.Contains(errOut, "stopped: "+a[:8]) {
		t.Errorf("stderr = %q, want the stop notice for a", errOut)
	}
	if strings.Contains(stdout, "stopped") {
		t.Errorf("stdout = %q; the side-effect notice belongs on stderr", stdout)
	}
	if got := runningIDs(t); len(got) != 1 || got[0] != b {
		t.Fatalf("running = %v, want only %s", got, b[:8])
	}
	if e := cliTaskByID(t, a).TimeEntries[0]; e.StoppedAt.IsZero() {
		t.Error("a's entry should be closed once b started")
	}
}

func TestCLIStopEndsTheRunningTimer(t *testing.T) {
	setTestHome(t, t.TempDir())
	a := addQuietID(t, "Write report")
	b := addQuietID(t, "Review PR")

	// Nothing running: a no-op, not an error, with or without a ref.
	errOut := captureStderr(t, func() {
		if code := cliStop(nil); code != 0 {
			t.Errorf("stop with nothing running: want 0, got %d", code)
		}
		if code := cliStop([]string{a}); code != 0 {
			t.Errorf("stop a with nothing running: want 0, got %d", code)
		}
	})
	if strings.Count(errOut, "no task is currently tracking") != 2 {
		t.Errorf("stderr = %q, want the no-timer notice twice", errOut)
	}

	captureStdout(t, func() { cliStart([]string{b}) })
	// Naming a task that is not the running one is a mistake worth an exit
	// code: the user thinks they are stopping something they are not.
	errOut = captureStderr(t, func() {
		if code := cliStop([]string{a}); code != 2 {
			t.Errorf("stop a while b runs: want 2, got %d", code)
		}
	})
	if !strings.Contains(errOut, a[:8]+" is not currently tracking") {
		t.Errorf("stderr = %q", errOut)
	}
	if got := runningIDs(t); len(got) != 1 || got[0] != b {
		t.Fatalf("a refused stop changed the running set: %v", got)
	}

	out := captureStdout(t, func() {
		if code := cliStop(nil); code != 0 {
			t.Fatalf("stop: exit %d", code)
		}
	})
	if !strings.HasPrefix(out, "stopped: "+b[:8]) || !strings.Contains(out, "elapsed") {
		t.Errorf("stdout = %q, want the stopped line with the elapsed time", out)
	}
	if got := runningIDs(t); len(got) != 0 {
		t.Errorf("still running after stop: %v", got)
	}
}

// Two running timers can only come from outside the CLI (a sync from a
// device with an older build, a hand-edited store). The bare `stop` must not
// guess which one was meant.
func TestCLIStopWithTwoRunningNeedsARef(t *testing.T) {
	setTestHome(t, t.TempDir())
	a := addQuietID(t, "One")
	b := addQuietID(t, "Two")
	repo, todos, err := loadForCLI()
	if err != nil {
		t.Fatal(err)
	}
	var dirty []*todo.Todo
	for i := range todos {
		todos[i].StartTimer()
		dirty = append(dirty, &todos[i])
	}
	if err := repo.Save(dirty, nil); err != nil {
		t.Fatal(err)
	}

	errOut := captureStderr(t, func() {
		if code := cliStop(nil); code != 2 {
			t.Errorf("bare stop with two running: want 2, got %d", code)
		}
	})
	if !strings.Contains(errOut, "multiple tasks tracking") {
		t.Errorf("stderr = %q", errOut)
	}
	if got := runningIDs(t); len(got) != 2 {
		t.Fatalf("an ambiguous stop stopped something: %v", got)
	}
	captureStdout(t, func() {
		if code := cliStop([]string{a}); code != 0 {
			t.Errorf("stop a by ref: exit %d", code)
		}
	})
	if got := runningIDs(t); len(got) != 1 || got[0] != b {
		t.Errorf("after stopping a by ref, running = %v, want only b", got)
	}
}

func TestCLICommentAddEditDelete(t *testing.T) {
	setTestHome(t, t.TempDir())
	id := addQuietID(t, "Plan offsite")

	if code := cliComment(nil); code != 2 {
		t.Errorf("comment with no ref: want 2, got %d", code)
	}
	if code := cliComment([]string{id}); code != 2 {
		t.Errorf("comment with no text: want 2, got %d", code)
	}

	captureStdout(t, func() {
		if code := cliComment([]string{id, "Booked the room"}); code != 0 {
			t.Fatalf("add: exit %d", code)
		}
	})
	stdinWith(t, "Catering from stdin\n")
	captureStdout(t, func() {
		if code := cliComment([]string{id, "-"}); code != 0 {
			t.Fatalf("add from stdin: exit %d", code)
		}
	})
	got := cliTaskByID(t, id).Comments
	if len(got) != 2 || got[0].Text != "Booked the room" || got[1].Text != "Catering from stdin" {
		t.Fatalf("comments = %+v", got)
	}

	// Indices are 1-based, as `show` prints them, and the flag may follow the
	// ref like every other mutation command's.
	captureStdout(t, func() {
		if code := cliComment([]string{id, "--edit=1", "Booked the big room"}); code != 0 {
			t.Fatalf("edit: exit %d", code)
		}
	})
	if code := cliComment([]string{id, "--edit=1"}); code != 2 {
		t.Errorf("edit without text: want 2, got %d", code)
	}
	if code := cliComment([]string{id, "--edit=9", "x"}); code != 2 {
		t.Errorf("edit out of range: want 2, got %d", code)
	}
	if code := cliComment([]string{"--delete=3", id}); code != 2 {
		t.Errorf("delete out of range: want 2, got %d", code)
	}
	captureStdout(t, func() {
		if code := cliComment([]string{"--delete=2", id}); code != 0 {
			t.Fatalf("delete: exit %d", code)
		}
	})
	got = cliTaskByID(t, id).Comments
	if len(got) != 1 || got[0].Text != "Booked the big room" {
		t.Errorf("after edit 1 and delete 2, comments = %+v", got)
	}
}

// The prompting half of suggest is the half that writes, and it is only
// reachable on a terminal. The default direction is "the mentioned task
// depends on the mentioner"; "1" takes it and "2" reverses it, and each has to
// land on the right task or the graph is inverted without a word.
func TestCLISuggestLinksWhatTheUserAnswers(t *testing.T) {
	for _, tc := range []struct {
		answer         string
		dependentWaits bool // true: the mentioned task waits on the mentioner
	}{{"1\n", true}, {"2\n", false}} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			setTestHome(t, t.TempDir())
			mentioner := addQuietID(t, "Header status line")
			mentioned := addQuietID(t, "Toast kinds")
			// A note ref is the highest-precision evidence suggest looks for.
			captureStdout(t, func() {
				if code := cliEdit([]string{mentioner, "--note", "Absorbs " + mentioned[:8] + " once it lands."}); code != 0 {
					t.Fatalf("edit note: exit %d", code)
				}
			})
			orig := stdinIsTTY
			stdinIsTTY = func() bool { return true }
			t.Cleanup(func() { stdinIsTTY = orig })

			stdinWith(t, tc.answer)
			out := captureStdout(t, func() {
				if code := cliSuggest(nil); code != 0 {
					t.Fatalf("suggest: exit %d", code)
				}
			})
			if !strings.Contains(out, "linked 1 dependency") {
				t.Errorf("stdout = %q, want the linked summary", out)
			}
			waiter, blocker := mentioned, mentioner
			if !tc.dependentWaits {
				waiter, blocker = mentioner, mentioned
			}
			if deps := cliTaskByID(t, waiter).Dependencies; len(deps) != 1 || deps[0] != blocker {
				t.Fatalf("%s's deps = %v, want [%s]", waiter[:8], deps, blocker[:8])
			}
			if deps := cliTaskByID(t, blocker).Dependencies; len(deps) != 0 {
				t.Fatalf("the link landed on both ends: %s deps = %v", blocker[:8], deps)
			}

			// Linked now, so there is nothing left to suggest.
			out = captureStdout(t, func() { cliSuggest(nil) })
			if !strings.Contains(out, "no dependency suggestions") {
				t.Errorf("a linked pair was suggested again: %q", out)
			}
		})
	}
}

func TestCLISuggestSkipAndQuitWriteNothing(t *testing.T) {
	setTestHome(t, t.TempDir())
	blocker := addQuietID(t, "Header status line")
	dependent := addQuietID(t, "Toast kinds")
	captureStdout(t, func() {
		cliEdit([]string{blocker, "--note", "Absorbs " + dependent[:8] + "."})
	})
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = orig })

	for _, answer := range []string{"s\n", "q\n", ""} {
		stdinWith(t, answer)
		out := captureStdout(t, func() {
			if code := cliSuggest(nil); code != 0 {
				t.Fatalf("suggest (%q): exit %d", answer, code)
			}
		})
		if !strings.Contains(out, "nothing linked") {
			t.Errorf("answer %q: stdout = %q, want 'nothing linked'", answer, out)
		}
		for _, x := range reloadCLI(t) {
			if len(x.Dependencies) != 0 {
				t.Fatalf("answer %q linked %s", answer, x.ID[:8])
			}
		}
	}
}

// The plain-text `show` is what a person reads; every section it can print
// is populated here so a field dropped from it fails by name.
func TestCLIShowPrintsEverySection(t *testing.T) {
	setTestHome(t, t.TempDir())
	parent := addQuietID(t, "Launch site", "--project", "web", "--tag", "ops,urgent", "--due", "+3d", "--p", "h")
	captureStdout(t, func() {
		if code := cliSubtask([]string{parent, "Write copy"}); code != 0 {
			t.Fatalf("subtask: exit %d", code)
		}
	})
	child := findByTitle(t, reloadCLI(t), "Write copy").ID
	prereq := addQuietID(t, "Buy domain")
	captureStdout(t, func() {
		cliEdit([]string{parent, "--add-dep", prereq, "--note", "Ship by Friday."})
		cliComment([]string{parent, "Looks good"})
	})

	out := captureStdout(t, func() {
		if code := cliShow([]string{parent}); code != 0 {
			t.Fatalf("show: exit %d", code)
		}
	})
	for _, want := range []string{
		"ID:       " + parent,
		"Title:    Launch site",
		"Status:   pending",
		"Priority: high",
		"Due:      ",
		"Project:  web",
		"Tags:     ops, urgent",
		"Score:    ",
		"Subtasks (1):",
		child[:8],
		"Dependencies (1):",
		prereq[:8],
		"↧ Buy domain",
		"Comments (1):",
		"1. [",
		"Looks good",
		"Notes:\nShip by Friday.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output is missing %q:\n%s", want, out)
		}
	}

	// The inbound side: the prerequisite lists who waits on it.
	out = captureStdout(t, func() { cliShow([]string{prereq}) })
	if !strings.Contains(out, "↥ Launch site") {
		t.Errorf("prereq's show should list its dependent:\n%s", out)
	}

	// A done task drops the score and gains its completion time.
	captureStdout(t, func() { cliDone([]string{prereq}) })
	out = captureStdout(t, func() { cliShow([]string{prereq}) })
	if !strings.Contains(out, "Status:   done") || !strings.Contains(out, "Done at:  ") {
		t.Errorf("done task's show:\n%s", out)
	}
	if strings.Contains(out, "Score:") {
		t.Errorf("a done task has no score to show:\n%s", out)
	}
}

// A title made of nothing but tokens (or nothing at all) is refused rather
// than stored as a task with no name to find it by.
func TestCLIAddRefusesAnEmptyTitle(t *testing.T) {
	setTestHome(t, t.TempDir())
	for _, args := range [][]string{{""}, {"   "}, {"#work", "p:high"}} {
		var code int
		captureStderr(t, func() { code = cliAdd(args) })
		if code != 2 {
			t.Errorf("add %q: exit %d, want 2", args, code)
		}
	}
	_, todos, err := loadForCLI()
	if err != nil {
		t.Fatal(err)
	}
	if len(todos) != 0 {
		t.Errorf("stored %d task(s) without a title", len(todos))
	}
}

// Output a script depends on refuses a bad request instead of printing
// something else: a misspelled stats format, a top with no rows.
func TestCLIRefusesBadOutputRequests(t *testing.T) {
	setTestHome(t, t.TempDir())
	for _, c := range []struct {
		run  func([]string) int
		args []string
	}{
		{cliStats, []string{"--format=bogus"}},
		{cliTop, []string{"-n=0"}},
		{cliTop, []string{"-n=-3"}},
	} {
		var code int
		captureStderr(t, func() { code = c.run(c.args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", c.args, code)
		}
	}
}
