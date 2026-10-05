package main

import (
	"fmt"
	"time"

	"github.com/Iliorn/tjek/todo"
)

// ── Localization ──────────────────────────────────────────────────────────────
//
// Translation follows the gettext convention: the English string is the lookup
// key, so call sites stay readable (tr("Settings")) and any string that lacks a
// translation simply falls back to its English source. Adding a new language is
// therefore just a new entry in `translations` plus its date-name tables — no
// call sites change. The active language is a package-level global, mirroring
// the theme pattern (applyTheme); rendering code reads it through the helpers
// below rather than receiving it as a parameter.

type language string

const (
	langEN language = "en"
	langDA language = "da"
	langDE language = "de"
)

// availableLanguages is the cycle order used by the settings toggle.
var availableLanguages = []language{langEN, langDA, langDE}

func (l language) displayName() string {
	switch l {
	case langDA:
		return "Dansk"
	case langDE:
		return "Deutsch"
	default:
		return "English"
	}
}

// activeLang is the language all rendering reads from. English is the source
// language, so it needs no translation table.
var activeLang = langEN

// applyLang sets the active language from a stored code, defaulting to English
// for empty or unknown values.
// It also rebuilds the parser's alias index (lang_input.go), so switching the
// interface language switches the words the quick-add and search grammars
// accept in the same step — the two cannot end up out of sync.
func applyLang(code string) {
	activeLang = langEN
	for _, l := range availableLanguages {
		if string(l) == code {
			activeLang = l
			break
		}
	}
	activeInputWords = buildInputIndex(activeLang)
}

// tr translates an English source string into the active language, falling back
// to the source itself when no translation exists. Format strings are translated
// by their template (e.g. tr("%d active")) and then fed to fmt.Sprintf.
func tr(s string) string {
	if activeLang == langEN {
		return s
	}
	if table, ok := translations[activeLang]; ok {
		if t, ok := table[s]; ok {
			return t
		}
	}
	return s
}

// trCount is fmt.Sprintf(tr(s), args...) for a template that counts n of
// something, in the form n agrees with: "1 day", "2 days"; "1 færdig",
// "2 færdige". The translation tables hold the plural, which is what most
// counts are; countOne holds a template's singular where a language's
// differs, English included, since tr hands English its key unchanged.
func trCount(s string, n int, args ...any) string {
	f := tr(s)
	if n == 1 {
		if one, ok := countOne[activeLang][s]; ok {
			f = one
		}
	}
	return fmt.Sprintf(f, args...)
}

var countOne = map[language]map[string]string{
	langEN: {
		"%d days overdue":                            "%d day overdue",
		"created %d days ago":                        "created %d day ago",
		"due in %d days; the ramp adds points daily": "due in %d day; the ramp adds points daily",
		"%s (%d entries)":                            "%s (%d entry)",
		"%d entries · %s":                            "%d entry · %s",
	},
	langDA: {
		"%d done":                     "%d færdig",
		"  ✓ %d done · %s shows them": "  ✓ %d færdig · %s viser dem",
		"%d open":                     "%d åben",
		"%d overdue":                  "%d forfalden",
		"%d days overdue":             "%d dag forfalden",
		"created %d days ago":         "oprettet for %d dag siden",
		"due in %d days; the ramp adds points daily": "forfalder om %d dag; rampen giver point hver dag",
		"%s (%d entries)": "%s (%d post)",
		"%d entries · %s": "%d post · %s",
	},
	langDE: {
		"%d days overdue":                            "%d Tag überfällig",
		"created %d days ago":                        "vor %d Tag erstellt",
		"due in %d days; the ramp adds points daily": "fällig in %d Tag; die Rampe gibt täglich Punkte",
		"%s (%d entries)":                            "%s (%d Eintrag)",
		"%d entries · %s":                            "%d Eintrag · %s",
	},
}

// ── Date names ────────────────────────────────────────────────────────────────
//
// Go's time package has no locale support, so the few date layouts the UI shows
// with month/weekday *names* are composed by hand from these tables. Purely
// numeric layouts ("02-01-06", "15:04", "02-01") need no translation.

var monthNames = map[language][12]string{
	langDA: {"Januar", "Februar", "Marts", "April", "Maj", "Juni",
		"Juli", "August", "September", "Oktober", "November", "December"},
	langDE: {"Januar", "Februar", "März", "April", "Mai", "Juni",
		"Juli", "August", "September", "Oktober", "November", "Dezember"},
}

var monthAbbrevs = map[language][12]string{
	langDA: {"Jan", "Feb", "Mar", "Apr", "Maj", "Jun",
		"Jul", "Aug", "Sep", "Okt", "Nov", "Dec"},
	langDE: {"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
		"Jul", "Aug", "Sep", "Okt", "Nov", "Dez"},
}

// Weekday tables are indexed by time.Weekday (Sunday = 0).
var weekdayNames = map[language][7]string{
	langDA: {"Søndag", "Mandag", "Tirsdag", "Onsdag", "Torsdag", "Fredag", "Lørdag"},
	langDE: {"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"},
}

var weekdayAbbrevs = map[language][7]string{
	langDA: {"Søn", "Man", "Tir", "Ons", "Tor", "Fre", "Lør"},
	langDE: {"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"},
}

var weekdayInitials = map[language][7]rune{
	langEN: {'S', 'M', 'T', 'W', 'T', 'F', 'S'},
	langDA: {'S', 'M', 'T', 'O', 'T', 'F', 'L'},
	langDE: {'S', 'M', 'D', 'M', 'D', 'F', 'S'},
}

// Short tab labels, used when the full ones do not fit the tab bar. A table
// and not tr() keys: tr maps one English string to one translation, and in
// English several tabs are short already ("1 Tasks" is its own abbreviation)
// while their Danish and German names are not, so the two tiers need separate
// entries. TestTabBarKeepsItsLabelsAcrossTabs checks every language's set is
// internally stable, so a longer word here fails the build instead of turning
// the bar into bare digits.
var tabShortLabels = map[language][numTabs]string{
	langEN: {"1 Tasks", "2 Cal", "3 Tags", "4 Proj", "5 Board", "6 Stats", "7 Setup", "8 Meet"},
	langDA: {"1 Opgaver", "2 Kal", "3 Mærk", "4 Proj", "5 Tavle", "6 Stat", "7 Indst", "8 Møde"},
	langDE: {"1 Aufg.", "2 Kal", "3 Tags", "4 Proj", "5 Board", "6 Stat.", "7 Einst.", "8 Meet."},
}

// Short task-list column headings. A column is as wide as the wider of its
// heading and its values (hugColW), so a heading much wider than what the
// column holds — Danish "Størrelse" over a single s/m/l — strands cells on
// every row and pushes Project off the list first. These are the headings
// that would, in the words each language abbreviates them with; everything
// else goes through tr. A table rather than tr keys for the reason the tab
// labels are one: the English heading is already short, and tr maps one
// English string to one translation, which the detail pane's full "Size"
// label needs.
var listHeaderShort = map[language]map[string]string{
	langDA: {"Size": "Str.", "Due": "Frist"},
	langDE: {"Size": "Gr.", "Tags": "Tags"},
}

// listHeader is the active language's heading for a task-list column.
func listHeader(english string) string {
	if s, ok := listHeaderShort[activeLang][english]; ok {
		return s
	}
	return tr(english)
}

// Monday-first two-letter column header for the month grid.
var weekdayHeader = map[language]string{
	langEN: "Mo Tu We Th Fr Sa Su",
	langDA: "Ma Ti On To Fr Lø Sø",
	langDE: "Mo Di Mi Do Fr Sa So",
}

// localizedMonthYear renders the calendar title ("January 2006" equivalent).
func localizedMonthYear(t time.Time) string {
	if activeLang == langEN {
		return t.Format("January 2006")
	}
	names := monthNames[activeLang]
	return names[int(t.Month())-1] + t.Format(" 2006")
}

// localizedDayDateAbbrev renders the timeline header ("Mon 02 Jan 2006" equiv).
func localizedDayDateAbbrev(t time.Time) string {
	if activeLang == langEN {
		return t.Format("Mon 02 Jan 2006")
	}
	wd := weekdayAbbrevs[activeLang][int(t.Weekday())]
	mon := monthAbbrevs[activeLang][int(t.Month())-1]
	return wd + t.Format(" 02 ") + mon + t.Format(" 2006")
}

// localizedWeekday returns the full weekday name (stats wide bars).
func localizedWeekday(wd time.Weekday) string {
	if activeLang == langEN {
		return wd.String()
	}
	return weekdayNames[activeLang][int(wd)]
}

// localizedWeekdayShort returns the 3-letter weekday abbreviation (stats bars).
func localizedWeekdayShort(wd time.Weekday) string {
	if activeLang == langEN {
		return wd.String()[:3]
	}
	return weekdayAbbrevs[activeLang][int(wd)]
}

// localizedWeekdayInitial returns the single-letter weekday label (narrow bars).
func localizedWeekdayInitial(wd time.Weekday) rune {
	if init, ok := weekdayInitials[activeLang]; ok {
		return init[int(wd)]
	}
	return weekdayInitials[langEN][int(wd)]
}

// localizedWeekdayHeader returns the Monday-first month-grid column header.
func localizedWeekdayHeader() string {
	if h, ok := weekdayHeader[activeLang]; ok {
		return h
	}
	return weekdayHeader[langEN]
}

// trPriority localizes a task priority word at the view layer; the todo domain
// package stays framework- and locale-free.
func trPriority(p todo.Priority) string {
	return tr(p.String())
}

// trSize localizes the size word for the same reason — the todo package keeps
// only the English source word.
func trSize(s todo.Size) string {
	return tr(s.String())
}

// trRecurrence renders a recurrence rule for display. Canonical rules
// (daily/weekly/monthly/yearly/weekdays) are translated; "every:Nd|w|m|y" is
// kept as-is, since the prefix is a recognizable English keyword and the
// number+unit is locale-neutral.
func trRecurrence(rule string) string {
	switch rule {
	case "daily", "weekly", "monthly", "yearly", "weekdays":
		return tr(rule)
	}
	return rule
}

// ── Translation tables ──────────────────────────────────────────────────────
//
// Keyed by English source string. Keep entries grouped by where they appear so
// new strings are easy to slot in. Missing keys fall back to English.

var translations = map[language]map[string]string{
	langDA: daTranslations,
	langDE: deTranslations,
}

