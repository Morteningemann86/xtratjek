package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Fuzzing the parsers.
//
// Every one of these takes free-form text a user typed — the quick-add line,
// a search box, a date field, a time-entry edit — and hands back a struct the
// rest of the app then trusts. smallterm_test.go already established that
// unexpected *input dimensions* are a real crash class here (negative widths
// panicking a rune slice); unexpected input *text* is the same class through
// a different door, and index arithmetic like parseDueDate's
// `lower[1 : len(lower)-1]` is exactly where it lands.
//
// These run as ordinary unit tests over their seed corpus in CI, so the seeds
// are regression tests for free. Run them as actual fuzzers with:
//
//	go test -run=Fuzz -fuzz=FuzzParseQuickAdd -fuzztime=60s .
//
// Any crasher the fuzzer finds gets written to testdata/fuzz/<Name>/ and
// becomes part of the seed corpus — commit it with the fix.

func FuzzParseQuickAdd(f *testing.F) {
	for _, seed := range []string{
		"",
		"buy milk",
		"buy milk #home @errands p:high s:l due:tomorrow",
		"#", "@", "p:", "s:", "due:", "dep:", "every:",
		"#  @  p: s: due:",
		"task #tag#tag @proj@proj",
		"due:+1d", "due:+999999999999999999999d", "due:+",
		"dep:^ dep:abc123",
		"every:weekday every:",
		"   ", "\t\n", "\x00", "🙂 #🙂 @🙂",
		strings.Repeat("#a ", 200),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got := parseQuickAdd(input)

		// An empty tag or project renders as a bare "#"/"@" in every list
		// and cannot be searched for or removed — it is a value that
		// should never have been constructed, not a display bug.
		for _, tag := range got.tags {
			if tag == "" {
				t.Fatalf("parseQuickAdd(%q) produced an empty tag", input)
			}
			if strings.ContainsAny(tag, " \t\n") {
				t.Fatalf("parseQuickAdd(%q) produced tag %q containing whitespace", input, tag)
			}
		}
		if strings.ContainsAny(got.project, " \t\n") {
			t.Fatalf("parseQuickAdd(%q) produced project %q containing whitespace", input, got.project)
		}
		// Valid UTF-8 in must stay valid UTF-8 out: a byte-index split
		// through a multi-byte rune would show up as a replacement
		// character in the stored title.
		if utf8.ValidString(input) && !utf8.ValidString(got.title) {
			t.Fatalf("parseQuickAdd(%q) produced an invalid-UTF-8 title %q", input, got.title)
		}
	})
}

func FuzzParseDueDate(f *testing.F) {
	for _, seed := range []string{
		"", "today", "tomorrow", "yesterday", "next week", "next month",
		"monday", "next monday", "next ", "next",
		"+1d", "+2w", "+3m", "+", "+d", "+0d", "+-1d",
		"+99999999999999999999d",
		"01-02-06", "01-02-2006", "31-13-99", "00-00-00",
		"  TODAY  ", "\x00", "🙂", strings.Repeat("9", 500),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := parseDueDate(input)
		if err != nil {
			// The error is shown verbatim to the user, so it has to say
			// what to type instead.
			if got != (time.Time{}) {
				t.Fatalf("parseDueDate(%q) failed but returned %v", input, got)
			}
			return
		}
		// A successful parse that yields the zero time reads downstream as
		// "no due date", which silently discards what the user typed.
		if got.IsZero() {
			t.Fatalf("parseDueDate(%q) succeeded with the zero time", input)
		}
	})
}

func FuzzCompileSearch(f *testing.F) {
	for _, seed := range []string{
		"", "milk", "#tag", "@project", "p:high", "p:", "s:l", "due:<fri",
		"due:>", "due:", "overdue", "done", "#", "@", ":", "::",
		"#tag @proj p:high s:s due:<tomorrow overdue",
		"   ", "\x00", "🙂", strings.Repeat("#a ", 200),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		match := compileSearch(query)
		if match == nil {
			t.Fatalf("compileSearch(%q) returned nil — every caller invokes it unconditionally", query)
		}
		// Applying the predicate is half the surface: a filter that panics
		// on the task it is handed is no better than one that fails to
		// compile.
		for _, task := range fuzzProbeTasks() {
			match(task)
		}
	})
}

// fuzzProbeTasks returns tasks spanning the fields a compiled search can
// touch, including the empty task — a filter is run over every task in the
// store, not just well-formed ones.
func fuzzProbeTasks() []todo.Todo {
	due := time.Now().AddDate(0, 0, -1)
	return []todo.Todo{
		{},
		{Title: "milk", Tags: []string{"home"}, Project: "errands", Priority: todo.PriorityHigh, Size: todo.SizeLarge},
		{Title: "🙂", Tags: []string{""}, DueDate: due, Status: todo.Done},
	}
}

func FuzzParseEntryEdit(f *testing.F) {
	for _, seed := range []string{
		"", "45m", "1h", "2h30m", "10:00-11:30", "10:00-", "-11:30", "-",
		"99:99-00:00", "10:00-09:00", "0m", "-5m", "1h-2h",
		"  45m  ", "\x00", "🙂", strings.Repeat("9", 400) + "m",
	} {
		f.Add(seed)
	}
	oldStart := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, input string) {
		for _, running := range []bool{false, true} {
			start, stop, err := parseEntryEdit(input, oldStart, running)
			if err != nil {
				continue
			}
			// A time entry whose stop precedes its start yields a negative
			// duration, which every roll-up (calendar, stats, the tag
			// totals) then sums into a wrong total rather than rejecting.
			if !running && stop.Before(start) {
				t.Fatalf("parseEntryEdit(%q, running=%v) = %v..%v, which runs backwards", input, running, start, stop)
			}
		}
	})
}

func FuzzParseManualEntry(f *testing.F) {
	for _, seed := range []string{
		"", "45m", "10:00-11:30", "10:00-09:00", "-", "0m", "🙂",
		strings.Repeat("1", 300) + "h",
	} {
		f.Add(seed)
	}
	now := time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, input string) {
		start, stop, err := parseManualEntry(input, now)
		if err != nil {
			return
		}
		if stop.Before(start) {
			t.Fatalf("parseManualEntry(%q) = %v..%v, which runs backwards", input, start, stop)
		}
	})
}

func FuzzParseClockOn(f *testing.F) {
	for _, seed := range []string{
		"", "10:00", "24:00", "99:99", "1:0", "10:00:00", ":", "🙂", "\x00",
	} {
		f.Add(seed)
	}
	day := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, input string) {
		got, err := parseClockOn(input, day)
		if err != nil {
			return
		}
		// The parsed clock time must land on the day it was anchored to;
		// a carry into the next day would silently move a time entry.
		if got.Year() != day.Year() || got.YearDay() != day.YearDay() {
			t.Fatalf("parseClockOn(%q) landed on %v, not on %v", input, got, day)
		}
	})
}
