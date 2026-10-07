package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

func TestQuickAddToken(t *testing.T) {
	cases := []struct {
		name         string
		value        string
		pos          int
		wantSigil    string
		wantQuery    string
		wantOK       bool
		wantSt, wnEn int
	}{
		{"bare sigil", "buy milk #", 10, "#", "", true, 9, 10},
		{"partial tag", "buy milk #ho", 12, "#", "ho", true, 9, 12},
		{"project token", "buy milk @ho", 12, "@", "ho", true, 9, 12},
		{"caret inside the token completes the whole token", "buy #home now", 7, "#", "home", true, 4, 9},
		{"plain word is not a completion", "buy milk", 8, "", "", false, 0, 0},
		{"caret parked before a token has nothing typed yet", "buy #home", 4, "", "", false, 0, 0},
		{"empty input", "", 0, "", "", false, 0, 0},
		// "købé " is five runes, so the token runs 5..8 in rune space — the
		// byte offsets would be 6..10 and would slice mid-rune.
		{"multibyte before the token", "købé #hø", 8, "#", "hø", true, 5, 8},
	}
	for _, c := range cases {
		sigil, query, start, end, ok := quickAddToken(c.value, c.pos)
		if ok != c.wantOK || sigil != c.wantSigil || query != c.wantQuery {
			t.Errorf("%s: quickAddToken(%q, %d) = %q/%q/%v, want %q/%q/%v",
				c.name, c.value, c.pos, sigil, query, ok, c.wantSigil, c.wantQuery, c.wantOK)
			continue
		}
		if ok && (start != c.wantSt || end != c.wnEn) {
			t.Errorf("%s: bounds = %d..%d, want %d..%d", c.name, start, end, c.wantSt, c.wnEn)
		}
	}
}

// A caret position past the end of the string (or below zero) must clamp
// rather than slice out of range — Update can hand either shape through.
func TestQuickAddTokenClampsCaret(t *testing.T) {
	if _, _, _, _, ok := quickAddToken("buy #home", 99); !ok {
		t.Error("caret past the end should clamp to the end and still complete the last token")
	}
	if _, _, _, _, ok := quickAddToken("buy #home", -3); ok {
		t.Error("negative caret should clamp to 0, where nothing is typed yet")
	}
}

// suggestModel stamps ModifiedAt explicitly: the suggestion order is
// recency-first, and two todo.New calls in the same test would otherwise be
// separated by nanoseconds — enough to pass, not enough to trust.
func suggestModel(t *testing.T) model {
	t.Helper()
	now := time.Now()
	home := todo.New("Fix boiler")
	home.AddTag("home")
	home.AddTag("house-stuff")
	home.Project = "House"
	home.ModifiedAt = now.Add(-2 * time.Hour)
	work := todo.New("Write memo")
	work.AddTag("work")
	work.AddTag("writing")
	work.Project = "Q3"
	work.ModifiedAt = now
	spaced := todo.New("Redo kitchen")
	spaced.Project = "Home renovation"
	spaced.ModifiedAt = now.Add(-time.Hour)
	return modelWithTasks(t, home, work, spaced)
}

func TestQuickAddSuggestions(t *testing.T) {
	m := suggestModel(t)

	sigil, got := m.quickAddSuggestions("buy milk #", 10)
	if sigil != "#" {
		t.Fatalf("sigil = %q, want #", sigil)
	}
	if len(got) != 4 {
		t.Fatalf("bare '#' = %v, want every tag offered", got)
	}

	// Prefix matches come first; a plain substring match still shows up.
	_, got = m.quickAddSuggestions("buy milk #o", 11)
	if want := []string{"work", "home", "house-stuff"}; !reflect.DeepEqual(got, want) {
		t.Errorf("'#o' = %v, want %v (substring matches, recency order)", got, want)
	}
	_, got = m.quickAddSuggestions("buy milk #w", 11)
	if len(got) == 0 || got[0] != "work" {
		t.Errorf("'#w' = %v, want the prefix match first", got)
	}

	// A tag already on the line is not worth offering again — but the token
	// being typed must not exclude itself.
	_, got = m.quickAddSuggestions("buy milk #work #w", 17)
	for _, g := range got {
		if g == "work" {
			t.Errorf("'#work #w' = %v, want the already-typed tag left out", got)
		}
	}
	_, got = m.quickAddSuggestions("buy milk #work", 14)
	for _, g := range got {
		if g == "work" {
			t.Errorf("'#work' = %v, want the fully typed tag left out", got)
		}
	}
}

