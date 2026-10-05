package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// ── Theme ───────────────────────────────────────────────────────────────────
//
// A theme is the semantic palette the app is built from. All the xxxStyle vars
// below are (re)built by applyTheme, so switching theme is just a matter of
// calling applyTheme with a different palette. The hand-tuned gradients further
// down stay fixed (palette-only theming).

type theme struct {
	name string

	accent   lipgloss.Color // primary accent — titles, labels, input border
	green    lipgloss.Color
	orange   lipgloss.Color
	purple   lipgloss.Color
	purpleLt lipgloss.Color
	yellow   lipgloss.Color
	yellowLt lipgloss.Color
	blue     lipgloss.Color
	teal     lipgloss.Color
	red      lipgloss.Color

	fg   lipgloss.Color // primary text
	dim  lipgloss.Color // borders, completed/muted text
	help lipgloss.Color // hint text
	bg   lipgloss.Color // dark base — used as fg on colored backgrounds
	sel  lipgloss.Color // selected-row background — subtle, must keep every row foreground readable
}

// themes holds the built-in palettes. The first entry is the default.
var themes = []theme{
	{
		name:     "TokyoNight",
		accent:   "#FF6E9C",
		green:    "#A8FF78",
		orange:   "#FF9E64",
		purple:   "#d480f0",
		purpleLt: "#e8a0ff",
		yellow:   "#FFE66D",
		yellowLt: "#FFF5A0",
		blue:     "#78D4FF",
		teal:     "#5EEAD4",
		red:      "#FF0000",
		fg:       "#FFFFFF",
		dim:      "#555555",
		help:     "#888888",
		bg:       "#1a1a1a",
		sel:      "#292e42",
	},
	{
		name:   "Catppuccin",
		accent: "#cba6f7",
		green:  "#a6e3a1",
		orange: "#fab387",
		// purple is reused for tag chips, so we pick Catppuccin Pink rather
		// than Mauve — Mauve is what `accent` already uses for titles, which
		// made tags and titles indistinguishable.
		purple:   "#f5c2e7",
		purpleLt: "#fdd6ed",
		yellow:   "#f9e2af",
		yellowLt: "#faf0c6",
		blue:     "#89b4fa",
		teal:     "#94e2d5",
		red:      "#f38ba8",
		fg:       "#cdd6f4",
		dim:      "#585b70",
		help:     "#6c7086",
		bg:       "#1e1e2e",
		sel:      "#313244",
	},
	{
		name:     "Gruvbox",
		accent:   "#fabd2f",
		green:    "#b8bb26",
		orange:   "#fe8019",
		purple:   "#d3869b",
		purpleLt: "#e0a0b4",
		yellow:   "#fabd2f",
		yellowLt: "#ffe5a0",
		blue:     "#83a598",
		teal:     "#8ec07c",
		red:      "#fb4934",
		fg:       "#ebdbb2",
		dim:      "#665c54",
		help:     "#928374",
		bg:       "#1d2021",
		sel:      "#3c3836",
	},
	{
		name:     "Nord",
		accent:   "#88c0d0",
		green:    "#a3be8c",
		orange:   "#d08770",
		purple:   "#b48ead",
		purpleLt: "#c8a0c0",
		yellow:   "#ebcb8b",
		yellowLt: "#f0dcad",
		blue:     "#81a1c1",
		teal:     "#8fbcbb",
		red:      "#bf616a",
		fg:       "#eceff4",
		dim:      "#4c566a",
		help:     "#616e88",
		bg:       "#2e3440",
		sel:      "#3b4252",
	},
}

func themeByName(name string) theme {
	for _, t := range themes {
		if t.name == name {
			return t
		}
	}
	return themes[0]
}

// currentTheme holds the active palette so rendering helpers (e.g.
// withBorderTitle) can read theme colors without receiving them as parameters.
// Set by applyTheme on every theme switch.
var currentTheme theme

// ── Styles (assigned by applyTheme) ───────────────────────────────────────────

