package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// lang_input_test.go pins the contract lang_input.go makes: on a translated
// screen the parser answers to the words that screen uses, and it never stops
// answering to English. The failure mode this guards is subtle — a keyword
// advertised in the help that silently drops into the task title, which looks
// like the app ignoring you.

// withLang runs fn with a language applied and restores English afterwards.
func withLang(t *testing.T, l language, fn func()) {
	t.Helper()
	applyLang(string(l))
	defer applyLang(string(langEN))
	fn()
}

// Every keyword in the table must resolve from its translated spelling — and
// from the space-free form, which is the only shape that survives inside a
// quick-add token.
func TestEveryKeywordResolvesInEveryLanguage(t *testing.T) {
	for _, l := range availableLanguages {
		if l == langEN {
			continue
		}
		withLang(t, l, func() {
			for _, word := range inputKeywords {
				local := strings.ToLower(tr(word))
				if local == word {
					t.Errorf("%s: %q has no translation, so it cannot be typed in that language", l, word)
					continue
				}
				if got := canonicalInputWord(local); got != word {
					t.Errorf("%s: %q resolved to %q, want %q", l, local, got, word)
				}
				squashed := strings.ReplaceAll(local, " ", "")
				if got := canonicalInputWord(squashed); got != word {
					t.Errorf("%s: space-free %q resolved to %q, want %q", l, squashed, got, word)
				}
			}
		})
	}
}

// An alias that happens to be another keyword's English spelling would quietly
// change what a token means. Nothing in the shipped tables may do that.
func TestNoAliasShadowsADifferentEnglishKeyword(t *testing.T) {
	english := map[string]bool{}
	for _, w := range inputKeywords {
		english[w] = true
	}
	for _, wd := range []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday,
		time.Thursday, time.Friday, time.Saturday} {
		english[strings.ToLower(wd.String())] = true
	}
	for _, l := range availableLanguages {
		for alias, canonical := range buildInputIndex(l) {
			if english[alias] && alias != canonical {
				t.Errorf("%s: %q is an alias for %q but is itself an English keyword", l, alias, canonical)
			}
		}
	}
}

// The whole point, end to end: the same quick-add line, written the way each
// language writes it, produces the same task.
func TestQuickAddSpeaksTheActiveLanguage(t *testing.T) {
	cases := map[language]string{
		langEN: "Fix the boiler due:tomorrow p:high size:small r:weekly",
		langDA: "Fix the boiler frist:imorgen p:høj størrelse:lille r:ugentligt",
		langDE: "Fix the boiler fällig:morgen p:hoch größe:klein r:wöchentlich",
	}
	tomorrow := startOfDay(time.Now()).AddDate(0, 0, 1)
	for l, line := range cases {
		withLang(t, l, func() {
			p := parseQuickAdd(line)
			if p.title != "Fix the boiler" {
				t.Errorf("%s: title = %q — a token fell into the title", l, p.title)
			}
			if !startOfDay(p.dueDate).Equal(tomorrow) {
				t.Errorf("%s: due = %v, want tomorrow", l, p.dueDate)
			}
			if p.priority != todo.PriorityHigh {
				t.Errorf("%s: priority = %v, want high", l, p.priority)
			}
			if p.size != todo.SizeSmall {
				t.Errorf("%s: size = %v, want small", l, p.size)
			}
			if p.recurrence != "weekly" {
				t.Errorf("%s: recurrence = %q, want the canonical English \"weekly\"", l, p.recurrence)
			}
		})
	}
}

// English is never revoked: a Danish or German install still takes the tokens
// its user's scripts, notes and muscle memory already hold.
func TestEnglishStillParsesUnderEveryLanguage(t *testing.T) {
	for _, l := range availableLanguages {
		withLang(t, l, func() {
			p := parseQuickAdd("Ship it due:tomorrow p:high s:l r:daily")
			if p.title != "Ship it" || p.dueDate.IsZero() ||
				p.priority != todo.PriorityHigh || p.size != todo.SizeLarge || p.recurrence != "daily" {
				t.Errorf("%s: English quick-add stopped working: %+v", l, p)
			}
			task := todo.New("overdue check")
			task.DueDate = time.Now().Add(-24 * time.Hour)
			if !compileSearch("overdue")(task) {
				t.Errorf("%s: the English \"overdue\" search keyword stopped matching", l)
			}
			if _, err := parseDueDate("next friday"); err != nil {
				t.Errorf("%s: English date words stopped parsing: %v", l, err)
			}
		})
	}
}

// Weekday names come from the calendar's own tables, so what the month grid
// prints is what `due:` takes.
func TestWeekdayNamesParseInTheActiveLanguage(t *testing.T) {
	for _, l := range availableLanguages {
		withLang(t, l, func() {
			for _, wd := range []time.Weekday{time.Monday, time.Wednesday, time.Sunday} {
				for _, form := range []string{
					strings.ToLower(localizedWeekday(wd)),
					strings.ToLower(localizedWeekdayShort(wd)),
				} {
					got, ok := parseWeekday(form)
					if !ok || got != wd {
						t.Errorf("%s: %q parsed as %v/%v, want %v", l, form, got, ok, wd)
					}
				}
				// "next <weekday>", in this language's word for "next".
				phrase := strings.ToLower(inputWord("next") + " " + localizedWeekday(wd))
				d, err := parseDueDate(phrase)
				if err != nil {
					t.Errorf("%s: %q did not parse: %v", l, phrase, err)
					continue
				}
				if d.Weekday() != wd {
					t.Errorf("%s: %q landed on %v, want %v", l, phrase, d.Weekday(), wd)
				}
			}
		})
	}
}

