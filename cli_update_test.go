package main

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

// TestPlanUpdateOrdersItsVerdicts pins the decision both update surfaces share.
// The order is the point: a local build must be recognised before "newer tag
// wins", or every release ever published looks like an update to a binary the
// user just compiled.
func TestPlanUpdateOrdersItsVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		current, latest string
		want            updateAction
	}{
		{"same tag", "v1.33.1", "v1.33.1", updateUpToDate},
		{"tolerates a missing v", "1.33.1", "v1.33.1", updateUpToDate},
		{"build metadata is not identity", "v1.33.1+deb", "v1.33.1", updateUpToDate},
		{"no releases found", "v1.33.1", "", updateUpToDate},
		{"local build never gets overwritten", devVersion, "v99.0.0", updateLocalBuild},
		{"newer release", "v1.33.1", "v1.34.0", updateAvailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == updateAvailable && runtime.GOOS == "darwin" {
				want = updateManaged // no macOS release binary exists to install
			}
			if got, _ := planUpdate(tc.current, tc.latest); got != want {
				t.Errorf("planUpdate(%q, %q) = %v, want %v", tc.current, tc.latest, got, want)
			}
		})
	}
}

// TestPackageManagerForNamesTheOwner covers the guard that keeps self-update
// from writing over a file a package manager owns — which that manager's next
// upgrade would silently revert.
func TestPackageManagerForNamesTheOwner(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/opt/homebrew/Cellar/tjek/1.33.1/bin/tjek", "brew update && brew upgrade tjek"},
		{`C:\Users\mark\scoop\apps\tjek\current\tjek.exe`, "scoop update tjek"},
		{"/usr/bin/tjek", "your distribution's package manager"},
		{"/usr/local/bin/tjek", ""},
		{"/home/mark/.local/bin/tjek", ""},
		{"/home/mark/src/tjek/tjek", ""},
	} {
		if got := packageManagerFor(tc.path); got != tc.want {
			t.Errorf("packageManagerFor(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// withVersion runs fn with appVersion pretending to be a published release, so
// the update paths past the local-build guard are reachable in a test binary
// (which is always built without the release ldflags).
func withVersion(t *testing.T, v string) {
	t.Helper()
	prev := appVersion
	appVersion = v
	t.Cleanup(func() { appVersion = prev })
}

// newerReleaseServer answers the latest-release lookup with tag.
func newerReleaseServer(t *testing.T, tag string) {
	t.Helper()
	releaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"tjek","browser_download_url":"https://example.invalid/tjek"}]}`, tag)
	})
}

// TestCLIUpdateCheckReportsWithoutInstalling asserts --check is a read: it
// names both versions and stops, so it is safe in a shell profile or a cron.
func TestCLIUpdateCheckReportsWithoutInstalling(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS is directed to go install before the install path is reached")
	}
	withVersion(t, "v1.0.0")
	newerReleaseServer(t, "v9.9.9")

	var code int
	out := captureStdout(t, func() { code = cliUpdate([]string{"--check"}) })
	if code != 0 {
		t.Fatalf("update --check: exit %d, want 0", code)
	}
	if !strings.Contains(out, "v9.9.9") || !strings.Contains(out, "v1.0.0") {
		t.Errorf("--check should report both versions, got %q", out)
	}
}

// TestCLIUpdateRefusesToPromptWithoutATTY covers the non-interactive path: a
// confirmation nobody can answer would otherwise read EOF as "no" and look
// like a failed update, so it says which flag carries it through instead.
func TestCLIUpdateRefusesToPromptWithoutATTY(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS is directed to go install before the install path is reached")
	}
	if stdinIsTTY() {
		t.Skip("test runner has a TTY on stdin; the prompt would block")
	}
	withVersion(t, "v1.0.0")
	newerReleaseServer(t, "v9.9.9")

	var code int
	err := captureStderr(t, func() { code = cliUpdate(nil) })
	if code != 1 {
		t.Fatalf("update without -y on a pipe: exit %d, want 1", code)
	}
	if !strings.Contains(err, "-y") {
		t.Errorf("refusal should name the flag that would work, got %q", err)
	}
}
