package main

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
	"github.com/charmbracelet/x/ansi"
)

// reminderDay is the fixed "today" of these tests, in local time because due
// dates are local calendar days.
var reminderDay = time.Date(2026, 3, 10, 0, 0, 0, 0, time.Local)

func reminderAt(hour, minute int) time.Time {
	return reminderDay.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func dueTask(title string, due time.Time, p todo.Priority) todo.Todo {
	t := todo.New(title)
	t.DueDate = due
	t.Priority = p
	return t
}

// captureNotifications swaps the desktop notifier for a recorder.
func captureNotifications(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	orig := sendDesktopNotification
	sendDesktopNotification = func(title, body string) error {
		sent = append(sent, title+"\n"+body)
		return nil
	}
	t.Cleanup(func() { sendDesktopNotification = orig })
	return &sent
}

func TestReminderSettingRoundTrips(t *testing.T) {
	for in, want := range map[string]int{
		"":       defaultReminderAt,
		"off":    reminderOff,
		" OFF ":  reminderOff,
		"07:00":  7 * 60,
		"08:30":  8*60 + 30,
		"banana": defaultReminderAt,
	} {
		if got := reminderFromSettings(in); got != want {
			t.Errorf("reminderFromSettings(%q) = %d, want %d", in, got, want)
		}
	}
	for _, at := range []int{reminderOff, 5 * 60, 8*60 + 30, 22 * 60} {
		if got := reminderFromSettings(formatReminder(at)); got != at {
			t.Errorf("%d → %q → %d", at, formatReminder(at), got)
		}
	}
}

func TestReminderTimeStepsByTheHourAndWraps(t *testing.T) {
	cases := []struct{ at, dir, want int }{
		{9 * 60, 1, 10 * 60},
		{9 * 60, -1, 8 * 60},
		{8*60 + 30, 1, 9 * 60}, // a hand-edited time steps to the neighbouring hour
		{8*60 + 30, -1, 8 * 60},
		{22 * 60, 1, 5 * 60},
		{5 * 60, -1, 22 * 60},
	}
	for _, c := range cases {
		if got := nextReminder(c.at, c.dir); got != c.want {
			t.Errorf("nextReminder(%s, %d) = %s, want %s", formatReminder(c.at), c.dir, formatReminder(got), formatReminder(c.want))
		}
	}
}

func TestReminderDueOncePerDayAfterItsTime(t *testing.T) {
	today := reminderDay.Format(reminderDayLayout)
	cases := []struct {
		name      string
		now       time.Time
		at        int
		reminded  string
		wantOwing bool
	}{
		{"before the time", reminderAt(8, 59), 9 * 60, "", false},
		{"at the time", reminderAt(9, 0), 9 * 60, "", true},
		{"later that day", reminderAt(15, 0), 9 * 60, "2026-03-09", true},
		{"already reminded", reminderAt(15, 0), 9 * 60, today, false},
		{"switched off", reminderAt(15, 0), reminderOff, "", false},
	}
	for _, c := range cases {
		if got := reminderDue(c.now, c.at, c.reminded); got != c.wantOwing {
			t.Errorf("%s: reminderDue = %v, want %v", c.name, got, c.wantOwing)
		}
	}
}

func TestReminderCoversOverdueAndTodayOnly(t *testing.T) {
	now := reminderAt(9, 0)
	done := dueTask("finished", reminderDay, todo.PriorityHigh)
	done.Status = todo.Done
	deleted := dueTask("deleted", reminderDay, todo.PriorityHigh)
	deleted.Deleted = true
	tasks := []todo.Todo{
		dueTask("Today low", reminderDay, todo.PriorityLow),
		dueTask("Today high", reminderDay, todo.PriorityHigh),
		dueTask("Last week", reminderDay.AddDate(0, 0, -7), todo.PriorityLow),
		dueTask("Yesterday", reminderDay.AddDate(0, 0, -1), todo.PriorityHigh),
		dueTask("tomorrow", reminderDay.AddDate(0, 0, 1), todo.PriorityHigh),
		todo.New("no date"),
		done, deleted,
	}
	overdue, today := reminderTasks(todoPtrs(tasks), now)
	titles := func(ts []*todo.Todo) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Title)
		}
		return out
	}
	if got, want := titles(overdue), []string{"Last week", "Yesterday"}; !slices.Equal(got, want) {
		t.Errorf("overdue = %v, want %v (oldest deadline first)", got, want)
	}
	if got, want := titles(today), []string{"Today high", "Today low"}; !slices.Equal(got, want) {
		t.Errorf("today = %v, want %v (highest priority first)", got, want)
	}

	title, body := reminderMessage(overdue, today)
	if title != "tjek: 2 due today, 2 overdue" {
		t.Errorf("title = %q", title)
	}
	if want := "• Last week\n• Yesterday\n• Today high\n• Today low"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestReminderBodyCapsTheList(t *testing.T) {
	var tasks []todo.Todo
	for i := range reminderListMax + 3 {
		tasks = append(tasks, dueTask(strings.Repeat("x", i+1), reminderDay, todo.PriorityMedium))
	}
	overdue, today := reminderTasks(todoPtrs(tasks), reminderAt(9, 0))
	_, body := reminderMessage(overdue, today)
	lines := strings.Split(body, "\n")
	if len(lines) != reminderListMax+1 {
		t.Fatalf("body has %d lines, want %d titles and a count:\n%s", len(lines), reminderListMax, body)
	}
	if last := lines[len(lines)-1]; last != "… and 3 more" {
		t.Errorf("last line = %q", last)
	}
}

