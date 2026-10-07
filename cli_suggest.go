package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Morteningemann86/xtratjek/todo"
)

// tjek suggest mines the dependency structure the user already wrote down
// implicitly and offers to make it explicit, one y/n at a time. Two sources:
//
//   - note refs: a task whose notes mention another pending task's id prefix
//     ("absorbs e3027f5d", "see fd8502d1") almost always relates to it — the
//     graph is sitting there as prose
//   - title overlap: two pending tasks in the same project sharing several
//     significant title words are usually steps of one effort, typed in
//     execution order (earlier created = suggested blocker)
//
// Suggestions are never applied silently: inference feeding the score rollup
// unconfirmed could catapult an unrelated task to the top (inheritance is
// max()), so a human picks the direction or skips. --list prints without
// prompting; non-TTY stdin implies --list.

// depSuggestion pairs two pending tasks with the evidence that they relate.
// a/b ordering encodes the suggested default direction: option [1] is
// "a depends on b" (b blocks a).
type depSuggestion struct {
	a, b     *todo.Todo
	evidence string
}

const suggestMaxSuggestions = 20

// suggestStopwords are title tokens too generic to signal relatedness even at
// four+ characters.
var suggestStopwords = map[string]bool{
	"with": true, "from": true, "into": true, "that": true, "this": true,
	"then": true, "when": true, "make": true, "task": true, "tasks": true,
	"after": true, "before": true, "update": true, "check": true, "the": true,
}

// noteRefPattern matches a bare 8-hex id prefix (the form every tjek surface
// prints); \b keeps it from firing inside longer hex runs, and a full UUID's
// first segment matches on its own because '-' is a word boundary.
var noteRefPattern = regexp.MustCompile(`\b[0-9a-f]{8}\b`)

func cliSuggest(args []string) int {
	fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listOnly := fs.Bool("list", false, "print the suggestions without prompting to link them")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tjek suggest [--list]   suggest dependency links from note refs and related titles")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, todos, err := loadForCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 1
	}
	suggestions := collectDepSuggestions(todos)
	if len(suggestions) == 0 {
		fmt.Println("no dependency suggestions: notes and titles carry no unlinked structure")
		return 0
	}
	if *listOnly || !stdinIsTTY() {
		for _, s := range suggestions {
			fmt.Printf("%s %q  ⇄  %s %q\n    %s\n", s.a.ID[:8], s.a.Title, s.b.ID[:8], s.b.Title, s.evidence)
		}
		if !*listOnly {
			fmt.Fprintln(os.Stderr, "stdin is not a terminal, so these are printed without prompting (run interactively to link)")
		}
		return 0
	}

	byID := make(map[string]*todo.Todo, len(todos))
	for i := range todos {
		byID[todos[i].ID] = &todos[i]
	}
	sc := bufio.NewScanner(os.Stdin)
	var dirty []*todo.Todo
	linked := 0
prompts:
	for i, s := range suggestions {
		// Re-resolve against byID: earlier answers may have changed the graph.
		a, b := byID[s.a.ID], byID[s.b.ID]
		if a == nil || b == nil || directlyLinked(a, b) {
			continue
		}
		fmt.Printf("\n[%d/%d] %s %q\n        %s %q\n    %s\n",
			i+1, len(suggestions), a.ID[:8], a.Title, b.ID[:8], b.Title, s.evidence)
		fmt.Printf("    [1] %.8s depends on %.8s   [2] %.8s depends on %.8s   [s]kip  [q]uit: ",
			a.ID, b.ID, b.ID, a.ID)
		if !sc.Scan() {
			break
		}
		dependent, blocker := a, b
		switch strings.TrimSpace(sc.Text()) {
		case "1":
		case "2":
			dependent, blocker = b, a
		case "q":
			break prompts
		default:
			continue
		}
		if loopingDepCandidates(byID, dependent.ID)[blocker.ID] {
			fmt.Printf("    refused: %.8s already depends on %.8s (directly or transitively), so the link would loop\n",
				blocker.ID, dependent.ID)
			continue
		}
		dependent.AddDependency(blocker.ID)
		dirty = append(dirty, dependent)
		linked++
		fmt.Printf("    linked: %.8s now blocks %.8s\n", blocker.ID, dependent.ID)
	}
	if len(dirty) == 0 {
		fmt.Println("\nnothing linked")
		return 0
	}
	if err := repo.Save(dirty, nil); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	fmt.Printf("\nlinked %d dependenc%s\n", linked, map[bool]string{true: "y", false: "ies"}[linked == 1])
	return 0
}

