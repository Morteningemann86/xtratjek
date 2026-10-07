package main

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/tasksync"
)

// boardsync.go is this app's half of the shared kanban column list: the
// server's copy of it, and the client's rules for offering and accepting one.
// The wire format and the merge rule live in tasksync/board.go; what is here
// is storage, settings and the timing.

// ── The server's copy ────────────────────────────────────────────────────────

// boardStore keeps the fleet's column list in one small JSON file beside the
// sync state. Not in settings.json and not in the task database: a server is a
// place the list is *kept*, not a device that uses it, and the hub is usually
// headless. A file needs no migration and is trivially inspectable when a
// fleet's columns are not what someone expects.
type boardStore struct{ mu sync.Mutex }

func boardStatePath() string { return paths.For(paths.State, "board.json") }

// LoadBoard returns the stored list. A missing file is the zero Board with no
// error — a server that has never been told anything has nothing to say, which
// is exactly what MergeBoard reads as "take the client's".
func (b *boardStore) LoadBoard() (tasksync.Board, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := os.ReadFile(boardStatePath())
	if os.IsNotExist(err) {
		return tasksync.Board{}, nil
	}
	if err != nil {
		return tasksync.Board{}, err
	}
	var out tasksync.Board
	if err := json.Unmarshal(data, &out); err != nil {
		return tasksync.Board{}, err
	}
	return out, nil
}

func (b *boardStore) SaveBoard(board tasksync.Board) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := paths.Ensure(paths.State); err != nil {
		return err
	}
	data, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(boardStatePath(), data, 0600)
}

// ── The client's half ────────────────────────────────────────────────────────

// wire is what this device offers the fleet: nil when the preference is off,
// which is the wire's way of saying "leave my columns alone" — the server then
// neither stores this list nor sends one back.
func (c boardConfig) wire() *tasksync.Board {
	if !c.sync {
		return nil
	}
	var icons map[string]string
	if len(c.icons) > 0 {
		icons = make(map[string]string, len(c.icons))
		for k, v := range c.icons {
			icons[k] = v
		}
	}
	return &tasksync.Board{
		Stages:     append([]string(nil), c.stages...),
		Icons:      icons,
		ModifiedAt: c.modifiedAt,
	}
}

// adoptFromSync installs into c a column list that came back from a sync and
// writes it to settings.json. It reports whether the visible list changed, so
// the TUI knows to redraw; a newer stamp carrying the same names is recorded
// silently, which is what stops two devices trading the same list forever.
//
// Tasks are deliberately not re-staged. A card whose stored stage names a
// column this list does not have falls into the first one — the same rule that
// applies to any unknown name, and the behaviour asked for. The fleet's own
// cards already carry the fleet's names.
func (c *boardConfig) adoptFromSync(b *tasksync.Board) bool {
	if b == nil || !c.sync || len(b.Stages) == 0 {
		return false
	}
	if !b.ModifiedAt.After(c.modifiedAt) {
		return false
	}
	changed := !tasksync.SameBoard(*b, tasksync.Board{Stages: c.stages, Icons: c.icons})
	if changed {
		c.setColumns(b.Stages, b.Icons)
	}
	c.modifiedAt = b.ModifiedAt
	// Read-modify-write rather than rebuilding the whole file from app state:
	// this runs from the CLI too, where there is no model to rebuild it from.
	if s, err := loadSettings(); err == nil {
		s.Stages = c.stages
		s.StageIcons = c.icons
		s.StagesModifiedAt = c.modifiedAt
		_ = saveSettings(s)
	}
	return changed
}