// The TUI's tick reminds once, at the set time, and records the day where
// `tjek remind` will see it.
func TestReminderTickNotifiesOncePerDay(t *testing.T) {
	sent := captureNotifications(t)
	m := modelWithTasks(t, dueTask("Pay rent", reminderDay, todo.PriorityHigh))
	m.reminderAt, m.remindedOn = 9*60, ""

	if send, _ := m.checkReminder(reminderAt(8, 59)); send != nil {
		t.Fatal("reminded before the set time")
	}
	send, flashed := m.checkReminder(reminderAt(9, 0))
	if send == nil || !flashed {
		t.Fatalf("no reminder at the set time (send=%v flashed=%v)", send != nil, flashed)
	}
	if msg, ok := send().(reminderSentMsg); !ok || msg.err != nil {
		t.Fatalf("send returned %#v", msg)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0], "Pay rent") {
		t.Fatalf("notifications = %q", *sent)
	}
	if !strings.Contains(m.err, "1 due today") {
		t.Errorf("toast = %q, want the reminder heading", m.err)
	}
	if got := loadRemindedOn(); got != "2026-03-10" {
		t.Errorf("reminded sidecar = %q", got)
	}
	if send, _ := m.checkReminder(reminderAt(9, 1)); send != nil {
		t.Error("reminded twice on one day")
	}
}

// Opening the app after the reminder time is the reminder: the list on screen
// already says what is due.
func TestReminderLaunchAfterTheTimeCountsAsReminded(t *testing.T) {
	sent := captureNotifications(t)
	m := modelWithTasks(t, dueTask("Pay rent", reminderDay, todo.PriorityHigh))
	m.reminderAt, m.remindedOn = 9*60, ""
	m.settleReminderAtLaunch(reminderAt(10, 0))
	if send, _ := m.checkReminder(reminderAt(10, 1)); send != nil {
		t.Error("a launch after the time still reminded")
	}
	if len(*sent) != 0 {
		t.Errorf("notifications = %q", *sent)
	}

	m.remindedOn = ""
	m.settleReminderAtLaunch(reminderAt(8, 0))
	if send, _ := m.checkReminder(reminderAt(9, 0)); send == nil {
		t.Error("a launch before the time swallowed the day's reminder")
	}
}

// Settings has a switch for the reminder and, while it is on, a row for its
// time; switching it off keeps the time for when it comes back.
func TestScriptReminderSettingPersists(t *testing.T) {
	m := settingsModel(t)
	m.settingsCursor = settingReminderTime
	m = sendKey(t, m, "right")
	if m.reminderAt != 10*60 {
		t.Fatalf("reminderAt = %s after →, want 10:00", formatReminder(m.reminderAt))
	}
	if s, _ := loadSettings(); s.Reminder != "10:00" {
		t.Errorf("settings.json reminder = %q, want 10:00", s.Reminder)
	}
	if !strings.Contains(m.View(), "10:00") {
		t.Error("the Settings pane does not show the new time")
	}

	m.settingsCursor = settingReminder
	m = sendKey(t, m, "enter")
	if m.reminderOn || m.reminderTime() != reminderOff {
		t.Fatal("enter on Daily reminder did not switch it off")
	}
	if s, _ := loadSettings(); !s.ReminderOff || s.Reminder != "10:00" {
		t.Errorf("settings.json = off %v at %q, want off, time kept", s.ReminderOff, s.Reminder)
	}
	if m.settingsRowVisible(settingReminderTime) || strings.Contains(ansi.Strip(m.View()), "10:00") {
		t.Error("the time row should be hidden while the reminder is off")
	}
	if send, _ := m.checkReminder(time.Now().Add(24 * time.Hour)); send != nil {
		t.Error("a reminder that is off still reminded")
	}

	m = sendKey(t, m, "enter")
	if !m.reminderOn || m.reminderAt != 10*60 {
		t.Errorf("switched back on at %s, want 10:00", formatReminder(m.reminderAt))
	}
}