var (
	titleStyle lipgloss.Style

	tabTasksActiveStyle    lipgloss.Style
	tabProjectsActiveStyle lipgloss.Style
	tabTagsActiveStyle     lipgloss.Style
	tabBoardActiveStyle    lipgloss.Style
	tabStatsActiveStyle    lipgloss.Style
	tabCalendarActiveStyle lipgloss.Style
	tabSettingsActiveStyle lipgloss.Style
	tabMeetingsActiveStyle lipgloss.Style

	tabTasksInactiveStyle    lipgloss.Style
	tabProjectsInactiveStyle lipgloss.Style
	tabTagsInactiveStyle     lipgloss.Style
	tabBoardInactiveStyle    lipgloss.Style
	tabStatsInactiveStyle    lipgloss.Style
	tabCalendarInactiveStyle lipgloss.Style
	tabSettingsInactiveStyle lipgloss.Style
	tabMeetingsInactiveStyle lipgloss.Style

	selectedStyle   lipgloss.Style
	normalStyle     lipgloss.Style
	overdueStyle    lipgloss.Style
	depOverdueStyle lipgloss.Style
	helpStyle       lipgloss.Style

	// Row-selected variants: base foreground plus the theme's sel background,
	// so the cursor row stays visible even when a status colour
	// (overdue/dep-overdue/timer) owns the foreground.
	selectedRowStyle           lipgloss.Style
	selectedOverdueRowStyle    lipgloss.Style
	selectedDepOverdueRowStyle lipgloss.Style
	selectedTimerRowStyle      lipgloss.Style

	detailLabelStyle    lipgloss.Style
	detailValueStyle    lipgloss.Style
	detailSelectedStyle lipgloss.Style

	inputStyle   lipgloss.Style
	confirmStyle lipgloss.Style
	searchStyle  lipgloss.Style
	dimStyle     lipgloss.Style
	// barTrackStyle paints the unfilled part of a progress bar: a background
	// a small step from the base towards dim — see barTrack.
	barTrackStyle lipgloss.Style
	// selectedDimRowStyle is the dim tone carrying the selection background, so
	// a selected row's secondary columns stay secondary instead of jumping to
	// full strength the moment the cursor lands on them.
	selectedDimRowStyle lipgloss.Style

	// Toast styles by kind (see toastKind / renderStatusLine). Error reuses the
	// red confirm colour; success is green, info a calm blue.
	toastErrorStyle   lipgloss.Style
	toastSuccessStyle lipgloss.Style
	toastInfoStyle    lipgloss.Style

	// Fixed status-line pieces: filter chips on the left, the sync-health
	// glyph on the right. See renderStatusLine.
	focusChipStyle  lipgloss.Style
	searchChipStyle lipgloss.Style
	syncFailStyle   lipgloss.Style

	listPanelStyle   lipgloss.Style
	detailPanelStyle lipgloss.Style
	// Focused variants: accent border marking the pane that owns keystrokes,
	// shown whenever two panes are visible at once.
	listPanelFocusedStyle   lipgloss.Style
	detailPanelFocusedStyle lipgloss.Style

	ganttTodayStyle lipgloss.Style
	ganttDoneStyle  lipgloss.Style
	checkDoneStyle  lipgloss.Style
	headerStyle     lipgloss.Style

	tagStyle                lipgloss.Style
	tagSelectedStyle        lipgloss.Style
	taskTagSelectedRowStyle lipgloss.Style

	overdueCountStyle lipgloss.Style
	activeCountStyle  lipgloss.Style
	doneCountStyle    lipgloss.Style

	pageIndicatorStyle lipgloss.Style

	statsHeaderStyle lipgloss.Style
	statsAxisStyle   lipgloss.Style // brighter than dim — used for weekday/week labels under bars
	timerStyle       lipgloss.Style

	calSelectedDayStyle lipgloss.Style
	calTodayStyle       lipgloss.Style

	projLabelStyle lipgloss.Style
)

