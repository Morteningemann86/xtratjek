package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

func TestSyncStateRoundTrip(t *testing.T) {
	if err := writeSyncState(syncSummary{sent: 7, received: 9, conflicts: 2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, ok, err := readSyncState()
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if st.Sent != 7 || st.Received != 9 || st.Conflicts != 2 {
		t.Errorf("state = %+v, want sent7/recv9/conf2", st)
	}
	if time.Since(st.LastSync) > time.Minute || st.LastSync.IsZero() {
		t.Errorf("LastSync = %v, want ~now", st.LastSync)
	}
}

func TestReadSyncStateAbsent(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := os.Remove(syncStatePath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove: %v", err)
	}
	_, ok, err := readSyncState()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ok {
		t.Error("want ok=false when no state file exists")
	}
}

func TestPrintSyncStatus(t *testing.T) {
	setTestHome(t, t.TempDir())
	// No state → "never".
	if err := os.Remove(syncStatePath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove: %v", err)
	}
	out := captureStdout(t, func() { printSyncStatus(syncConfig{}) })
	if !strings.Contains(out, "last sync: never") {
		t.Errorf("no-state status = %q, want it to say never", out)
	}
	if !strings.Contains(out, "(none configured)") {
		t.Errorf("no-url status = %q, want it to note no server", out)
	}
	// With state + a URL → shows the server and a summary line.
	if err := writeSyncState(syncSummary{sent: 3, received: 4, conflicts: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}
	out = captureStdout(t, func() { printSyncStatus(syncConfig{URL: "http://example:8765"}) })
	if !strings.Contains(out, "http://example:8765") {
		t.Errorf("status = %q, want the configured URL", out)
	}
	if !strings.Contains(out, "sent 3, received 4, 1 conflict") {
		t.Errorf("status = %q, want the sync summary", out)
	}
}

// A local write landing while the sync round trip is in flight must survive
// the apply. Before the re-merge fix, runClientSync saved the server's merged
// set blind: a comment added after the client loaded its push set was missing
// from the response, so saveChildren tombstoned it as "vanished" — and that
// deletion then propagated. The test's server handler plays the concurrent
// writer: it mutates the client DB before responding.
func TestSyncConcurrentLocalWriteSurvives(t *testing.T) {
	ch := openTestDB(t)
	a := todo.New("task")
	a.ID = "a"
	saveTodos(t, ch, []todo.Todo{a})

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		var req tasksync.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode push: %v", err)
		}
		// The concurrent local write: a comment added mid-flight, so it is
		// absent from both the push and the response.
		withComment := a
		withComment.AddComment("added mid-flight")
		saveTodos(t, ch, []todo.Todo{withComment})
		if err := json.NewEncoder(w).Encode(tasksync.Response{Tasks: req.Tasks}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	if _, err := runClientSync(ch, syncConfig{URL: ts.URL, Token: "tok"}, 5*time.Second, rank.DefaultBiases(), defaultBoardConfig().wire()); err != nil {
		t.Fatalf("client sync: %v", err)
	}

	live, err := loadTodosFromDB(ch)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, task := range live {
		if task.ID == "a" {
			if len(task.Comments) != 1 {
				t.Fatalf("comment added during the round trip was lost: %d live comments, want 1", len(task.Comments))
			}
			return
		}
	}
	t.Fatal("task a missing after sync")
}

func TestDroppedLocalEditsDeletionVsEdit(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	// Baseline: the last successful sync predates every edit below, so the
	// since-filter is inert for these cases (they test the delete-vs-edit and
	// contested-edit rules). TestDroppedLocalEditsBaseline covers the filter.
	since := base.Add(-time.Hour)

	// A task the client still has live.
	live := todo.New("task")
	live.ModifiedAt = base

	// Case 1: another device deleted it AFTER our last edit → plain deletion,
	// not a conflict.
	delAfter := live
	delAfter.Deleted = true
	delAfter.DeletedAt = base.Add(time.Hour)
	if d := tasksync.DroppedLocalEdits([]todo.Todo{live}, []todo.Todo{delAfter}, since); len(d) != 0 {
		t.Errorf("remote deletion of an unedited live task should not be a conflict, got %d", len(d))
	}

	// Case 2: we edited it AFTER the deletion timestamp → a genuine edit that
	// lost to a delete → conflict.
	edited := live
	edited.ModifiedAt = base.Add(2 * time.Hour)
	delBefore := live
	delBefore.Deleted = true
	delBefore.DeletedAt = base.Add(time.Hour)
	if d := tasksync.DroppedLocalEdits([]todo.Todo{edited}, []todo.Todo{delBefore}, since); len(d) != 1 {
		t.Errorf("a local edit newer than the deletion should be a conflict, got %d", len(d))
	}

	// Case 3: both sides live, scalar fields differ, local edited since the
	// last sync → conflict.
	if d := tasksync.DroppedLocalEdits([]todo.Todo{live}, []todo.Todo{contested(live)}, since); len(d) != 1 {
		t.Errorf("a contested live edit should still be a conflict, got %d", len(d))
	}
}

func contested(l todo.Todo) todo.Todo {
	l.Title = "server wording"
	return l
}

// TestDroppedLocalEditsBaseline: a task NOT modified locally since the last
// successful sync is merely stale when the merge replaces it — inbound
// propagation of another device's edit, not a dropped local edit. Logging it
// was the false-positive noise that drowned sync.log and made the
// `sync --status` conflict count meaningless.
func TestDroppedLocalEditsBaseline(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	local := todo.New("task")
	local.ModifiedAt = base

	remote := local
	remote.Title = "edited elsewhere"
	remote.ModifiedAt = base.Add(2 * time.Hour)

	// Last sync happened after our copy's ModifiedAt → we haven't touched it →
	// the overwrite is plain propagation, not a conflict.
	if d := tasksync.DroppedLocalEdits([]todo.Todo{local}, []todo.Todo{remote}, base.Add(time.Hour)); len(d) != 0 {
		t.Errorf("inbound remote edit of an untouched task logged as conflict, got %d", len(d))
	}

	// Zero baseline (no sync ever recorded) → conservative: log it.
	if d := tasksync.DroppedLocalEdits([]todo.Todo{local}, []todo.Todo{remote}, time.Time{}); len(d) != 1 {
		t.Errorf("with no baseline the overwrite should be logged, got %d", len(d))
	}
}

// TestSyncSaveIgnoresEnvOverlay: `sync --save` persists file values + explicit
// flags, never the TJEK_SYNC_URL/TOKEN env overlay — a one-off env var must
// not get baked into sync.json where it would silently outlive the shell.
func TestSyncSaveIgnoresEnvOverlay(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("TJEK_SYNC_URL", "http://env-only:1")
	t.Setenv("TJEK_SYNC_TOKEN", "env-only-token")
	t.Cleanup(func() { _ = os.Remove(syncConfigPath()) })

	// URL from a flag, token only from env. The sync itself fails (dead
	// server, exit 1) but --save applies before the network round trip.
	if rc := cliSync([]string{"--url", "http://127.0.0.1:1", "--save", "--quiet"}); rc != 1 {
		t.Fatalf("sync against a dead server should fail with 1, got %d", rc)
	}

	saved := loadSyncConfigFile()
	if saved.URL != "http://127.0.0.1:1" {
		t.Errorf("--save should persist the flag URL, got %q", saved.URL)
	}
	if saved.Token != "" {
		t.Errorf("env token must not be baked into sync.json, got %q", saved.Token)
	}
	// The runtime view still sees the env overlay.
	if run := loadSyncConfig(); run.Token != "env-only-token" {
		t.Errorf("runtime config should overlay the env token, got %q", run.Token)
	}
}

// The insecure-URL warning must fire only for plain http to genuinely public
// hosts — private transports (Tailscale, LAN, loopback) and https stay quiet,
// so the warning keeps signal when it does appear.
func TestInsecureSyncURLWarning(t *testing.T) {
	quiet := []string{
		"",
		"https://tasks.example.com",
		"http://127.0.0.1:8765",
		"http://localhost:8765",
		"http://100.122.178.43:8765", // Tailscale CGNAT
		"http://192.168.1.10:8765",   // RFC1918
		"http://10.0.0.5:8765",
		"http://hoth.tail1234.ts.net:8765",
	}
	for _, u := range quiet {
		if w := tasksync.InsecureURLWarning(u); w != "" {
			t.Errorf("unexpected warning for %q: %s", u, w)
		}
	}
	loud := []string{
		"http://203.0.113.7:8765",     // public IP
		"http://tasks.example.com:80", // public hostname
		"http://100.32.1.1:8765",      // 100.x but below CGNAT range
	}
	for _, u := range loud {
		if w := tasksync.InsecureURLWarning(u); w == "" {
			t.Errorf("expected warning for %q, got none", u)
		}
	}
}

// TestClockSkewWarning: skew beyond the merge tolerance must warn (both
// directions), skew within it must not, and a server that predates the
// server_time field (zero) must skip the check entirely.
func TestClockSkewWarning(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	if w := tasksync.ClockSkewWarning(time.Time{}, now); w != "" {
		t.Errorf("zero server time should skip the check, got %q", w)
	}
	if w := tasksync.ClockSkewWarning(now.Add(-2*time.Minute), now); w != "" {
		t.Errorf("2m skew is inside tolerance, got %q", w)
	}
	if w := tasksync.ClockSkewWarning(now.Add(-20*time.Minute), now); w == "" {
		t.Error("client 20m ahead of server should warn")
	}
	if w := tasksync.ClockSkewWarning(now.Add(20*time.Minute), now); w == "" {
		t.Error("client 20m behind server should warn")
	}
}

// TestStaleSyncGuard: a device whose last successful sync predates the
// deletion-memory window must be flagged stale (auto-sync pauses; manual sync
// needs --accept-stale), a recently-synced device must not, and a device that
// has never synced must not (first-sync onboarding holds nothing the fleet
// ever deleted).
func TestStaleSyncGuard(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Cleanup(func() { _ = os.Remove(syncStatePath()) })
	now := time.Now()

	// Never synced → not stale.
	_ = os.Remove(syncStatePath())
	if _, stale := staleSyncGap(now); stale {
		t.Error("device with no sync history must not be stale")
	}

	// Synced recently → not stale.
	if err := writeSyncState(syncSummary{}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if gap, stale := staleSyncGap(now); stale {
		t.Errorf("freshly synced device flagged stale (gap %s)", gap)
	}

	// Fake a last sync from beyond the window → stale.
	old := syncState{LastSync: now.Add(-staleSyncThreshold - 24*time.Hour)}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(syncStatePath(), b, 0600); err != nil {
		t.Fatalf("write old state: %v", err)
	}
	gap, stale := staleSyncGap(now)
	if !stale {
		t.Fatalf("device %s past threshold not flagged stale", gap)
	}
	if notice := staleSyncNotice(gap); !strings.Contains(notice, "hasn't synced") {
		t.Errorf("notice should explain the situation, got %q", notice)
	}
}

// TestCLISyncRefusesStale: without --accept-stale a stale device's manual sync
// exits 2 before touching the network; with the flag it proceeds (and fails on
// the unreachable test URL with exit 1, proving it got past the guard).
func TestCLISyncRefusesStale(t *testing.T) {
	setTestHome(t, t.TempDir())
	// Only the data directory is created eagerly (the paths package); a writer of the
	// state directory has to make it, which is what writeSyncState does before
	// it writes this same file.
	if _, err := paths.Ensure(paths.State); err != nil {
		t.Fatalf("ensure state dir: %v", err)
	}
	old := syncState{LastSync: time.Now().Add(-staleSyncThreshold - 24*time.Hour)}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(syncStatePath(), b, 0600); err != nil {
		t.Fatalf("write old state: %v", err)
	}

	// 127.0.0.1:1 refuses instantly, so the --accept-stale path fails fast at
	// the network layer rather than hanging the test.
	args := []string{"--url=http://127.0.0.1:1", "--token=tok"}
	if rc := cliSync(args); rc != 2 {
		t.Errorf("stale sync without flag: want exit 2, got %d", rc)
	}
	if rc := cliSync(append(args, "--accept-stale")); rc != 1 {
		t.Errorf("stale sync with flag should pass the guard and fail on network: want 1, got %d", rc)
	}
}
