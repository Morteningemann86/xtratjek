package rank

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// laterStartFixture is an urgent task that cannot begin until tomorrow beside a
// calm one that can begin now: on score alone the urgent one leads.
func laterStartFixture() (urgent, calm todo.Todo) {
	urgent = todo.New("book the venue")
	urgent.ID = "urgent"
	urgent.CreatedAt = fixedNow
	urgent.Priority = todo.PriorityHigh
	urgent.DueDate = startOfDay(fixedNow).AddDate(0, 0, 2)
	urgent.StartDate = startOfDay(fixedNow).AddDate(0, 0, 1).Add(9 * time.Hour)
	calm = todo.New("tidy the notes")
	calm.ID = "calm"
	calm.CreatedAt = fixedNow
	calm.Priority = todo.PriorityLow
	return urgent, calm
}

// A start date on a later day sinks the task below work that can be picked up
// now, and the day it arrives the task is back on its score.
func TestLaterStartSinksUntilItsDay(t *testing.T) {
	urgent, calm := laterStartFixture()
	all := []*todo.Todo{&urgent, &calm}
	score := func(x *todo.Todo) float64 { return ComponentsAt(fixedNow, x, balancedBiases(), Heat{}).Total }
	if score(&urgent) <= score(&calm) {
		t.Fatal("fixture: the urgent task should outscore the calm one")
	}

	if rows := RankingAt(fixedNow, all, balancedBiases(), Heat{}); rows[0].ID != "calm" {
		t.Errorf("the day before its start, order = %s, %s; want the calm task first", rows[0].ID, rows[1].ID)
	}
	// Midnight, not the 09:00 SetStartDate stamps: start dates are days.
	midnight := startOfDay(fixedNow).AddDate(0, 0, 1)
	if rows := RankingAt(midnight, all, balancedBiases(), Heat{}); rows[0].ID != "urgent" {
		t.Errorf("on its start day, order = %s, %s; want the urgent task first", rows[0].ID, rows[1].ID)
	}
}

// A start date today or earlier is only a record of when work began.
func TestStartDateTodayOrEarlierDoesNotSink(t *testing.T) {
	for _, start := range []time.Time{fixedNow.Add(2 * time.Hour), fixedNow.AddDate(0, 0, -5)} {
		tt := todo.New("started")
		tt.StartDate = start
		if StartsLater(&tt, fixedNow) {
			t.Errorf("start %v counted as later than %v", start, fixedNow)
		}
	}
	done := todo.New("finished early")
	done.StartDate = fixedNow.AddDate(0, 0, 3)
	done.Status = todo.Done
	if StartsLater(&done, fixedNow) {
		t.Error("a done task counted as not started")
	}
}

// Sunk adds to the set it is handed without writing to it: the caller's
// blocked set also draws the dependency marker, which a start date is not.
func TestSunkLeavesTheBlockedSetAlone(t *testing.T) {
	urgent, calm := laterStartFixture()
	blocked := map[string]bool{"calm": true}
	sunk := Sunk(blocked, []*todo.Todo{&urgent, &calm}, fixedNow)
	if !sunk["urgent"] || !sunk["calm"] {
		t.Errorf("sunk = %v, want both tasks", sunk)
	}
	if blocked["urgent"] {
		t.Error("Sunk wrote the start-date task into the blocked set")
	}
}

// The overlay names the date, and the forecast sees the task come back.
func TestExplainAndForecastKnowTheStartDate(t *testing.T) {
	urgent, calm := laterStartFixture()
	all := []*todo.Todo{&urgent, &calm}
	e := ExplainAt(fixedNow, &urgent, all, balancedBiases(), Heat{})
	if !e.StartsOn.Equal(urgent.StartDate) {
		t.Errorf("StartsOn = %v, want %v", e.StartsOn, urgent.StartDate)
	}
	if e.Pos != 2 {
		t.Errorf("Pos = %d, want 2", e.Pos)
	}
	var back bool
	for _, s := range e.Shifts {
		if s.At.Equal(startOfDay(fixedNow).AddDate(0, 0, 1)) && s.Pos == 1 {
			back = true
		}
	}
	if !back {
		t.Errorf("forecast did not show the task reaching #1 at its start day; shifts = %+v", e.Shifts)
	}
	if e := ExplainAt(fixedNow, &calm, all, balancedBiases(), Heat{}); !e.StartsOn.IsZero() {
		t.Errorf("a task with no start date carries StartsOn %v", e.StartsOn)
	}
}