// applyTheme rebuilds every style from the given palette. Call at startup and
// whenever the user switches theme.
func applyTheme(t theme) {
	currentTheme = t
	activeTab := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Bold(true).Foreground(t.bg).Background(c).Padding(0, 1)
	}
	inactiveTab := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Padding(0, 1)
	}

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(t.accent)

	tabTasksActiveStyle = activeTab(t.green)
	tabProjectsActiveStyle = activeTab(t.orange)
	tabTagsActiveStyle = activeTab(t.purple)
	tabBoardActiveStyle = activeTab(t.yellow)
	tabStatsActiveStyle = activeTab(t.blue)
	tabCalendarActiveStyle = activeTab(t.teal)
	tabSettingsActiveStyle = activeTab(t.accent)
	tabMeetingsActiveStyle = activeTab(t.red)

	tabTasksInactiveStyle = inactiveTab(t.green)
	tabProjectsInactiveStyle = inactiveTab(t.orange)
	tabTagsInactiveStyle = inactiveTab(t.purple)
	tabBoardInactiveStyle = inactiveTab(t.yellow)
	tabStatsInactiveStyle = inactiveTab(t.blue)
	tabCalendarInactiveStyle = inactiveTab(t.teal)
	tabSettingsInactiveStyle = inactiveTab(t.accent)
	tabMeetingsInactiveStyle = inactiveTab(t.red)

	selectedStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)
	normalStyle = lipgloss.NewStyle().Foreground(t.fg)
	overdueStyle = lipgloss.NewStyle().Foreground(t.red).Bold(true)
	depOverdueStyle = lipgloss.NewStyle().Foreground(t.orange).Bold(true)
	helpStyle = lipgloss.NewStyle().Foreground(t.help)

	selectedRowStyle = selectedStyle.Background(t.sel)
	selectedOverdueRowStyle = overdueStyle.Background(t.sel)
	selectedDepOverdueRowStyle = depOverdueStyle.Background(t.sel)

	detailLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(t.accent)
	detailValueStyle = lipgloss.NewStyle().Foreground(t.fg)
	detailSelectedStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)

	// The boxed footer fields carry the panels' MarginLeft(2) so their borders
	// sit in the same two columns as the pane above them — without it the box
	// is inset two columns to the left of everything it appears under.
	inputStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.accent).Padding(0, 1).MarginLeft(2)
	confirmStyle = lipgloss.NewStyle().Foreground(t.red).Bold(true)
	toastErrorStyle = lipgloss.NewStyle().Foreground(t.red).Bold(true)
	toastSuccessStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)
	toastInfoStyle = lipgloss.NewStyle().Foreground(t.blue).Bold(true)
	searchStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.green).Padding(0, 1).MarginLeft(2)
	dimStyle = lipgloss.NewStyle().Foreground(t.dim)
	barTrackStyle = lipgloss.NewStyle().Background(mixHex(t.bg, t.dim, 0.3))
	selectedDimRowStyle = dimStyle.Background(t.sel)

	focusChipStyle = lipgloss.NewStyle().Bold(true).Foreground(t.bg).Background(t.orange).Padding(0, 1)
	searchChipStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)
	syncFailStyle = lipgloss.NewStyle().Foreground(t.red).Bold(true)

	// Leave one quiet row below the border title before the pane's first
	// heading or field. Padding only at the top keeps related rows compact and
	// gives list and detail panes the same visual rhythm.
	listPanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.dim).Padding(0, 1).PaddingTop(1).MarginLeft(2)
	detailPanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.dim).Padding(0, 1).PaddingTop(1).MarginLeft(2)
	listPanelFocusedStyle = listPanelStyle.BorderForeground(t.accent)
	detailPanelFocusedStyle = detailPanelStyle.BorderForeground(t.accent)

	ganttTodayStyle = lipgloss.NewStyle().Foreground(t.orange).Bold(true)
	ganttDoneStyle = lipgloss.NewStyle().Foreground(t.dim)
	checkDoneStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(t.accent)

	tagStyle = lipgloss.NewStyle().Foreground(t.purple).Bold(true)
	tagSelectedStyle = lipgloss.NewStyle().Foreground(t.purpleLt).Bold(true)
	taskTagSelectedRowStyle = tagStyle.Background(t.sel)

	overdueCountStyle = lipgloss.NewStyle().Foreground(t.red).Bold(true)
	activeCountStyle = lipgloss.NewStyle().Foreground(t.green).Bold(true)
	doneCountStyle = lipgloss.NewStyle().Foreground(t.dim)

	pageIndicatorStyle = lipgloss.NewStyle().Foreground(t.orange).Bold(true)

	statsHeaderStyle = lipgloss.NewStyle().Foreground(t.blue).Bold(true)
	// Axis labels (weekday names, "w42" week numbers) need to be legible at
	// a glance — t.fg keeps them in the palette's main foreground (whitish
	// in TokyoNight) instead of the structural-dim grey used for the baseline.
	statsAxisStyle = lipgloss.NewStyle().Foreground(t.fg)
	timerStyle = lipgloss.NewStyle().Foreground(t.teal).Bold(true)
	selectedTimerRowStyle = timerStyle.Background(t.sel)

	calSelectedDayStyle = lipgloss.NewStyle().Foreground(t.bg).Background(t.teal).Bold(true)
	calTodayStyle = lipgloss.NewStyle().Foreground(t.orange).Bold(true)

	projLabelStyle = lipgloss.NewStyle().Foreground(t.orange)

	// Capture the SGR prefix/suffix of the per-row styles for the fast render
	// path; must run after the styles above are (re)built.
	rebuildFastStyles()
}

