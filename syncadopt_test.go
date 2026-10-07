package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// firstSyncHome sets up a private home with a store holding n live tasks and
// no recorded sync — the state a machine is in the moment someone configures
// sync on it for the first time.
func firstSyncHome(t *testing.T, titles ...string) {
	t.Helper()
	// A space in the home on purpose: macOS keeps state under "Application
	// Support", and the adopt-remote notice prints a path meant to be pasted.
	// Testing only space-free Linux paths is how that notice shipped unquoted.
	home := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	if err := openStore(); err != nil {
		t.Fatalf("open store: %v", err)
	}
	tasks := make([]todo.Todo, 0, len(titles))
	for _, title := range titles {
		tasks = append(tasks, todo.New(title))
	}
	if len(tasks) > 0 {
		saveTodos(t, db, tasks)
	}
}

// The gate is exactly "never synced AND holds tasks of its own". A fresh
// install has nothing to push and must not be stopped; a device already in the
// fleet answered this once and must never be asked again — least of all on an
// upgrade, where every existing machine would suddenly refuse to sync.
func TestFirstSyncGate(t *testing.T) {
	cfg := syncConfig{URL: "http://example:8765", Token: "tok"}

	firstSyncHome(t, "Leftover from June")
	if !firstSyncNeedsChoice(cfg, db) {
		t.Error("a never-synced device holding tasks should owe the choice")
	}

	firstSyncHome(t)
	if firstSyncNeedsChoice(cfg, db) {
		t.Error("a fresh install has nothing of its own to push; it must not be gated")
	}

	firstSyncHome(t, "Leftover from June")
	if err := writeSyncState(syncSummary{sent: 1}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if firstSyncNeedsChoice(cfg, db) {
		t.Error("a device that has synced before must not be asked")
	}

	// And the recorded answer survives a lost state directory, which is the
	// whole reason it is kept in sync.json.
	firstSyncHome(t, "Leftover from June")
	if err := recordFirstSyncChoice(); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := os.Remove(syncStatePath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove state: %v", err)
	}
	if firstSyncNeedsChoice(loadSyncConfig(), db) {
		t.Error("the recorded answer should outlive the sync state file")
	}
}

// An unanswered first sync stops before the network and prints both answers.
func TestCLISyncRefusesTheFirstSyncUntilAnswered(t *testing.T) {
	firstSyncHome(t, "Leftover from June", "Another leftover")
	// 127.0.0.1:1 refuses instantly, so anything that got past the gate would
	// fail at the network with exit 1 instead of the 2 asserted here.
	args := []string{"--url=http://127.0.0.1:1", "--token=tok"}
	out := captureStderr(t, func() {
		if rc := cliSync(args); rc != 2 {
			t.Errorf("unanswered first sync: want exit 2, got %d", rc)
		}
	})
	for _, want := range []string{"never synced", "2 task(s)", "--adopt-local", "--adopt-remote", "Leftover from June"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should mention %q:\n%s", want, out)
		}
	}
	// --adopt-local is an answer, so the sync runs and fails on the network.
	if rc := cliSync(append(args, "--adopt-local")); rc != 1 {
		t.Errorf("--adopt-local should pass the gate and fail on the network: want 1, got %d", rc)
	}
	if !loadSyncConfigFile().Adopted {
		t.Error("the answer should be recorded in sync.json")
	}
	// Answered once, never asked again.
	if rc := cliSync(args); rc != 1 {
		t.Errorf("after adopting, a plain sync should proceed: want 1 (network), got %d", rc)
	}
}

