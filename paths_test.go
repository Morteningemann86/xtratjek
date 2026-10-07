package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Morteningemann86/xtratjek/paths"
)

// clearPathEnv puts one test in a known state: a fresh home, no XDG variables,
// no TJEK_HOME, no legacy directory.
func clearPathEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setTestHome(t, dir)
	return dir
}

// An existing ~/.tjek keeps everything where it was. This is the promise that
// makes the whole change safe: nobody's database moves because tjek grew an
// opinion about the XDG spec.
func TestLegacyDirectoryKeepsItsFiles(t *testing.T) {
	home := clearPathEnv(t)
	legacy := filepath.Join(home, paths.LegacyDirName)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"database": dbPath(),
		"settings": settingsPath(),
		"undo":     undoPersistPath(),
		"sync":     syncConfigPath(),
	} {
		if filepath.Dir(got) != legacy {
			t.Errorf("%s = %q, want it left in the legacy directory %q", name, got, legacy)
		}
	}
	if !paths.UsingLegacyLayout() {
		t.Error("usingLegacyLayout() = false with ~/.tjek present")
	}
}

// A file called .tjek is not a legacy install.
func TestLegacyDetectionIgnoresAFile(t *testing.T) {
	home := clearPathEnv(t)
	if err := os.WriteFile(filepath.Join(home, paths.LegacyDirName), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if paths.UsingLegacyLayout() {
		t.Error("a regular file named .tjek was treated as the legacy directory")
	}
}

// TJEK_HOME collapses the split back into one directory, for people who would
// rather back up a single path.
func TestTjekHomeOverridesEverything(t *testing.T) {
	clearPathEnv(t)
	one := t.TempDir()
	t.Setenv("TJEK_HOME", one)
	for _, got := range []string{dbPath(), settingsPath(), undoPersistPath(), syncLogPath(), notesFilePath("x")} {
		if !strings.HasPrefix(got, one) {
			t.Errorf("%q ignored TJEK_HOME=%q", got, one)
		}
	}
	if paths.UsingLegacyLayout() {
		t.Error("TJEK_HOME should not report the legacy layout")
	}
}

// An explicitly exported XDG variable wins on every platform, including the two
// that have conventions of their own.
func TestExplicitXDGWinsEverywhere(t *testing.T) {
	clearPathEnv(t)
	data, config, state, cache := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CACHE_HOME", cache)

	cases := []struct{ got, wantBase, name string }{
		{dbPath(), data, "database"},
		{settingsPath(), config, "settings"},
		{syncConfigPath(), config, "sync config"},
		{undoPersistPath(), state, "undo"},
		{syncLogPath(), state, "sync log"},
		{notesFilePath("id"), cache, "editor scratch"},
	}
	for _, c := range cases {
		want := filepath.Join(c.wantBase, paths.AppDirName)
		if !strings.HasPrefix(c.got, want) {
			t.Errorf("%s = %q, want it under %q", c.name, c.got, want)
		}
	}
}

// A relative XDG value is invalid per the spec and must be ignored rather than
// scattering files across the working directory.
func TestRelativeXDGValueIsIgnored(t *testing.T) {
	home := clearPathEnv(t)
	t.Setenv("XDG_DATA_HOME", "relative/path")
	got := dbPath()
	if !strings.HasPrefix(got, home) {
		t.Errorf("database = %q, want it back under the home %q", got, home)
	}
}

// The defaults, on a machine with nothing set.
func TestPlatformDefaults(t *testing.T) {
	home := clearPathEnv(t)
	db, settings, undo := dbPath(), settingsPath(), undoPersistPath()

	switch runtime.GOOS {
	case "windows":
		// The test home has no APPDATA/LOCALAPPDATA of its own, so assert the
		// shape rather than the exact root: config roams, data does not.
		if !strings.Contains(db, paths.AppDirName) || !strings.Contains(settings, paths.AppDirName) {
			t.Errorf("windows paths missing the app directory: %q / %q", db, settings)
		}
	case "darwin":
		want := filepath.Join(home, "Library", "Application Support", paths.AppDirName)
		if filepath.Dir(db) != want {
			t.Errorf("database = %q, want %q", db, want)
		}
	default:
		for _, c := range []struct{ got, want, name string }{
			{db, filepath.Join(home, ".local", "share", paths.AppDirName), "database"},
			{settings, filepath.Join(home, ".config", paths.AppDirName), "settings"},
			{undo, filepath.Join(home, ".local", "state", paths.AppDirName), "undo"},
		} {
			if filepath.Dir(c.got) != c.want {
				t.Errorf("%s = %q, want it in %q", c.name, c.got, c.want)
			}
		}
	}
}

// The four kinds must not collapse into one directory by accident — that is the
// whole point of splitting them. How many directories that *should* be is a
// platform question, and both exceptions are deliberate: macOS has no state
// directory of its own (config, data and state share Application Support, and
// only the cache is separate), and Windows splits roaming config from the rest
// (data, state and cache share Local). Counting per platform pins the layout
// the README documents everywhere, where skipping the platforms with fewer
// directories asserted nothing about them at all.
func TestTheFourKindsAreDistinct(t *testing.T) {
	clearPathEnv(t)
	want := 4
	switch runtime.GOOS {
	case "darwin":
		want = 2 // Application Support + Caches
	case "windows":
		want = 2 // Roaming + Local
	}
	seen := map[string][]paths.Kind{}
	for _, kind := range []paths.Kind{paths.Config, paths.Data, paths.State, paths.Cache} {
		dir, err := paths.Dir(kind)
		if err != nil {
			t.Fatal(err)
		}
		seen[dir] = append(seen[dir], kind)
	}
	if len(seen) != want {
		t.Errorf("the four kinds resolve to %d directories on %s, want %d: %v",
			len(seen), runtime.GOOS, want, seen)
	}
}

// An install from before the rename keeps its data: ~/.taskr and each
// platform directory's taskr folder take tjek's names, once, and never over
// data that is already there.
func TestAdoptFormerDirsMovesTaskrData(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	// The platform's own directories, resolved the way tjek resolves them,
	// before ~/.tjek exists to pin the single-directory layout.
	configDir, err := paths.Dir(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir, err := paths.Dir(paths.Cache)
	if err != nil {
		t.Fatal(err)
	}
	oldLegacy := filepath.Join(home, ".taskr")
	oldConfig := filepath.Join(filepath.Dir(configDir), "taskr")
	for _, dir := range []string{oldLegacy, oldConfig} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tasks.db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A tjek cache directory already exists: its taskr twin stays put.
	oldCache := filepath.Join(filepath.Dir(cacheDir), "taskr")
	for _, dir := range []string{oldCache, cacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := paths.AdoptFormerDirs()
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("moved %v, want ~/.taskr and the config directory's taskr", moved)
	}
	for _, f := range []string{filepath.Join(home, ".tjek", "tasks.db"), filepath.Join(configDir, "tasks.db")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s should exist after the move: %v", f, err)
		}
	}
	if _, err := os.Stat(oldCache); err != nil {
		t.Error("a taskr directory whose tjek name is taken must be left where it is")
	}
	if again, _ := paths.AdoptFormerDirs(); len(again) != 0 {
		t.Errorf("a second run should move nothing, moved %v", again)
	}
	exe := "taskr"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if !paths.FormerExecutable(filepath.Join(home, "bin", exe)) {
		t.Error("a binary named taskr is the former executable")
	}
}
