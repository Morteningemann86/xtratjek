package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// boardHome gives the test a private home and returns a sharing board config
// with a known column list and edit stamp.
func boardHome(t *testing.T, stages []string, edited time.Time) *boardConfig {
	t.Helper()
	setTestHome(t, t.TempDir())
	c := boardConfig{modifiedAt: edited, shown: true, sync: true}
	c.setStages(stages)
	return &c
}

func TestBoardStoreRoundTrip(t *testing.T) {
	setTestHome(t, t.TempDir())
	var store boardStore
	// A server that has never been told anything has nothing to say, and that
	// is not an error — it is what lets the first client's list become the
	// fleet's.
	got, err := store.LoadBoard()
	if err != nil {
		t.Fatalf("load with no file: %v", err)
	}
	if len(got.Stages) != 0 {
		t.Errorf("a missing file should read as the zero board, got %v", got.Stages)
	}
	want := tasksync.Board{Stages: []string{"Analysis", "Completed"}, ModifiedAt: time.Now().UTC().Truncate(time.Second)}
	if err := store.SaveBoard(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = store.LoadBoard()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !tasksync.SameBoard(got, want) || !got.ModifiedAt.Equal(want.ModifiedAt) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestLocalBoardHonoursThePreference(t *testing.T) {
	c := boardHome(t, []string{"Analysis", "Completed"}, time.Now())
	if b := c.wire(); b == nil || len(b.Stages) != 2 {
		t.Fatalf("a sharing device should offer its list, got %+v", b)
	}
	c.sync = false
	if b := c.wire(); b != nil {
		t.Errorf("with sharing off the device must send no board, got %+v", b)
	}
}

func TestAdoptBoardFromSync(t *testing.T) {
	edited := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	mine := []string{"Analysis", "Completed"}

	c := boardHome(t, mine, edited)
	if c.adoptFromSync(nil) {
		t.Error("a sync with no board should change nothing")
	}
	// The fleet's list is older than this device's edit: ours stands.
	older := &tasksync.Board{Stages: []string{"Backlog", "Done"}, ModifiedAt: edited.Add(-time.Hour)}
	if c.adoptFromSync(older) || c.stages[0] != "Analysis" {
		t.Errorf("an older list overwrote a newer local edit: %v", c.stages)
	}
	// Newer: adopted, and written to settings.json so it survives a restart.
	newer := &tasksync.Board{Stages: []string{"Todo", "Doing", "Shipped"}, ModifiedAt: edited.Add(time.Hour)}
	if !c.adoptFromSync(newer) {
		t.Fatal("a newer list should be adopted")
	}
	if len(c.stages) != 3 || c.stages[2] != "Shipped" {
		t.Fatalf("columns = %v, want the fleet's", c.stages)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if len(s.Stages) != 3 || s.Stages[2] != "Shipped" || !s.StagesModifiedAt.Equal(newer.ModifiedAt) {
		t.Errorf("settings.json kept %v @%v, want the adopted list and its stamp", s.Stages, s.StagesModifiedAt)
	}
	// The same names with a newer stamp are recorded but redraw nothing —
	// otherwise two devices trade an identical list forever.
	again := &tasksync.Board{Stages: []string{"Todo", "Doing", "Shipped"}, ModifiedAt: newer.ModifiedAt.Add(time.Minute)}
	if c.adoptFromSync(again) {
		t.Error("an unchanged list should not report a change")
	}
	if !c.modifiedAt.Equal(again.ModifiedAt) {
		t.Error("the newer stamp should still be recorded")
	}
	// Opted out: nothing arrives, whatever the server says.
	c.sync = false
	if c.adoptFromSync(&tasksync.Board{Stages: []string{"No", "Thanks"}, ModifiedAt: time.Now()}) {
		t.Error("a device that opted out took the fleet's columns anyway")
	}
}

// End to end over HTTP: the client offers its edited list, the server keeps
// it, and a list edited more recently elsewhere comes back and lands in
// settings.json — the whole point being that a synced Stage field means the
// same thing on both machines.
func TestClientSyncExchangesTheBoard(t *testing.T) {
	edited := time.Now().UTC().Add(-time.Hour)
	c := boardHome(t, []string{"Analysis", "Completed"}, edited)
	h := openTestDB(t)

	fleet := tasksync.Board{Stages: []string{"Analysis", "Implementation", "On hold", "Completed"}, ModifiedAt: time.Now().UTC()}
	var offered *tasksync.Board
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		var req tasksync.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		offered = req.Board
		if err := json.NewEncoder(w).Encode(tasksync.Response{Board: &fleet, ServerTime: time.Now()}); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sum, err := runClientSync(h, syncConfig{URL: ts.URL, Token: "tok"}, 5*time.Second, rank.DefaultBiases(), c.wire())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if offered == nil || len(offered.Stages) != 2 || !offered.ModifiedAt.Equal(edited) {
		t.Fatalf("the client offered %+v, want its own list and edit time", offered)
	}
	// runClientSync deliberately does not install it — the config belongs to
	// the loop that owns it.
	if c.stages[1] != "Completed" {
		t.Errorf("the sync goroutine installed the list itself: %v", c.stages)
	}
	if !c.adoptFromSync(sum.board) {
		t.Fatal("the fleet's newer list should be adopted when applied")
	}
	if strings.Join(c.stages, ",") != strings.Join(fleet.Stages, ",") {
		t.Errorf("columns = %v, want %v", c.stages, fleet.Stages)
	}
}

// A card whose stage names a column the new list does not have falls into the
// first column — the same rule as any unknown name, and the behaviour asked
// for. Nothing re-stages the cards behind the user's back.
func TestAdoptedBoardLeavesCardsAlone(t *testing.T) {
	c := boardHome(t, []string{"Analysis", "Implementation", "Completed"}, time.Now().Add(-time.Hour))
	task := todo.New("a card")
	task.SetStage("Implementation")
	if got := c.stageDisplay(task.Stage); got != "Implementation" {
		t.Fatalf("stage display = %q before the change", got)
	}
	c.adoptFromSync(&tasksync.Board{Stages: []string{"Todo", "Doing", "Shipped"}, ModifiedAt: time.Now()})
	if task.Stage != "Implementation" {
		t.Errorf("the card's stored stage was rewritten to %q", task.Stage)
	}
	if got := c.stageDisplay(task.Stage); got != "Todo" {
		t.Errorf("an unknown stage should render in the first column, got %q", got)
	}
}

// Icons ride with the columns: a board that differs only in its icons is a
// different board, and adopting it installs them and writes them down.
func TestBoardSyncCarriesIcons(t *testing.T) {
	edited := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	c := boardHome(t, []string{"Todo", "Doing", "Done"}, edited)
	c.setColumns(c.stages, map[string]string{"doing": "D"})
	if w := c.wire(); w.Icons["doing"] != "D" {
		t.Fatalf("the wire should carry the icons, got %+v", w)
	}
	plain := tasksync.Board{Stages: c.stages}
	if tasksync.SameBoard(plain, *c.wire()) {
		t.Error("boards that differ in icons are not the same board")
	}
	newer := &tasksync.Board{Stages: []string{"Todo", "Doing", "Done"}, Icons: map[string]string{"todo": "T"}, ModifiedAt: edited.Add(time.Hour)}
	if !c.adoptFromSync(newer) {
		t.Fatal("a newer board with other icons should be adopted as a change")
	}
	if c.columnIcon(0) != "T" || c.columnIcon(1) != "" {
		t.Errorf("icons after adopting = %v, want the fleet's", c.icons)
	}
	if s, _ := loadSettings(); s.StageIcons["todo"] != "T" {
		t.Errorf("settings.json kept icons %v", s.StageIcons)
	}
}
