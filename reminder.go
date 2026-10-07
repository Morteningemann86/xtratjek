package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/todo"
)

// The daily reminder: once a day, at the time set in Settings, a desktop
// notification lists what is due today and what is overdue. The TUI checks on
// a minute tick; `tjek remind` runs the same check for a scheduler (cron, a
// systemd timer, Task Scheduler) so the reminder also arrives while the TUI is
// closed. Both record the day in a per-device sidecar, so a device reminds
// once a day however many of them are running.
//
// Due dates are calendar days, so the reminder is a time of day rather than a
// per-task alarm.

const (
	// defaultReminderAt is 09:00, in minutes after midnight. An unset
	// settings.json reminds, so the feature is found by the people who
	// never open Settings; Off is one ← away.
	defaultReminderAt = 9 * 60
	reminderOff       = -1
	// reminderTickInterval is how late a reminder can be. The check reads the
	// wall clock on every tick, so a laptop waking from sleep past the time
	// reminds on its next tick rather than waiting for a timer that slept too.
	reminderTickInterval = time.Minute
	// reminderListMax caps the titles in the notification body; the heading
	// carries the full counts.
	reminderListMax   = 5
	reminderTitleMax  = 60
	reminderDayLayout = "2006-01-02"
)

type reminderTickMsg struct{ at time.Time }

// reminderSentMsg reports the desktop notification's outcome to the TUI, with
// the reminder's heading so a failed pop-up can still show it in the app.
type reminderSentMsg struct {
	title string
	err   error
}

func reminderTick() tea.Cmd {
	return tea.Tick(reminderTickInterval, func(t time.Time) tea.Msg { return reminderTickMsg{at: t} })
}

// reminderFromSettings reads settings.json "reminder": "HH:MM", "off", or
// absent for the default. A value that does not parse reads as the default,
// as every other unknown setting value does.
func reminderFromSettings(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return defaultReminderAt
	case "off":
		return reminderOff
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return defaultReminderAt
	}
	return t.Hour()*60 + t.Minute()
}

// formatReminder is the settings.json form of a reminder time.
func formatReminder(at int) string {
	if at < 0 {
		return "off"
	}
	return fmt.Sprintf("%02d:%02d", at/60, at%60)
}

// storedReminder reads the reminder's time and switch from settings: the
// time always a real one, so switching the reminder back on finds it.
func storedReminder(s appSettings) (at int, on bool) {
	at, on = reminderFromSettings(s.Reminder), !s.ReminderOff
	if at == reminderOff {
		at, on = defaultReminderAt, false
	}
	return at, on
}

// reminderChoices are the stops ←/→ step through on the Settings time row:
// every hour of a waking day. A hand-edited time between two stops steps to
// the neighbouring one.
func reminderChoices() []int {
	var out []int
	for h := 5; h <= 22; h++ {
		out = append(out, h*60)
	}
	return out
}

// nextReminder steps the reminder time by dir (+1 later, -1 earlier), wrapping
// from the last hour to the first and back.
func nextReminder(at, dir int) int {
	choices := reminderChoices()
	if dir > 0 {
		for _, c := range choices {
			if c > at {
				return c
			}
		}
		return choices[0]
	}
	for i := len(choices) - 1; i >= 0; i-- {
		if choices[i] < at {
			return choices[i]
		}
	}
	return choices[len(choices)-1]
}

// reminderDue reports whether today's reminder is owed at now: it is switched
// on, the time has come, and this device has not reminded today.
func reminderDue(now time.Time, at int, remindedOn string) bool {
	if at < 0 || remindedOn == now.Format(reminderDayLayout) {
		return false
	}
	return now.Hour()*60+now.Minute() >= at
}