// The search grammar gets the same treatment as quick-add.
func TestSearchFiltersSpeakTheActiveLanguage(t *testing.T) {
	task := todo.New("Fix the boiler")
	task.Priority = todo.PriorityHigh
	task.DueDate = time.Now().Add(-24 * time.Hour)

	cases := map[language][]string{
		langEN: {"p:high", "overdue", "due:<tomorrow"},
		langDA: {"p:høj", "forfalden", "frist:<imorgen"},
		langDE: {"p:hoch", "überfällig", "fällig:<morgen"},
	}
	for l, queries := range cases {
		withLang(t, l, func() {
			for _, q := range queries {
				if !compileSearch(q)(task) {
					t.Errorf("%s: query %q did not match the task it describes", l, q)
				}
			}
		})
	}
}

// Localized input must not reach the stored data: a Danish install and a
// German one have to write the same bytes, or sync would need a translator.
func TestLocalizedInputStoresCanonicalEnglish(t *testing.T) {
	for l, line := range map[language]string{
		langDA: "x r:ugentligt",
		langDE: "x r:wöchentlich",
	} {
		withLang(t, l, func() {
			if got := parseQuickAdd(line).recurrence; got != "weekly" {
				t.Errorf("%s: stored recurrence %q, want \"weekly\"", l, got)
			}
		})
	}
}

// A hint is a promise about what will parse. These are assembled from the
// keyword helpers precisely so the promise stays true — this feeds each one
// back through the parser to prove it.
func TestHintsOnlyAdvertiseTokensThatParse(t *testing.T) {
	for _, l := range availableLanguages {
		withLang(t, l, func() {
			p := parseQuickAdd(quickAddHint())
			if p.dueDate.IsZero() {
				t.Errorf("%s: the quick-add hint advertises a due token that does not parse: %q", l, quickAddHint())
			}
			if p.priority != todo.PriorityHigh {
				t.Errorf("%s: the quick-add hint advertises a priority token that does not parse: %q", l, quickAddHint())
			}
			if p.size != todo.SizeLarge {
				t.Errorf("%s: the quick-add hint advertises a size token that does not parse: %q", l, quickAddHint())
			}
			if p.recurrence == "" {
				t.Errorf("%s: the quick-add hint advertises a recurrence token that does not parse: %q", l, quickAddHint())
			}
			if len(p.deps) != 1 {
				t.Errorf("%s: the quick-add hint advertises a dep token that does not parse: %q", l, quickAddHint())
			}

			// The filter placeholder's tokens live inside "Filter... (…)", so
			// pull them out by their sigil before feeding them back.
			for _, tok := range strings.Fields(strings.Trim(searchHint(), "()")) {
				if !strings.Contains(tok, ":") {
					continue
				}
				tok = strings.TrimRight(tok, ")")
				if strings.HasPrefix(canonicalInputToken(strings.ToLower(tok)), "p:") {
					if _, ok := parsePriorityFilter(strings.TrimPrefix(canonicalInputToken(strings.ToLower(tok)), "p:")); !ok {
						t.Errorf("%s: search hint advertises %q, which is not a priority filter", l, tok)
					}
					continue
				}
				spec := strings.TrimPrefix(canonicalInputToken(strings.ToLower(tok)), "due:")
				if _, ok := parseDueFilter(spec); !ok {
					t.Errorf("%s: search hint advertises %q, which is not a due filter", l, tok)
				}
			}
		})
	}
}

// The help overlay advertises the grammar. Whatever it prints in a language
// has to work in that language — that mismatch is the complaint this whole
// change answers.
func TestHelpAdvertisesTokensTheParserAccepts(t *testing.T) {
	m := modelWithTasks(t)
	task := todo.New("Fix the boiler")
	task.Priority = todo.PriorityHigh
	task.DueDate = time.Now().Add(-24 * time.Hour)

	for _, l := range availableLanguages {
		withLang(t, l, func() {
			body := strings.Join(m.helpBodyLines(), "\n")
			// Every documented token, in this language's spelling.
			for _, want := range []string{
				inputWord("due:") + inputToken("tomorrow"),
				"p:" + inputWord("high"),
				"r:" + inputWord("weekly"),
				inputWord("dep:") + "^",
				inputWord("overdue"),
				inputWord("today"),
				inputWord("next week"),
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: the help does not document %q", l, want)
				}
			}
			// And each of them parses.
			line := fmt.Sprintf("Boiler %s p:%s r:%s %s^",
				inputWord("due:")+inputToken("tomorrow"), inputWord("high"),
				inputWord("weekly"), inputWord("dep:"))
			p := parseQuickAdd(line)
			if p.dueDate.IsZero() || p.priority != todo.PriorityHigh || p.recurrence == "" || len(p.deps) != 1 {
				t.Errorf("%s: the help's own example did not parse: %q → %+v", l, line, p)
			}
			if !compileSearch(inputWord("overdue"))(task) {
				t.Errorf("%s: the help documents %q as a search keyword but it does not match",
					l, inputWord("overdue"))
			}
			for _, word := range []string{inputWord("today"), inputWord("tomorrow"), inputWord("next week"), inputWord("next month")} {
				if _, err := parseDueDate(word); err != nil {
					t.Errorf("%s: the help documents the date word %q but it does not parse: %v", l, word, err)
				}
			}
		})
	}
}
