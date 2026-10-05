package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docs_test.go guards the architecture notes against the drift that made them
// misleading: ARCHITECTURE.md (then CLAUDE.md) described prepareSave and
// markModifiedNoUndo as central
// mechanisms long after both had been refactored away, and a reader — human or
// a future Claude session — has no way to tell a stale name from a real one.
//
// The rule is narrow on purpose: every backticked, symbol-shaped word in the
// docs must appear somewhere in the source. It cannot check that the prose is
// *true*, but it does catch the failure that actually happened.

// symbolShaped matches identifiers with an internal case change (markModified,
// refreshCaches, sqliteRepo). Single lowercase words are prose ("dirty",
// "tasks"), and all-caps ones are acronyms ("MVU", "SQL").
var symbolShaped = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func looksLikeSymbol(s string) bool {
	if !symbolShaped.MatchString(s) {
		return false
	}
	// An internal lower→upper transition is the giveaway.
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' && s[i-1] >= 'a' && s[i-1] <= 'z' {
			return true
		}
	}
	return false
}

// docsProseExceptions are symbol-shaped words that are prose, a Go stdlib or
// third-party name, or a file/asset name rather than something this tree
// declares.
var docsProseExceptions = map[string]bool{
	"TokyoNight": true, "GitHub": true, "SQLite": true, "JetBrains": true,
	"KeyMsg": true, "SliceStable": true, "MaxOpenConns": true, "SetMaxOpenConns": true,
	"WriteFile": true, "ReadFile": true, "UserHomeDir": true, "StringWidth": true,
	"TempDir": true, "MkdirTemp": true, "PrintDefaults": true, "FlagSet": true,
	"NewRequest": true, "LimitReader": true, "GOOS": true, "GOARCH": true,
	"EnvironmentFile": true, // systemd directive
}

func docSymbols(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	seen := map[string]bool{}
	for _, span := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(string(data), -1) {
		for _, word := range regexp.MustCompile(`[^A-Za-z0-9_]+`).Split(span[1], -1) {
			if looksLikeSymbol(word) && !docsProseExceptions[word] {
				seen[word] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sourceText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, dir := range []string{".", "todo", "tasksync", "paths", "rank", "meeting", "aiprovider"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			b.Write(data)
		}
	}
	return b.String()
}

func TestDocsNameRealSymbols(t *testing.T) {
	src := sourceText(t)
	for _, doc := range []string{"ARCHITECTURE.md", "CLAUDE.md", "README.md"} {
		for _, sym := range docSymbols(t, doc) {
			if !strings.Contains(src, sym) {
				t.Errorf("%s references %q, which no longer exists in the source — "+
					"rename it, drop it, or add it to docsProseExceptions if it isn't ours",
					doc, sym)
			}
		}
	}
}

// The Go badge in the README is read as a support claim; go.mod is the truth.
func TestReadmeGoVersionMatchesGoMod(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no go directive")
	}
	want := string(m[1])

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	badge := regexp.MustCompile(`Go-(\d+\.\d+)-`).FindSubmatch(readme)
	if badge == nil {
		t.Fatal("README has no Go version badge")
	}
	if got := string(badge[1]); got != want {
		t.Errorf("README badge says Go %s, go.mod says %s", got, want)
	}
}

// The release workflow builds the release body from the CHANGELOG section that
// matches the tag (packaging/release-notes.sh). That coupling is invisible from
// the Go side and silent when it breaks: restructure the headings and the
// extractor finds nothing, the workflow falls back to generated notes, and the
// release ships with commit subjects nobody notices are wrong. This runs the
// real script against the real file.
func TestReleaseNotesExtractionMatchesTheChangelog(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash available (Windows runner); the script only runs in CI on ubuntu")
	}
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	// The newest released version — the heading the next tag will most likely
	// be cut against.
	m := regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`).FindSubmatch(data)
	if m == nil {
		t.Fatal("CHANGELOG.md has no released-version heading; release-notes.sh has nothing to find")
	}
	version := string(m[1])

	out, err := exec.Command(bash, "packaging/release-notes.sh", "v"+version, "CHANGELOG.md").Output()
	if err != nil {
		t.Fatalf("release-notes.sh found no section for v%s — the release body would silently "+
			"fall back to commit subjects: %v", version, err)
	}
	notes := string(out)
	if len(strings.TrimSpace(notes)) < 50 {
		t.Errorf("extracted notes for v%s are suspiciously short:\n%s", version, notes)
	}
	// The heading is dropped (the release is titled with the version already)
	// and the section must not run into the one below it.
	if strings.Contains(notes, "## [") {
		t.Errorf("extracted notes leak a release heading:\n%s", notes)
	}
	if !strings.Contains(notes, "blob/v"+version+"/CHANGELOG.md") {
		t.Errorf("extracted notes are missing the full-changelog link:\n%s", notes)
	}
}

// The README advertises `go install github.com/Iliorn/tjek@latest` as the
// install with the strongest integrity guarantee. That command resolves the
// module by the path in go.mod, not by the repository URL, so a module
// declared as bare `tjek` fails the install outright:
//
//	module declares its path as: tjek
//	        but was required as: github.com/Iliorn/tjek
//
// Nothing in the build catches that — the module path is only consulted by
// someone installing from outside the tree, which is exactly the person the
// README is talking to. This ties the advertised command to the declaration.
func TestGoInstallPathMatchesModulePath(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^module (\S+)`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no module directive")
	}
	modulePath := string(m[1])

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	install := regexp.MustCompile(`go install (\S+?)@latest`).FindSubmatch(readme)
	if install == nil {
		t.Skip("README no longer documents a `go install` command")
	}
	if got := string(install[1]); got != modulePath {
		t.Errorf("README documents `go install %s@latest`, but go.mod declares module %q — that install fails with a module path mismatch", got, modulePath)
	}
}

