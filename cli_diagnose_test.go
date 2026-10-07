package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// findDiagnostic returns the report line with the given name, or fails.
func findDiagnostic(t *testing.T, report []diagnostic, name string) diagnostic {
	t.Helper()
	for _, d := range report {
		if d.Name == name {
			return d
		}
	}
	var names []string
	for _, d := range report {
		names = append(names, d.Name)
	}
	t.Fatalf("no %q line in the report; got %v", name, names)
	return diagnostic{}
}

func TestDiagnosticsOnAFreshInstall(t *testing.T) {
	setTestHome(t, t.TempDir())

	report := collectDiagnostics()
	if len(report) == 0 {
		t.Fatal("empty report")
	}
	// Nothing exists yet, and that is not a failure — doctor must not tell a
	// first-time user their installation is broken.
	for _, d := range report {
		if d.Status == statusFail {
			t.Errorf("fresh install reported a failure: %s = %s (%s)", d.Name, d.Value, d.Detail)
		}
	}
	findDiagnostic(t, report, "tjek version")
	findDiagnostic(t, report, "platform")
	findDiagnostic(t, report, "data directory")
}

// TestDiagnosticsReportsFFmpeg doesn't assume whether ffmpeg happens to be
// installed on the machine running the test — only that doctor always says
// something about it (ok if found, a warning with an install hint if not),
// and never fails the report over it: Meetings recording is optional, not a
// broken installation.
func TestDiagnosticsReportsFFmpeg(t *testing.T) {
	got := findDiagnostic(t, collectDiagnostics(), "ffmpeg")
	if got.Status == statusFail {
		t.Errorf("ffmpeg diagnostic should never fail the report, got %+v", got)
	}
	if got.Status == statusWarn && !strings.Contains(got.Detail, "install") {
		t.Errorf("ffmpeg warning should say how to install it, got detail %q", got.Detail)
	}
}

// TestDiagnosticsReportsWhisperCLI mirrors TestDiagnosticsReportsFFmpeg:
// doesn't assume whether whisper-cli happens to be installed, only that
// doctor always says something about it and never fails the report over
// it — local Whisper is optional, off by default, and most installs will
// never have whisper-cli at all.
func TestDiagnosticsReportsWhisperCLI(t *testing.T) {
	setTestHome(t, t.TempDir())
	got := findDiagnostic(t, collectDiagnostics(), "whisper-cli")
	if got.Status == statusFail {
		t.Errorf("whisper-cli diagnostic should never fail the report, got %+v", got)
	}
	// The hint names the platform's package (Homebrew's whisper-cpp, apt's
	// whisper.cpp, the project page elsewhere), so compare against it.
	if got.Status == statusWarn && !strings.Contains(got.Detail, whisperCLIInstallHint()) {
		t.Errorf("whisper-cli warning should carry the install hint %q, got detail %q", whisperCLIInstallHint(), got.Detail)
	}
}

// TestDiagnoseWhisperCPPRespectsOverride checks resolveWhisperBinPath's
// override path through the diagnostic itself, not just the unit — an
// override pointing at something that doesn't exist is still trusted
// (same as ffmpegInput never being verified), so the diagnostic reports
// it as found regardless.
func TestDiagnoseWhisperCPPRespectsOverride(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	override := dir + "/my-whisper-cli"
	if err := saveSettings(appSettings{WhisperBinOverride: override}); err != nil {
		t.Fatal(err)
	}
	got := diagnoseWhisperCPP()
	if got.Status != statusOK || got.Value != override {
		t.Fatalf("diagnoseWhisperCPP() = %+v, want statusOK with the override path", got)
	}
}

func TestDiagnosticsReportsAHealthyStore(t *testing.T) {
	setTestHome(t, t.TempDir())
	repo := newSQLiteRepo()
	if _, err := repo.Load(); err != nil {
		t.Fatalf("open store: %v", err)
	}

	report := collectDiagnostics()
	if got := findDiagnostic(t, report, "integrity check"); got.Status != statusOK {
		t.Errorf("integrity check = %+v, want ok", got)
	}
	if got := findDiagnostic(t, report, "schema version"); got.Status != statusOK {
		t.Errorf("schema version = %+v, want ok", got)
	}
	// The counts query names a real table — it silently read "no such table"
	// as a warning during development, which is the failure mode a test has
	// to pin rather than an eyeball.
	counts := findDiagnostic(t, report, "tasks")
	if counts.Status != statusOK {
		t.Errorf("task counts = %+v, want ok", counts)
	}
	if !strings.Contains(counts.Value, "pending") {
		t.Errorf("task counts value = %q, want a pending/done/deleted breakdown", counts.Value)
	}
}

