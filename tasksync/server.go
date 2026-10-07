package tasksync

import (
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// Store is the doorway the sync engine needs into task storage — the only
// thing this package asks of the application. Keeping it to one method keeps
// the package free of SQL, file paths and app config.
type Store interface {
	// MergeIn folds incoming into the store atomically and returns the merged
	// authoritative set (tombstones included). changed=false means the store
	// already contained the result and nothing was written — callers use it to
	// skip change broadcasts so idle syncs can't feed back.
	MergeIn(incoming []todo.Todo) (merged []todo.Todo, changed bool, err error)
}

// Request and Response are the /v1/sync wire format: the client pushes its
// full task set (tombstones included) and receives the merged authoritative
// set in one round trip.
type Request struct {
	Tasks []todo.Todo `json:"tasks"`
	// Board is this device's kanban column list, when it is sharing one.
	// Optional in both directions and absent on every older peer — see
	// board.go for why that is the whole compatibility story.
	Board *Board `json:"board,omitempty"`
	// Protocol is the wire version the client was built against. Absent
	// (zero) means a client from before versioning existed, which speaks
	// v1 — see legacyProtocolVersion.
	Protocol int `json:"v,omitempty"`
}

type Response struct {
	Tasks []todo.Todo `json:"tasks"`
	// Board is the fleet's column list after the merge, or nil when this
	// server keeps none.
	Board *Board `json:"board,omitempty"`
	// Protocol is the wire version the server speaks, so a newer client can
	// tell an older server apart from one that simply sent nothing. Zero
	// from a server that predates the field.
	Protocol int `json:"v,omitempty"`
	// ServerTime lets the client detect a skewed local clock (see
	// ClockSkewWarning): the LWW merge runs on wall-clock timestamps, so a
	// device with a bad clock silently loses or wrongly wins conflicts, and
	// nothing else in the protocol would ever tell the user. Zero when the
	// server predates the field; clients skip the check then.
	ServerTime time.Time `json:"server_time,omitempty"`
	// ServerVersion is the tjek build that answered, filled in by PostSync
	// from VersionHeader. Not on the wire (`json:"-"`) on purpose: it has to
	// come from the header to be readable on the error responses that carry
	// no body at all, and one source beats two that can disagree.
	ServerVersion string `json:"-"`
}

// Server is the sync endpoint: POST /v1/sync does push+pull in one round trip,
// GET /v1/events streams change nudges. It is single-owner by design — one
// shared bearer token, not multi-tenant. Transport (Tailscale IP, localhost
// behind a proxy, LAN) is the caller's deployment choice.
type Server struct {
	Token string
	Store Store
	// Version is the tjek build this server is running, stamped onto every
	// response via VersionHeader. Empty means unknown (a test server, or a
	// build without the ldflags stamp) and the header is then omitted rather
	// than sent blank — a client must be able to tell "the server did not say"
	// from "the server said something".
	Version string
	// Board, when non-nil, is where the fleet's shared kanban column list is
	// kept. Nil leaves every client's columns to itself.
	Board BoardStore
	// Hub, when non-nil, is nudged after every merge that changed the store so
	// connected clients pull immediately.
	Hub *Hub
	// OnClientSync, when non-nil, is told the time of every authenticated sync
	// (the app records it for `sync --status`). Called under the sync lock —
	// keep it fast; throttling is the callback's business.
	OnClientSync func(time.Time)

	mu sync.Mutex
	// boardMu guards the column list's read-merge-write. Its own lock, not the
	// sync lock: the board is folded outside Sync so a preference can never
	// hold up (or fail) the task merge.
	boardMu sync.Mutex
}

// Handler builds the route set. Shared by every entry point (headless serve,
// in-process TUI server, tests) so they never drift.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/sync", s.handleSync)
	mux.HandleFunc("/v1/events", s.handleEvents)
	return s.stampVersion(mux)
}

// stampVersion sets VersionHeader on every response the server produces.
// Wrapping the mux rather than setting it per handler is deliberate: the
// responses that most need it are the ones no handler writes on purpose —
// the 500 from a failed merge, the 401 from a stale token — and a per-handler
// header is exactly what a future error path would forget.
func (s *Server) stampVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Version != "" {
			w.Header().Set(VersionHeader, s.Version)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// The supported range rides along as a header: the body is a liveness
	// contract other code asserts on, and health is deliberately
	// unauthenticated, so this is the one place an operator can read what a
	// server speaks without holding the sync token.
	w.Header().Set(ProtocolHeader, fmt.Sprintf("%d-%d", MinProtocolVersion, ProtocolVersion))
	fmt.Fprintln(w, "ok")
}