var daTranslations = map[string]string{
	// Header / chrome
	"? shortcuts":                              "? genveje",
	"Type a command…":                          "Skriv en kommando…",
	"No command matches that.":                 "Ingen kommando matcher det.",
	"command palette: find any action by name": "kommandopalet: find enhver handling ved navn",
	"Go to Tasks":                              "Gå til Opgaver",
	"Go to Calendar":                           "Gå til Kalender",
	"Go to Projects":                           "Gå til Projekter",
	"Go to Tags":                               "Gå til Mærker",
	"Go to Board":                              "Gå til Tavle",
	"Go to Stats":                              "Gå til Statistik",
	"Go to Settings":                           "Gå til Indstillinger",
	"FOCUS: today + overdue only (f to toggle)": "FOKUS: kun i dag + forfaldne (f for at skifte)",
	"(untagged)": "(uden mærke)",

	// Tab labels (number prefix kept; only the word is translated)
	"1 Tasks":    "1 Opgaver",
	"2 Calendar": "2 Kalender",
	"3 Tags":     "3 Mærker",
	"4 Projects": "4 Projekter",
	"5 Board":    "5 Tavle",
	"6 Stats":    "6 Statistik",
	"7 Settings": "7 Indstillinger",
	"8 Meetings": "8 Møder",

	// Key hints (footer)

	// Footer / timer
	"#tag @project %s p:%s s:l r:%s %s^": "#mærke @projekt %s p:%s s:l r:%s %s^",
	" · t to stop":                       " · t for at stoppe",
	"create new tag: ":                   "opret nyt mærke: ",
	"create new project: ":               "opret nyt projekt: ",

	// Help screen
	"Keyboard shortcuts":                          "Tastaturgenveje",
	"Press ? or esc to close":                     "Tryk ? eller esc for at lukke",
	"/ filter  ·  ? or esc to close":              "/ filtrér  ·  ? eller esc for at lukke",
	"type to filter  ·  enter keep  ·  esc clear": "skriv for at filtrere  ·  enter behold  ·  esc ryd",
	"/ filter  ·  esc clear  ·  ? to close":       "/ filtrér  ·  esc ryd  ·  ? for at lukke",
	"No shortcut matches that.":                   "Ingen genvej matcher det.",
	"Quick-add syntax":                            "Hurtig-tilføj syntaks",
	"Filters":                                     "Filtre",
	"Navigation":                                  "Navigation",
	"navigate list":                               "navigér liste",
	"open details":                                "åbn detaljer",
	"go back":                                     "gå tilbage",
	"switch tabs (forward / back / direct)":       "skift faneblad (frem / tilbage / direkte)",
	"Board":                                       "Tavle",
	"focus previous/next column":                  "fokusér forrige/næste kolonne",
	"move card between stages (into Done completes it)": "flyt kort mellem faser (ind i Færdige afslutter)",
	"pick up card to move it":                           "løft kort for at flytte det",
	"←/→ carry to a column · enter/esc put it down":     "←/→ bær til en kolonne · enter/esc sæt det ned",
	"filter cards (#tag, @project, text)":               "filtrér kort (#mærke, @projekt, tekst)",
	"show card details":                                 "vis kortets detaljer",
	"new card in this column":                           "nyt kort i denne kolonne",
	"Card":                                              "Kort",
	"empty":                                             "tom",
	"more":                                              "flere",
	"close help":                                        "luk hjælp",
	"Tasks":                                             "Opgaver",
	"Workflow":                                          "Arbejdsgang",
	"Summary":                                           "Overblik",
	"Preferences":                                       "Præferencer",
	"Sequencer":                                         "Sekventering",
	"Overview":                                          "Oversigt",
	"History":                                           "Historik",
	"Activity":                                          "Aktivitet",
	"add task (quick-add: #tag due:date p:high @proj s:M)": "tilføj opgave (hurtig: #mærke frist:dato p:høj @proj s:M)",
	"rename task":                      "omdøb opgave",
	"toggle done":                      "skift færdig",
	"start/stop time tracking":         "start/stop tidsregistrering",
	"cycle priority low/med/high":      "skift prioritet lav/mellem/høj",
	"delete":                           "slet",
	"edit description (opens $EDITOR)": "rediger beskrivelse (åbner $EDITOR)",
	"ctrl+e  edit in $EDITOR":          "ctrl+e  rediger i $EDITOR",
	"focus: today + overdue only":      "fokus: kun i dag + forfaldne",
	"toggle history":                   "skift historik",
	"cycle sort order":                 "skift sorteringsrækkefølge",
	"why this rank":                    "hvorfor denne placering",
	"why this rank: the score, its causes, what moves it": "hvorfor denne placering: scoren, årsagerne, hvad der flytter den",
	"expand/collapse subtasks":                            "fold delopgaver ud/ind",
	"search":                                              "søg",
	"Detail view":                                         "Detaljevisning",
	"jump section":                                        "hop til sektion",
	"edit field / open subtask":                           "rediger felt / åbn delopgave",
	"add tag / dep / comment / subtask":                   "tilføj mærke / afh. / kommentar / delopgave",
	"quick add tag":                                       "tilføj hurtigt mærke",
	"quick add / change project":                          "tilføj / skift projekt hurtigt",
	"toggle subtask done":                                 "skift delopgave færdig",
	"rename subtask / edit time entry":                    "omdøb delopgave / rediger tidsregistrering",
	"remove field / delete subtask":                       "fjern felt / slet delopgave",
	"Tags & Projects":                                     "Mærker & Projekter",
	"Inside a tag / project":                              "Inde i et mærke / projekt",
	"open the tasks in it":                                "åbn opgaverne i det",
	"new task in it":                                      "ny opgave i det",
	"show its tasks on the Tasks tab":                     "vis opgaverne på Opgaver-fanen",
	"delete task":                                         "slet opgave",
	"back to the list":                                    "tilbage til listen",
	"rename globally":                                     "omdøb globalt",
	"delete globally":                                     "slet globalt",
	"filter":                                              "filtrér",
	"sort date/alpha":                                     "sortér dato/alfabetisk",
	"Calendar (tab 2)":                                    "Kalender (fane 2)",
	"move by day / week":                                  "flyt med dag / uge",
	"previous / next month":                               "forrige / næste måned",
	"jump to today":                                       "hop til i dag",
	"focus the day's entries":                             "fokusér dagens poster",
	"edit entry times (09:12-10:00 or 45m)":               "rediger posttider (09:12-10:00 eller 45m)",
	"delete selected entry":                               "slet valgt post",
	"Stats (tab 6)":                                       "Statistik (fane 6)",
	"switch to stats view":                                "skift til statistik",
	"Settings (tab 7)":                                    "Indstillinger (fane 7)",
	"select setting":                                      "vælg indstilling",
	"change theme":                                        "skift tema",
	"activate / edit the selected setting":                "aktivér / redigér den valgte indstilling",
	"confirm update when one is offered":                  "bekræft opdatering når en tilbydes",
	"App":                                                 "App",
	"undo last change":                                    "fortryd sidste ændring",
	"keyboard shortcuts and help":                         "tastaturgenveje og hjælp",
	"quit":                                                "afslut",
	"Date input":                                          "Datoindtastning",
	"exact date (e.g. 15-06-25)":                          "præcis dato (f.eks. 15-06-25)",
	"today's date":                                        "dagens dato",
	"tomorrow":                                            "i morgen",
	"7 days from now":                                     "7 dage fra nu",
	"1 month from now":                                    "1 måned fra nu",
	"next occurrence of weekday":                          "næste forekomst af ugedag",
	"relative days/weeks/months":                          "relative dage/uger/måneder",

	// Stats detail
	"Last 30 days":                  "Sidste 30 dage",
	"Last 26 weeks":                 "Sidste 26 uger",
	"Last 7 days":                   "Sidste 7 dage",
	"%d done":                       "%d færdige",
	"No completions in this range.": "Ingen afsluttede i denne periode.",
	"1 block = 1 completed task":    "1 blok = 1 afsluttet opgave",

	// Stats list — sections & labels
	"  Workload":            "  Arbejdsbyrde",
	"Overdue":               "Forfaldne",
	"Due today":             "Forfalder i dag",
	"Due this week":         "Forfalder denne uge",
	"Active total":          "Aktive i alt",
	"Seq hit (top-5)":       "Sekvens-hit (top-5)",
	"Created":               "Oprettet",
	"Completed":             "Afsluttet",
	"  Net backlog":         "  Netto-efterslæb",
	"+%d ▲ growing":         "+%d ▲ vokser",
	"%d ▼ shrinking":        "%d ▼ skrumper",
	"±0 → steady":           "±0 → stabil",
	"%d done vs %d  %s":     "%d færdige mod %d  %s",
	"Flow (last 7 days)":    "Flow (sidste 7 dage)",
	"vs last week":          "mod sidste uge",
	"Flow (last 30 days)":   "Flow (sidste 30 dage)",
	"vs prior 30d":          "mod forrige 30d",
	"  Throughput":          "  Gennemløb",
	"  Time to done (30d)":  "  Færdigtid (30d)",
	"median ":               "median ",
	"none yet":              "ingen endnu",
	"  Median active age":   "  Median aktiv alder",
	"  Oldest active":       "  Ældste aktive",
	"  Active by priority":  "  Aktive efter prioritet",
	"↑ High":                "↑ Høj",
	"→ Medium":              "→ Mellem",
	"↓ Low":                 "↓ Lav",
	"  Completion velocity": "  Afslutningshastighed",
	"Today":                 "I dag",
	"today":                 "i dag", // tasks-list Due column (formatDueShort)
	"This week":             "Denne uge",
	"This month":            "Denne måned",
	"  Avg (7d)":            "  Gns. (7d)",
	"%.1f tasks/day":        "%.1f opgaver/dag",

	// Priority, size, and recurrence words
	"high":     "høj",
	"medium":   "mellem",
	"low":      "lav",
	"small":    "lille",
	"large":    "stor",
	"daily":    "dagligt",
	"weekly":   "ugentligt",
	"monthly":  "månedligt",
	"yearly":   "årligt",
	"weekdays": "hverdage",

	// ── Parser keywords (lang_input.go) ──
	// These double as input: the word shown here is the word the quick-add and
	// search grammars accept, alongside the English one. Changing a spelling
	// changes what parses, so keep them to words a Danish user would type.
	"yesterday":  "i går",
	"next week":  "næste uge",
	"next month": "næste måned",
	"next":       "næste",
	"due:":       "frist:",
	"size:":      "størrelse:",
	"recur:":     "gentag:",
	"dep:":       "afh:",

	// List headers / sort
	"Completed tasks":   "Afsluttede opgaver",
	">Completed tasks<": ">Afsluttede opgaver<",
	">Completed<":       ">Afsluttet<",
	"Start":             "Start",
	"Due":               "Forfald",
	"Priority":          "Prioritet",
	">Due<":             ">Forfald<",
	">Start<":           ">Start<",
	">Priority<":        ">Prioritet<",
	"Tags":              "Mærker",
	"sort:":             "sortér:",

	// Tag list / detail
	"  No tags match your filter.":                         "  Ingen mærker matcher dit filter.",
	"  No tags yet. Add tags to tasks in the detail view.": "  Ingen mærker endnu. Tilføj mærker til opgaver i detaljevisningen.",
	"Age":                           "Alder",
	"  No tag selected.":            "  Intet mærke valgt.",
	"  (untagged)":                  "  (uden mærke)",
	"%d open":                       "%d åbne",
	"  %s shows the finished ones.": "  %s viser de færdige.",
	"  Every project is finished.":  "  Alle projekter er færdige.",
	"  Every tag is finished.":      "  Alle mærker er færdige.",
	"  Nothing open here.":          "  Intet åbent her.",
	"  Tags group related tasks; this tab shows what is open in each.": "  Mærker samler relaterede opgaver; fanen viser, hvad der er åbent i hvert.",
	"  ✓ %d done · %s shows them":                                      "  ✓ %d færdige · %s viser dem",
	"Last":                                                             "Senest",
	"Next up":                                                          "Næste",
	"Open":                                                             "Åbne",
	"[+%d finished]":                                                   "[+%d færdige]",
	"open":                                                             "åbne",
	"show / hide done tasks":                                           "vis / skjul færdige opgaver",
	"show / hide finished":                                             "vis / skjul færdige",
	"  often with: ":                                                   "  ofte med: ",
	"  … and %d more":                                                  "  … og %d mere",

	"Date":           "Dato",
	"Source task:  ": "Kildeopgave:  ",
	"[done]":         "[færdig]",
	"[task removed]": "[opgave fjernet]",
	"Date:         ": "Dato:         ",
	"Tags:         ": "Mærker:       ",
	"none":           "ingen",

	// Task list empty states
	"  No tasks match the filter.":           "  Ingen opgaver matcher filteret.",
	"  No tasks due today or overdue. Nice!": "  Ingen opgaver forfalder i dag eller er forfaldne. Flot!",
	"  No tasks yet. Press 'a' to add one.":  "  Ingen opgaver endnu. Tryk 'a' for at tilføje en.",
	"  Try:  ":                               "  Prøv:  ",
	// Free text (title/tag/project) is localized; the quick-add keywords
	// due:/friday/p:high stay English because the parser only accepts English.
	"Buy milk #shopping due:friday p:high @home": "Køb mælk #indkøb due:friday p:high @hjem",
	"  Press ? for all keyboard shortcuts.":      "  Tryk ? for alle tastaturgenveje.",
	"  No completed tasks match the filter.":     "  Ingen afsluttede opgaver matcher filteret.",
	"  No completed tasks yet.":                  "  Ingen afsluttede opgaver endnu.",

	// Projects
	"  No projects match the filter.":                           "  Ingen projekter matcher filteret.",
	"  No projects yet. Add a project to a task first.":         "  Ingen projekter endnu. Tilføj et projekt til en opgave først.",
	"  A project groups its tasks into a timeline on this tab.": "  Et projekt samler dets opgaver i en tidslinje på denne fane.",
	"Project":                     "Projekt",
	"Done":                        "Færdige",
	"  Timeline":                  "  Tidslinje",
	"today:":                      "i dag:",
	"  No tasks in this project.": "  Ingen opgaver i dette projekt.",
	"%d active":                   "%d aktive",
	"%d overdue":                  "%d forfaldne", // "%d done" shared with the stats section above

	// Detail pages
	"not set":                            "ikke sat",
	" ! overdue":                         " ! forfalden",
	"Start date":                         "Startdato",
	"Due date":                           "Forfaldsdato",
	"Recurrence":                         "Gentagelse",
	"Size":                               "Størrelse",
	"Stage":                              "Fase",
	"Description":                        "Beskrivelse",
	"none (press enter or 'n' to edit)":  "ingen (tryk enter eller 'n' for at redigere)",
	"Created:":                           "Oprettet:",
	"Modified:":                          "Ændret:",
	"%s (%d entries)":                    "%s (%d poster)",
	" ◉ tracking":                        " ◉ registrerer",
	"Time spent:":                        "Tid brugt:",
	"Completed on":                       "Afsluttet",
	"Tags:":                              "Mærker:",
	"No tags. Press 'a' to add one.":     "Ingen mærker. Tryk 'a' for at tilføje et.",
	"  Closed today (%d)":                "  Lukket i dag (%d)",
	"stop":                               "stop",
	"reopen":                             "genåbn",
	"  ↑ %d more":                        "  ↑ %d mere",
	"  ↓ %d more":                        "  ↓ %d mere",
	"Fields":                             "Felter",
	"Subtasks":                           "Delopgaver",
	"Dependencies":                       "Afhængigheder",
	"Time":                               "Tid",
	"Comments":                           "Kommentarer",
	"Subtasks:":                          "Delopgaver:",
	"No subtasks. Press 'a' to add one.": "Ingen delopgaver. Tryk 'a' for at tilføje en.",
	"%s[?] unknown subtask":              "%s[?] ukendt delopgave",
	"Dependencies:":                      "Afhængigheder:",
	"Inbound dependency: remove it from the other task": "Indgående afhængighed: fjern den fra den anden opgave",
	"A subtask can't outrank its parent":                "En delopgave kan ikke have højere prioritet end sin forældreopgave",
	"No dependencies. Press 'a' to add one.":            "Ingen afhængigheder. Tryk 'a' for at tilføje en.",
	"%s[?] unknown task":                                "%s[?] ukendt opgave",
	"Comments:":                                         "Kommentarer:",
	"No comments yet. Press 'a' to add one.":            "Ingen kommentarer endnu. Tryk 'a' for at tilføje en.",
	"Time entries:":                                     "Tidsregistreringer:",
	"No time entries. Press 'T' to add one.":            "Ingen tidsregistreringer. Tryk 'T' for at tilføje en.",

	// Calendar
	"Month ":                     "Måned ",
	"Day ":                       "Dag ",
	"%d entries · %s":            "%d poster · %s",
	"1 entry · ":                 "1 post · ",
	"  No activity on this day.": "  Ingen aktivitet på denne dag.",
	"  Press t on a task (tab 1) to start tracking.": "  Tryk t på en opgave (fane 1) for at starte registrering.",
	" now ": " nu ",

	// Settings
	"Theme":                          "Tema",
	"Language":                       "Sprog",
	"tab insert · ↑/↓ pick":          "tab indsæt · ↑/↓ vælg",
	"Board columns":                  "Tavlekolonner",
	"Board columns, comma-separated": "Tavlekolonner, adskilt af komma",
	"Comma-separated column names · [x] before a name gives it an icon · the last holds completed tasks": "Kolonnenavne adskilt af komma · [x] foran et navn giver et ikon · den sidste rummer færdige opgaver",
	"Version":              "Version",
	"Check for updates":    "Søg efter opdateringer",
	"press enter to check": "tryk enter",
	"Settings":             "Indstillinger",

	// Update / status / errors
	"Update failed":                    "Opdatering mislykkedes",
	"Updated! Restart tjek to apply.":  "Opdateret! Genstart tjek for at anvende.",
	"Error saving settings: %v":        "Kunne ikke gemme indstillinger: %v",
	"Updated; restart to apply":        "Opdateret; genstart for at anvende",
	"Check failed":                     "Søgning mislykkedes",
	"Up to date (":                     "Opdateret (",
	"Latest release: ":                 "Seneste udgivelse: ",
	"; this is a local build (":        "; dette er en lokal bygning (",
	"Update available: ":               "Opdatering tilgængelig: ",
	"Update available: %s. Run `%s`":   "Opdatering tilgængelig: %s. Kør `%s`",
	" is available. Update now? (y/n)": " er tilgængelig. Opdatér nu? (y/n)",
	"Checking…":                        "Søger…",
	"Updating…":                        "Opdaterer…",
	"Nothing to undo":                  "Intet at fortryde",
	"Undid: %s":                        "Fortrød: %s",
	"No editor found. Set EDITOR permanently, e.g: setx EDITOR notepad (then restart tjek)":                                "Ingen editor fundet. Sæt EDITOR permanent, f.eks: setx EDITOR notepad (genstart derefter tjek)",
	"No editor found. Set $EDITOR permanently, e.g: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish": "Ingen editor fundet. Sæt $EDITOR permanent, f.eks: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish",
	"Editor failed; falling back to notepad":                "Editor fejlede; falder tilbage til notepad",
	"Invalid date - use dd-mm-yy, %q, %q, %q, %q, or '+3d'": "Ugyldig dato - brug dd-mm-yy, %q, %q, %q, %q eller '+3d'",
	"Dependency not linked":                                 "Afhængighed ikke koblet",
	"Remove project '%s' from ALL its tasks? (y/n)":         "Fjern projektet '%s' fra ALLE dets opgaver? (j/n)",

	// Confirm prompts
	"Delete '%s'? (y/n)":                        "Slet '%s'? (y/n)",
	"Delete tag '#%s' from ALL tasks? (y/n)":    "Slet mærke '#%s' fra ALLE opgaver? (y/n)",
	"Delete this comment? (y/n)":                "Slet denne kommentar? (y/n)",
	"Remove project '%s' from this task? (y/n)": "Fjern projekt '%s' fra denne opgave? (y/n)",
	"Remove tag '#%s' from this task? (y/n)":    "Fjern mærke '#%s' fra denne opgave? (y/n)",
	"Remove this dependency? (y/n)":             "Fjern denne afhængighed? (y/n)",
	"Delete subtask '%s'? (y/n)":                "Slet delopgave '%s'? (y/n)",
	"Delete %s entry for '%s'? (y/n)":           "Slet %s-post for '%s'? (y/n)",

	// Input placeholders
	"Filter... (#tag @project p:%s %s<%s)":    "Filtrér... (#mærke @projekt p:%s %s<%s)",
	"Search for task to add as dependency...": "Søg efter opgave at tilføje som afhængighed...",
	"Search or create tag...":                 "Søg eller opret mærke...",
	"Search or create project...":             "Søg eller opret projekt...",
	"Filter tags...":                          "Filtrér mærker...",

	// Inline edit/add placeholders (set when a text-entry mode opens)
	"New task...": "Ny opgave...",
	"HH:MM-HH:MM or duration (45m, 1h30m)...": "TT:MM-TT:MM eller varighed (45m, 1t30m)...",
	"Edit tag name...":                        "Rediger mærkenavn...",
	"Edit task title...":                      "Rediger opgavetitel...",
	"Add comment...":                          "Tilføj kommentar...",
	"Add subtask...":                          "Tilføj delopgave...",
	"Edit comment...":                         "Rediger kommentar...",
	"Start date (dd-mm-yy, 'today', 'next week', '+3d')...": "Startdato (dd-mm-yy, 'today', 'next week', '+3d')...",
	"Completed (dd-mm-yy hh:mm, 'today', 'yesterday')...":   "Afsluttet (dd-mm-yy tt:mm, 'today', 'yesterday')...",
	"Completion can't be in the future":                     "Afslutning kan ikke ligge i fremtiden",
	"Due date (dd-mm-yy, 'today', 'next week', '+3d')...":   "Forfaldsdato (dd-mm-yy, 'today', 'next week', '+3d')...",

	// ── Settings group headings ──
	"Appearance": "Udseende",
	"General":    "Generelt",
	"About":      "Om",

	// Row labels that sit under a heading naming the same thing.
	"Automatic":           "Automatisk",
	"Enabled":             "Aktiveret",
	"Share board columns": "Del tavlens kolonner",

	// ── Sync + server (Settings rows, status line, toasts) ──
	"Sync":                            "Synkronisering",
	"Sync server":                     "Synk.server",
	"Sync token":                      "Synk.token",
	"Sync now":                        "Synkronisér nu",
	"Server":                          "Server",
	"Listen":                          "Lytteadresse",
	"Server token":                    "Servertoken",
	"press enter to sync":             "tryk enter",
	"needs server":                    "kræver server",
	"external":                        "ekstern",
	"set":                             "sat",
	"On":                              "Til",
	"Off":                             "Fra",
	"Syncing…":                        "Synkroniserer…",
	"Server stopped":                  "Server stoppet",
	"Server: ":                        "Server: ",
	"Serving on ":                     "Betjener på ",
	"✕ sync":                          "✕ synk.",
	"Set a server token first":        "Sæt en servertoken først",
	"Set sync server + token first":   "Sæt synk.server + token først",
	"Last sync failed: ":              "Seneste synk. mislykkedes: ",
	"Last sync: sent %d, received %d": "Seneste synk.: sendt %d, modtaget %d",
	"Sync failing: devices may be diverging (see Settings)":                       "Synk. mislykkes: enhederne kan være ved at glide fra hinanden (se Indstillinger)",
	"Sync: %d conflict(s) resolved; tjek sync --recover lists them":               "Synk.: %d konflikt(er) løst; tjek sync --recover viser dem",
	"Sync server URL, e.g. http://100.x.y.z:8765":                                 "URL til synk.server, fx http://100.x.y.z:8765",
	"Sync token (clear the field to remove it)":                                   "Synk.token (ryd feltet for at fjerne den)",
	"Server token clients must present (ctrl+g generates one · blank removes it)": "Servertoken som klienter skal vise (ctrl+g genererer et · tomt felt fjerner det)",
	"Could not generate a token: %v":                                              "Kunne ikke generere et token: %v",
	"Too weak for a server token; press ctrl+g to generate a strong one":          "For svagt til et servertoken; tryk ctrl+g for at generere et stærkt",
	"weak token; ctrl+g on this row generates a strong one":                       "svagt token; ctrl+g på denne række genererer et stærkt",
	"Bind address, e.g. 100.x.y.z:8765 or 127.0.0.1:8765":                         "Bind-adresse, fx 100.x.y.z:8765 eller 127.0.0.1:8765",
	"Plain http to a public host: token travels unencrypted":                      "Almindelig http til en offentlig vært: token sendes ukrypteret",

	// ── AI & Meetings (Settings) ──
	"AI & Meetings":       "AI & Møder",
	"AI provider":         "AI-udbyder",
	"Anthropic API key":   "Anthropic API-nøgle",
	"OpenAI API key":      "OpenAI API-nøgle",
	"Gemini API key":      "Gemini API-nøgle",
	"Microphone override": "Mikrofonoverstyring",
	"Anthropic API key (clear the field to remove it)":                        "Anthropic API-nøgle (ryd feltet for at fjerne den)",
	"Gemini API key (clear the field to remove it)":                           "Gemini API-nøgle (ryd feltet for at fjerne den)",
	"OpenAI API key — also needed to transcribe recordings (clear to remove)": "OpenAI API-nøgle — kræves også til transskribering af optagelser (ryd for at fjerne)",
	"Microphone override: format:input, e.g. dshow:audio=Microphone":          "Mikrofonoverstyring: format:input, fx dshow:audio=Microphone",
	"platform default": "platformens standard",

	// ── Meetings tab ──
	"Meetings":                      "Møder",
	"Meeting":                       "Møde",
	"Meeting title...":              "Mødetitel...",
	"Meeting added":                 "Møde tilføjet",
	"  No meetings yet.":            "  Ingen møder endnu.",
	"  Press %s to add one.":        "  Tryk %s for at tilføje et.",
	"  Meeting not found.":          "  Møde ikke fundet.",
	"Attendees: ":                   "Deltagere: ",
	"Date: ":                        "Dato: ",
	"Recording: ":                   "Optagelse: ",
	"No transcript yet.":            "Ingen transskription endnu.",
	"Transcript":                    "Transskription",
	"Action items":                  "Handlingspunkter",
	"draft":                         "udkast",
	"recording":                     "optager",
	"transcribing…":                 "transskriberer…",
	"summarizing…":                  "opsummerer…",
	"ready for review":              "klar til gennemgang",
	"reviewed":                      "gennemgået",
	"error":                         "fejl",
	"Recording… press r to stop":    "Optager… tryk r for at stoppe",
	"Stopping recording…":           "Stopper optagelse…",
	"Could not start recording: %v": "Kunne ikke starte optagelse: %v",
	"Recording error: %v":           "Optagelsesfejl: %v",
	"Recording saved. Add an OpenAI API key in Settings to transcribe it, or press n to type notes.": "Optagelse gemt. Tilføj en OpenAI API-nøgle i Indstillinger for at transskribere den, eller tryk n for at skrive noter.",
	"Transcription failed: %v":                  "Transskription mislykkedes: %v",
	"Summarizing and looking for action items…": "Opsummerer og leder efter handlingspunkter…",
	"AI pass failed: %v":                        "AI-gennemgang mislykkedes: %v",
	"Found %d action item(s) to review":         "Fandt %d handlingspunkt(er) til gennemgang",
	"Added task: %s":                            "Tilføjede opgave: %s",
	"accept action item":                        "accepter handlingspunkt",
	"Delete meeting '%s'? (y/n)":                "Slet møde '%s'? (y/n)",
	"Error saving meeting: %v":                  "Fejl ved lagring af møde: %v",
	"Error deleting meeting: %v":                "Fejl ved sletning af møde: %v",
	"Error saving action items: %v":             "Fejl ved lagring af handlingspunkter: %v",
	"Error saving review: %v":                   "Fejl ved lagring af gennemgang: %v",
	"Error saving edit: %v":                     "Fejl ved lagring af ændring: %v",
	"Error writing transcript file: %v":         "Fejl ved skrivning af transskriptionsfil: %v",
	"Edit action item (quick-add syntax)...":    "Rediger handlingspunkt (hurtig-tilføj-syntaks)...",
	// Meetings keymap descriptions (keymap.go)
	"open meeting":                                    "åbn møde",
	"add meeting":                                     "tilføj møde",
	"start/stop recording":                            "start/stop optagelse",
	"edit transcript (opens $EDITOR)":                 "rediger transskription (åbner $EDITOR)",
	"summarize + find action items":                   "opsummer + find handlingspunkter",
	"delete meeting":                                  "slet møde",
	"move between action items":                       "flyt mellem handlingspunkter",
	"accept the focused action item → creates a task": "accepter det fokuserede handlingspunkt → opretter en opgave",
	"reject the focused action item":                  "afvis det fokuserede handlingspunkt",
	"edit the focused action item (quick-add syntax)": "rediger det fokuserede handlingspunkt (hurtig-tilføj-syntaks)",

	// ── Sequencer / Settings rows ──
	"Deadline pressure":         "Deadlinepres",
	"Priority focus":            "Prioritetsfokus",
	"Momentum bias":             "Momentumvægt",
	"Aging increases score":     "Alder øger scoren",
	"Auto-close parent":         "Luk forælder automatisk",
	"Auto-close subtasks":       "Luk delopgaver automatisk",
	"Daily reminder":            "Daglig påmindelse",
	"Reminder time":             "Påmindelse kl.",
	"Subtasks copy tags":        "Delopgaver arver mærker",
	"Kanban board":              "Kanban-tavle",
	"Detail pane":               "Detaljerude",
	"Right":                     "Højre",
	"Left":                      "Venstre",
	"Bottom":                    "Bund",
	"Top 5 with these weights:": "Top 5 med disse vægte:",
	"Score resync failed: %v":   "Genberegning af score mislykkedes: %v",

	// ── Settings: export and import ──
	"Export":             "Eksport",
	"Auto-export folder": "Mappe til autoeksport",
	"Import from file":   "Importér fra fil",
	"its board column's icon, once columns have icons (ST column)": "tavlekolonnens ikon, når kolonnerne har ikoner (ST-kolonnen)",
	"choose a file": "vælg en fil",
	"✓ is kept for the last column; pick another icon":          "✓ er forbeholdt sidste kolonne; vælg et andet ikon",
	"An icon is one character wide; %s is not":                  "Et ikon fylder ét tegn; %s gør ikke",
	"The last column keeps ✓; its icon %s was left out":         "Sidste kolonne beholder ✓; ikonet %s blev udeladt",
	"Path to a tjek export (.json)":                             "Sti til en tjek-eksport (.json)",
	"Auto-export off":                                           "Autoeksport slået fra",
	"Not a folder: %s":                                          "Ikke en mappe: %s",
	"Exporting to %s":                                           "Eksporterer til %s",
	"Import failed: %v":                                         "Import mislykkedes: %v",
	"Auto-export failed: %v":                                    "Autoeksport mislykkedes: %v",
	"Folder to keep tjek-export.json in (blank turns it off)":   "Mappe til tjek-export.json (tom slår den fra)",
	"Nothing to import: every task in the file is already here": "Intet at importere: alle opgaver i filen er her allerede",
	"Imported %d new, %d updated · u undoes it":                 "Importeret %d nye, %d opdaterede · u fortryder",
	"tab completes the name · enter confirms · esc cancels":     "tab fuldfører navnet · enter bekræfter · esc annullerer",

	// ── Adding tasks ──
	"A task needs a title": "En opgave skal have en titel",

	// ── Calendar: coming up ──
	"Coming up":   "Kommende",
	"nothing due": "intet forfalder",

	// ── Reminders ──
	"%d due today":               "%d forfalder i dag",
	"desktop pop-up unavailable": "skrivebords-pop-up utilgængelig",

	// ── List header / panel titles ──
	"Active tasks":        "Aktive opgaver",
	"Detail":              "Detalje",
	"Tag":                 "Mærke",
	"  No task selected.": "  Ingen opgave valgt.",

	// ── Sort labels (drawn in the panel border, keep them short) ──
	"alpha":     "alfa",
	"completed": "afsluttet",
	"due":       "forfald",
	"size":      "størrelse",
	"count":     "antal",
	"recent":    "nyeste",

	// ── Quick-add preview ──
	"(no title yet)":                        "(ingen titel endnu)",
	"not understood: %s":                    "ikke forstået: %s",
	"Kept in the title, not understood: %s": "Beholdt i titlen, ikke forstået: %s",
	"due ":                                  "forfald ",
	"overdue":                               "forfalden",
	"title~":                                "titel~",

	// ── Calendar entries ──
	"✓ done at ": "✓ færdig kl. ",
	"⧗ due":      "⧗ forfalder",
	"⧗ overdue":  "⧗ forfalden",

	// ── Task detail ──
	"  +%s subtasks = %s": "  +%s delopgaver = %s",
	"now":                 "nu",

	// ── Stats ──
	"  Cycle time by size":      "  Gennemløbstid efter størrelse",
	"  Projected backlog clear": "  Forventet tømning af backlog",
	"  Projected clear":         "  Forventet tømt",
	"%d pending, no pace":       "%d udestående, intet tempo",
	"Small":                     "Lille",
	"Medium":                    "Mellem",
	"Large":                     "Stor",

	// ── Confirm prompts and toasts ──
	"Close '%s' with %d open subtask(s)? (y/n)":       "Luk '%s' med %d åbne delopgave(r)? (y/n)",
	"Move '%s' to active? (y/n)":                      "Flyt '%s' til aktiv? (y/n)",
	"Delete '%s' and %d subtask(s)? (y/n)":            "Slet '%s' og %d delopgave(r)? (y/n)",
	"Delete %s entry? (y/n)":                          "Slet %s-post? (y/n)",
	"Merge #%s into…":                                 "Flet #%s ind i…",
	"Edit subtask title...":                           "Rediger delopgavetitel...",
	"Time spent (45m, 1h30m) or HH:MM-HH:MM…":         "Brugt tid (45m, 1t30m) eller TT:MM-TT:MM…",
	"Stop the timer before deleting a running entry":  "Stop tidtagningen, før du sletter en kørende post",
	"Stop the timer before editing a running entry":   "Stop tidtagningen, før du redigerer en kørende post",
	"Dependency '%s' is done":                         "Afhængigheden '%s' er færdig",
	"Dependency '%s' is hidden by the current filter": "Afhængigheden '%s' er skjult af det aktuelle filter",
	"Dependency no longer exists":                     "Afhængigheden findes ikke længere",

	// ── Help: quick-add tokens (the token itself stays English — it is syntax) ──
	"add a tag (existing tags are suggested; tab inserts)": "tilføj et mærke (eksisterende mærker foreslås; tab indsætter)",
	"put it in a project":                             "læg den i et projekt",
	"set a due date (see Date input below)":           "sæt en forfaldsdato (se Datoinput nedenfor)",
	"priority: %s / %s / %s (p:h, p:m, p:l)":          "prioritet: %s / %s / %s (p:h, p:m, p:l)",
	"size: s / m / l (also %s)":                       "størrelse: s / m / l (også %s)",
	"repeat: %s / %s / %s / %s / %s":                  "gentagelse: %s / %s / %s / %s / %s",
	"block on the last added task (or %s<id prefix>)": "bloker på den senest tilføjede opgave (eller %s<id-præfiks>)",

	// ── Help: search filters ──
	"only tasks carrying the tag":                                       "kun opgaver med mærket",
	"only tasks in the project":                                         "kun opgaver i projektet",
	"only that priority":                                                "kun den prioritet",
	"due before a date (also >, <=, >= and an exact date)":              "forfalder før en dato (også >, <=, >= og en præcis dato)",
	"only overdue tasks":                                                "kun forfaldne opgaver",
	"anything else fuzzy-matches the title, or the description as text": "alt andet fuzzy-matcher titlen, eller beskrivelsen som tekst",

	// ── Help: row symbols and scroll hints ──
	"Row symbols":                "Rækkesymboler",
	"ready to start (ST column)": "klar til at gå i gang (ST-kolonnen)",
	"in progress: time has been logged against it (ST column)": "i gang: der er logget tid på den (ST-kolonnen)",
	"overdue (ST column)": "forfalden (ST-kolonnen)",
	"done (ST column)":    "færdig (ST-kolonnen)",
	"blocked: waiting on an unfinished dependency; sorts last": "blokeret: venter på en uafsluttet afhængighed; sorteres nederst",
	"timer running":                 "tidtagning kører",
	"recurring task":                "gentagende opgave",
	"subtasks done / total":         "delopgaver færdige / i alt",
	"subtasks collapsed / expanded": "delopgaver foldet sammen / ud",
	"score lifted by a subtask or by work waiting on it (detail pane)": "score løftet af en delopgave eller af arbejde der venter på den (detaljeruden)",
	"blocked: waiting on an unfinished dependency":                     "blokeret: venter på en uafsluttet afhængighed",
	"blocked: waiting on an unfinished dependency (ST column)":         "blokeret: venter på en uafsluttet afhængighed (ST-kolonnen)",
	"others depend on this: finishing it unblocks them":                "andre afhænger af denne: at afslutte den frigør dem",

	// ── Help: the status line ──
	"Status line": "Statuslinje",
	"background sync is failing: Settings has the error": "baggrundssynkronisering fejler: fejlen står under Indstillinger",
	"the focus filter is on: today + overdue only":       "fokusfilteret er slået til: kun i dag + overskredne",
	"a filter is narrowing the list":                     "et filter indsnævrer listen",
	"↑ scroll up":                                        "↑ rul op",
	"↓ scroll down":                                      "↓ rul ned",
	"↑/↓ scroll":                                         "↑/↓ rul",
	"FOCUS":                                              "FOKUS",

	// ── Unchanged in Danish, listed so the completeness check stays honest ──
	"Score":  "Score",
	"Score:": "Score:",
	"score":  "score",
	"ID:":    "ID:",
	" ◉":     " ◉",
	"%s  (%sD %s · P %s · M %s · S %s · A %s)": "%s  (%sD %s · P %s · M %s · S %s · A %s)",
	"starts %s": "starter %s",

	// ── Bias levels (shown in the ‹ … › pickers) ──
	"relaxed":  "afslappet",
	"balanced": "balanceret",
	"intense":  "intens",

	// ── Why this rank (the explain overlay and `tjek why`) ──
	"Why this rank":              "Hvorfor denne placering",
	"Deadline":                   "Deadline",
	"Momentum":                   "Momentum",
	"Total":                      "I alt",
	"#%d of %d by sequence · %s": "#%d af %d efter sekvens · %s",
	"%s = %.1f of the %.1f points the top task scores right now":          "%s = %.1f af de %.1f point, den højeste opgave scorer lige nu",
	"%s · not in the ranking (subtasks rank with their parent)":           "%s · uden for rangeringen (delopgaver rangerer med deres forælder)",
	"done: done tasks score 0 and leave the ranking":                      "færdig: færdige opgaver scorer 0 og forlader rangeringen",
	"ranked on %.1f, lifted by a subtask or by work waiting on it":        "rangeret på %.1f, løftet af en delopgave eller af arbejde, der venter på den",
	"starts %s, so it ranks below the work you can start today":           "starter %s, så den rangeres under det arbejde, du kan starte i dag",
	"the list is on another sort right now; this is the Sequence ranking": "listen er sorteret anderledes lige nu; dette er sekvensrangeringen",
	"%.1f points short of #%d %s":                                         "%.1f point fra at overhale #%d %s",
	"%.1f points clear of #%d %s":                                         "%.1f point foran #%d %s",
	"Moves on its own:":                                                   "Flytter sig af sig selv:",
	"Moves on its own, with no edit from you:":                            "Flytter sig af sig selv, uden at du rører den:",
	"nothing in the next three days; this position is stable":             "intet de næste tre dage; placeringen er stabil",
	"momentum runs out":                                                   "momentum løber ud",
	"the deadline ramp steps a day closer":                                "deadlinerampen rykker en dag tættere på",
	"still #%d":                                                           "stadig #%d",
	"unranked":                                                            "uden for listen",
	"in %dm":                                                              "om %dm",
	"in %dh":                                                              "om %dt",
	"in %dd":                                                              "om %dd",
	"esc or w to close  ·  tune the weights in Settings → Sequencer":      "esc eller w lukker  ·  justér vægtene i Indstillinger → Sekvensering",

	// ── Why this rank: the sentence behind each score factor ──
	"no due date":     "ingen forfaldsdato",
	"due today":       "forfalder i dag",
	"due tomorrow":    "forfalder i morgen",
	"1 day overdue":   "1 dag forfalden",
	"%d days overdue": "%d dage forfalden",
	"due in %d days; the ramp adds points daily":      "forfalder om %d dage; rampen giver point hver dag",
	"due in %d days, further out than the 7-day ramp": "forfalder om %d dage, længere ude end 7-dages-rampen",
	"priority is %s": "prioriteten er %s",
	"size is %s":     "størrelsen er %s",
	"you worked on this task in the last 48h":  "du har arbejdet på denne opgave inden for 48 timer",
	"project @%s saw activity in the last 48h": "projektet @%s havde aktivitet inden for 48 timer",
	"tag #%s saw activity in the last 48h":     "mærket #%s havde aktivitet inden for 48 timer",
	"nothing here was touched in the last 48h": "intet her er rørt inden for 48 timer",
	"created today":                     "oprettet i dag",
	"created 1 day ago":                 "oprettet for 1 dag siden",
	"created %d days ago":               "oprettet for %d dage siden",
	"aging is switched off in Settings": "aldring er slået fra i Indstillinger",

	// ── Keymap descriptions the footer and help overlay render ──
	"jump to ends / page through list":          "spring til start/slut · bladr i listen",
	"toggle this help":                          "slå denne hjælp til/fra",
	"add task (#tag due:date p:high @proj s:M)": "tilføj opgave (#mærke frist:dato p:høj @projekt s:M)",
	"add manual time entry":                     "tilføj manuel tidsregistrering",
	"start/stop subtask timer":                  "start/stop tidtagning på delopgave",
	"back to list":                              "tilbage til listen",
	"merge tags (Tags tab)":                     "flet mærker (fanen Mærker)",
	"select entry":                              "vælg post",
	"back":                                      "tilbage",
	"cycle activity range":                      "skift aktivitetsperiode",
	"scroll the summary":                        "rul i overblikket",
	"change value / theme":                      "skift værdi / tema",
	"set / clear due date":                      "sæt / ryd forfaldsdato",

	// ── Help section titles ──
	"Calendar": "Kalender",
	"Stats":    "Statistik",

	// ── Short labels (the terse footer hint on a narrow window) ──
	"add":   "tilføj",
	"done":  "færdig",
	"track": "tid",
	"del":   "slet",
	"sort":  "sortér",

	// Board card-view footer, kept apart so its long key does not realign the table.
	"↑/↓ previous/next card · enter edit in Tasks · esc close": "↑/↓ forrige/næste kort · enter rediger i Opgaver · esc luk",
}
var deTranslations = map[string]string{
	// Header / chrome
	"? shortcuts":                              "? Tastenkürzel",
	"Type a command…":                          "Befehl eingeben…",
	"No command matches that.":                 "Kein Befehl passt dazu.",
	"command palette: find any action by name": "Befehlspalette: jede Aktion per Name finden",
	"Go to Tasks":                              "Zu Aufgaben",
	"Go to Calendar":                           "Zum Kalender",
	"Go to Projects":                           "Zu Projekten",
	"Go to Tags":                               "Zu Schlagwörtern",
	"Go to Board":                              "Zum Board",
	"Go to Stats":                              "Zur Statistik",
	"Go to Settings":                           "Zu Einstellungen",
	"FOCUS: today + overdue only (f to toggle)": "FOKUS: nur heute + überfällig (f schaltet um)",
	"(untagged)": "(ohne Schlagwort)",

	// Tab labels (number prefix kept; only the word is translated)
	"1 Tasks":    "1 Aufgaben",
	"2 Calendar": "2 Kalender",
	"3 Tags":     "3 Schlagwörter",
	"4 Projects": "4 Projekte",
	"5 Board":    "5 Board",
	"6 Stats":    "6 Statistik",
	"7 Settings": "7 Einstell.",
	"8 Meetings": "8 Meetings",

	// Key hints (footer)

	// Footer / timer
	"#tag @project %s p:%s s:l r:%s %s^": "#Schlagwort @Projekt %s p:%s s:l r:%s %s^",
	" · t to stop":                       " · t zum Stoppen",
	"create new tag: ":                   "neues Schlagwort: ",
	"create new project: ":               "neues Projekt: ",

	// Help screen
	"Keyboard shortcuts":                          "Tastenkürzel",
	"Press ? or esc to close":                     "? oder esc zum Schließen",
	"/ filter  ·  ? or esc to close":              "/ Filter  ·  ? oder esc schließt",
	"type to filter  ·  enter keep  ·  esc clear": "tippen filtert  ·  enter übernimmt  ·  esc löscht",
	"/ filter  ·  esc clear  ·  ? to close":       "/ Filter  ·  esc löscht  ·  ? schließt",
	"No shortcut matches that.":                   "Kein Kürzel passt dazu.",
	"Quick-add syntax":                            "Schnellerfassung",
	"Filters":                                     "Filter",
	"Navigation":                                  "Navigation",
	"navigate list":                               "Liste navigieren",
	"open details":                                "Details öffnen",
	"go back":                                     "zurück",
	"switch tabs (forward / back / direct)":       "Reiter wechseln (vor / zurück / direkt)",
	"Board":                                       "Board",
	"focus previous/next column":                  "vorherige/nächste Spalte",
	"move card between stages (into Done completes it)": "Karte verschieben (nach Fertig schließt sie ab)",
	"pick up card to move it":                           "Karte zum Verschieben aufnehmen",
	"←/→ carry to a column · enter/esc put it down":     "←/→ in eine Spalte tragen · Enter/Esc ablegen",
	"filter cards (#tag, @project, text)":               "Karten filtern (#Schlagwort, @Projekt, Text)",
	"show card details":                                 "Kartendetails anzeigen",
	"new card in this column":                           "neue Karte in dieser Spalte",
	"Card":                                              "Karte",
	"empty":                                             "leer",
	"more":                                              "mehr",
	"close help":                                        "Hilfe schließen",
	"Tasks":                                             "Aufgaben",
	"Workflow":                                          "Arbeitsablauf",
	"Summary":                                           "Übersicht",
	"Preferences":                                       "Einstellungen",
	"Sequencer":                                         "Sequenzer",
	"Overview":                                          "Überblick",
	"History":                                           "Verlauf",
	"Activity":                                          "Aktivität",
	"add task (quick-add: #tag due:date p:high @proj s:M)": "Aufgabe anlegen (#tag fällig:datum p:hoch @proj s:M)",
	"rename task":                      "Aufgabe umbenennen",
	"toggle done":                      "fertig umschalten",
	"start/stop time tracking":         "Zeiterfassung starten/stoppen",
	"cycle priority low/med/high":      "Priorität niedrig/mittel/hoch",
	"delete":                           "löschen",
	"edit description (opens $EDITOR)": "Beschreibung bearbeiten (öffnet $EDITOR)",
	"ctrl+e  edit in $EDITOR":          "ctrl+e  in $EDITOR bearbeiten",
	"focus: today + overdue only":      "Fokus: nur heute + überfällig",
	"toggle history":                   "Verlauf umschalten",
	"cycle sort order":                 "Sortierung wechseln",
	"why this rank":                    "warum dieser Rang",
	"why this rank: the score, its causes, what moves it": "warum dieser Rang: die Punkte, ihre Ursachen, was sie bewegt",
	"expand/collapse subtasks":                            "Teilaufgaben auf/zu",
	"search":                                              "suchen",
	"Detail view":                                         "Detailansicht",
	"jump section":                                        "Abschnitt wechseln",
	"edit field / open subtask":                           "Feld bearbeiten / Teilaufgabe",
	"add tag / dep / comment / subtask":                   "Schlagwort / Abhäng. / Kommentar / Teilaufgabe",
	"quick add tag":                                       "Schlagwort schnell hinzufügen",
	"quick add / change project":                          "Projekt schnell setzen/ändern",
	"toggle subtask done":                                 "Teilaufgabe fertig",
	"rename subtask / edit time entry":                    "Teilaufgabe umbenennen / Zeit bearbeiten",
	"remove field / delete subtask":                       "Feld leeren / Teilaufgabe löschen",
	"Tags & Projects":                                     "Schlagwörter & Projekte",
	"Inside a tag / project":                              "In einem Schlagwort / Projekt",
	"open the tasks in it":                                "enthaltene Aufgaben öffnen",
	"new task in it":                                      "neue Aufgabe darin",
	"show its tasks on the Tasks tab":                     "seine Aufgaben im Reiter Aufgaben",
	"delete task":                                         "Aufgabe löschen",
	"back to the list":                                    "zurück zur Liste",
	"rename globally":                                     "global umbenennen",
	"delete globally":                                     "global löschen",
	"filter":                                              "filtern",
	"sort date/alpha":                                     "Sortierung Datum/A-Z",
	"Calendar (tab 2)":                                    "Kalender (Reiter 2)",
	"move by day / week":                                  "Tag / Woche wechseln",
	"previous / next month":                               "voriger / nächster Monat",
	"jump to today":                                       "zu heute springen",
	"focus the day's entries":                             "Einträge des Tages",
	"edit entry times (09:12-10:00 or 45m)":               "Zeiten bearbeiten (09:12-10:00 oder 45m)",
	"delete selected entry":                               "gewählten Eintrag löschen",
	"Stats (tab 6)":                                       "Statistik (Reiter 6)",
	"switch to stats view":                                "zur Statistikansicht",
	"Settings (tab 7)":                                    "Einstellungen (Reiter 7)",
	"select setting":                                      "Einstellung wählen",
	"change theme":                                        "Farbschema ändern",
	"activate / edit the selected setting":                "gewählte Einstellung aktivieren/bearbeiten",
	"confirm update when one is offered":                  "Aktualisierung bestätigen",
	"App":                                                 "App",
	"undo last change":                                    "letzte Änderung rückgängig",
	"keyboard shortcuts and help":                         "Tastenkürzel und Hilfe",
	"quit":                                                "beenden",
	"Date input":                                          "Datumseingabe",
	"exact date (e.g. 15-06-25)":                          "genaues Datum (z. B. 15-06-25)",
	"today's date":                                        "heutiges Datum",
	"tomorrow":                                            "morgen",
	"7 days from now":                                     "in 7 Tagen",
	"1 month from now":                                    "in 1 Monat",
	"next occurrence of weekday":                          "nächster dieser Wochentage",
	"relative days/weeks/months":                          "relative Tage/Wochen/Monate",

	// Stats detail
	"Last 30 days":                  "Letzte 30 Tage",
	"Last 26 weeks":                 "Letzte 26 Wochen",
	"Last 7 days":                   "Letzte 7 Tage",
	"%d done":                       "%d erledigt",
	"No completions in this range.": "Keine Abschlüsse in diesem Zeitraum.",
	"1 block = 1 completed task":    "1 Block = 1 erledigte Aufgabe",

	// Stats list — sections & labels
	"  Workload":            "  Auslastung",
	"Overdue":               "Überfällig",
	"Due today":             "Heute fällig",
	"Due this week":         "Diese Woche",
	"Active total":          "Aktiv gesamt",
	"Seq hit (top-5)":       "Seq-Treffer (Top 5)",
	"Created":               "Erstellt",
	"Completed":             "Erledigt",
	"  Net backlog":         "  Netto-Rückstand",
	"+%d ▲ growing":         "+%d ▲ wächst",
	"%d ▼ shrinking":        "%d ▼ schrumpft",
	"±0 → steady":           "±0 → stabil",
	"%d done vs %d  %s":     "%d erledigt vs. %d  %s",
	"Flow (last 7 days)":    "Fluss (7 Tage)",
	"vs last week":          "vs. Vorwoche",
	"Flow (last 30 days)":   "Fluss (30 Tage)",
	"vs prior 30d":          "vs. vorige 30 T",
	"  Throughput":          "  Durchsatz",
	"  Time to done (30d)":  "  Bis fertig (30 T)",
	"median ":               "Median ",
	"none yet":              "noch keine",
	"  Median active age":   "  Medianalter aktiv",
	"  Oldest active":       "  Älteste aktive",
	"  Active by priority":  "  Aktiv nach Priorität",
	"↑ High":                "↑ Hoch",
	"→ Medium":              "→ Mittel",
	"↓ Low":                 "↓ Niedrig",
	"  Completion velocity": "  Abschlusstempo",
	"Today":                 "Heute",
	"today":                 "heute",
	"This week":             "Diese Woche",
	"This month":            "Dieser Monat",
	"  Avg (7d)":            "  Ø (7 T)",
	"%.1f tasks/day":        "%.1f Aufgaben/Tag",

	// Priority, size, and recurrence words
	"high":     "hoch",
	"medium":   "mittel",
	"low":      "niedrig",
	"small":    "klein",
	"large":    "groß",
	"daily":    "täglich",
	"weekly":   "wöchentlich",
	"monthly":  "monatlich",
	"yearly":   "jährlich",
	"weekdays": "werktags",

	// ── Parser keywords (lang_input.go) ──
	// These double as input: the word shown here is the word the quick-add and
	// search grammars accept, alongside the English one. Changing a spelling
	// changes what parses, so keep them to words a German user would type.
	"yesterday":  "gestern",
	"next week":  "nächste Woche",
	"next month": "nächster Monat",
	"next":       "nächste",
	"due:":       "fällig:",
	"size:":      "größe:",
	"recur:":     "wiederh:",
	"dep:":       "abh:",

	// List headers / sort
	"Completed tasks":   "Erledigte Aufgaben",
	">Completed tasks<": ">Erledigte Aufgaben<",
	">Completed<":       ">Erledigt<",
	"Start":             "Start",
	"Due":               "Fällig",
	"Priority":          "Priorität",
	">Due<":             ">Fällig<",
	">Start<":           ">Start<",
	">Priority<":        ">Priorität<",
	"Tags":              "Schlagwörter",
	"sort:":             "Sort.:",

	// Tag list / detail
	"  No tags match your filter.":                         "  Keine Schlagwörter passen zum Filter.",
	"  No tags yet. Add tags to tasks in the detail view.": "  Noch keine Schlagwörter. In der Detailansicht hinzufügen.",
	"Age":                           "Alter",
	"  No tag selected.":            "  Kein Schlagwort gewählt.",
	"  (untagged)":                  "  (ohne Schlagwort)",
	"%d open":                       "%d offen",
	"  %s shows the finished ones.": "  %s zeigt die fertigen.",
	"  Every project is finished.":  "  Alle Projekte sind fertig.",
	"  Every tag is finished.":      "  Alle Schlagwörter sind fertig.",
	"  Nothing open here.":          "  Hier ist nichts offen.",
	"  Tags group related tasks; this tab shows what is open in each.": "  Schlagwörter bündeln Aufgaben; dieser Tab zeigt, was jeweils offen ist.",
	"  ✓ %d done · %s shows them":                                      "  ✓ %d fertig · %s zeigt sie",
	"Last":                                                             "Zuletzt",
	"Next up":                                                          "Als Nächstes",
	"Open":                                                             "Offen",
	"[+%d finished]":                                                   "[+%d fertig]",
	"open":                                                             "offen",
	"show / hide done tasks":                                           "fertige Aufgaben ein- / ausblenden",
	"show / hide finished":                                             "Fertige ein- / ausblenden",
	"  often with: ":                                                   "  oft mit: ",
	"  … and %d more":                                                  "  … und %d weitere",

	"Date":           "Datum",
	"Source task:  ": "Aufgabe:      ",
	"[done]":         "[fertig]",
	"[task removed]": "[Aufgabe entfernt]",
	"Date:         ": "Datum:        ",
	"Tags:         ": "Schlagw.:     ",
	"none":           "keine",

	// Task list empty states
	"  No tasks match the filter.":           "  Keine Aufgaben passen zum Filter.",
	"  No tasks due today or overdue. Nice!": "  Nichts heute fällig oder überfällig. Stark!",
	"  No tasks yet. Press 'a' to add one.":  "  Noch keine Aufgaben. 'a' legt eine an.",
	"  Try:  ":                               "  Beispiel:  ",
	// Free text (title/tag/project) is localized; the quick-add keywords
	// due:/friday/p:high stay English because the parser only accepts English.
	"Buy milk #shopping due:friday p:high @home": "Milch kaufen #einkauf due:friday p:high @zuhause",
	"  Press ? for all keyboard shortcuts.":      "  ? zeigt alle Tastenkürzel.",
	"  No completed tasks match the filter.":     "  Keine erledigten Aufgaben passen zum Filter.",
	"  No completed tasks yet.":                  "  Noch keine erledigten Aufgaben.",

	// Projects
	"  No projects match the filter.":                           "  Keine Projekte passen zum Filter.",
	"  No projects yet. Add a project to a task first.":         "  Noch keine Projekte. Erst einer Aufgabe eines zuweisen.",
	"  A project groups its tasks into a timeline on this tab.": "  Ein Projekt bündelt seine Aufgaben hier zu einer Zeitleiste.",
	"Project":                     "Projekt",
	"Done":                        "Fertig",
	"  Timeline":                  "  Zeitleiste",
	"today:":                      "heute:",
	"  No tasks in this project.": "  Keine Aufgaben in diesem Projekt.",
	"%d active":                   "%d aktiv",
	"%d overdue":                  "%d überfällig",

	// Detail pages
	"not set":                            "nicht gesetzt",
	" ! overdue":                         " ! überfällig",
	"Start date":                         "Startdatum",
	"Due date":                           "Fälligkeit",
	"Recurrence":                         "Wiederholung",
	"Size":                               "Größe",
	"Stage":                              "Phase",
	"Description":                        "Beschreibung",
	"none (press enter or 'n' to edit)":  "keine (enter oder 'n' bearbeitet)",
	"Created:":                           "Erstellt:",
	"Modified:":                          "Geändert:",
	"%s (%d entries)":                    "%s (%d Einträge)",
	" ◉ tracking":                        " ◉ läuft",
	"Time spent:":                        "Aufgewendet:",
	"Completed on":                       "Erledigt am",
	"Tags:":                              "Schlagwörter:",
	"No tags. Press 'a' to add one.":     "Keine Schlagwörter. 'a' fügt eines hinzu.",
	"  Closed today (%d)":                "  Heute geschlossen (%d)",
	"stop":                               "stopp",
	"reopen":                             "öffnen",
	"  ↑ %d more":                        "  ↑ %d weitere",
	"  ↓ %d more":                        "  ↓ %d weitere",
	"Fields":                             "Felder",
	"Subtasks":                           "Teilaufgaben",
	"Dependencies":                       "Abhängigkeiten",
	"Time":                               "Zeit",
	"Comments":                           "Kommentare",
	"Subtasks:":                          "Teilaufgaben:",
	"No subtasks. Press 'a' to add one.": "Keine Teilaufgaben. 'a' fügt eine hinzu.",
	"%s[?] unknown subtask":              "%s[?] unbekannte Teilaufgabe",
	"Dependencies:":                      "Abhängigkeiten:",
	"Inbound dependency: remove it from the other task": "Eingehende Abhängigkeit: an der anderen Aufgabe entfernen",
	"A subtask can't outrank its parent":                "Eine Teilaufgabe kann ihre übergeordnete Aufgabe nicht überholen",
	"No dependencies. Press 'a' to add one.":            "Keine Abhängigkeiten. 'a' fügt eine hinzu.",
	"%s[?] unknown task":                                "%s[?] unbekannte Aufgabe",
	"Comments:":                                         "Kommentare:",
	"No comments yet. Press 'a' to add one.":            "Noch keine Kommentare. 'a' fügt einen hinzu.",
	"Time entries:":                                     "Zeiteinträge:",
	"No time entries. Press 'T' to add one.":            "Keine Zeiteinträge. 'T' fügt einen hinzu.",

	// Calendar
	"Month ":                     "Monat ",
	"Day ":                       "Tag ",
	"%d entries · %s":            "%d Einträge · %s",
	"1 entry · ":                 "1 Eintrag · ",
	"  No activity on this day.": "  Keine Aktivität an diesem Tag.",
	"  Press t on a task (tab 1) to start tracking.": "  t auf einer Aufgabe (Reiter 1) startet die Erfassung.",
	" now ": " jetzt ",

	// Settings
	"Theme":                          "Farbschema",
	"Language":                       "Sprache",
	"tab insert · ↑/↓ pick":          "tab einfügen · ↑/↓ wählen",
	"Board columns":                  "Board-Spalten",
	"Board columns, comma-separated": "Board-Spalten, kommagetrennt",
	"Comma-separated column names · [x] before a name gives it an icon · the last holds completed tasks": "Spaltennamen kommagetrennt · [x] vor einem Namen gibt ein Symbol · die letzte enthält erledigte Aufgaben",
	"Version":              "Version",
	"Check for updates":    "Nach Updates suchen",
	"press enter to check": "enter prüft",
	"Settings":             "Einstellungen",

	// Update / status / errors
	"Update failed":                    "Update fehlgeschlagen",
	"Updated! Restart tjek to apply.":  "Aktualisiert! tjek neu starten.",
	"Error saving settings: %v":        "Fehler beim Speichern der Einstellungen: %v",
	"Updated; restart to apply":        "Aktualisiert; Neustart nötig",
	"Check failed":                     "Prüfung fehlgeschlagen",
	"Up to date (":                     "Aktuell (",
	"Latest release: ":                 "Neueste Version: ",
	"; this is a local build (":        "; dies ist ein lokaler Build (",
	"Update available: ":               "Update verfügbar: ",
	"Update available: %s. Run `%s`":   "Update verfügbar: %s. `%s` ausführen",
	" is available. Update now? (y/n)": " ist verfügbar. Jetzt aktualisieren? (y/n)",
	"Checking…":                        "Prüfe…",
	"Updating…":                        "Aktualisiere…",
	"Nothing to undo":                  "Nichts rückgängig zu machen",
	"Undid: %s":                        "Rückgängig: %s",
	"No editor found. Set EDITOR permanently, e.g: setx EDITOR notepad (then restart tjek)":                                "Kein Editor gefunden. EDITOR dauerhaft setzen, z. B.: setx EDITOR notepad (dann tjek neu starten)",
	"No editor found. Set $EDITOR permanently, e.g: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish": "Kein Editor gefunden. $EDITOR dauerhaft setzen, z. B.: echo 'set -Ux EDITOR /usr/lib/helix/hx' >> ~/.config/fish/config.fish",
	"Editor failed; falling back to notepad":                "Editor fehlgeschlagen; nutze notepad",
	"Invalid date - use dd-mm-yy, %q, %q, %q, %q, or '+3d'": "Ungültiges Datum - nutze dd-mm-yy, %q, %q, %q, %q oder '+3d'",
	"Dependency not linked":                                 "Abhängigkeit nicht verknüpft",
	"Remove project '%s' from ALL its tasks? (y/n)":         "Projekt '%s' von ALLEN seinen Aufgaben entfernen? (y/n)",

	// Confirm prompts
	"Delete '%s'? (y/n)":                        "'%s' löschen? (y/n)",
	"Delete tag '#%s' from ALL tasks? (y/n)":    "Schlagwort '#%s' von ALLEN Aufgaben löschen? (y/n)",
	"Delete this comment? (y/n)":                "Diesen Kommentar löschen? (y/n)",
	"Remove project '%s' from this task? (y/n)": "Projekt '%s' von dieser Aufgabe entfernen? (y/n)",
	"Remove tag '#%s' from this task? (y/n)":    "Schlagwort '#%s' von dieser Aufgabe entfernen? (y/n)",
	"Remove this dependency? (y/n)":             "Diese Abhängigkeit entfernen? (y/n)",
	"Delete subtask '%s'? (y/n)":                "Teilaufgabe '%s' löschen? (y/n)",
	"Delete %s entry for '%s'? (y/n)":           "Eintrag %s für '%s' löschen? (y/n)",

	// Input placeholders
	"Filter... (#tag @project p:%s %s<%s)":    "Filtern… (#Schlagwort @Projekt p:%s %s<%s)",
	"Search for task to add as dependency...": "Aufgabe als Abhängigkeit suchen…",
	"Search or create tag...":                 "Schlagwort suchen oder anlegen…",
	"Search or create project...":             "Projekt suchen oder anlegen…",
	"Filter tags...":                          "Schlagwörter filtern…",

	// Inline edit/add placeholders (set when a text-entry mode opens)
	"New task...": "Neue Aufgabe…",
	"HH:MM-HH:MM or duration (45m, 1h30m)...": "HH:MM-HH:MM oder Dauer (45m, 1h30m)…",
	"Edit tag name...":                        "Schlagwortnamen bearbeiten…",
	"Edit task title...":                      "Aufgabentitel bearbeiten…",
	"Add comment...":                          "Kommentar hinzufügen…",
	"Add subtask...":                          "Teilaufgabe hinzufügen…",
	"Edit comment...":                         "Kommentar bearbeiten…",
	"Start date (dd-mm-yy, 'today', 'next week', '+3d')...": "Startdatum (dd-mm-yy, 'today', 'next week', '+3d')…",
	"Completed (dd-mm-yy hh:mm, 'today', 'yesterday')...":   "Erledigt am (dd-mm-yy hh:mm, 'today', 'yesterday')…",
	"Completion can't be in the future":                     "Erledigt kann nicht in der Zukunft liegen",
	"Due date (dd-mm-yy, 'today', 'next week', '+3d')...":   "Fälligkeit (dd-mm-yy, 'today', 'next week', '+3d')…",

	// ── Settings group headings ──
	"Appearance": "Darstellung",
	"General":    "Allgemein",
	"About":      "Über",

	// Row labels that sit under a heading naming the same thing.
	"Automatic":           "Automatisch",
	"Enabled":             "Aktiviert",
	"Share board columns": "Board-Spalten teilen",

	// ── Sync + server (Settings rows, status line, toasts) ──
	"Sync":                            "Sync",
	"Sync server":                     "Sync-Server",
	"Sync token":                      "Sync-Token",
	"Sync now":                        "Jetzt syncen",
	"Server":                          "Server",
	"Listen":                          "Adresse",
	"Server token":                    "Server-Token",
	"press enter to sync":             "enter synct",
	"needs server":                    "Server fehlt",
	"external":                        "extern",
	"set":                             "gesetzt",
	"On":                              "An",
	"Off":                             "Aus",
	"Syncing…":                        "Synce…",
	"Server stopped":                  "Server gestoppt",
	"Server: ":                        "Server: ",
	"Serving on ":                     "Läuft auf ",
	"✕ sync":                          "✕ Sync",
	"Set a server token first":        "Erst ein Server-Token setzen",
	"Set sync server + token first":   "Erst Sync-Server + Token setzen",
	"Last sync failed: ":              "Letzter Sync fehlgeschlagen: ",
	"Last sync: sent %d, received %d": "Letzter Sync: %d gesendet, %d empfangen",
	"Sync failing: devices may be diverging (see Settings)":                       "Sync schlägt fehl: Geräte laufen evtl. auseinander (siehe Einstellungen)",
	"Sync: %d conflict(s) resolved; tjek sync --recover lists them":               "Sync: %d Konflikt(e) gelöst; tjek sync --recover zeigt sie",
	"Sync server URL, e.g. http://100.x.y.z:8765":                                 "Sync-Server-URL, z. B. http://100.x.y.z:8765",
	"Sync token (clear the field to remove it)":                                   "Sync-Token (Feld leeren zum Entfernen)",
	"Server token clients must present (ctrl+g generates one · blank removes it)": "Token, das Clients vorzeigen müssen (ctrl+g erzeugt eines · leer entfernt es)",
	"Could not generate a token: %v":                                              "Token konnte nicht erzeugt werden: %v",
	"Too weak for a server token; press ctrl+g to generate a strong one":          "Zu schwach für ein Server-Token; ctrl+g erzeugt ein starkes",
	"weak token; ctrl+g on this row generates a strong one":                       "schwaches Token; ctrl+g in dieser Zeile erzeugt ein starkes",
	"Bind address, e.g. 100.x.y.z:8765 or 127.0.0.1:8765":                         "Bind-Adresse, z. B. 100.x.y.z:8765 oder 127.0.0.1:8765",
	"Plain http to a public host: token travels unencrypted":                      "Klartext-HTTP zu öffentlichem Host: Token wird unverschlüsselt übertragen",

	// ── AI & Meetings (Settings) ──
	"AI & Meetings":       "KI & Meetings",
	"AI provider":         "KI-Anbieter",
	"Anthropic API key":   "Anthropic-API-Schlüssel",
	"OpenAI API key":      "OpenAI-API-Schlüssel",
	"Gemini API key":      "Gemini-API-Schlüssel",
	"Microphone override": "Mikrofon-Übersteuerung",
	"Anthropic API key (clear the field to remove it)":                        "Anthropic-API-Schlüssel (Feld leeren zum Entfernen)",
	"Gemini API key (clear the field to remove it)":                           "Gemini-API-Schlüssel (Feld leeren zum Entfernen)",
	"OpenAI API key — also needed to transcribe recordings (clear to remove)": "OpenAI-API-Schlüssel — wird auch zur Transkription von Aufnahmen benötigt (leeren zum Entfernen)",
	"Microphone override: format:input, e.g. dshow:audio=Microphone":          "Mikrofon-Übersteuerung: Format:Eingabe, z. B. dshow:audio=Microphone",
	"platform default": "Plattform-Standard",

	// ── Meetings tab ──
	"Meetings":                      "Meetings",
	"Meeting":                       "Meeting",
	"Meeting title...":              "Meeting-Titel...",
	"Meeting added":                 "Meeting hinzugefügt",
	"  No meetings yet.":            "  Noch keine Meetings.",
	"  Press %s to add one.":        "  %s drücken, um eins hinzuzufügen.",
	"  Meeting not found.":          "  Meeting nicht gefunden.",
	"Attendees: ":                   "Teilnehmer: ",
	"Date: ":                        "Datum: ",
	"Recording: ":                   "Aufnahme: ",
	"No transcript yet.":            "Noch kein Transkript.",
	"Transcript":                    "Transkript",
	"Action items":                  "Aufgabenpunkte",
	"draft":                         "Entwurf",
	"recording":                     "Aufnahme läuft",
	"transcribing…":                 "transkribiert…",
	"summarizing…":                  "fasst zusammen…",
	"ready for review":              "bereit zur Prüfung",
	"reviewed":                      "geprüft",
	"error":                         "Fehler",
	"Recording… press r to stop":    "Aufnahme läuft… r zum Stoppen drücken",
	"Stopping recording…":           "Aufnahme wird gestoppt…",
	"Could not start recording: %v": "Aufnahme konnte nicht gestartet werden: %v",
	"Recording error: %v":           "Aufnahmefehler: %v",
	"Recording saved. Add an OpenAI API key in Settings to transcribe it, or press n to type notes.": "Aufnahme gespeichert. Fügen Sie einen OpenAI-API-Schlüssel in den Einstellungen hinzu, um sie zu transkribieren, oder drücken Sie n, um Notizen einzugeben.",
	"Transcription failed: %v":                  "Transkription fehlgeschlagen: %v",
	"Summarizing and looking for action items…": "Fasst zusammen und sucht nach Aufgabenpunkten…",
	"AI pass failed: %v":                        "KI-Durchlauf fehlgeschlagen: %v",
	"Found %d action item(s) to review":         "%d Aufgabenpunkt(e) zur Prüfung gefunden",
	"Added task: %s":                            "Aufgabe hinzugefügt: %s",
	"accept action item":                        "Aufgabenpunkt annehmen",
	"Delete meeting '%s'? (y/n)":                "Meeting '%s' löschen? (y/n)",
	"Error saving meeting: %v":                  "Fehler beim Speichern des Meetings: %v",
	"Error deleting meeting: %v":                "Fehler beim Löschen des Meetings: %v",
	"Error saving action items: %v":             "Fehler beim Speichern der Aufgabenpunkte: %v",
	"Error saving review: %v":                   "Fehler beim Speichern der Prüfung: %v",
	"Error saving edit: %v":                     "Fehler beim Speichern der Änderung: %v",
	"Error writing transcript file: %v":         "Fehler beim Schreiben der Transkriptdatei: %v",
	"Edit action item (quick-add syntax)...":    "Aufgabenpunkt bearbeiten (Schnellerfassungs-Syntax)...",
	// Meetings keymap descriptions (keymap.go)
	"open meeting":                                    "Meeting öffnen",
	"add meeting":                                     "Meeting hinzufügen",
	"start/stop recording":                            "Aufnahme starten/stoppen",
	"edit transcript (opens $EDITOR)":                 "Transkript bearbeiten (öffnet $EDITOR)",
	"summarize + find action items":                   "zusammenfassen + Aufgabenpunkte finden",
	"delete meeting":                                  "Meeting löschen",
	"move between action items":                       "zwischen Aufgabenpunkten bewegen",
	"accept the focused action item → creates a task": "den fokussierten Aufgabenpunkt annehmen → erstellt eine Aufgabe",
	"reject the focused action item":                  "den fokussierten Aufgabenpunkt ablehnen",
	"edit the focused action item (quick-add syntax)": "den fokussierten Aufgabenpunkt bearbeiten (Schnellerfassungs-Syntax)",

	// ── Sequencer / Settings rows ──
	"Deadline pressure":         "Fristendruck",
	"Priority focus":            "Prioritätsfokus",
	"Momentum bias":             "Momentum-Gewicht",
	"Aging increases score":     "Alter erhöht Punktzahl",
	"Auto-close parent":         "Eltern autom. schließen",
	"Auto-close subtasks":       "Teilaufg. autom. schließen",
	"Daily reminder":            "Tägliche Erinnerung",
	"Reminder time":             "Erinnerungszeit",
	"Subtasks copy tags":        "Teilaufg. erben Schlagw.",
	"Kanban board":              "Kanban-Tafel",
	"Detail pane":               "Detailbereich",
	"Right":                     "Rechts",
	"Left":                      "Links",
	"Bottom":                    "Unten",
	"Top 5 with these weights:": "Top 5 mit diesen Gewichten:",
	"Score resync failed: %v":   "Punkte-Neuberechnung fehlgeschlagen: %v",

	// ── Settings: export and import ──
	"Export":             "Export",
	"Auto-export folder": "Auto-Export-Ordner",
	"Import from file":   "Aus Datei importieren",
	"its board column's icon, once columns have icons (ST column)": "Symbol der Board-Spalte, sobald Spalten Symbole haben (Spalte ST)",
	"choose a file": "Datei wählen",
	"✓ is kept for the last column; pick another icon":          "✓ ist der letzten Spalte vorbehalten; wähle ein anderes Symbol",
	"An icon is one character wide; %s is not":                  "Ein Symbol ist ein Zeichen breit; %s nicht",
	"The last column keeps ✓; its icon %s was left out":         "Die letzte Spalte behält ✓; das Symbol %s wurde weggelassen",
	"Path to a tjek export (.json)":                             "Pfad zu einem tjek-Export (.json)",
	"Auto-export off":                                           "Auto-Export aus",
	"Not a folder: %s":                                          "Kein Ordner: %s",
	"Exporting to %s":                                           "Exportiere nach %s",
	"Import failed: %v":                                         "Import fehlgeschlagen: %v",
	"Auto-export failed: %v":                                    "Auto-Export fehlgeschlagen: %v",
	"Folder to keep tjek-export.json in (blank turns it off)":   "Ordner für tjek-export.json (leer schaltet ihn ab)",
	"Nothing to import: every task in the file is already here": "Nichts zu importieren: alle Aufgaben der Datei sind schon da",
	"Imported %d new, %d updated · u undoes it":                 "%d neu, %d aktualisiert importiert · u macht es rückgängig",
	"tab completes the name · enter confirms · esc cancels":     "Tab vervollständigt · Enter bestätigt · Esc bricht ab",

	// ── Adding tasks ──
	"A task needs a title": "Eine Aufgabe braucht einen Titel",

	// ── Calendar: coming up ──
	"Coming up":   "Demnächst",
	"nothing due": "nichts fällig",

	// ── Reminders ──
	"%d due today":               "%d heute fällig",
	"desktop pop-up unavailable": "Desktop-Pop-up nicht verfügbar",

	// ── List header / panel titles ──
	"Active tasks":        "Aktive Aufgaben",
	"Detail":              "Detail",
	"Tag":                 "Schlagwort",
	"  No task selected.": "  Keine Aufgabe gewählt.",

	// ── Sort labels (drawn in the panel border, keep them short) ──
	"alpha":     "A-Z",
	"completed": "erledigt",
	"due":       "fällig",
	"size":      "Größe",
	"count":     "Anzahl",
	"recent":    "neueste",

	// ── Quick-add preview ──
	"(no title yet)":                        "(noch ohne Titel)",
	"not understood: %s":                    "nicht erkannt: %s",
	"Kept in the title, not understood: %s": "Im Titel belassen, nicht erkannt: %s",
	"due ":                                  "fällig ",
	"overdue":                               "überfällig",
	"title~":                                "Titel~",

	// ── Calendar entries ──
	"✓ done at ": "✓ fertig am ",
	"⧗ due":      "⧗ fällig",
	"⧗ overdue":  "⧗ überfällig",

	// ── Task detail ──
	"  +%s subtasks = %s": "  +%s Teilaufgaben = %s",
	"now":                 "jetzt",

	// ── Stats ──
	"  Cycle time by size":      "  Durchlaufzeit nach Größe",
	"  Projected backlog clear": "  Rückstand voraussichtl. frei",
	"  Projected clear":         "  Voraussichtl. frei",
	"%d pending, no pace":       "%d offen, kein Tempo",
	"Small":                     "Klein",
	"Medium":                    "Mittel",
	"Large":                     "Groß",

	// ── Confirm prompts and toasts ──
	"Close '%s' with %d open subtask(s)? (y/n)":       "'%s' mit %d offenen Teilaufgabe(n) schließen? (y/n)",
	"Move '%s' to active? (y/n)":                      "'%s' wieder aktivieren? (y/n)",
	"Delete '%s' and %d subtask(s)? (y/n)":            "'%s' und %d Teilaufgabe(n) löschen? (y/n)",
	"Delete %s entry? (y/n)":                          "Eintrag %s löschen? (y/n)",
	"Merge #%s into…":                                 "#%s vereinen mit…",
	"Edit subtask title...":                           "Titel der Teilaufgabe bearbeiten…",
	"Time spent (45m, 1h30m) or HH:MM-HH:MM…":         "Aufgewendet (45m, 1h30m) oder HH:MM-HH:MM…",
	"Stop the timer before deleting a running entry":  "Erst die Uhr stoppen, dann den laufenden Eintrag löschen",
	"Stop the timer before editing a running entry":   "Erst die Uhr stoppen, dann den laufenden Eintrag bearbeiten",
	"Dependency '%s' is done":                         "Abhängigkeit '%s' ist erledigt",
	"Dependency '%s' is hidden by the current filter": "Abhängigkeit '%s' ist vom aktuellen Filter verdeckt",
	"Dependency no longer exists":                     "Abhängigkeit existiert nicht mehr",

	// ── Help: quick-add tokens (the token itself stays English — it is syntax) ──
	"add a tag (existing tags are suggested; tab inserts)": "Schlagwort setzen (Vorschläge; tab fügt ein)",
	"put it in a project":                             "einem Projekt zuordnen",
	"set a due date (see Date input below)":           "Fälligkeit setzen (siehe Datumseingabe)",
	"priority: %s / %s / %s (p:h, p:m, p:l)":          "Priorität: %s / %s / %s (p:h, p:m, p:l)",
	"size: s / m / l (also %s)":                       "Größe: s / m / l (auch %s)",
	"repeat: %s / %s / %s / %s / %s":                  "Wiederholung: %s / %s / %s / %s / %s",
	"block on the last added task (or %s<id prefix>)": "auf zuletzt angelegte Aufgabe warten (oder %s<ID-Präfix>)",

	// ── Help: search filters ──
	"only tasks carrying the tag":                                       "nur Aufgaben mit dem Schlagwort",
	"only tasks in the project":                                         "nur Aufgaben im Projekt",
	"only that priority":                                                "nur diese Priorität",
	"due before a date (also >, <=, >= and an exact date)":              "fällig vor einem Datum (auch >, <=, >= und ein genaues Datum)",
	"only overdue tasks":                                                "nur überfällige Aufgaben",
	"anything else fuzzy-matches the title, or the description as text": "alles andere trifft den Titel unscharf oder die Beschreibung als Text",

	// ── Help: row symbols and scroll hints ──
	"Row symbols":                "Zeilensymbole",
	"ready to start (ST column)": "bereit (Spalte ST)",
	"in progress: time has been logged against it (ST column)": "in Arbeit: es wurde Zeit erfasst (Spalte ST)",
	"overdue (ST column)": "überfällig (Spalte ST)",
	"done (ST column)":    "erledigt (Spalte ST)",
	"blocked: waiting on an unfinished dependency; sorts last": "blockiert: wartet auf offene Abhängigkeit; sortiert zuletzt",
	"timer running":                 "Uhr läuft",
	"recurring task":                "wiederkehrende Aufgabe",
	"subtasks done / total":         "Teilaufgaben fertig / gesamt",
	"subtasks collapsed / expanded": "Teilaufgaben ein-/ausgeklappt",
	"score lifted by a subtask or by work waiting on it (detail pane)": "Score angehoben durch eine Teilaufgabe oder wartende Arbeit (Detailbereich)",
	"blocked: waiting on an unfinished dependency":                     "blockiert: wartet auf offene Abhängigkeit",
	"blocked: waiting on an unfinished dependency (ST column)":         "blockiert: wartet auf offene Abhängigkeit (Spalte ST)",
	"others depend on this: finishing it unblocks them":                "andere hängen daran: Abschluss gibt sie frei",

	// ── Help: the status line ──
	"Status line": "Statuszeile",
	"background sync is failing: Settings has the error": "Hintergrund-Sync schlägt fehl: der Fehler steht in den Einstellungen",
	"the focus filter is on: today + overdue only":       "der Fokusfilter ist an: nur heute + überfällig",
	"a filter is narrowing the list":                     "ein Filter schränkt die Liste ein",
	"↑ scroll up":                                        "↑ nach oben",
	"↓ scroll down":                                      "↓ nach unten",
	"↑/↓ scroll":                                         "↑/↓ scrollen",
	"FOCUS":                                              "FOKUS",

	// ── Unchanged in Danish, listed so the completeness check stays honest ──
	"Score":  "Punkte",
	"Score:": "Punkte:",
	"score":  "Punkte",
	"ID:":    "ID:",
	" ◉":     " ◉",
	"%s  (%sD %s · P %s · M %s · S %s · A %s)": "%s  (%sF %s · P %s · M %s · G %s · A %s)",
	"starts %s": "beginnt %s",

	// ── Bias levels (shown in the ‹ … › pickers) ──
	"relaxed":  "gelassen",
	"balanced": "ausgewogen",
	"intense":  "intensiv",

	// ── Why this rank (the explain overlay and `tjek why`) ──
	"Why this rank":              "Warum dieser Rang",
	"Deadline":                   "Frist",
	"Momentum":                   "Momentum",
	"Total":                      "Gesamt",
	"#%d of %d by sequence · %s": "#%d von %d nach Sequenz · %s",
	"%s = %.1f of the %.1f points the top task scores right now":          "%s = %.1f von den %.1f Punkten, die die höchste Aufgabe gerade erreicht",
	"%s · not in the ranking (subtasks rank with their parent)":           "%s · nicht in der Rangliste (Teilaufgaben zählen bei ihrer Hauptaufgabe)",
	"done: done tasks score 0 and leave the ranking":                      "erledigt: erledigte Aufgaben zählen 0 und verlassen die Rangliste",
	"ranked on %.1f, lifted by a subtask or by work waiting on it":        "eingestuft mit %.1f, angehoben durch eine Teilaufgabe oder durch wartende Arbeit",
	"starts %s, so it ranks below the work you can start today":           "beginnt am %s, daher eingestuft unter der Arbeit, die du heute anfangen kannst",
	"the list is on another sort right now; this is the Sequence ranking": "die Liste ist gerade anders sortiert; dies ist die Sequenz-Rangliste",
	"%.1f points short of #%d %s":                                         "%.1f Punkte fehlen zu #%d %s",
	"%.1f points clear of #%d %s":                                         "%.1f Punkte Vorsprung vor #%d %s",
	"Moves on its own:":                                                   "Ändert sich von selbst:",
	"Moves on its own, with no edit from you:":                            "Ändert sich von selbst, ganz ohne dein Zutun:",
	"nothing in the next three days; this position is stable":             "nichts in den nächsten drei Tagen; die Position ist stabil",
	"momentum runs out":                                                   "Momentum läuft aus",
	"the deadline ramp steps a day closer":                                "die Fristenrampe rückt einen Tag näher",
	"still #%d":                                                           "weiter #%d",
	"unranked":                                                            "ohne Rang",
	"in %dm":                                                              "in %dm",
	"in %dh":                                                              "in %dh",
	"in %dd":                                                              "in %dT",
	"esc or w to close  ·  tune the weights in Settings → Sequencer":      "esc oder w schließt  ·  Gewichte in Einstellungen → Sequenzierung",

	// ── Why this rank: the sentence behind each score factor ──
	"no due date":     "kein Fälligkeitsdatum",
	"due today":       "heute fällig",
	"due tomorrow":    "morgen fällig",
	"1 day overdue":   "1 Tag überfällig",
	"%d days overdue": "%d Tage überfällig",
	"due in %d days; the ramp adds points daily":      "fällig in %d Tagen; die Rampe gibt täglich Punkte",
	"due in %d days, further out than the 7-day ramp": "fällig in %d Tagen, weiter weg als die 7-Tage-Rampe",
	"priority is %s": "Priorität ist %s",
	"size is %s":     "Größe ist %s",
	"you worked on this task in the last 48h":  "du hast in den letzten 48 h daran gearbeitet",
	"project @%s saw activity in the last 48h": "Projekt @%s hatte in den letzten 48 h Aktivität",
	"tag #%s saw activity in the last 48h":     "Schlagwort #%s hatte in den letzten 48 h Aktivität",
	"nothing here was touched in the last 48h": "hier wurde in den letzten 48 h nichts angefasst",
	"created today":                     "heute erstellt",
	"created 1 day ago":                 "vor 1 Tag erstellt",
	"created %d days ago":               "vor %d Tagen erstellt",
	"aging is switched off in Settings": "Alterung ist in den Einstellungen abgeschaltet",

	// ── Keymap descriptions the footer and help overlay render ──
	"jump to ends / page through list":          "an den Rand / seitenweise blättern",
	"toggle this help":                          "diese Hilfe umschalten",
	"add task (#tag due:date p:high @proj s:M)": "Aufgabe anlegen (#tag fällig:datum p:hoch @proj s:M)",
	"add manual time entry":                     "Zeiteintrag von Hand",
	"start/stop subtask timer":                  "Uhr der Teilaufgabe starten/stoppen",
	"back to list":                              "zurück zur Liste",
	"merge tags (Tags tab)":                     "Schlagwörter vereinen (Reiter Schlagwörter)",
	"select entry":                              "Eintrag wählen",
	"back":                                      "zurück",
	"cycle activity range":                      "Zeitraum wechseln",
	"scroll the summary":                        "Übersicht blättern",
	"change value / theme":                      "Wert / Farbschema ändern",
	"set / clear due date":                      "Fälligkeit setzen / löschen",

	// ── Help section titles ──
	"Calendar": "Kalender",
	"Stats":    "Statistik",

	// ── Short labels (the terse footer hint on a narrow window) ──
	"add":   "neu",
	"done":  "fertig",
	"track": "Zeit",
	"del":   "weg",
	"sort":  "Sort.",

	// Board card-view footer, kept apart so its long key does not realign the table.
	"↑/↓ previous/next card · enter edit in Tasks · esc close": "↑/↓ vorige/nächste Karte · Enter in Aufgaben bearbeiten · Esc schließen",
}