// changelogEntryMaxLen caps a CHANGELOG bullet, marker included. Wide enough
// for a sentence naming a flag or a key, too narrow for a second clause.
const changelogEntryMaxLen = 100

// A CHANGELOG entry is one line, and this is what keeps it that way.
//
// The file grew to 661 lines of entries that each opened with a bold headline
// and then explained themselves for a paragraph — the reasoning was good, it
// was just written for the wrong reader. Someone reading a release page is
// deciding whether to upgrade; the argument for a change belongs in the commit
// message, next to the code it explains, where it does not have to be re-read
// by everyone who ever upgrades. The rule is stated at the top of the file for
// the human writing an entry, and enforced here because a prose convention with
// nothing checking it lasts exactly as long as the memory of the conversation
// that set it.
//
// Only entries are checked. The prose above the first release heading, the
// headings themselves and any blank lines are not entries and are left alone.
func TestChangelogEntriesAreOneLine(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	// A Windows checkout ends every line in \r, which is not part of the entry.
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	inEntries := false
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			// The header prose ends at the first release heading.
			inEntries = true
			continue
		}
		if !inEntries || !strings.HasPrefix(line, "- ") {
			continue
		}
		if n := len([]rune(line)); n > changelogEntryMaxLen {
			t.Errorf("CHANGELOG.md:%d is %d characters, over the %d-character entry cap — say less, or say the rest in the commit message:\n%s",
				i+1, n, changelogEntryMaxLen, line)
		}
		// A continuation line is the shape the old paragraphs came back in:
		// anything indented under the bullet rather than starting a new one.
		if i+1 < len(lines) {
			next := lines[i+1]
			if strings.HasPrefix(next, "  ") && strings.TrimSpace(next) != "" {
				t.Errorf("CHANGELOG.md:%d runs onto a second line — an entry is one line:\n%s\n%s",
					i+1, line, next)
			}
		}
	}
}
