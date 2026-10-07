package main

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Morteningemann86/xtratjek/tasksync"
)

// synclive.go is the thin Bubble Tea bridge to tasksync.Listener: the engine
// owns the SSE stream and reconnect loop; this file only carries its nudges
// into the Update loop as messages.

// syncEventMsg reaches the Update loop when the server signalled a change. The
// handler re-arms the listener and triggers a background sync.
type syncEventMsg struct{}

// startLiveSync launches the SSE listener for cfg, or returns nil if cfg isn't
// ready. The listener reconnects on its own; the caller just arms
// waitForSyncEvent(ls.C).
func startLiveSync(cfg syncConfig) *tasksync.Listener {
	if !cfg.ready() {
		return nil
	}
	return tasksync.StartListener(cfg.URL, cfg.Token)
}

// waitForSyncEvent bridges the listener channel into the Update loop, mirroring
// waitForDBChange: it blocks for the next nudge and is re-armed by the handler.
func waitForSyncEvent(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return syncEventMsg{}
	}
}