// Ensure styles are always populated (e.g. in tests that render without
// constructing a full model). initialModel overrides this with the saved theme.
func init() { applyTheme(themes[0]) }

var ganttGradient = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#2a5a14")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#2e6a18")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#327a1c")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3a8c20")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3a9c22")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#42aa28")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#4ebc30")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#5ccc36")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#6cd642")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#7adf52")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#88e860")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#98f06c")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#a8f878")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#a8ff78")),
}

var ganttOverdueGradient = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#7a0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#8a0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#9b0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#aa0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#bc0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#ce0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#d40000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#da0000")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#de1111")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#e42222")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#e43333")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#ec4444")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#f45555")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#f86666")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#ff8888")),
}

// tagProgressGradient colours a group's progress bar, dark to light purple
// along the filled part (renderProgressBar).
var tagProgressGradient = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#1a0a2e")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#2d1b4e")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3d2060")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#5a2d8a")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#7a3aaa")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#9b4cc8")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#b865e0")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#d480f0")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#e8a0ff")),
}

var calGradient = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#1f4a40")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#2a6356")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#357c6c")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#409682")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#4bb098")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#56caae")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#5EEAD4")),
}

// 10 stops so the histogram has a unique gradient step per task when the
// chart halves block height (chartH=5 rows × 2 tasks/row = 10 tasks).
var statsGradient = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#1a3a5c")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#244b6e")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#2f5c80")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#396e92")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#447fa5")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#4e90b7")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#59a1c9")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#63b2db")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#6ec3ed")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#78d4ff")),
}

// mixHex mixes two #rrggbb colours, f=0 giving a and f=1 giving b. A colour
// that is not six-digit hex (a theme using ANSI numbers) is returned as b
// rather than guessed at.
func mixHex(a, b lipgloss.Color, f float64) lipgloss.Color {
	var ar, ag, ab, br, bg, bb int
	if _, err := fmt.Sscanf(string(a), "#%02x%02x%02x", &ar, &ag, &ab); err != nil {
		return b
	}
	if _, err := fmt.Sscanf(string(b), "#%02x%02x%02x", &br, &bg, &bb); err != nil {
		return b
	}
	mix := func(x, y int) int { return x + int(float64(y-x)*f+0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb)))
}
