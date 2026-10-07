package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// testHome is the fake home directory TestMain redirects the whole binary into.
// Kept in a package var so TestStorageStaysInsideTheTestHome can assert the
// redirect actually took, rather than trusting that it did.
var testHome string

// TestMain isolates the entire test binary from the real ~/.tjek. Storage
// paths derive from os.UserHomeDir (getStoragePath/dbPath/tjekDir), and several
// tests build a model via initialModel — which opens the store — so without
// this redirect a plain `go test` would read and create files under the
// developer's real task directory.
//
// os.UserHomeDir reads a *different* variable per platform: $HOME on unix,
// %USERPROFILE% on Windows. Setting only HOME therefore left Windows test runs
// pointed at the real home — the exact accident this function exists to
// prevent — so both are set.
func TestMain(m *testing.M) {
	// Run as the fake whisper-cli a test pointed the app at (fakeWhisperCLI),
	// before anything else, since that process is not a test run.
	if text, ok := os.LookupEnv(fakeWhisperTextEnv); ok {
		fmt.Println(text)
		os.Exit(0)
	}
	tmp, err := os.MkdirTemp("", "tjek-test-home")
	if err != nil {
		panic(err)
	}
	testHome = tmp
	os.Setenv("HOME", tmp)
	os.Setenv("USERPROFILE", tmp) // os.UserHomeDir on Windows
	// Windows resolves config to %APPDATA% and everything else to
	// %LOCALAPPDATA% *before* it ever looks at the home directory (the paths package),
	// so redirecting the home alone left the whole Windows suite reading and
	// writing the runner's real C:\Users\…\AppData\Local\tjek — the
	// accident this function exists to prevent, and the reason
	// TestStorageStaysInsideTheTestHome was failing on that platform only.
	// Pointed into the temp home rather than unset, so the branch real Windows
	// takes is the branch the tests exercise.
	setWindowsAppData(os.Setenv, tmp)
	// Paths now resolve through XDG (the paths package), and an XDG_* variable exported
	// in the developer's shell is absolute — it would send the whole suite to
	// the real ~/.local/share while HOME pointed somewhere harmless. Redirect
	// them into the temp home rather than unsetting them, so the XDG branch is
	// what the tests actually exercise.
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		os.Unsetenv(v)
	}
	os.Unsetenv("TJEK_HOME")
	code := m.Run()
	// The last test to open the store leaves it open; on Windows that is
	// enough to keep the temp home from being removed.
	closeStoreSingleton()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// setTestHome points os.UserHomeDir at dir for the duration of one test,
// restoring the previous value afterwards. Both variables, for the reason
// TestMain sets both: t.Setenv("HOME", …) alone is a no-op on Windows, where
// os.UserHomeDir reads %USERPROFILE% — so a test "isolating" itself that way
// was still writing into the shared test home, and the files it left behind
// (a legacy tasks.json, a settings.json) leaked into every later test.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	// Same reason TestMain sets them: on Windows these win over the home this
	// function just redirected, so a test isolating itself would still share
	// one real AppData directory with every other test — and with the person
	// running the suite.
	setWindowsAppData(func(k, v string) error { t.Setenv(k, v); return nil }, dir)
	// Same reason TestMain clears these: an absolute XDG_* or TJEK_HOME would
	// override the home this function just redirected, and the test would write
	// outside its own directory.
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TJEK_HOME"} {
		t.Setenv(v, "")
	}
	// Redirecting the paths is only half of it: the store the paths lead to is
	// a singleton that outlives any one test.
	releaseStoreSingleton(t)
}