func (s *Server) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimPrefix(h, prefix)
	// Constant-time compare so the token can't be guessed by timing.
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// On every answer, errors included: this is how a client learns it may
	// compress its next request (see compress.go).
	w.Header().Set("Accept-Encoding", "gzip")
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, closeBody, ok := syncRequestBody(w, r)
	if !ok {
		return
	}
	defer closeBody()
	var req Request
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Version check before the merge, never after: the whole point is to
	// refuse a payload we might misread rather than fold it into the
	// authoritative set and discover the mismatch through corrupt data.
	// 409 rather than 400 — the request is well-formed, the two ends just
	// disagree about what it means.
	if err := negotiate(req.Protocol); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	merged, err := s.Sync(req.Tasks)
	if err != nil {
		http.Error(w, "merge failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(ProtocolHeader, fmt.Sprintf("%d-%d", MinProtocolVersion, ProtocolVersion))
	w.Header().Add("Vary", "Accept-Encoding")
	var out io.Writer = w
	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		defer func() {
			if err := zw.Close(); err != nil {
				log.Printf("tjek serve: finish gzip response: %v", err)
			}
		}()
		out = zw
	}
	if err := json.NewEncoder(out).Encode(Response{
		Tasks:      merged,
		Board:      s.syncBoard(req.Board),
		ServerTime: time.Now().UTC(),
		Protocol:   ProtocolVersion,
	}); err != nil {
		log.Printf("tjek serve: encode response: %v", err)
	}
}

// Sync merges the client's tasks into the authoritative set and returns it.
// Serialized by mu so concurrent HTTP syncs can't interleave; atomicity
// against writers in OTHER processes is the Store's job (MergeIn).
func (s *Server) Sync(clientTasks []todo.Todo) ([]todo.Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clampFutureEventTimes(clientTasks, time.Now())
	if s.OnClientSync != nil {
		s.OnClientSync(time.Now())
	}
	merged, changed, err := s.Store.MergeIn(clientTasks)
	if err != nil {
		return nil, err
	}
	// Nudge every connected client to pull — only when something was actually
	// written (a no-op pull must not broadcast, or syncs would feed back).
	if changed && s.Hub != nil {
		s.Hub.Broadcast()
	}
	return merged, nil
}

// syncBoard folds a client's column list into the stored one and returns the
// winner, or nil when this server keeps no board.
//
// A failure here is logged and swallowed: the column names are a preference
// riding along with the task sync, and the tasks are the part that must not be
// lost to a missing or unreadable preferences file.
func (s *Server) syncBoard(incoming *Board) *Board {
	if s.Board == nil {
		return nil
	}
	s.boardMu.Lock()
	defer s.boardMu.Unlock()
	stored, err := s.Board.LoadBoard()
	if err != nil {
		log.Printf("tjek serve: read board: %v", err)
		return nil
	}
	winner := stored
	if incoming != nil {
		winner = MergeBoard(stored, *incoming)
	}
	if !SameBoard(winner, stored) || winner.ModifiedAt.After(stored.ModifiedAt) {
		if err := s.Board.SaveBoard(winner); err != nil {
			log.Printf("tjek serve: write board: %v", err)
		}
	}
	if len(winner.Stages) == 0 {
		return nil
	}
	return &winner
}

