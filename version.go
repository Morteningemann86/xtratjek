package main

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version resolution.
//
// The release workflow bakes the tag in with
// `-ldflags "-X main.appVersion=v1.31.0"`, and that is always authoritative.
// But it is not the only way tjek gets installed: `go install
// github.com/Morteningemann86/xtratjek@latest` (which the README recommends) compiles
// without ldflags. Reporting "dev" there would make the update check, which
// compares versions by string, offer an update on every run.
//
// The Go toolchain already knows the answer in both remaining cases and
// stamps it into the binary: a module-proxy install records the module
// version, and a plain `go build` inside the repo records the VCS revision.
// resolveVersion reads whichever of those is present.

// buildVersion is the ldflags slot. Kept separate from appVersion so the
// resolution below has an unmodified input to test against; main.go's
// -X target stays `main.appVersion` for compatibility with every existing
// build script, and init() reconciles the two.
func init() {
	appVersion = resolveVersion(appVersion, readBuildInfo())
}

// readBuildInfo is a var so tests can substitute a synthetic BuildInfo
// instead of depending on how the test binary itself was compiled.
var readBuildInfo = func() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}

// devVersion is what appVersion holds when no ldflags version was injected.
const devVersion = "dev"

// resolveVersion picks the most specific version available, in order:
//
//  1. the ldflags value, when the build actually set one (a release);
//  2. the module version recorded by `go install module@version`;
//  3. the VCS revision recorded by `go build` in a git checkout, as
//     "dev+<short sha>" with a "-dirty" suffix for uncommitted changes;
//  4. plain "dev".
//
// Case 3 deliberately keeps the "dev" prefix: it is not a release, and
// isReleaseVersion below keys off exactly that to avoid offering a
// self-update that would replace a locally built binary with a stale tag.
func resolveVersion(ldflags string, info *debug.BuildInfo) string {
	if v := strings.TrimSpace(ldflags); v != "" && v != devVersion {
		return v
	}
	if info == nil {
		return devVersion
	}
	// A module-proxy install records a real semver here. Two other things
	// can land in the same field and neither is a release: an older
	// toolchain writes "(devel)", and Go 1.24+ synthesises a pseudo-version
	// like v1.31.1-0.20260814055711-7c41718316f8+dirty when building from a
	// checkout. Taking that at face value marked every local build as a
	// released version, which is exactly the confusion this file exists to
	// remove — so both fall through to the revision branch below and come
	// out in the one dev+<sha> shape.
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" && !isPseudoVersion(v) {
		return v
	}
	var revision string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		return devVersion
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	v := devVersion + "+" + revision
	if dirty {
		v += "-dirty"
	}
	return v
}

// pseudoVersionSuffix matches the tail the module system appends when it
// derives a version from a commit rather than a tag: a 14-digit UTC
// timestamp and a 12-character revision, optionally marked "+dirty".
// The separator before the timestamp is "-" for a commit with no earlier tag
// (v0.0.0-<ts>-<rev>) and "." for one derived from a tag (v1.31.1-0.<ts>-<rev>).
var pseudoVersionSuffix = regexp.MustCompile(`[-.][0-9]{14}-[0-9a-f]{12}(\+dirty)?$`)

func isPseudoVersion(v string) bool {
	return pseudoVersionSuffix.MatchString(v)
}

// isReleaseVersion reports whether the running binary identifies itself as a
// published release, i.e. something the latest-release tag can meaningfully
// be compared against. Anything derived from a local checkout is not.
func isReleaseVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != devVersion && !strings.HasPrefix(v, devVersion+"+")
}

// sameRelease compares a running version against a release tag tolerantly.
// Tags are published as "v1.31.0" but a version can reach us without the
// leading v (a packager's ldflags, a module version normalised elsewhere),
// and a build-metadata suffix after "+" is by semver definition not part of
// version identity. Comparing raw strings made all of those read as "an
// update is available" on every check.
func sameRelease(running, latest string) bool {
	return normalizeVersion(running) == normalizeVersion(latest)
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	return v
}

// ── What a version check means ────────────────────────────────────────────────

// updateAction is the verdict of a latest-release check for *this* binary.
// The decision is shared rather than duplicated: the Settings tab and
// `tjek update` must not reach different conclusions about the same
// installation. Only the verdict is shared — each surface writes its own
// sentence, since the TUI's are translated and the CLI's deliberately are not.
type updateAction int

const (
	// updateUpToDate — already running the newest published release.
	updateUpToDate updateAction = iota
	// updateLocalBuild — built from a checkout, so every release tag compares
	// as newer. Report the release, never overwrite the build: silently
	// replacing a binary the user just compiled is the surprising outcome.
	updateLocalBuild
	// updateManaged — a package manager owns this binary and has to do the
	// upgrade itself; the accompanying hint names the command.
	updateManaged
	// updateAvailable — a newer release exists and we can install it.
	updateAvailable
)

// planUpdate decides what latest means for a binary running current. Both are
// arguments so the ordering is testable without a network or a tagged build;
// the environment questions — who owns the file, which platform — it asks
// directly, because they are properties of the running process.
func planUpdate(current, latest string) (updateAction, string) {
	if latest == "" || sameRelease(current, latest) {
		return updateUpToDate, ""
	}
	if !isReleaseVersion(current) {
		return updateLocalBuild, ""
	}
	if hint := runningPackageManager(); hint != "" {
		return updateManaged, hint
	}
	if runtime.GOOS == "darwin" {
		// Not from a keg, but macOS ships no release binary at all.
		return updateManaged, "brew install iliorn/tap/tjek"
	}
	return updateAvailable, ""
}
