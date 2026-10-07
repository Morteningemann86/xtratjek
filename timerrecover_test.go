package main

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// runningEntry builds a task carrying one running time entry (no stop time).
func taskWithTimer(title string, e todo.TimeEntry) todo.Todo {
	t := todo.New(title)
	t.TimeEntries = []todo.TimeEntry{e}
	return t
}

func TestReconcileStopsAbandonedTimer(t *testing.T) {
	h := openTestDB(t)
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Started 5h ago, never heartbeated (CLI/agent orphan).
	task := taskWithTimer("deploy thing", todo.TimeEntry{ID: "te1", StartedAt: now.Add(-5 * time.Hour)})
	saveTodos(t, h, []todo.Todo{task})

	rec, err := reconcileStaleTimers(h, now, 4*time.Hour)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rec) != 1 || rec[0].Title != "Deploy thing" {
		t.Fatalf("expected 1 recovered timer for the task, got %+v", rec)
	}
	// No heartbeat → stopped at start → ~0 logged (honest: we never saw the work).
	if rec[0].Logged != 0 {
		t.Errorf("orphan with no heartbeat should log ~0, got %s", rec[0].Logged)
	}
	// And it's no longer running in the store.
	got, _ := loadTodosFromDB(h)
	if got[0].TimeEntries[0].IsRunning() {
		t.Errorf("timer should be stopped after recovery")
	}
	// modified_at must be bumped to the recovery time: the sync merge orders
	// entry versions by ModifiedAt, and an unbumped stop ties with (and can
	// lose to) another device's still-running copy of the same entry.
	if !got[0].TimeEntries[0].ModifiedAt.Equal(now) {
		t.Errorf("recovery must bump modified_at to now (%s), got %s", now, got[0].TimeEntries[0].ModifiedAt)
	}
}

func TestReconcileKeepsFreshlyHeartbeatedTimer(t *testing.T) {
	h := openTestDB(t)
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Long-running but a live TUI heartbeated it 1 minute ago → must NOT be touched.
	task := taskWithTimer("focus block", todo.TimeEntry{
		ID: "te1", StartedAt: now.Add(-10 * time.Hour), LastSeen: now.Add(-1 * time.Minute),
	})
	saveTodos(t, h, []todo.Todo{task})

	rec, err := reconcileStaleTimers(h, now, 4*time.Hour)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rec) != 0 {
		t.Fatalf("a freshly-heartbeated timer must be kept, got %+v", rec)
	}
	got, _ := loadTodosFromDB(h)
	if !got[0].TimeEntries[0].IsRunning() {
		t.Errorf("live timer should still be running")
	}
}

func TestReconcileStopsAtLastSeen(t *testing.T) {
	h := openTestDB(t)
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Heartbeated until 5h ago, then went silent (TUI killed): recover at last_seen.
	task := taskWithTimer("write report", todo.TimeEntry{
		ID: "te1", StartedAt: now.Add(-6 * time.Hour), LastSeen: now.Add(-5 * time.Hour),
	})
	saveTodos(t, h, []todo.Todo{task})

	rec, err := reconcileStaleTimers(h, now, 4*time.Hour)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rec) != 1 {
		t.Fatalf("expected the silent timer to be recovered, got %+v", rec)
	}
	// Stopped at last_seen → logged ≈ last_seen - started = 1h.
	if rec[0].Logged != time.Hour {
		t.Errorf("should log up to last heartbeat (1h), got %s", rec[0].Logged)
	}
}

func TestHeartbeatTouchesOnlyRunningEntries(t *testing.T) {
	h := openTestDB(t)
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	running := taskWithTimer("running", todo.TimeEntry{ID: "run", StartedAt: now.Add(-time.Hour)})
	stopped := taskWithTimer("stopped", todo.TimeEntry{
		ID: "stop", StartedAt: now.Add(-2 * time.Hour), StoppedAt: now.Add(-time.Hour),
	})
	saveTodos(t, h, []todo.Todo{running, stopped})

	if err := heartbeatRunningTimers(h, now); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _ := loadTodosFromDB(h)
	byTitle := map[string]todo.TimeEntry{}
	for _, tk := range got {
		if len(tk.TimeEntries) > 0 {
			byTitle[tk.Title] = tk.TimeEntries[0]
		}
	}
	if byTitle["Running"].LastSeen.IsZero() {
		t.Errorf("running entry should have last_seen stamped")
	}
	if !byTitle["Stopped"].LastSeen.IsZero() {
		t.Errorf("stopped entry must not be heartbeated")
	}
}

// TestSaveDoesNotWipeHeartbeat reproduces the stale-save hazard: the TUI
// heartbeats last_seen, then an ordinary edit saves the task. If the save
// carries a stale in-memory LastSeen, the heartbeat is wiped and a concurrent
// CLI's stale-timer recovery could auto-stop a live timer. The fix is
// two-sided (StartTimer stamps LastSeen; stampRunningTimersSeen mirrors each
// heartbeat in memory), so a save after a heartbeat must persist a last_seen
// at least as fresh as the in-memory stamp.
func TestSaveDoesNotWipeHeartbeat(t *testing.T) {
	h := openTestDB(t)

	s := &Store{}
	s.ensureTasks()
	task := todo.New("live timer")
	held := s.add(task)
	s.startTimer(held.ID)
	saveTodos(t, h, []todo.Todo{*held})

	// Minute tick: DB heartbeat + in-memory mirror, exactly as Update does.
	beat := time.Now().Add(time.Minute)
	if err := heartbeatRunningTimers(h, beat); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	s.stampRunningTimersSeen(beat)

	// User edits the task → debounced save writes the in-memory copy.
	held.Title = "live timer (renamed)"
	saveTodos(t, h, []todo.Todo{*held})

	got, err := loadTodosFromDB(h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || len(got[0].TimeEntries) != 1 {
		t.Fatalf("unexpected store shape: %+v", got)
	}
	if ls := got[0].TimeEntries[0].LastSeen; ls.Before(beat.Truncate(time.Second)) {
		t.Errorf("save wiped the heartbeat: last_seen=%v, want >= %v", ls, beat)
	}
}

// TestStartTimerStampsLastSeen: a freshly started entry is born with a
// heartbeat, so the window between start and the first minute tick can't
// persist an empty last_seen.
func TestStartTimerStampsLastSeen(t *testing.T) {
	task := todo.New("fresh")
	task.StartTimer()
	if e := task.RunningEntry(); e == nil || e.LastSeen.IsZero() {
		t.Errorf("running entry should start with LastSeen set, got %+v", e)
	}
}