// A settings.json from before the switch wrote "off" for the time.
func TestStoredReminderReadsTheOldOff(t *testing.T) {
	for _, c := range []struct {
		s      appSettings
		at     int
		wantOn bool
	}{
		{appSettings{}, defaultReminderAt, true},
		{appSettings{Reminder: "07:00"}, 7 * 60, true},
		{appSettings{Reminder: "07:00", ReminderOff: true}, 7 * 60, false},
		{appSettings{Reminder: "off"}, defaultReminderAt, false},
	} {
		if at, on := storedReminder(c.s); at != c.at || on != c.wantOn {
			t.Errorf("storedReminder(%+v) = %s %v, want %s %v", c.s, formatReminder(at), on, formatReminder(c.at), c.wantOn)
		}
	}
}

func TestCLIRemindRunsOncePerDay(t *testing.T) {
	setTestHome(t, t.TempDir())
	sent := captureNotifications(t)
	clock := reminderAt(8, 0)
	orig := remindClock
	remindClock = func() time.Time { return clock }
	t.Cleanup(func() { remindClock = orig })

	if code := cliAdd([]string{"Pay rent", "--due", reminderDay.Format("02-01-06")}); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	run := func(args ...string) string {
		t.Helper()
		var code int
		out := captureStdout(t, func() { code = cliRemind(args) })
		if code != 0 {
			t.Fatalf("remind %v: exit %d", args, code)
		}
		return out
	}

	if out := run(); out != "" || len(*sent) != 0 {
		t.Fatalf("reminded before the time: %q", out)
	}
	clock = reminderAt(9, 5)
	if out := run(); !strings.Contains(out, "1 due today") || len(*sent) != 1 {
		t.Fatalf("no reminder after the time: %q, %d sent", out, len(*sent))
	}
	if out := run(); out != "" || len(*sent) != 1 {
		t.Fatalf("reminded twice: %q", out)
	}
	if out := run("--now"); !strings.Contains(out, "Pay rent") || len(*sent) != 2 {
		t.Fatalf("--now did not remind: %q", out)
	}
}

func TestNotifyCommandKeepsTextOutOfTheScript(t *testing.T) {
	ctx := context.Background()
	title, body := `tjek: 1 due today`, `• say "hi" & <bye> $(rm -rf ~)`

	linux := notifyCommand(ctx, "linux", title, body).Args
	if want := []string{"notify-send", "--app-name=tjek", title, body}; !slices.Equal(linux, want) {
		t.Errorf("linux args = %q", linux)
	}
	mac := notifyCommand(ctx, "darwin", title, body).Args
	if mac[0] != "osascript" || !slices.Equal(mac[len(mac)-2:], []string{title, body}) {
		t.Errorf("darwin args = %q", mac)
	}
}

// The Windows toast is XML built in Go: the texts are escaped, so a title with
// markup in it is shown as text and cannot end the element it sits in.
func TestToastXMLEscapesTheTexts(t *testing.T) {
	doc := toastXML(`tjek: 1 overdue`, "• Fix <b> & \"quote\"\n• Second")
	var parsed struct {
		Texts []string `xml:"visual>binding>text"`
	}
	if err := xml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("toast XML does not parse: %v\n%s", err, doc)
	}
	want := []string{"tjek: 1 overdue", "• Fix <b> & \"quote\"\n• Second"}
	if !slices.Equal(parsed.Texts, want) {
		t.Errorf("toast texts = %q, want %q", parsed.Texts, want)
	}
}

// A notifier that fails says why in one line, not a page of tool output.
func TestNotifyFailureIsOneLine(t *testing.T) {
	out := []byte("\n  GDBus.Error: The name org.freedesktop.Notifications was not provided   \nsecond line\n")
	if got := notifyFailureReason(out, errors.New("exit status 1")); got != "GDBus.Error: The name org.freedesktop.Notifications was not provided" {
		t.Errorf("reason = %q", got)
	}
	if got := notifyFailureReason(nil, errors.New("exit status 1")); got != "exit status 1" {
		t.Errorf("a silent failure reads %q, want the exit status", got)
	}
}

// A pop-up that cannot be shown still leaves the reminder on screen in the app.
func TestReminderSurvivesAFailedPopUp(t *testing.T) {
	m := modelWithTasks(t)
	next, _ := m.Update(reminderSentMsg{title: "tjek: 1 overdue", err: errors.New("no toast")})
	m = next.(model)
	if !strings.Contains(m.err, "tjek: 1 overdue") || !strings.Contains(m.err, "unavailable") {
		t.Errorf("toast = %q, want the reminder and a note that the pop-up failed", m.err)
	}
}