// handleEvents streams change notifications to a client as Server-Sent Events.
// It blocks for the life of the connection (its own request goroutine) and
// returns — closing the stream — when the client disconnects or the request
// context is cancelled.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if s.Hub == nil {
		http.Error(w, "events unavailable", http.StatusServiceUnavailable)
		return
	}
	ch := s.Hub.Subscribe()
	if ch == nil {
		http.Error(w, "too many subscribers", http.StatusServiceUnavailable)
		return
	}
	defer s.Hub.Unsubscribe(ch)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// An initial comment opens the stream so the client (and any proxy) flushes
	// headers and treats the connection as live immediately.
	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()

	beat := time.NewTicker(sseHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: changed\ndata: 1\n\n")
			flusher.Flush()
		case <-beat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// maxClientClockSkew bounds how far ahead of the server's clock a client's
// merge-ordering timestamps may run. The merge is last-writer-wins by
// ModifiedAt/DeletedAt, all stamped from device wall clocks — a device with a
// clock hours in the future would win every conflict it touches until real
// time catches up (and its edits would be unbeatable by devices with correct
// clocks). Five minutes tolerates ordinary NTP drift without letting a broken
// clock own the store.
const maxClientClockSkew = 5 * time.Minute

// clampFutureEventTimes pulls any merge-ordering timestamp (task and child
// ModifiedAt/DeletedAt) that is more than maxClientClockSkew ahead of now back
// to now, in place. Domain dates (DueDate, StartDate, time-entry bounds) are
// deliberately untouched — a future due date is data, not clock skew.
func clampFutureEventTimes(tasks []todo.Todo, now time.Time) {
	limit := now.Add(maxClientClockSkew)
	clamp := func(t *time.Time) {
		if t.After(limit) {
			*t = now
		}
	}
	for i := range tasks {
		t := &tasks[i]
		clamp(&t.ModifiedAt)
		clamp(&t.DeletedAt)
		for j := range t.Comments {
			clamp(&t.Comments[j].ModifiedAt)
			clamp(&t.Comments[j].DeletedAt)
		}
		for j := range t.TimeEntries {
			clamp(&t.TimeEntries[j].ModifiedAt)
			clamp(&t.TimeEntries[j].DeletedAt)
		}
	}
}

// StoreDigest is an order-independent fingerprint of a task set: identical
// content hashes identically regardless of slice ordering. It must have no
// false positives — a stable order is imposed on the task slice and every
// child/tag slice before hashing.
func StoreDigest(ts []todo.Todo) [32]byte {
	cp := make([]todo.Todo, len(ts))
	copy(cp, ts)
	for i := range cp {
		canonicalizeForDigest(&cp[i])
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	b, _ := json.Marshal(cp)
	return sha256.Sum256(b)
}

// CanonicalJSON is a single task's order-insensitive fingerprint source: a
// value copy with its unordered slices sorted, marshalled. The app's
// changed-row detection compares these to decide what a merge must write.
func CanonicalJSON(t todo.Todo) []byte {
	canonicalizeForDigest(&t)
	b, _ := json.Marshal(t)
	return b
}

// canonicalizeForDigest replaces a task's order-insensitive slices with sorted
// copies — never mutating the caller's backing arrays — so digests are stable
// across the reordering a merge may introduce. Timestamps are normalized to
// UTC: json.Marshal embeds the zone offset, and times are compared as
// instants, so two loads of the same store must hash identically even when
// one rehydrated in a different zone (parseTime returns local time).
func canonicalizeForDigest(t *todo.Todo) {
	t.Tags = sortedStrings(t.Tags)
	t.Dependencies = sortedStrings(t.Dependencies)
	t.CreatedAt = t.CreatedAt.UTC()
	t.ModifiedAt = t.ModifiedAt.UTC()
	t.CompletedAt = t.CompletedAt.UTC()
	t.StartDate = t.StartDate.UTC()
	t.DueDate = t.DueDate.UTC()
	t.DeletedAt = t.DeletedAt.UTC()
	if len(t.Comments) > 0 {
		c := append([]todo.Comment(nil), t.Comments...)
		sort.Slice(c, func(i, j int) bool { return c[i].ID < c[j].ID })
		for i := range c {
			c[i].CreatedAt = c[i].CreatedAt.UTC()
			c[i].ModifiedAt = c[i].ModifiedAt.UTC()
			c[i].DeletedAt = c[i].DeletedAt.UTC()
		}
		t.Comments = c
	}
	if len(t.TimeEntries) > 0 {
		e := append([]todo.TimeEntry(nil), t.TimeEntries...)
		sort.Slice(e, func(i, j int) bool { return e[i].ID < e[j].ID })
		for i := range e {
			e[i].StartedAt = e[i].StartedAt.UTC()
			e[i].StoppedAt = e[i].StoppedAt.UTC()
			e[i].ModifiedAt = e[i].ModifiedAt.UTC()
			e[i].DeletedAt = e[i].DeletedAt.UTC()
			e[i].LastSeen = e[i].LastSeen.UTC()
		}
		t.TimeEntries = e
	}
}

func sortedStrings(s []string) []string {
	if len(s) < 2 {
		return s
	}
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}