// setWindowsAppData points the two variables the paths package consults on Windows at
// dir. Harmless elsewhere — nothing reads them — so it is unconditional rather
// than behind a GOOS check, which keeps the Linux run honest about what the
// Windows run does.
func setWindowsAppData(set func(string, string) error, dir string) {
	_ = set("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	_ = set("LOCALAPPDATA", filepath.Join(dir, "AppData", "Local"))
}

// releaseStoreSingleton gives a test the store to itself, at both ends.
//
// openStore keeps one *sql.DB for the whole binary behind a sync.Once, so
// without this the first test to open it decides where every later test's
// tasks live: a test that redirects its home and then loads tasks gets handed
// the previous home's database, silently, and asserts against the wrong file.
// Releasing on entry closes that; releasing again on exit means the next test
// is not pinned to a directory that t.TempDir has since deleted — and on
// Windows, which refuses to delete an open file, that cleanup is what fails
// with an unlinkat error saying nothing about the test.
func releaseStoreSingleton(t *testing.T) {
	t.Helper()
	closeStoreSingleton()
	t.Cleanup(closeStoreSingleton)
}

// closeStoreSingleton closes the process-wide database and puts openStore back
// to "not opened yet", so the next caller opens one wherever the paths point
// then.
func closeStoreSingleton() {
	if db != nil {
		_ = db.Close()
	}
	db, dbErr, dbOnce = nil, nil, sync.Once{}
}

// testStore is how a test gets at the process-wide database: it opens it if
// nobody has since the last release, and hands back the handle. Reading the
// `db` global directly is only correct while something else has already opened
// it — and now that setTestHome releases the singleton around every isolated
// test, "already open" is not a property any test can assume.
func testStore(t *testing.T) *sql.DB {
	t.Helper()
	if err := openStore(); err != nil {
		t.Fatalf("openStore: %v", err)
	}
	return db
}

// The redirect is load-bearing: if it silently stops working on some platform,
// the suite starts writing to the developer's real ~/.tjek. Assert every
// storage path lands inside the temp home instead of finding out the hard way.
func TestStorageStaysInsideTheTestHome(t *testing.T) {
	if testHome == "" {
		t.Fatal("TestMain did not record the temp home")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	if home != testHome {
		t.Fatalf("os.UserHomeDir() = %q, want the temp home %q — on %s it reads a variable TestMain does not set",
			home, testHome, runtime.GOOS)
	}
	// Every path the app can write, not just the database: config, state and
	// cache resolve through different roots now, and each is a way out of the
	// temp home if the redirect misses one.
	for _, path := range []string{
		tjekDir(), dbPath(), getStoragePath(), settingsPath(),
		syncConfigPath(), syncStatePath(), syncLogPath(), serveStatePath(),
		undoPersistPath(), lastAddedPath(), remindedPath(), notesFilePath("some-task-id"),
	} {
		if !strings.HasPrefix(path, testHome+string(filepath.Separator)) {
			t.Errorf("%q is outside the test home %q", path, testHome)
		}
	}
}

// A test that redirects the home directory must do it for every platform. The
// bare t.Setenv("HOME", …) form silently does nothing on Windows, where
// os.UserHomeDir reads %USERPROFILE% — the test then writes into the shared
// test home and leaves files behind for everything that runs after it. That is
// exactly how three Windows-only failures appeared: a legacy tasks.json from an
// import test seeded every database opened afterwards.
func TestNoBareHomeRedirectsInTests(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") || e.Name() == "main_test.go" {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `Setenv("HOME"`) {
			t.Errorf(`%s redirects HOME directly — use setTestHome(t, dir), which also sets `+
				`USERPROFILE so the redirect works on Windows`, e.Name())
		}
	}
}

// mustMkdirFor creates the directory a path lives in. Tests that write one of
// tjek's files directly need it now that config, data and state resolve to
// three different directories, only one of which the app creates eagerly.
func mustMkdirFor(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
}

// TestSetTestHomeGivesTheTestItsOwnStore pins the half of the isolation that is
// easy to lose: redirecting the paths is not enough while the store those paths
// lead to is a sync.Once that outlives the test. Without the release, the
// handle below would still be the one opened against the shared home, and a
// test asserting on "its" database would be reading someone else's.
func TestSetTestHomeGivesTheTestItsOwnStore(t *testing.T) {
	shared := testStore(t) // what an earlier test would have left open

	home := t.TempDir()
	setTestHome(t, home)

	if err := shared.Ping(); err == nil {
		t.Error("the previously opened store is still live after setTestHome")
	}
	own := testStore(t)
	if own == shared {
		t.Fatal("setTestHome handed back the store from the previous home")
	}
	if err := own.Ping(); err != nil {
		t.Fatalf("the test's own store is not usable: %v", err)
	}
	if got := dbPath(); !strings.HasPrefix(got, home) {
		t.Errorf("database at %q, want it inside the test's own home %q", got, home)
	}
}