func TestQuickAddSuggestionsProjects(t *testing.T) {
	m := suggestModel(t)
	sigil, got := m.quickAddSuggestions("buy milk @", 10)
	if sigil != "@" {
		t.Fatalf("sigil = %q, want @", sigil)
	}
	// "Home renovation" carries a space, which quick-add's whitespace
	// tokenisation cannot express, so it must not be offered.
	if want := []string{"Q3", "House"}; !reflect.DeepEqual(got, want) {
		t.Errorf("'@' = %v, want %v (space-free projects, recency order)", got, want)
	}
}

func TestQuickAddSuggestionsCapped(t *testing.T) {
	task := todo.New("Tagged")
	for _, tag := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"} {
		task.AddTag(tag)
	}
	m := modelWithTasks(t, task)
	if _, got := m.quickAddSuggestions("x #a", 4); len(got) != maxQuickAddSuggestions {
		t.Errorf("got %d suggestions, want the cap of %d", len(got), maxQuickAddSuggestions)
	}
}

func TestAcceptQuickAddSuggestion(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		pos      int
		choice   string
		want     string
		wantPos  int
		wantDone bool
	}{
		{"completes and leaves room for the next token", "buy milk #ho", 12, "home", "buy milk #home ", 15, true},
		// The caret lands at the end of the completed token; the space that was
		// already there is left alone rather than duplicated.
		{"does not double an existing space", "buy #ho milk", 7, "home", "buy #home milk", 9, true},
		{"project keeps its sigil", "buy @ho", 7, "House", "buy @House ", 11, true},
		{"nothing to complete", "buy milk", 8, "home", "buy milk", 8, false},
	}
	for _, c := range cases {
		got, pos, ok := acceptQuickAddSuggestion(c.value, c.pos, c.choice)
		if got != c.want || pos != c.wantPos || ok != c.wantDone {
			t.Errorf("%s: accept(%q, %d, %q) = %q/%d/%v, want %q/%d/%v",
				c.name, c.value, c.pos, c.choice, got, pos, ok, c.want, c.wantPos, c.wantDone)
		}
	}
}

// ── The search field takes the same completions ───────────────────────────────

// suggestSearchModel gives every tab something to complete against: three tags
// where two share a prefix, and two projects.
func suggestSearchModel(t *testing.T) model {
	t.Helper()
	var tasks []todo.Todo
	// Stamped an hour apart rather than left on the wall clock: the candidate
	// pool is ordered by recency (sortByRecency over cache.tagLastUsed), and
	// three tasks built in a loop are the same instant on a clock as coarse as
	// Windows' ~15ms — which left "home" and "homework", both prefix matches
	// of "#ho", in whichever order the tie broke. The last task is the most
	// recent one, and every assertion below can say so.
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	for i, spec := range []struct{ title, tag, project string }{
		{"Fix the boiler", "home", "House"},
		{"Write the memo", "work", "Q3"},
		{"Call the plumber", "homework", "House"},
	} {
		task := todo.New(spec.title)
		task.AddTag(spec.tag)
		task.Project = spec.project
		task.CreatedAt = base.Add(time.Duration(i) * time.Hour)
		task.ModifiedAt = task.CreatedAt
		tasks = append(tasks, task)
	}
	return modelWithTasks(t, tasks...)
}

// typeInto sends a string one key at a time, the way a user produces it.
func typeInto(t *testing.T, m model, s string) model {
	t.Helper()
	for _, r := range s {
		m = sendKey(t, m, string(r))
	}
	return m
}