func TestDiagnosticsFlagsCorruptSettings(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	mustMkdirFor(t, settingsPath())
	if err := os.WriteFile(settingsPath(), []byte("not json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	got := findDiagnostic(t, collectDiagnostics(), "settings")
	if got.Status != statusFail {
		t.Fatalf("settings = %+v, want a failure", got)
	}
	// The consequence is the part the user needs: a corrupt file is not just
	// ignored, it gets overwritten by the next save.
	if !strings.Contains(got.Detail, "overwrite") {
		t.Errorf("detail should warn that saving overwrites the file, got %q", got.Detail)
	}
}

func TestDiagnosticsNeverPrintsTokens(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-bearer-token"
	cfg := syncConfig{URL: "https://sync.example.com", Token: secret, ServerListen: ":8080", ServerToken: secret}
	if err := saveSyncConfig(cfg); err != nil {
		t.Fatal(err)
	}

	report := collectDiagnostics()
	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	// doctor output is meant to be pasted into a public bug report. A token
	// leaking into it hands over write access to the user's whole task set.
	if strings.Contains(string(blob), secret) {
		t.Fatal("the sync token appeared in the diagnostics report")
	}
	if got := findDiagnostic(t, report, "sync client"); got.Detail != "token set" {
		t.Errorf("sync client detail = %q, want confirmation without the value", got.Detail)
	}
}

func TestDiagnosticsWarnsAboutPlainHTTPToARemoteHost(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	if err := saveSyncConfig(syncConfig{URL: "http://sync.example.com", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	got := findDiagnostic(t, collectDiagnostics(), "sync client")
	if got.Status != statusWarn {
		t.Errorf("plain http to a public host should warn, got %+v", got)
	}

	// Loopback over plain http is the normal local-hub setup and must not
	// nag: the token never leaves the machine. The token has to be a real one,
	// or the weak-token check below fires instead and the case proves nothing.
	strong, err := newSyncToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveSyncConfig(syncConfig{URL: "http://127.0.0.1:8080", Token: strong}); err != nil {
		t.Fatal(err)
	}
	if got := findDiagnostic(t, collectDiagnostics(), "sync client"); got.Status != statusOK {
		t.Errorf("loopback http should be fine, got %+v", got)
	}
}

// A guessable token is the likeliest real compromise of a sync setup, so the
// doctor has to say so — without ever printing the token itself.
func TestDiagnosticsWarnsAboutAWeakToken(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	if err := saveSyncConfig(syncConfig{URL: "http://127.0.0.1:8080", Token: "hunter2"}); err != nil {
		t.Fatal(err)
	}
	got := findDiagnostic(t, collectDiagnostics(), "sync client")
	if got.Status != statusWarn {
		t.Errorf("a short token should warn, got %+v", got)
	}
	if strings.Contains(got.Detail+got.Value, "hunter2") {
		t.Errorf("the diagnostic leaked the token: %+v", got)
	}
}

func TestDiagnosticsFlagsARejectedKeybinding(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	// A rejected rebind is otherwise near-silent, and reads to the user as
	// "my key does nothing".
	if err := saveSettings(appSettings{Keys: map[string]string{"not-an-action": "Z"}}); err != nil {
		t.Fatal(err)
	}
	got := findDiagnostic(t, collectDiagnostics(), "key overrides")
	if got.Status != statusWarn {
		t.Errorf("key overrides = %+v, want a warning", got)
	}
}

func TestCliDoctorExitCodes(t *testing.T) {
	setTestHome(t, t.TempDir())
	// A healthy (if empty) installation exits 0 so the command can gate a
	// script; only a real failure is non-zero.
	var code int
	captureStdout(t, func() { code = cliDoctor(nil) })
	if code != 0 {
		t.Errorf("exit = %d on a clean install, want 0", code)
	}

	if err := ensureStorageDir(); err != nil {
		t.Fatal(err)
	}
	mustMkdirFor(t, settingsPath())
	if err := os.WriteFile(settingsPath(), []byte("{{{"), 0644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { code = cliDoctor(nil) })
	if code != 1 {
		t.Errorf("exit = %d with a corrupt settings file, want 1", code)
	}
}

func TestCliDoctorJSONIsParseable(t *testing.T) {
	setTestHome(t, t.TempDir())
	out := captureStdout(t, func() {
		if code := cliDoctor([]string{"--json"}); code != 0 {
			t.Errorf("exit = %d", code)
		}
	})
	var report []diagnostic
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out)
	}
	if len(report) == 0 {
		t.Fatal("--json produced an empty report")
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	} {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPluralReadsCorrectly(t *testing.T) {
	if got := plural(1, "problem"); got != "1 problem" {
		t.Errorf("got %q", got)
	}
	if got := plural(2, "problem"); got != "2 problems" {
		t.Errorf("got %q", got)
	}
}
