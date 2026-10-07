package tasksync

import (
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// memBoard is a BoardStore in memory, so a round trip through the handler
// exercises the real merge without a file.
type memBoard struct {
	board Board
	err   error
	saves int
}

func (m *memBoard) LoadBoard() (Board, error) { return m.board, m.err }
func (m *memBoard) SaveBoard(b Board) error   { m.saves++; m.board = b; return nil }

func TestMergeBoardRules(t *testing.T) {
	early := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	stored := Board{Stages: []string{"Backlog", "Done"}, ModifiedAt: early}

	// A device that has never edited its columns is carrying the defaults and
	// must never overwrite a fleet that has. This is what makes the preference
	// safe to default to on.
	never := Board{Stages: []string{"Todo", "Done"}}
	if got := MergeBoard(stored, never); !SameBoard(got, stored) {
		t.Errorf("an unstamped list won the merge: %v", got.Stages)
	}
	// A later edit wins.
	newer := Board{Stages: []string{"Analysis", "Completed"}, ModifiedAt: late}
	if got := MergeBoard(stored, newer); !SameBoard(got, newer) {
		t.Errorf("the later edit lost: %v", got.Stages)
	}
	// An earlier one does not.
	older := Board{Stages: []string{"Old", "Done"}, ModifiedAt: early.Add(-time.Hour)}
	if got := MergeBoard(stored, older); !SameBoard(got, stored) {
		t.Errorf("an older edit won: %v", got.Stages)
	}
	// An exact tie keeps what is stored — the same edit, nothing to choose.
	tie := Board{Stages: []string{"Something else", "Done"}, ModifiedAt: early}
	if got := MergeBoard(stored, tie); !SameBoard(got, stored) {
		t.Errorf("a tie changed the stored list: %v", got.Stages)
	}
	// A server with nothing yet takes whatever it is given.
	if got := MergeBoard(Board{}, newer); !SameBoard(got, newer) {
		t.Errorf("an empty server did not adopt the client's list: %v", got.Stages)
	}
}

// The wire half: one device's edited list reaches another device through a
// normal task sync, and a device that never edited its own gets it handed to
// it rather than overwriting anyone.
func TestSyncCarriesTheBoard(t *testing.T) {
	now := time.Now().UTC()
	srv := &Server{Token: "t", Store: &fakeStore{}, Board: &memBoard{}}
	hs := testServer(t, srv)

	edited := &Board{Stages: []string{"Analysis", "Implementation", "Completed"}, ModifiedAt: now}
	resp, err := PostSync(hs.URL, "t", "", []todo.Todo{newTask("a", now)}, edited, 5*time.Second)
	if err != nil {
		t.Fatalf("PostSync: %v", err)
	}
	if resp.Board == nil || !SameBoard(*resp.Board, *edited) {
		t.Fatalf("the server did not take the edited list: %+v", resp.Board)
	}

	// The second device has never touched its columns, so it sends an unstamped
	// list — and gets the fleet's back.
	fresh := &Board{Stages: []string{"Backlog", "In progress", "Done"}}
	resp2, err := PostSync(hs.URL, "t", "", nil, fresh, 5*time.Second)
	if err != nil {
		t.Fatalf("PostSync: %v", err)
	}
	if resp2.Board == nil || !SameBoard(*resp2.Board, *edited) {
		t.Fatalf("a never-edited device overwrote the fleet's columns: %+v", resp2.Board)
	}
}

// Not sharing is the wire's way of saying "leave my columns alone": the
// server neither stores that client's list nor sends one back.
func TestSyncWithoutABoardIsLeftAlone(t *testing.T) {
	board := &memBoard{}
	srv := &Server{Token: "t", Store: &fakeStore{}, Board: board}
	hs := testServer(t, srv)

	resp, err := PostSync(hs.URL, "t", "", nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("PostSync: %v", err)
	}
	if resp.Board != nil {
		t.Errorf("an empty server answered with a board: %+v", resp.Board)
	}
	if board.saves != 0 {
		t.Errorf("a non-participating client caused %d board write(s)", board.saves)
	}
}

// A server that keeps no board is what every older fleet looks like: the sync
// still works and the client's columns are its own business.
func TestSyncAgainstAServerWithoutABoard(t *testing.T) {
	srv := &Server{Token: "t", Store: &fakeStore{}}
	hs := testServer(t, srv)
	resp, err := PostSync(hs.URL, "t", "", nil, &Board{Stages: []string{"A", "Done"}, ModifiedAt: time.Now()}, 5*time.Second)
	if err != nil {
		t.Fatalf("PostSync: %v", err)
	}
	if resp.Board != nil {
		t.Errorf("a board-less server answered with one: %+v", resp.Board)
	}
}
