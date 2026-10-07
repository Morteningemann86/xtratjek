package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

func TestParseCompletedAt(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 9, 25, 14, 0, 0, 0, loc)
	prev := time.Date(2026, 9, 25, 11, 30, 0, 0, loc)
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"23-09-26 08:15", time.Date(2026, 9, 23, 8, 15, 0, 0, loc), false},
		{"23-09-26", time.Date(2026, 9, 23, 11, 30, 0, 0, loc), false}, // keeps the recorded time of day
		{"yesterday", time.Date(2026, 9, 24, 11, 30, 0, 0, loc), false},
		{"yesterday 22:00", time.Date(2026, 9, 24, 22, 0, 0, 0, loc), false},
		{"09:05", time.Date(2026, 9, 25, 9, 5, 0, 0, loc), false}, // bare time: the recorded day
		{"15:00", time.Time{}, true},                              // later today is the future
		{"tomorrow", time.Time{}, true},
		{"", time.Time{}, true},
		{"soon", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseCompletedAt(c.in, prev, now)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && !got.Equal(c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}

	// A date-only correction to today whose kept time is still ahead clamps to now.
	late := time.Date(2026, 9, 20, 18, 0, 0, 0, loc)
	if got, err := parseCompletedAt("today", late, now); err != nil || !got.Equal(now) {
		t.Errorf("today with a later kept time = %v, %v; want now", got, err)
	}
}

func TestScriptEditCompletedOnAndUndo(t *testing.T) {
	done := todo.New("Filed the taxes")
	done.Toggle()
	orig := done.CompletedAt
	m := modelWithTasks(t, done)

	m.pane = paneDetail
	m.detailTaskID = done.ID
	m.detail.field = fieldDueDate
	m = sendKey(t, m, "down")
	if m.detail.field != fieldCompleted {
		t.Fatalf("down from Due on a done task: field = %v, want fieldCompleted", m.detail.field)
	}
	m = sendKey(t, m, "enter")
	if m.mode != modeInput {
		t.Fatalf("enter on Completed on: mode = %v, want modeInput", m.mode)
	}
	m.textInput.SetValue("01-02-24 10:00")
	m = sendKey(t, m, "enter")

	want := time.Date(2024, 2, 1, 10, 0, 0, 0, time.Local)
	got := m.get(done.ID)
	if !got.CompletedAt.Equal(want) || got.Status != todo.Done {
		t.Fatalf("after edit: completed = %v status = %v, want %v and done", got.CompletedAt, got.Status, want)
	}
	if _, dirty := m.dirtyIDs[done.ID]; !dirty {
		t.Error("task not marked dirty after editing its completion")
	}

	m.performUndo()
	if got := m.get(done.ID).CompletedAt; !got.Equal(orig) {
		t.Errorf("undo: completed = %v, want %v", got, orig)
	}
}

func TestCompletedOnRowSkippedForPendingTasks(t *testing.T) {
	open := todo.New("Still open")
	m := modelWithTasks(t, open)
	m.pane = paneDetail
	m.detailTaskID = open.ID
	m.detail.field = fieldDueDate
	m = sendKey(t, m, "down")
	if m.detail.field != fieldRecurrence {
		t.Errorf("down from Due on a pending task: field = %v, want fieldRecurrence", m.detail.field)
	}
}
