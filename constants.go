package main

import "time"

// ── Layout & rendering constants ──────────────────────────────────────────────

const (
	detailMaxHeightPct = 55
	overlayWidthPct    = 60

	// cursorMark is the one marker for "the row you are on", and cursorGap the
	// blank of the same width for the rows you are not. Every list the app
	// draws uses them — tags, projects, the calendar timeline, every section
	// of the detail pane, board cards, the Settings rows, the pickers and the
	// palette. Task rows (active, subtask and history) are the exception: the
	// selected one is highlighted across the pane's full width, which says
	// "you are here" on its own, and their gutter holds the subtask fold sign.
	//
	// They were once three different glyphs (▶ in the lists, > on the board, →
	// in Settings and the pickers), which read as three different kinds of
	// selection rather than one idea drawn three ways. ▶ won on numbers and on
	// one hard fact: → is already the medium-priority icon (todo.Priority.Icon),
	// so a → cursor would sit in the same row as a → that means something else.
	cursorMark = "▶ "
	cursorGap  = "  "

	// headerHintGap is the least blank space between the tab bar and the "?"
	// in the header, so the hint reads as its own thing rather than as another
	// tab. The bar's width budget subtracts it, which is what keeps a bar that
	// exactly fills its budget from costing the hint a column it needed.
	headerHintGap = 2

	// detailScrollMargin is how many rendered lines of the detail document stay
	// visible past the cursor before the pane scrolls. It is what makes the
	// scroll gradual: the window holds still while the cursor moves inside it
	// and then follows by a line at a time, instead of re-anchoring under a
	// cursor pinned to a fixed row. On a pane too short to hold both margins
	// the two clamps meet and it degrades to the centred anchor.
	detailScrollMargin = 2

	// Shared "name" column (task title, project name, tag) so the
	// gap before the next column follows one rule on every list tab and all four
	// reflow identically on resize.
	nameColWidthPct = 30
	nameColMinWidth = 20
	nameColMaxWidth = 50

	// Activity-chart bar height: never squashed below the floor even on a
	// short window, never past the ceiling even on a tall one (statsGradient
	// runs out around there, and the stats list wants the rows more).
	//
	// The floor is what a chart costs when there is barely anything to plot.
	// At five it spent four empty rows above a single block on any quiet week
	// — a chart that is mostly the space where bars would go, if there were
	// any. Three keeps the shape readable and gives the rest back.
	statsChartMinH = 3
	statsChartMaxH = 12
	// statsChartRowsPerTaskMax caps how tall one task's block grows when a
	// quiet range has the height to spare: past four rows a block stops
	// reading as one of a stack.
	statsChartRowsPerTaskMax = 4
	// statsChartMinTermH is the shortest window the Stats tab draws its
	// Activity chart in; see statsChartShown.
	statsChartMinTermH = 30

	minGanttBarWidth = 10
	maxGanttBarWidth = 60
	minOverlayWidth  = 50
	minTitleColWidth = 20
	minInnerWidth    = 20

	// Tasks-tab side-by-side layout: at or above
	// sideBySideMinWidth the list keeps full height on the left and the detail
	// pane becomes an always-on preview column on the right; below it the tab
	// falls back to its stacked layout. The detail column takes sideDetailColPct
	// of the content width, clamped so neither column gets unusably narrow.
	sideBySideMinWidth = 110
	// projStripMinWidth is the narrowest pane that holds the project timeline
	// beside its task list (the timeline's floor, the list's floor and the
	// gap between them). Below it the timeline is dropped and the list takes
	// the pane, the way the Calendar drops its month grid: two floored columns
	// joined on a narrow window is how lines end up wider than the terminal.
	projStripMinWidth = sideDetailColMin + minInnerWidth + projStripGap
	projStripGap      = 3
	sideDetailColPct  = 38
	sideDetailColMin  = 36
	sideDetailColMax  = 56

	commentPrefixLen    = 22
	detailLabelColWidth = 14

	// Release lookup / self-update. The asset cap is generous next to a ~20 MB
	// binary but bounded, so a wrong URL can't stream forever; the download
	// timeout has to cover a slow connection pulling those megabytes.
	releaseAPITimeout      = 15 * time.Second
	releaseDownloadTimeout = 10 * time.Minute
	maxReleaseJSONBytes    = 1 << 20  // 1 MB of release JSON is already absurd
	maxReleaseAssetBytes   = 96 << 20 // 96 MB

	// Local Whisper model download (localwhisper.go). The size cap sits
	// just above the known file (574,041,195 bytes for
	// ggml-large-v3-turbo-q5_0.bin, verified by hand against the real
	// download) rather than a round number far beyond it — a wrong URL or
	// a swapped file should fail well before streaming most of a 600MB
	// response.
	whisperModelDownloadTimeout = 30 * time.Minute
	maxWhisperModelBytes        = 600 << 20 // 600 MB

	maxDepSearchResults  = 5
	maxTagSearchResults  = 5
	maxProjSearchResults = 5

	// maxPaletteResults is how many command-palette rows are shown at once. The
	// palette replaces the footer hint while it is open, so the budget is what
	// fits there comfortably rather than the whole list.
	maxPaletteResults = 7

	// Quick-add completions render as one chip row under the input, so the cap
	// is what fits a line comfortably rather than the pickers' window height.
	maxQuickAddSuggestions = 5

	// listColGap is the gap between any two columns of the Tasks list — the only
	// spacing number in the layout. Every column is sized by hugColW as "the
	// wider of its header and its widest value, plus this", so no column is ever
	// padded for a value it does not have. Two is what separates two columns
	// legibly; the cells it saves go to the title and the tags, which are the
	// columns whose content is actually open-ended.
	listColGap = 2

	// scoreValW is the widest Score value: "100%". The header is wider, so it is
	// the header that sizes the column — but a shorter translated header must
	// not clip the values, which is what this floor is for. Values are
	// right-aligned in the field: they are numbers, and left-aligning them
	// floated the % sign one cell left on every two-digit score.
	scoreValW = 4

	// dueValMaxW caps the Due column's value field at the full-date worst case,
	// "DD-MM-YY". Below it the column hugs whatever this frame's widest due
	// actually renders to, so an all-"3d" list costs three cells and not eight.
	dueValMaxW = 8

	// projectColCompactW is the Project column's compact baseline on the Tasks
	// list. At wider widths the column can grow past this to reveal the full
	// project name; narrow layouts retain the familiar compact footprint.
	projectColCompactW = 14

	// tagsOverflowMinW is the smallest Tags cell worth keeping: a leading gap
	// plus the "+N" count that stands in for the chips that did not fit. A row
	// that has tags always gets at least this much, because "there are two more
	// tags here" is a different statement from "this task has no tags", and the
	// list cannot make it in fewer cells.
	tagsOverflowMinW = 3

	// tagClipMinW is the narrowest a clipped tag is drawn: "#ab…", the sigil,
	// two letters and the ellipsis. Anything shorter names no tag, so the cell
	// falls back to the bare count.
	tagClipMinW = 4

	// groupBarWidth is the progress bar under a tag or project's name in the
	// pane below its list: small, since the counts beside it carry the numbers.
	groupBarWidth = 20

	// barTrack is the glyph the unfilled part of a progress bar is drawn with:
	// blank cells on a faint background tint (barTrackStyle). Any visible glyph
	// here turns a mostly empty bar into texture; a shade keeps the bar's
	// extent visible while an empty bar reads as empty.
	barTrack = " "

	// tagsReservePct caps how much of the pane the Tags column may hold back
	// from the task titles when the title column grows into spare width.
	//
	// Reserving the widest tag row's full width let one tag-heavy task clip
	// every title in the list, and the reserved cells then went unused anyway,
	// because the chips still did not fit and collapsed to a marker. The title
	// is the primary read, so it takes what it needs first; the cap only bites
	// when space is tight, which is exactly when the title needs it. Nothing is
	// lost to the cap either — the tags cell is sized at render time from
	// whatever the row has left, so width the title did not claim still ends up
	// in the chips.
	tagsReservePct = 20

	footerHeight   = 1
	minHeaderLines = 2
	// Two border rows plus the shared blank row below a pane's border title.
	detailBorderLines = 3
	minListPanelLines = 3
	minDetailHeight   = 3
	minListHeight     = 1

	statsBarWidth   = 30
	statsLabelWidth = 23
	// unsetMark stands in for an empty detail-pane field or section.
	unsetMark = "-"
	// statsOldestMinW is the fewest title runes the Stats "Oldest active" row
	// accepts before the page drops to fewer columns to show more of it.
	statsOldestMinW = 12
	statsValueWidth = 12

	calPanelWidth = 22

	// calSideBySideMinWidth is the narrowest window that fits the fixed-width
	// month grid beside a usable day timeline (grid + timeline floor + the
	// borders/gap between them). Below it buildCalendarContent drops the grid
	// rather than emitting lines wider than the terminal.
	calSideBySideMinWidth = calPanelWidth + minInnerWidth + 4

	// syncStatusMaxLines caps the wrapped sync-status footer in Settings. Four
	// lines is enough for the longest message the sync path produces (a
	// version gap plus the server's own words) at half-pane width, and the cap
	// is what keeps a pathological server body from pushing the settings rows
	// off the pane.
	syncStatusMaxLines = 4
)
