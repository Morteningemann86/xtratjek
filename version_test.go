package main

import (
	"runtime/debug"
	"testing"
)

func buildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{}
	info.Main.Version = mainVersion
	for k, v := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return info
}

func TestResolveVersionPrefersLdflags(t *testing.T) {
	// A release build wins over everything the toolchain recorded: the tag
	// the release workflow injected is the identity users see on the
	// Releases page and what self-update compares against.
	got := resolveVersion("v1.31.0", buildInfo("v0.0.1", map[string]string{"vcs.revision": "deadbeefcafe"}))
	if got != "v1.31.0" {
		t.Fatalf("ldflags version should win, got %q", got)
	}
}

func TestResolveVersionUsesModuleVersion(t *testing.T) {
	// `go install github.com/Morteningemann86/xtratjek@latest` — no ldflags, but the
	// module proxy version is stamped in. This is the case that used to
	// report "dev" and make the update check cry wolf on every run.
	got := resolveVersion("dev", buildInfo("v1.30.2", nil))
	if got != "v1.30.2" {
		t.Fatalf("module version should be used, got %q", got)
	}
}

func TestResolveVersionFallsBackToRevision(t *testing.T) {
	// `go build .` in a checkout records "(devel)" plus the git revision.
	got := resolveVersion("dev", buildInfo("(devel)", map[string]string{
		"vcs.revision": "1234567890abcdef",
	}))
	if got != "dev+1234567" {
		t.Fatalf("want short revision, got %q", got)
	}
}

func TestResolveVersionMarksDirtyTree(t *testing.T) {
	got := resolveVersion("dev", buildInfo("(devel)", map[string]string{
		"vcs.revision": "1234567890abcdef",
		"vcs.modified": "true",
	}))
	if got != "dev+1234567-dirty" {
		t.Fatalf("want dirty marker, got %q", got)
	}
}

func TestResolveVersionWithoutBuildInfo(t *testing.T) {
	if got := resolveVersion("dev", nil); got != "dev" {
		t.Fatalf("want dev, got %q", got)
	}
	if got := resolveVersion("", nil); got != "dev" {
		t.Fatalf("empty ldflags should degrade to dev, got %q", got)
	}
	// "(devel)" with no VCS stamp (build from a tarball, or -buildvcs=false)
	// carries no more information than "dev" does.
	if got := resolveVersion("dev", buildInfo("(devel)", nil)); got != "dev" {
		t.Fatalf("want dev, got %q", got)
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for _, v := range []string{"v1.31.0", "1.31.0", "v2.0.0-rc1"} {
		if !isReleaseVersion(v) {
			t.Errorf("%q should count as a release", v)
		}
	}
	for _, v := range []string{"", "dev", "dev+1234567", "dev+1234567-dirty"} {
		if isReleaseVersion(v) {
			t.Errorf("%q should not count as a release", v)
		}
	}
}

func TestSameReleaseIgnoresPrefixAndMetadata(t *testing.T) {
	// The tag is published as "v1.31.0"; a packager's ldflags may drop the
	// "v", and semver build metadata after "+" is not part of identity.
	// Comparing raw strings reported an update for all three.
	for _, running := range []string{"v1.31.0", "1.31.0", "v1.31.0+brew"} {
		if !sameRelease(running, "v1.31.0") {
			t.Errorf("%q should match release v1.31.0", running)
		}
	}
	if sameRelease("v1.30.0", "v1.31.0") {
		t.Error("different versions must not compare equal")
	}
}

func TestRealBuildInfoResolves(t *testing.T) {
	// Guards the wiring, not the value: whatever this test binary was
	// built from, resolution must produce something non-empty rather than
	// panicking on a nil or surprising BuildInfo.
	if got := resolveVersion("dev", readBuildInfo()); got == "" {
		t.Fatal("resolveVersion returned empty for the real build")
	}
	if appVersion == "" {
		t.Fatal("init() left appVersion empty")
	}
}

func TestResolveVersionRejectsPseudoVersion(t *testing.T) {
	// Go 1.24+ synthesises a pseudo-version from the commit when building
	// from a checkout, so Main.Version is populated with something that
	// looks like a release but is not one. Taking it at face value marked
	// every local build as released and re-enabled self-update for it.
	got := resolveVersion("dev", buildInfo("v1.31.1-0.20260814055711-7c41718316f8", map[string]string{
		"vcs.revision": "7c41718316f8aaaabbbb",
	}))
	if got != "dev+7c41718" {
		t.Fatalf("pseudo-version should degrade to the revision form, got %q", got)
	}
	if isReleaseVersion(got) {
		t.Errorf("%q must not count as a release", got)
	}
}

func TestResolveVersionRejectsDirtyPseudoVersion(t *testing.T) {
	got := resolveVersion("dev", buildInfo("v1.31.1-0.20260814055711-7c41718316f8+dirty", map[string]string{
		"vcs.revision": "7c41718316f8aaaabbbb",
		"vcs.modified": "true",
	}))
	if got != "dev+7c41718-dirty" {
		t.Fatalf("got %q", got)
	}
}

func TestIsPseudoVersion(t *testing.T) {
	for _, v := range []string{
		"v0.0.0-20260814055711-7c41718316f8",
		"v1.31.1-0.20260814055711-7c41718316f8",
		"v1.31.1-0.20260814055711-7c41718316f8+dirty",
	} {
		if !isPseudoVersion(v) {
			t.Errorf("%q should be recognised as a pseudo-version", v)
		}
	}
	// Real releases, including prereleases, must not be mistaken for one.
	for _, v := range []string{"v1.31.0", "v2.0.0-rc1", "v1.0.0-beta.2", ""} {
		if isPseudoVersion(v) {
			t.Errorf("%q is a real version, not a pseudo-version", v)
		}
	}
}
