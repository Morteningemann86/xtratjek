package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// ── start / stop ─────────────────────────────────────────────────────────────

func cliStart(args []string) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek start <ref>")
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	target, err := findTaskByRef(todoPtrs(todos), fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// Idempotent re-start: if the target is already the running task, don't
	// rotate the timer entry. todo.StartTimer unconditionally calls StopTimer
	// then appends a fresh entry, so a stray repeat would split one session
	// into two zero-gap entries — silent data drift the user would only
	// notice on export.
	if target.IsTimerRunning() {
		fmt.Fprintf(os.Stderr, "already tracking: %s  %s\n", target.ID[:8], target.Title)
		return 0
	}
	// Stop any other running timer first — the TUI enforces single-task
	// time tracking and the CLI preserves that invariant via the shared
	// helper. Collect all touched tasks so they're flushed in one Save.
	dirty := []*todo.Todo{target}
	for _, t := range stopOtherRunningTimers(todos, target.ID) {
		dirty = append(dirty, t)
		// Side-effect notice → stderr, like add --start: stdout carries only
		// the primary result so scripts can parse it.
		fmt.Fprintf(os.Stderr, "stopped: %s  %s\n", t.ID[:8], t.Title)
	}
	target.StartTimer()
	if err := repo.Save(dirty, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	fmt.Printf("started: %s  %s\n", target.ID[:8], target.Title)
	return 0
}

func cliStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	var target *todo.Todo
	if fs.NArg() == 1 {
		// Explicit ref: stop the named task if it's actually running.
		t, err := findTaskByRef(todoPtrs(todos), fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if !t.IsTimerRunning() {
			// Distinguish "nothing is tracking anywhere" from "a different
			// task is tracking" — the first is the common case (user just
			// typo'd or forgot a timer wasn't running) and deserves the same
			// message as the no-ref form.
			anyRunning := false
			for i := range todos {
				if todos[i].IsTimerRunning() {
					anyRunning = true
					break
				}
			}
			if !anyRunning {
				fmt.Fprintln(os.Stderr, "no task is currently tracking")
				return 0
			}
			fmt.Fprintf(os.Stderr, "task %s is not currently tracking\n", t.ID[:8])
			return 2
		}
		target = t
	} else {
		// No ref: stop whichever task is running. Zero or two-plus is an error.
		var running []*todo.Todo
		for i := range todos {
			if todos[i].IsTimerRunning() {
				running = append(running, &todos[i])
			}
		}
		switch len(running) {
		case 0:
			fmt.Fprintln(os.Stderr, "no task is currently tracking")
			return 0
		case 1:
			target = running[0]
		default:
			fmt.Fprintln(os.Stderr, "multiple tasks tracking; pass a <ref> to disambiguate")
			return 2
		}
	}
	// Capture the elapsed time before StopTimer wipes the running entry's
	// in-progress state, so we can report it.
	var elapsed time.Duration
	if e := target.RunningEntry(); e != nil {
		elapsed = time.Since(e.StartedAt)
	}
	target.StopTimer()
	if err := repo.Save([]*todo.Todo{target}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	fmt.Printf("stopped: %s  %s  (elapsed %s)\n", target.ID[:8], target.Title, formatDuration(elapsed))
	return 0
}

// ── log ──────────────────────────────────────────────────────────────────────

// cliLog backfills a closed time entry on a task — work the live timer didn't
// capture (forgot to start it, or the stale-timer recovery under-logged a
// session). Same input semantics as the TUI's 'T' shortcut via
// parseManualEntry: a bare duration ends now, a clock range is literal today.
func cliLog(args []string) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: tjek log <ref> <45m|1h30m|HH:MM-HH:MM>
  duration form ends now ("I just spent 45m on this")
  range form is taken literally on today (crosses midnight if end < start)`)
	}
	flagArgs, positionals := splitFlagsAndPositionals(fs, args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positionals) != 2 {
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
	start, stop, err := parseManualEntry(positionals[1], time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek log: %v\n", err)
		return 2
	}
	t.AddTimeEntry(start, stop)
	if err := repo.Save([]*todo.Todo{t}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	fmt.Printf("logged %s on %s  %s  (%s–%s)\n", formatDuration(stop.Sub(start)),
		t.ID[:8], t.Title, start.Format("15:04"), stop.Format("15:04"))
	return 0
}

// ── export ───────────────────────────────────────────────────────────────────

func cliExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	includeDone := fs.Bool("include-done", false, "include completed tasks (default: only pending live tasks)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	out := make([]todo.Todo, 0, len(todos))
	for _, t := range todos {
		if !*includeDone && t.Status == todo.Done {
			continue
		}
		out = append(out, t)
	}
	return emitJSON(exportEnvelope{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Tasks:      out,
	})
}