// --adopt-remote is the answer that had no implementation: the local tasks are
// backed up and cleared before the push, so they cannot reach the fleet, and
// what comes back is the fleet's list.
func TestCLISyncAdoptRemote(t *testing.T) {
	firstSyncHome(t, "Leftover from June")
	fleet := todo.New("The fleet's task")
	fleet.ID = "fleet-1"

	var pushed []todo.Todo
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		var req tasksync.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode push: %v", err)
		}
		pushed = req.Tasks
		if err := json.NewEncoder(w).Encode(tasksync.Response{Tasks: []todo.Todo{fleet}, ServerTime: time.Now()}); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	out := captureStderr(t, func() {
		if rc := cliSync([]string{"--url=" + ts.URL, "--token=tok", "--adopt-remote", "--quiet"}); rc != 0 {
			t.Fatalf("adopt-remote: want exit 0, got %d", rc)
		}
	})
	if len(pushed) != 0 {
		t.Errorf("adopt-remote pushed %d task(s); the whole point is that it pushes none", len(pushed))
	}
	live, err := loadTodosFromDB(db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(live) != 1 || live[0].Title != "The fleet's task" {
		t.Fatalf("after adopt-remote the store should hold the fleet's list, got %+v", live)
	}
	// The backup is real, is an export, and the message says where it is —
	// as a command that survives being pasted. The file is found on disk
	// rather than cut out of the sentence: on macOS the state directory is
	// "Application Support", and splitting the notice on spaces is exactly
	// the mistake a user's shell would make with an unquoted path.
	matches, err := filepath.Glob(paths.For(paths.State, "pre-sync-backup-*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("want exactly one backup in the state dir, got %v (err %v)", matches, err)
	}
	path := matches[0]
	if want := "tjek import " + shellArg(path, runtime.GOOS); !strings.Contains(out, want) {
		t.Errorf("the notice should carry a pasteable %q:\n%s", want, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	tasks, err := parseExportData(b)
	if err != nil {
		t.Fatalf("backup is not a valid export: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Leftover from June" {
		t.Errorf("backup should hold the cleared tasks, got %+v", tasks)
	}
}

// A stray --adopt-remote on a device that is already in the fleet must not
// clear anything: the flag answers a question that device is not being asked.
func TestAdoptRemoteIsInertOnAnEstablishedDevice(t *testing.T) {
	firstSyncHome(t, "A real task")
	if err := writeSyncState(syncSummary{sent: 1}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if rc := resolveFirstSync(syncConfig{}, false, true); rc != 0 {
		t.Fatalf("want 0 (carry on), got %d", rc)
	}
	live, err := loadTodosFromDB(db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("the store was cleared by a stray --adopt-remote: %d task(s) left", len(live))
	}
}

// The unattended paths are the ones that caused this: they must decline, and
// say where the choice is made.
func TestAutoSyncPausesOnAnUnansweredFirstSync(t *testing.T) {
	firstSyncHome(t, "Leftover from June")
	var pushed int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		pushed++
		if err := json.NewEncoder(w).Encode(tasksync.Response{ServerTime: time.Now()}); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := saveSyncConfig(syncConfig{URL: ts.URL, Token: "tok"}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	out := captureStderr(t, maybeAutoSyncCLI)
	if pushed != 0 {
		t.Errorf("CLI auto-sync uploaded on an unanswered first sync (%d request(s))", pushed)
	}
	if !strings.Contains(out, "auto-sync paused") || !strings.Contains(out, "tjek sync") {
		t.Errorf("auto-sync should say why it paused and where to choose:\n%s", out)
	}

	// The TUI path takes the same gate, and its message reaches the Settings
	// footer through the error on syncDoneMsg.
	m := model{syncCfg: loadSyncConfig()}
	msg, ok := m.backgroundSync()().(syncDoneMsg)
	if !ok {
		t.Fatal("backgroundSync should answer with a syncDoneMsg")
	}
	if msg.err == nil || !strings.Contains(msg.err.Error(), "never synced") {
		t.Errorf("TUI sync should pause with an explanation, got %v", msg.err)
	}
	if pushed != 0 {
		t.Errorf("TUI auto-sync uploaded on an unanswered first sync (%d request(s))", pushed)
	}
}

// The recovery command is printed to be pasted, so a path with a space must
// come out as one argument. POSIX is checked by actually running sh, which is
// the only honest test of a quoting rule.
func TestShellArgQuotesWhatAShellWouldSplit(t *testing.T) {
	cases := []struct{ in, goos, want string }{
		{"/home/u/.local/state/tjek/b.json", "linux", "/home/u/.local/state/tjek/b.json"},
		{"/Users/u/Library/Application Support/tjek/b.json", "darwin", "'/Users/u/Library/Application Support/tjek/b.json'"},
		{"/tmp/it's/b.json", "linux", `'/tmp/it'\''s/b.json'`},
		{`C:\Users\u\AppData\Local\tjek\b.json`, "windows", `"C:\Users\u\AppData\Local\tjek\b.json"`},
		{"", "linux", "''"},
	}
	for _, c := range cases {
		if got := shellArg(c.in, c.goos); got != c.want {
			t.Errorf("shellArg(%q, %s) = %s, want %s", c.in, c.goos, got, c.want)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	for _, in := range []string{"/a b/c.json", "/it's/$HOME/`x`/b.json", "/plain/b.json"} {
		out, err := exec.Command(sh, "-c", `printf %s `+shellArg(in, runtime.GOOS)).Output()
		if err != nil {
			t.Fatalf("sh: %v", err)
		}
		if string(out) != in {
			t.Errorf("sh read %q back as %q", in, out)
		}
	}
}
