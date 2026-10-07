package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

func TestSSEEventsRequireToken(t *testing.T) {
	h := openTestDB(t)
	srv := &tasksync.Server{Token: "tok", Store: dbStore{h: h}, Hub: tasksync.NewHub()}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated events stream: want 401, got %d", w.Code)
	}
}

// TestSSEPushOnChange is the end-to-end contract: a subscriber connected to
// /v1/events is nudged when a sync changes the store, and is NOT nudged by a
// no-op sync (the skip-write guard).
func TestSSEPushOnChange(t *testing.T) {
	h := openTestDB(t)
	srv := &tasksync.Server{Token: "tok", Store: dbStore{h: h}, Hub: tasksync.NewHub()}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	events := make(chan string, 8)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open events stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events stream: want 200, got %d", resp.StatusCode)
	}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "event:") {
				events <- strings.TrimSpace(line[len("event:"):])
			}
		}
	}()

	// Give the subscriber a moment to register before the first broadcast.
	waitFor(t, func() bool { return srv.Hub.SubscriberCount() == 1 })

	// A real change → exactly one "changed" event.
	if _, err := srv.Sync([]todo.Todo{todo.New("brand new")}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	select {
	case ev := <-events:
		if ev != "changed" {
			t.Errorf("unexpected event %q", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a change event, got none")
	}

	// A no-op sync (client already in sync) must not nudge.
	if _, err := srv.Sync(nil); err != nil {
		t.Fatalf("no-op sync: %v", err)
	}
	select {
	case ev := <-events:
		t.Errorf("no-op sync should not push, got %q", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