// reminderTasks picks the pending tasks a reminder at now covers: the overdue
// ones, oldest deadline first, and the ones due today, highest priority first.
func reminderTasks(tasks []*todo.Todo, now time.Time) (overdue, today []*todo.Todo) {
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	for _, t := range tasks {
		if t.Deleted || t.Status == todo.Done || t.DueDate.IsZero() {
			continue
		}
		switch {
		case t.IsOverdueAt(now):
			overdue = append(overdue, t)
		case t.DueDate.Before(tomorrow):
			today = append(today, t)
		}
	}
	sort.Slice(overdue, func(i, j int) bool { return lessByDueDate(overdue[i], overdue[j]) })
	sort.Slice(today, func(i, j int) bool {
		if today[i].Priority != today[j].Priority {
			return today[i].Priority > today[j].Priority
		}
		return lessByDueDate(today[i], today[j])
	})
	return overdue, today
}

// reminderMessage is the notification: the counts as its heading, then the
// titles, overdue first.
func reminderMessage(overdue, today []*todo.Todo) (title, body string) {
	var counts []string
	if n := len(today); n > 0 {
		counts = append(counts, fmt.Sprintf(tr("%d due today"), n))
	}
	if n := len(overdue); n > 0 {
		counts = append(counts, trCount("%d overdue", n, n))
	}
	title = "tjek: " + strings.Join(counts, ", ")

	all := append(append([]*todo.Todo(nil), overdue...), today...)
	var lines []string
	for i, t := range all {
		if i == reminderListMax {
			lines = append(lines, strings.TrimSpace(fmt.Sprintf(tr("  … and %d more"), len(all)-i)))
			break
		}
		lines = append(lines, "• "+truncate(t.Title, reminderTitleMax))
	}
	return title, strings.Join(lines, "\n")
}

// The sidecar holding the day this device last reminded. It is not a
// settings.json field because the TUI and `tjek remind` both write it, and
// neither should rewrite the other's settings to do so.
func remindedPath() string {
	return paths.For(paths.State, "reminded")
}

func loadRemindedOn() string {
	b, err := os.ReadFile(remindedPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveRemindedOn(day string) {
	if _, err := paths.Ensure(paths.State); err != nil {
		return
	}
	_ = writeFileAtomic(remindedPath(), []byte(day+"\n"), 0o644)
}

// settleReminderAtLaunch counts a launch after the reminder time as today's
// reminder: the list the user is opening already shows what is due, so a
// notification on top of it would only repeat the screen.
func (m *model) settleReminderAtLaunch(now time.Time) {
	if reminderDue(now, m.reminderTime(), m.remindedOn) {
		m.remindedOn = now.Format(reminderDayLayout)
		saveRemindedOn(m.remindedOn)
	}
}

// checkReminder is the tick's work: when today's reminder is owed, record the
// day, flash the heading in the app and return the command that sends the
// desktop notification. flashed says whether the toast needs clearing. A day
// with nothing due is recorded too, so a task given today's date later in the
// day does not set off a reminder at an odd hour.
func (m *model) checkReminder(now time.Time) (send tea.Cmd, flashed bool) {
	if !reminderDue(now, m.reminderTime(), m.remindedOn) {
		return nil, false
	}
	day := now.Format(reminderDayLayout)
	m.remindedOn = day
	overdue, today := reminderTasks(m.Store.allTodos(), now)
	if len(overdue)+len(today) == 0 {
		return func() tea.Msg {
			saveRemindedOn(day)
			return nil
		}, false
	}
	title, body := reminderMessage(overdue, today)
	m.flashInfo(title)
	return func() tea.Msg {
		saveRemindedOn(day)
		return reminderSentMsg{title: title, err: sendDesktopNotification(title, body)}
	}, true
}

// reminderTime is when today's reminder is due, or reminderOff when the
// Settings switch is off.
func (m model) reminderTime() int {
	if !m.reminderOn {
		return reminderOff
	}
	return m.reminderAt
}

// toggleReminder is the "Daily reminder" row: on or off, the time kept.
func (m *model) toggleReminder() {
	m.reminderOn = !m.reminderOn
	m.persistSettings()
}

// cycleReminder is the "Reminder time" row's ←/→.
func (m *model) cycleReminder(dir int) {
	m.reminderAt = nextReminder(m.reminderAt, dir)
	m.persistSettings()
}
