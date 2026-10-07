package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

// watcher.go bridges fs events in the data directory into Bubble Tea messages so the
// TUI reloads when the CLI (or another process) mutates the database. The
// design choices are constrained by SQLite WAL semantics and Bubble Tea's
// goroutine model:
//
//   - We watch the *directory*, not just tasks.db. WAL writes touch
//     tasks.db-wal first and only periodically flush into tasks.db, so a
//     file-level watch on tasks.db alone misses most events.
//
//   - One goroutine reads fsnotify events, debounces them (200ms quiet
//     period coalesces a burst of WAL writes into a single reload signal),
//     and posts to a channel.
//
//   - The Update loop returns a tea.Cmd that reads one signal from that
//     channel — when it arrives, Update reloads via the Repository and
//     re-arms by returning the same Cmd. This is the canonical Bubble Tea
//     pattern for long-lived goroutines.
//
//   - Self-write suppression: every Save records lastSelfSaveAt; reload is
//     skipped if a fs event arrives within the suppression window
//     (selfWriteWindow = 500ms). Without this, every save the TUI itself
//     does would round-trip back as a reload.
//
//   - Modal suppression: when m.mode != modeNormal (text input, confirm
//     prompt, etc.) the reload is deferred — clobbering an in-flight edit
//     would be jarring. The watcher sets pendingExternalReload; the next
//     return to modeNormal triggers the deferred reload.

const (
	watcherDebounceWindow  = 200 * time.Millisecond
	watcherSelfWriteWindow = 500 * time.Millisecond
)

// watchSignal is the single-bit "the DB changed, you should consider reloading"
// signal posted by the watcher goroutine. We use a 1-buffered channel and
// drop subsequent signals while one is pending — there's no useful coalescing
// signal richer than "something happened".
type dbChangedMsg struct{}

// reloadedMsg carries the result of an async repo.Load triggered by a watcher
// event so Update can swap the task set in atomically.
type reloadedMsg struct {
	todos []todo.Todo
	err   error
}

// watcherState lives on the model. The mutex protects lastSelfSaveAt and
// pendingExternalReload — both are touched from the Update goroutine
// (single-threaded) AND from the user-driven save path; the lock is cheap
// insurance against accidental concurrent reads in tests.
type watcherState struct {
	mu                    sync.Mutex
	ch                    chan dbChangedMsg
	lastSelfSaveAt        time.Time
	pendingExternalReload bool
}

func newWatcherState() *watcherState {
	return &watcherState{ch: make(chan dbChangedMsg, 1)}
}

func (w *watcherState) recordSelfSave() {
	w.mu.Lock()
	w.lastSelfSaveAt = time.Now()
	w.mu.Unlock()
}

// shouldReloadNow decides whether a watcher signal arriving at `now` should
// trigger a reload right away. Pure given the inputs; unit-testable without
// fsnotify or a real DB.
func (w *watcherState) shouldReloadNow(now time.Time, mode appMode) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if mode != modeNormal {
		// Defer — user is editing. Mark pending; the mode-exit path picks
		// it up.
		w.pendingExternalReload = true
		return false
	}
	if now.Sub(w.lastSelfSaveAt) < watcherSelfWriteWindow {
		// Looks like our own save round-tripping back. Skip.
		return false
	}
	return true
}

// drainPending returns true if a deferred reload was queued while the user
// was in a modal mode, and atomically clears the flag.
func (w *watcherState) drainPending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pendingExternalReload {
		w.pendingExternalReload = false
		return true
	}
	return false
}

// startWatcher spawns the fsnotify goroutine. Returns the dbChangedMsg
// channel for the model to read via waitForDBChange, and a cleanup func that
// stops the watcher. If fsnotify or the directory watch fails, the function
// returns with err — the TUI should continue without live reload rather than
// abort startup.
func startWatcher(state *watcherState, dir string) (cleanup func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}
	// Ensure the directory exists before we try to watch it — fresh installs
	// reach openStore which creates it, but we can be called either before
	// or after depending on test path.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			w.Close()
			return nil, fmt.Errorf("mkdir watch target: %w", err)
		}
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}

	stop := make(chan struct{})
	go func() {
		var debounce *time.Timer
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// Care about writes to tasks.db / tasks.db-wal only.
				base := filepath.Base(ev.Name)
				if base != "tasks.db" && base != "tasks.db-wal" {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(watcherDebounceWindow, func() {
					select {
					case state.ch <- dbChangedMsg{}:
					default:
						// channel full — a signal is already pending,
						// no point queueing more
					}
				})
			case err, ok := <-w.Errors:
				if !ok {
					return // watcher closed — Errors drained, stop reading it
				}
				// fsnotify error — log and keep going. A spurious error here
				// shouldn't kill the watcher; rare enough that a stderr line
				// (even under a live TUI) beats swallowing it.
				log.Printf("tjek watcher: %v", err)
			}
		}
	}()

	// Idempotent: the model is a value type, so several copies can hold this
	// same func (the quit path and a test cleanup, say) and each will call it.
	// Without the Once, the second call closes an already-closed channel and
	// panics.
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			w.Close()
		})
	}, nil
}

// waitForDBChange is the tea.Cmd that bridges the watcher's channel into the
// Update loop. Returns the next dbChangedMsg from the channel (blocking),
// which Bubble Tea delivers as a regular msg. The Update handler re-arms by
// returning waitForDBChange again.
func waitForDBChange(ch chan dbChangedMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// startChangeWatcher watches the storage directory and nudges the hub whenever
// tasks.db changes, so a direct CLI write on the server host (tjek add/done…)
// reaches connected clients in real time — not only client-initiated merges. It
// reuses the same fsnotify plumbing the TUI uses for live reload. Returns a stop
// func; if the watcher can't start the server still works, it just loses
// real-time push for out-of-process writes (client syncs still nudge directly).
func startChangeWatcher(hub *tasksync.Hub, dir string) (stop func(), err error) {
	state := newWatcherState()
	cleanup, err := startWatcher(state, dir)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-state.ch:
				hub.Broadcast()
			}
		}
	}()
	return func() { close(done); cleanup() }, nil
}
