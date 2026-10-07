package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Morteningemann86/xtratjek/todo"
)

// The CLI's routing tables are three parallel lists that have to agree:
// isCLICommand decides what does not launch the TUI, dispatchCLI decides what
// each verb runs, and cliMutates decides what triggers an auto-sync afterwards.
// Nothing tied them together, so renaming a verb could leave it routable but
// unsynced — or, worse, leave a read-only command in the mutating set, firing
// a network round trip on every invocation.

func TestEveryRoutableCommandIsRecognised(t *testing.T) {
	// cliCommandSpecs drives the completions and the man page. Anything in
	// it must also be something main() routes away from the TUI, or the
	// completion offers a verb that opens the task list instead.
	for _, spec := range cliCommandSpecs {
		if !isCLICommand(spec.name) {
			t.Errorf("%q is documented and completed but isCLICommand rejects it — it would launch the TUI", spec.name)
		}
	}
}

func TestMutatingCommandsAreRoutable(t *testing.T) {
	// A verb in the mutating set that no longer routes is dead weight; one
	// that routes under a new name and was left behind here stops syncing.
	for _, cmd := range []string{
		"add", "done", "edit", "delete", "rm", "undelete", "comment",
		"start", "stop", "log", "subtask", "undo", "suggest", "import",
	} {
		if !cliMutates(cmd) {
			t.Errorf("%q should be in the mutating set", cmd)
		}
		if !isCLICommand(cmd) {
			t.Errorf("%q is in the mutating set but is not routable", cmd)
		}
	}
}

func TestReadOnlyCommandsDoNotTriggerSync(t *testing.T) {
	// doctor is the one this pins hardest: it used to name the mutating
	// suggestion pass, and leaving it in this set would fire a sync round
	// trip every time someone ran a diagnostic.
	for _, cmd := range []string{
		"doctor", "list", "ls", "show", "top", "search", "tags", "projects",
		"stats", "export", "completion", "man", "help", "serve",
	} {
		if cliMutates(cmd) {
			t.Errorf("%q is read-only but is marked as mutating — it would auto-sync on every run", cmd)
		}
	}
}

func TestIsCLICommandRejectsNonCommands(t *testing.T) {
	// A bare flag meant for the TUI, or a typo, must fall through to the
	// TUI rather than being swallowed by the dispatcher's default branch.
	for _, arg := range []string{"", "nonsense", "--fullscreen", "-x", "Add", "DONE"} {
		if isCLICommand(arg) {
			t.Errorf("%q should not route to the CLI dispatcher", arg)
		}
	}
}

func TestParseSizeFlagAcceptsBothForms(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want todo.Size
	}{
		{"s", todo.SizeSmall}, {"small", todo.SizeSmall}, {"SMALL", todo.SizeSmall},
		{"l", todo.SizeLarge}, {"large", todo.SizeLarge}, {"Large", todo.SizeLarge},
		{"m", todo.SizeMedium}, {"medium", todo.SizeMedium},
		// Anything unrecognised lands on Medium rather than erroring: the
		// flag is a convenience and a typo should not fail the whole add.
		{"", todo.SizeMedium}, {"enormous", todo.SizeMedium},
	} {
		if got := parseSizeFlag(tc.in); got != tc.want {
			t.Errorf("parseSizeFlag(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSortNameCountsRanksByCountThenName(t *testing.T) {
	rows := []nameCount{
		{Name: "zebra", Count: 2},
		{Name: "alpha", Count: 5},
		{Name: "beta", Count: 2},
		{Name: "gamma", Count: 5},
	}
	sortNameCounts(rows)

	var got []string
	for _, r := range rows {
		got = append(got, r.Name)
	}
	// Count descending, then name ascending so equal counts have a stable,
	// predictable order rather than whatever the map iteration produced.
	want := []string{"alpha", "gamma", "beta", "zebra"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestSortNameCountsHandlesEmptyAndSingle(t *testing.T) {
	sortNameCounts(nil)
	rows := []nameCount{{Name: "only", Count: 1}}
	sortNameCounts(rows)
	if rows[0].Name != "only" {
		t.Errorf("single row was disturbed: %+v", rows[0])
	}
}

func TestCliHelpListsEveryRoutableCommand(t *testing.T) {
	out := captureStdout(t, func() {
		if code := cliHelp(); code != 0 {
			t.Errorf("cliHelp exit = %d, want 0", code)
		}
	})
	if out == "" {
		t.Fatal("cliHelp printed nothing")
	}
	// The help screen is the discovery surface: a command missing from it
	// effectively does not exist for anyone not reading the source.
	for _, spec := range cliCommandSpecs {
		if spec.name == "help" {
			continue // documented by being the thing you are reading
		}
		if !strings.Contains(out, "tjek "+spec.name) {
			t.Errorf("`tjek %s` is routable but absent from the help output", spec.name)
		}
	}
}

// `tjek help` is read in a terminal, most of which open 80 columns wide; a
// longer line wraps mid-word into the next command's column.
func TestCliHelpFitsEightyColumns(t *testing.T) {
	out := captureStdout(t, func() { cliHelp() })
	for i, line := range strings.Split(out, "\n") {
		if n := utf8.RuneCountInString(line); n > helpWidth {
			t.Errorf("help line %d is %d columns: %q", i+1, n, line)
		}
	}
}