// collectDepSuggestions is the pure scan: note-ref matches first (highest
// precision), then same-project title overlaps, deduped on the unordered
// pair, capped at suggestMaxSuggestions.
func collectDepSuggestions(todos []todo.Todo) []depSuggestion {
	var pending []*todo.Todo
	for i := range todos {
		if todos[i].Status == todo.Pending && !todos[i].Deleted {
			pending = append(pending, &todos[i])
		}
	}
	byPrefix := make(map[string]*todo.Todo, len(pending))
	for _, t := range pending {
		if len(t.ID) >= 8 {
			byPrefix[t.ID[:8]] = t
		}
	}

	seen := make(map[string]bool)
	pairKey := func(a, b *todo.Todo) string {
		if a.ID < b.ID {
			return a.ID + "|" + b.ID
		}
		return b.ID + "|" + a.ID
	}
	// relatable filters pairs that can't or needn't be suggested: self,
	// parent/child (already structured), or an existing direct link.
	relatable := func(a, b *todo.Todo) bool {
		if a.ID == b.ID || a.ParentID == b.ID || b.ParentID == a.ID {
			return false
		}
		return !directlyLinked(a, b)
	}

	var out []depSuggestion
	add := func(s depSuggestion) bool {
		if seen[pairKey(s.a, s.b)] {
			return len(out) < suggestMaxSuggestions
		}
		seen[pairKey(s.a, s.b)] = true
		out = append(out, s)
		return len(out) < suggestMaxSuggestions
	}

	// Pass 1: id prefixes mentioned in notes.
	for _, t := range pending {
		if t.Notes == "" {
			continue
		}
		for _, ref := range noteRefPattern.FindAllString(strings.ToLower(t.Notes), -1) {
			other, ok := byPrefix[ref]
			if !ok || !relatable(t, other) {
				continue
			}
			// Default direction: the mentioned task depends on the mentioner —
			// notes usually describe what this task covers or absorbs. The
			// prompt offers the reverse.
			if !add(depSuggestion{a: other, b: t,
				evidence: fmt.Sprintf("notes of %.8s mention %.8s", t.ID, other.ID)}) {
				return out
			}
		}
	}

	// Pass 2: same-project pairs sharing ≥2 significant title tokens.
	// Sorted by creation so the suggested blocker (earlier task, option [1])
	// and the output order are deterministic.
	byProject := make(map[string][]*todo.Todo)
	for _, t := range pending {
		if t.Project != "" {
			byProject[t.Project] = append(byProject[t.Project], t)
		}
	}
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	for _, p := range projects {
		group := byProject[p]
		sort.Slice(group, func(i, j int) bool { return group[i].CreatedAt.Before(group[j].CreatedAt) })
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				earlier, later := group[i], group[j]
				if !relatable(earlier, later) {
					continue
				}
				shared := sharedTitleTokens(earlier.Title, later.Title)
				// A token that just restates the project name is no signal
				// inside that project's group.
				shared = deleteToken(shared, strings.ToLower(p))
				if len(shared) < 2 {
					continue
				}
				if !add(depSuggestion{a: later, b: earlier,
					evidence: fmt.Sprintf("same project %q, titles share: %s", p, strings.Join(shared, ", "))}) {
					return out
				}
			}
		}
	}
	return out
}

func directlyLinked(a, b *todo.Todo) bool {
	for _, dep := range a.Dependencies {
		if dep == b.ID {
			return true
		}
	}
	for _, dep := range b.Dependencies {
		if dep == a.ID {
			return true
		}
	}
	return false
}

// deleteToken returns tokens without w (order preserved).
func deleteToken(tokens []string, w string) []string {
	out := tokens[:0]
	for _, t := range tokens {
		if t != w {
			out = append(out, t)
		}
	}
	return out
}

// sharedTitleTokens returns the significant words two titles share: ≥4 chars,
// not a stopword, deduped, sorted for deterministic output.
func sharedTitleTokens(t1, t2 string) []string {
	tokens := func(s string) map[string]bool {
		set := make(map[string]bool)
		for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
		}) {
			if len(w) >= 4 && !suggestStopwords[w] {
				set[w] = true
			}
		}
		return set
	}
	set1 := tokens(t1)
	var shared []string
	for w := range tokens(t2) {
		if set1[w] {
			shared = append(shared, w)
		}
	}
	sort.Strings(shared)
	return shared
}