// The Board's search offers the same tag and project names quick-add does, and
// tab splices one in and narrows the columns with it.
func TestSearchCompletesTagsOnTheBoard(t *testing.T) {
	m := suggestSearchModel(t)
	m.switchTab(tabBoard)
	m = sendKey(t, m, "/")
	m = typeInto(t, m, "#ho")

	sigil, matches := m.completionMatches()
	if sigil != "#" || len(matches) != 2 {
		t.Fatalf("sigil %q matches %v, want both ho-tags offered", sigil, matches)
	}
	// Prefix matches float to the front of the row.
	if matches[0] != "homework" && matches[0] != "home" {
		t.Errorf("first candidate = %q, want a tag starting with the typed text", matches[0])
	}

	m = sendKey(t, m, "tab")
	if m.searchQuery != "#"+matches[0]+" " {
		t.Fatalf("query = %q after tab, want the completed tag", m.searchQuery)
	}
	m.ensureCache()
	cards := 0
	for _, col := range m.boardColumns() {
		cards += len(col)
	}
	if cards != 1 {
		t.Errorf("board shows %d cards for %q, want the one task carrying it", cards, m.searchQuery)
	}
}

func TestSearchCompletesProjectsOnTheBoard(t *testing.T) {
	m := suggestSearchModel(t)
	m.switchTab(tabBoard)
	m = sendKey(t, m, "/")
	m = typeInto(t, m, "@Ho")

	sigil, matches := m.completionMatches()
	if sigil != "@" || len(matches) != 1 || matches[0] != "House" {
		t.Fatalf("sigil %q matches %v, want the House project", sigil, matches)
	}
}

// ↑/↓ move the highlight rather than the list cursor, and enter still applies
// the filter — the completion row must not stand between the user and the
// thing they opened the field to do.
func TestSearchCompletionKeysKeepEnterAndEsc(t *testing.T) {
	m := suggestSearchModel(t)
	m.switchTab(tabBoard)
	m = sendKey(t, m, "/")
	m = typeInto(t, m, "#ho")

	_, matches := m.completionMatches()
	m = sendKey(t, m, "down")
	if m.suggestCursor != 1%len(matches) {
		t.Errorf("suggestCursor = %d after ↓, want the next candidate", m.suggestCursor)
	}
	m = sendKey(t, m, "enter")
	if m.mode != modeNormal {
		t.Errorf("mode = %v after enter, want the filter applied and the field closed", m.mode)
	}
	if m.searchQuery != "#ho" {
		t.Errorf("query = %q, want what was typed — enter applies, it does not complete", m.searchQuery)
	}
}

// The Projects tab filters project *names* with a plain substring, so a #tag
// completion there would offer to build a query it does not run.
func TestSearchOffersNoCompletionsWhereTheGrammarDoesNotRun(t *testing.T) {
	m := suggestSearchModel(t)
	m.switchTab(tabProjects)
	m = sendKey(t, m, "/")
	m = typeInto(t, m, "#ho")

	if _, matches := m.completionMatches(); len(matches) != 0 {
		t.Errorf("Projects offered %v, but its search is a name substring", matches)
	}
	if m.searchUsesTokenGrammar() {
		t.Error("searchUsesTokenGrammar is true for Projects")
	}
	for _, tab := range []tab{tabTasks, tabBoard, tabStats} {
		probe := suggestSearchModel(t)
		probe.switchTab(tab)
		if !probe.searchUsesTokenGrammar() {
			t.Errorf("tab %v runs compileSearch on its query but is not marked as such", tab)
		}
	}
}

// The parse preview used to be gated to the Tasks tab on the belief that no
// other tab ran the grammar. Board and Stats both do, and typing a token there
// with no feedback is how a mistyped one goes unnoticed.
func TestSearchPreviewShowsWhereverTheGrammarRuns(t *testing.T) {
	for _, tab := range []tab{tabTasks, tabBoard, tabStats} {
		m := suggestSearchModel(t)
		m.switchTab(tab)
		m = sendKey(t, m, "/")
		m = typeInto(t, m, "p:high")
		// The caret is not in a #/@ token, so the preview owns the footer row.
		if _, matches := m.completionMatches(); len(matches) != 0 {
			t.Fatalf("tab %v offered completions for a p: token: %v", tab, matches)
		}
		// Assert on the preview's own marker, not on the token: "p:high" is in
		// the input field too, so looking for it would pass with no preview at
		// all — which is exactly how the first version of this test lied.
		body := ansi.Strip(m.View())
		if !strings.Contains(body, "→ p:") {
			t.Errorf("tab %v shows no parse preview while filtering:\n%s", tab, body)
		}
	}
}
