package main

import (
	"fmt"
	"strings"

	"github.com/Morteningemann86/xtratjek/tasksync"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// syncsettings.go is the Settings-tab surface for cross-device sync: the
// auto-sync toggle and the inline editors for the server URL and token. Config
// is the same sync.json that the CLI and auto-sync use (loadSyncConfig
// / saveSyncConfig), so a change here takes effect everywhere.

// saveSyncCfg persists the current sync config and refreshes the live auto-sync
// gate. A write failure surfaces on the error line.
func (m *model) saveSyncCfg() {
	m.autoSync = autoSyncEnabled(m.syncCfg)
	if err := saveSyncConfig(m.syncCfg); err != nil {
		m.flashError(fmt.Sprintf("Sync config save failed: %v", err))
	}
}

// toggleSyncAuto flips auto-sync on/off. It's a no-op (with a hint) until a
// server URL and token are set, since there's nothing to sync against yet.
func (m *model) toggleSyncAuto() {
	if !m.syncCfg.ready() {
		m.syncStatus = tr("Set sync server + token first")
		return
	}
	v := !autoSyncEnabled(m.syncCfg)
	m.syncCfg.AutoSync = &v
	m.saveSyncCfg()
	// Turning auto-sync off: stop the real-time SSE listener so its goroutine
	// and connection don't linger for the rest of the session. On re-enable the
	// listener is NOT started here — this runs in void key-handler contexts that
	// can't arm its reader cmd; the next syncTick starts and arms it instead.
	if !v && m.liveSync != nil {
		m.liveSync.Close()
		m.liveSync = nil
	}
}

// restartLiveSync tears down the SSE listener and starts a fresh one against
// the current config, returning the cmd that arms its reader (nil when live
// sync shouldn't run). Must be called after any edit to the client-side sync
// config (URL/token): the listener captures the config it was started with, so
// without a restart it keeps streaming from — or reconnect-hammering — the old
// server for the rest of the session.
func (m *model) restartLiveSync() tea.Cmd {
	if m.liveSync != nil {
		m.liveSync.Close()
		m.liveSync = nil
	}
	if !m.autoSync {
		return nil
	}
	if ls := startLiveSync(m.syncCfg); ls != nil {
		m.liveSync = ls
		return waitForSyncEvent(ls.C)
	}
	return nil
}

// updateEditSyncURL handles the inline server-URL editor.
func (m model) updateEditSyncURL(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			// Save whatever's in the field, blank included: the editor is
			// pre-filled with the current value, so an empty field is a
			// deliberate clear, not an accidental no-op.
			m.syncCfg.URL = strings.TrimSpace(m.textInput.Value())
			m.saveSyncCfg()
			// Set *and* clear: a security notice that outlives the condition
			// it describes teaches the user to ignore it. Only this message
			// is cleared; a sync status from somewhere else is not ours to
			// erase.
			warning := tr("Plain http to a public host: token travels unencrypted")
			switch {
			case tasksync.InsecureURLWarning(m.syncCfg.URL) != "":
				m.syncStatus = warning
			case m.syncStatus == warning:
				m.syncStatus = ""
			}
			m.mode = modeNormal
			return m, m.restartLiveSync()
		case "esc":
			m.mode = modeNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// updateEditSyncToken handles the inline token editor. The field is pre-filled
// with the current token, so clearing it to blank clears the stored token.
func (m model) updateEditSyncToken(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			m.syncCfg.Token = strings.TrimSpace(m.textInput.Value())
			m.saveSyncCfg()
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal // un-mask for the next mode
			return m, m.restartLiveSync()
		case "esc":
			m.mode = modeNormal
			m.textInput.EchoMode = textinput.EchoNormal
			return m, nil
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}
