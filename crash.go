package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	tea "github.com/charmbracelet/bubbletea"
)

// crash.go is what happens between a panic and the user's next sentence.
//
// Bubble Tea already recovers panics raised on its event loop and puts the
// terminal back (Program.recoverFromPanic), and it does the same for the
// goroutines it runs commands on. Taking that over with WithoutCatchPanics
// would trade one crash class for another — a panic inside the save or sync
// command would then kill the process with the screen still in alt mode — so
// the guard here sits *inside* Update and View and re-panics when it is done,
// leaving the terminal to the code that already handles it.
//
// Being inside is the whole point. It is the only place with the stack from
// the panic site rather than from the recovery point three frames up, and the
// only place holding the live model — whose Store still carries the edits the
// 300ms save debounce had not written yet. From main, after Run returns, both
// are gone: the model copy there stopped being the live one at the first
// drainDirty, and the stack has long since unwound.

// maxCrashReports is how many reports survive in the state directory. Enough
// to cover "it did it three times in a row"; few enough that a reproducible
// crash cannot fill a disk.
const maxCrashReports = 5

// lastCrashReport is the path the most recent guard wrote, and lastCrashSaved
// whether the flush that came with it got through. main reads both once Run
// returns, so the sentence a user acts on is the last thing on screen instead
// of being scrolled off by Bubble Tea's own stack dump.
var (
	lastCrashReport string
	lastCrashSaved  bool
)

// crashGuard is deferred at the top of Update and View. It does nothing at all
// when there is no panic, which is the case it is optimized for.
//
// msg is the message being handled, or nil from View; it is passed unexamined
// so the hot path never pays for msgKind's formatting.
func (m *model) crashGuard(stage string, msg tea.Msg) {
	r := recover()
	if r == nil {
		return
	}
	// Captured here, while the panicking frames are still on this goroutine's
	// stack — the point of guarding at this depth.
	stack := debug.Stack()
	if msg != nil {
		stage += " (" + msgKind(msg) + ")"
	}
	lastCrashSaved = saveOnCrash(m)
	lastCrashReport = writeCrashReport(r, stack, stage, m)
	// Hand the panic back to Bubble Tea, which restores the terminal.
	panic(r)
}

// saveOnCrash writes out whatever the debounce still owed. Reports false when
// it could not: a panic mid-mutation can leave the store in a shape that
// panics again on the way out, and a second panic must not replace the first
// or the report describes the cleanup instead of the bug.
func saveOnCrash(m *model) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	m.flushPendingWrites()
	checkpointStore()
	return true
}

// writeCrashReport writes one report and returns its path, or "" if it could
// not be written — in which case the crash is still handled, just unreported.
//
// The header is the set of questions an issue would otherwise be a round trip
// of: which build, which platform, how big was the terminal, what was on
// screen, what was it doing. Terminal size in particular is worth its line:
// the layout math is where the panics have historically been, and a report
// from an 8-column window says so immediately.
func writeCrashReport(r any, stack []byte, stage string, m *model) string {
	dir, err := paths.Ensure(paths.State)
	if err != nil {
		return ""
	}
	now := time.Now().UTC()
	// Milliseconds because two frames can crash inside the same second, and a
	// name that collides overwrites the first report with the second.
	path := filepath.Join(dir, "crash-"+now.Format("20060102-150405.000")+".log")

	var b strings.Builder
	b.WriteString("tjek crash report\n\n")
	fmt.Fprintf(&b, "time:     %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "version:  %s\n", appVersion)
	fmt.Fprintf(&b, "runtime:  %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "stage:    %s\n", stage)
	if m != nil {
		fmt.Fprintf(&b, "terminal: %dx%d\n", m.termWidth, m.termHeight)
		fmt.Fprintf(&b, "state:    tab=%d mode=%d tasks=%d\n", m.tab, m.mode, len(m.tasks))
	}
	fmt.Fprintf(&b, "\npanic: %v\n\n%s", r, stack)

	// 0600: a report carries task titles in the stack's arguments now and
	// then, and it lands in a directory a user may well share.
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return ""
	}
	pruneCrashReports(dir)
	return path
}

// pruneCrashReports keeps the newest maxCrashReports and removes the rest.
// Best-effort: a report that cannot be removed is not worth failing a crash
// over.
func pruneCrashReports(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if err != nil || len(matches) <= maxCrashReports {
		return
	}
	// The timestamp is fixed-width, so lexical order is chronological order.
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-maxCrashReports] {
		_ = os.Remove(old)
	}
}

// noteCrashToUser prints the two things a crash owes its user: whether their
// work survived, and where the evidence is. Called from main once the terminal
// is back, and silent unless a guard actually fired.
func noteCrashToUser() {
	if lastCrashReport == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "\ntjek %s crashed.\n", appVersion)
	if lastCrashSaved {
		fmt.Fprintf(os.Stderr, "Unsaved edits were written to the database before exiting.\n")
	} else {
		fmt.Fprintf(os.Stderr, "The last few seconds of edits could not be saved: the crash happened mid-write.\n")
	}
	fmt.Fprintf(os.Stderr, "A report with the stack trace is at:\n  %s\n"+
		"Please attach it to an issue at https://github.com/Morteningemann86/xtratjek/issues\n", lastCrashReport)
}
