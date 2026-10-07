package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// testSyncServer bundles a tasksync.Server with the test database its Store
// adapter writes to, since tests assert against the DB directly.
type testSyncServer struct {
	*tasksync.Server
	db *sql.DB
}

func newTestServer(t *testing.T) (*testSyncServer, *httptest.Server) {
	t.Helper()
	h := openTestDB(t)
	srv := &testSyncServer{Server: &tasksync.Server{Token: "tok", Store: dbStore{h: h}}, db: h}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// Local copies of the merge test-table helpers that moved to tasksync with
// merge_test.go — the integration tests here still speak in the same shorthand.
var mBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return mBase.Add(d) }

func mkTask(id, title string, modified time.Time) todo.Todo {
	return todo.Todo{ID: id, Title: title, CreatedAt: mBase, ModifiedAt: modified}
}

func mkComment(id, text string, created time.Time) todo.Comment {
	return todo.Comment{ID: id, Text: text, CreatedAt: created}
}

func indexByID(ts []todo.Todo) map[string]todo.Todo {
	out := make(map[string]todo.Todo, len(ts))
	for _, t := range ts {
		out[t.ID] = t
	}
	return out
}

func TestSyncHandlerAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	handler := srv.Handler()
	const body = `{"tasks":[]}`

	// No token → 401.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/sync", strings.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing token: want 401, got %d", w.Code)
	}

	// Wrong token → 401.
	r := httptest.NewRequest(http.MethodPost, "/v1/sync", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer nope")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", w.Code)
	}

	// Right token → 200.
	r = httptest.NewRequest(http.MethodPost, "/v1/sync", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer tok")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("right token: want 200, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestSyncClientServerRoundTrip(t *testing.T) {
	srv, ts := newTestServer(t)

	// Server starts with task A.
	a := todo.New("from server")
	saveTodos(t, srv.db, []todo.Todo{a})

	// Client starts with a different task B.
	ch := openTestDB(t)
	b := todo.New("from client")
	saveTodos(t, ch, []todo.Todo{b})

	cfg := syncConfig{URL: ts.URL, Token: "tok"}
	sum, err := runClientSync(ch, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire())
	if err != nil {
		t.Fatalf("client sync: %v", err)
	}
	if sum.received != 2 {
		t.Errorf("expected 2 tasks back, got %d", sum.received)
	}

	// Both sides now hold both tasks.
	clientLive, _ := loadTodosFromDB(ch)
	serverLive, _ := loadTodosFromDB(srv.db)
	if len(clientLive) != 2 {
		t.Errorf("client should have 2 tasks, got %d", len(clientLive))
	}
	if len(serverLive) != 2 {
		t.Errorf("server should have 2 tasks, got %d", len(serverLive))
	}
}

func TestSyncServerPropagatesDeletion(t *testing.T) {
	srv, ts := newTestServer(t)

	a := todo.New("doomed")
	saveTodos(t, srv.db, []todo.Todo{a})

	// Client holds the same task but as a tombstone deleted after the server's edit.
	ch := openTestDB(t)
	saveTodos(t, ch, []todo.Todo{a})
	tomb := a
	tomb.Deleted = true
	tomb.DeletedAt = a.ModifiedAt.Add(time.Hour)
	saveTodos(t, ch, []todo.Todo{tomb})

	cfg := syncConfig{URL: ts.URL, Token: "tok"}
	if _, err := runClientSync(ch, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client sync: %v", err)
	}

	serverLive, _ := loadTodosFromDB(srv.db)
	if len(serverLive) != 0 {
		t.Errorf("deletion should propagate to server, still %d live", len(serverLive))
	}
	serverAll, _ := loadTodosForSync(srv.db)
	if len(serverAll) != 1 || !serverAll[0].Deleted {
		t.Errorf("server should retain the tombstone, got %+v", serverAll)
	}
}

// TestSyncMergesChildCollections drives a concurrent comment add on the *same*
// task through the full client↔server round trip (SQLite encode/decode + HTTP +
// merge + write-back), not just the in-memory Merge. Both sides must end up
// holding the union of the two comments. This guards the child-collection path
// that the recent merge fixes touched, where a child tombstone or row could be
// lost across the storage boundary rather than in the merge function itself.
func TestSyncMergesChildCollections(t *testing.T) {
	srv, ts := newTestServer(t)

	// Same task ID on both sides, each with a different comment added offline.
	sx := mkTask("x", "server wording", at(time.Hour))
	sx.Comments = []todo.Comment{mkComment("c1", "from server", at(0))}
	saveTodos(t, srv.db, []todo.Todo{sx})

	ch := openTestDB(t)
	cx := mkTask("x", "client wording", at(0)) // older scalar, loses LWW
	cx.Comments = []todo.Comment{mkComment("c2", "from client", at(0))}
	saveTodos(t, ch, []todo.Todo{cx})

	cfg := syncConfig{URL: ts.URL, Token: "tok"}
	if _, err := runClientSync(ch, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client sync: %v", err)
	}

	// Both stores converge: server wording wins the scalar (newer), but the two
	// comments union — neither side's offline note is lost.
	for _, tc := range []struct {
		name string
		db   *sql.DB
	}{{"client", ch}, {"server", srv.db}} {
		live, _ := loadTodosFromDB(tc.db)
		got := indexByID(live)
		x, ok := got["x"]
		if !ok {
			t.Fatalf("%s: task x missing after sync", tc.name)
		}
		if x.Title != "server wording" {
			t.Errorf("%s: Title = %q, want server wording", tc.name, x.Title)
		}
		ids := map[string]bool{}
		for _, c := range x.Comments {
			ids[c.ID] = true
		}
		if len(x.Comments) != 2 || !ids["c1"] || !ids["c2"] {
			t.Errorf("%s: want comments c1+c2 unioned, got %+v", tc.name, x.Comments)
		}
	}
}

// TestSyncMultiClientConvergence checks that two clients syncing in sequence
// against one server all converge to the union of every task — i.e. the second
// client's sync doesn't clobber the first client's contribution, and a client
// that synced before the others later picks up everything.
func TestSyncMultiClientConvergence(t *testing.T) {
	srv, ts := newTestServer(t)
	cfg := syncConfig{URL: ts.URL, Token: "tok"}

	// Server starts with A; client 1 brings B; client 2 brings C.
	saveTodos(t, srv.db, []todo.Todo{mkTask("a", "A", at(0))})

	c1 := openTestDB(t)
	saveTodos(t, c1, []todo.Todo{mkTask("b", "B", at(0))})
	c2 := openTestDB(t)
	saveTodos(t, c2, []todo.Todo{mkTask("c", "C", at(0))})

	// Client 1 syncs: server gains B; client 1 gains A.
	if _, err := runClientSync(c1, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client 1 sync: %v", err)
	}
	// Client 2 syncs: server gains C; client 2 gains A and B.
	if _, err := runClientSync(c2, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client 2 sync: %v", err)
	}
	// Client 1 syncs again: now picks up C too.
	if _, err := runClientSync(c1, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client 1 re-sync: %v", err)
	}

	want := map[string]bool{"a": true, "b": true, "c": true}
	for _, tc := range []struct {
		name string
		db   *sql.DB
	}{{"server", srv.db}, {"client 1", c1}, {"client 2", c2}} {
		live, _ := loadTodosFromDB(tc.db)
		got := indexByID(live)
		if len(got) != len(want) {
			t.Errorf("%s: have %d tasks, want %d (%v)", tc.name, len(got), len(want), got)
		}
		for id := range want {
			if _, ok := got[id]; !ok {
				t.Errorf("%s: missing task %q after convergence", tc.name, id)
			}
		}
	}
}

func TestSyncConflictLogged(t *testing.T) {
	srv, ts := newTestServer(t)

	// Server has the authoritative (newer) version of task X.
	x := todo.New("server wording")
	x.ModifiedAt = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	saveTodos(t, srv.db, []todo.Todo{x})

	// Client has an older edit of the same task — it will lose LWW.
	ch := openTestDB(t)
	stale := x
	stale.Title = "client wording"
	stale.ModifiedAt = x.ModifiedAt.Add(-time.Hour)
	saveTodos(t, ch, []todo.Todo{stale})

	logPath := syncLogPath()
	_ = os.Remove(logPath)
	// Clear the sync-state baseline: another test's runClientSync may have
	// recorded a LastSync newer than this test's backdated edit, which would
	// (correctly) classify the overwrite as inbound propagation. This test
	// wants the first-sync path where every dropped edit is logged.
	_ = os.Remove(syncStatePath())

	cfg := syncConfig{URL: ts.URL, Token: "tok"}
	sum, err := runClientSync(ch, cfg, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire())
	if err != nil {
		t.Fatalf("client sync: %v", err)
	}
	if sum.conflicts != 1 {
		t.Fatalf("expected 1 conflict, got %d", sum.conflicts)
	}

	// Client converges to the server's wording…
	live, _ := loadTodosFromDB(ch)
	if len(live) != 1 || live[0].Title != "Server wording" {
		t.Fatalf("client should adopt server wording, got %+v", live)
	}
	// …and the dropped local edit is in the recovery log.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read sync log: %v", err)
	}
	if !strings.Contains(string(data), "client wording") {
		t.Errorf("sync log should record the dropped local edit, got: %s", data)
	}
}

// The hub-side serve state must round-trip and gate on "ever recorded":
// sync --status uses ok=false to print "no client sync recorded yet" instead
// of a zero-time ago.
func TestServeStateRoundTrip(t *testing.T) {
	// The test binary shares one HOME; another test's sync round trip may
	// already have recorded a client contact. Start from a clean slate.
	_ = os.Remove(serveStatePath())
	if _, ok := readServeState(); ok {
		t.Fatal("no-file case reports a recorded client sync")
	}
	now := time.Now().Truncate(time.Second)
	if err := writeServeState(now); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, ok := readServeState()
	if !ok {
		t.Fatal("state not readable after write")
	}
	if !st.LastClientSync.Equal(now.UTC()) {
		t.Errorf("LastClientSync = %v, want %v", st.LastClientSync, now.UTC())
	}
}
