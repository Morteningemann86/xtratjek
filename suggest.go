package main

import (
	"strings"
	"unicode"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/bubbles/textinput"
)

// suggest.go — inline completion for the #tag / @project tokens of the
// quick-add field. Quick-add is the fastest way into the app but the only one
// that expected you to remember your own vocabulary: the detail pane has
// pickers behind '#' and '@', the add field had nothing. Typing '#' here now
// offers the existing tags (most recently used first) and tab splices one in.
//
// The matching deliberately mirrors the detail-pane pickers
// (tagSearchResults / projSearchResults): case-insensitive substring, recency
// order. Prefix matches are floated to the front, because a completion popup
// is read left to right and the thing you started spelling should be first.

// quickAddToken returns the token the caret sits in when that token is a #tag
// or @project reference: the sigil, the text typed after it, and the token's
// rune bounds so an accepted completion can splice back in. ok is false for
// anything else, including a caret parked before a token (nothing typed yet
// means nothing to complete).
func quickAddToken(value string, pos int) (sigil, query string, start, end int, ok bool) {
	runes := []rune(value)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	start = pos
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if pos == start {
		return "", "", 0, 0, false
	}
	end = pos
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	tok := string(runes[start:end])
	if s := tok[:1]; s == "#" || s == "@" {
		return s, string([]rune(tok)[1:]), start, end, true
	}
	return "", "", 0, 0, false
}

// quickAddSuggestions returns the completion candidates for the token under
// the caret — existing tags for '#', existing projects for '@' — capped at
// maxQuickAddSuggestions. An empty query (a bare '#') lists the most recently
// used names, which is the point: it turns "what did I call that tag again?"
// into a glance.
func (m model) quickAddSuggestions(value string, pos int) (sigil string, matches []string) {
	sigil, query, _, _, ok := quickAddToken(value, pos)
	if !ok {
		return "", nil
	}
	q := strings.ToLower(query)

	var pool []string
	switch sigil {
	case "#":
		pool = append([]string(nil), m.getAllTagsSorted()...)
		sortByRecency(pool, m.cache.tagLastUsed)
		// A tag already spelled out elsewhere on the line is noise — but the
		// token being typed parses as a tag too, so it must not exclude itself.
		typed := todo.NormalizeTag(query)
		used := make(map[string]bool)
		for _, tag := range parseQuickAdd(value).tags {
			if tag != typed {
				used[tag] = true
			}
		}
		pool = filterOut(pool, used)
	case "@":
		pool = m.quickAddProjectPool()
	}

	var prefix, contains []string
	for _, name := range pool {
		lower := strings.ToLower(name)
		switch {
		case lower == q:
			continue // fully typed — the completion has nothing left to add
		case q == "" || strings.HasPrefix(lower, q):
			prefix = append(prefix, name)
		case strings.Contains(lower, q):
			contains = append(contains, name)
		}
	}
	matches = append(prefix, contains...)
	if len(matches) > maxQuickAddSuggestions {
		matches = matches[:maxQuickAddSuggestions]
	}
	return sigil, matches
}

// quickAddProjectPool is the project half of the candidate pool, most recently
// used first. Projects whose name carries whitespace are left out: quick-add
// tokenises on whitespace, so "@Home renovation" cannot round-trip through it
// — offering one would splice in a token that parses as project "Home" plus a
// stray title word. Those stay an affair for the detail pane's '@' picker.
func (m model) quickAddProjectPool() []string {
	out := make([]string, 0, len(m.cache.projectNames))
	for _, p := range m.cache.projectNames {
		if strings.ContainsFunc(p, unicode.IsSpace) {
			continue
		}
		out = append(out, p)
	}
	sortByRecency(out, m.cache.projLastUsed)
	return out
}

func filterOut(names []string, drop map[string]bool) []string {
	if len(drop) == 0 {
		return names
	}
	out := names[:0:0]
	for _, n := range names {
		if !drop[n] {
			out = append(out, n)
		}
	}
	return out
}

// acceptQuickAddSuggestion splices choice into value in place of the token
// under the caret, and returns the caret position just past it — with a
// trailing space added when one isn't already there, so the next token can be
// typed straight away.
func acceptQuickAddSuggestion(value string, pos int, choice string) (string, int, bool) {
	sigil, _, start, end, ok := quickAddToken(value, pos)
	if !ok || choice == "" {
		return value, pos, false
	}
	runes := []rune(value)
	token := sigil + choice
	tail := string(runes[end:])
	if !strings.HasPrefix(tail, " ") {
		token += " "
	}
	next := string(runes[:start]) + token + tail
	return next, start + len([]rune(token)), true
}

// searchUsesTokenGrammar reports whether the active tab runs the search query
// through compileSearch — the #tag / @project / p: / due: vocabulary — rather
// than matching a plain name substring.
//
// Tasks, Board and Stats all filter the same task set with the same grammar:
// the Board's columns are a projection of cache.active/cache.done, and
// statsScopedTodos compiles the query itself. The Projects tab is the odd one
// out, filtering project *names*, where "#home" would only ever match a
// project literally called that. So it gets neither the completions nor the
// parse preview — offering either would describe a filter it does not run.
func (m model) searchUsesTokenGrammar() bool {
	switch m.tab {
	case tabTasks, tabBoard, tabStats:
		return true
	}
	return false
}

// completionMatches is the live completion set for whichever field is on
// screen, and the gate for the keys that drive it. It returns nothing unless a
// field that takes completions is showing, so tab/↑/↓ keep their normal
// meaning in the detail pane's editors (which share modeInput) and on the tabs
// whose search is a name substring.
func (m model) completionMatches() (string, []string) {
	switch {
	case m.mode == modeInput && m.pane == paneList:
		return m.quickAddSuggestions(m.textInput.Value(), m.textInput.Position())
	case m.mode == modeSearch && m.searchUsesTokenGrammar():
		// The same vocabulary reads differently here — a search `#tag` matches
		// a substring rather than setting one — but the question the row
		// answers is identical: what did I call that tag again.
		return m.quickAddSuggestions(m.searchInput.Value(), m.searchInput.Position())
	}
	return "", nil
}

// applyCompletionKey handles the keys that drive the completion row against
// whichever input is showing it: tab splices the highlighted candidate in, ↑/↓
// move the highlight. Reports whether the key was consumed, so a caller can
// fall through to its own meaning when no completions are live.
func (m *model) applyCompletionKey(key string, in *textinput.Model, matches []string) bool {
	if len(matches) == 0 {
		return false
	}
	switch key {
	case "tab":
		// Enter deliberately keeps its own meaning at both call sites — submit
		// the task, apply the filter. A completion row must never stand
		// between the user and the thing they came to do.
		val, pos, ok := acceptQuickAddSuggestion(in.Value(), in.Position(), matches[m.suggestIndex(len(matches))])
		if ok {
			in.SetValue(val)
			in.SetCursor(pos)
			m.suggestCursor = 0
		}
		return true
	case "up", "down":
		step := 1
		if key == "up" {
			step = -1
		}
		m.suggestCursor = (m.suggestIndex(len(matches)) + step + len(matches)) % len(matches)
		return true
	}
	return false
}

// suggestIndex clamps the stored highlight against the live match count — the
// list shrinks as you type, and a stale index must degrade to a valid chip
// rather than panic.
func (m model) suggestIndex(n int) int {
	if n <= 0 {
		return 0
	}
	if m.suggestCursor < 0 || m.suggestCursor >= n {
		return 0
	}
	return m.suggestCursor
}
