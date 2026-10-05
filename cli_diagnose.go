package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Iliorn/tjek/paths"
	"github.com/Iliorn/tjek/todo"
)

// `tjek doctor` — diagnose this installation.
//
// This is the output to paste into a bug report, so it answers the questions a
// maintainer would otherwise have to ask: version, platform, where the data
// lives, whether SQLite is healthy, what schema it is on, whether settings
// parse, whether sync is set up, and whether an editor can be found. It never
// prints a token — only whether one is set.

// diagnostic is one line of the report. Status drives both the symbol in text
// mode and the machine-readable field, so the two can never disagree.
type diagnostic struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Status string `json:"status"` // "ok", "warn", "fail", or "" for plain facts
	Detail string `json:"detail,omitempty"`
}

const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
)

func cliDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tjek doctor [--json]   report this installation's health (paste into a bug report)")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report := collectDiagnostics()

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			return 1
		}
	} else {
		printDiagnostics(os.Stdout, report)
	}

	// A failure is worth a non-zero exit so `tjek doctor` can gate a script
	// or a CI step. A warning is not: warnings are things a healthy
	// installation can legitimately have (no sync configured, no editor set).
	for _, d := range report {
		if d.Status == statusFail {
			return 1
		}
	}
	return 0
}

// collectDiagnostics gathers the report. Every probe is individually
// fail-soft: doctor is what a user runs *because* something is wrong, so a
// broken database or an unreadable settings file has to become a reported
// line rather than an abort that hides the twenty lines after it.
func collectDiagnostics() []diagnostic {
	var out []diagnostic
	add := func(d diagnostic) { out = append(out, d) }

	add(diagnostic{Name: "tjek version", Value: appVersion})
	if !isReleaseVersion(appVersion) {
		out[len(out)-1].Status = statusWarn
		out[len(out)-1].Detail = "not a released build, so self-update is disabled for it"
	}
	add(diagnostic{Name: "platform", Value: runtime.GOOS + "/" + runtime.GOARCH})
	add(diagnostic{Name: "go runtime", Value: runtime.Version()})
	add(diagnostic{Name: "keyboard input", Value: inputPathName()})
	// Empty off Windows, where there is no console code page to get wrong.
	if note := terminalCharsetNote(); note != "" {
		d := diagnostic{Name: "terminal charset", Value: note}
		if strings.Contains(note, "garbled") {
			d.Status = statusWarn
			d.Detail = "run in Windows Terminal, or `chcp 65001` before tjek"
		}
		add(d)
	}
	add(diagnoseLiveReload())
	if p := tracePath(); p != "" {
		add(diagnostic{Name: "latency trace", Value: p})
	}

	out = append(out, diagnoseStorage()...)
	out = append(out, diagnoseSettings()...)
	out = append(out, diagnoseSync()...)
	out = append(out, diagnoseEditor())
	out = append(out, diagnoseFFmpeg())
	return out
}

func diagnoseStorage() []diagnostic {
	var out []diagnostic
	dir := tjekDir()
	out = append(out, diagnostic{Name: "data directory", Value: dir})
	if paths.UsingLegacyLayout() {
		out = append(out, diagnostic{
			Name: "layout", Value: "legacy (~/" + paths.LegacyDirName + ")",
			Detail: "kept because the directory exists; move it to switch to the XDG paths, or set TJEK_HOME",
		})
	} else if home := paths.HomeOverride(); home != "" {
		out = append(out, diagnostic{Name: "layout", Value: "single directory (TJEK_HOME)"})
	} else {
		for _, k := range []struct {
			name string
			kind paths.Kind
		}{{"config directory", paths.Config}, {"state directory", paths.State}} {
			if d, err := paths.Dir(k.kind); err == nil {
				out = append(out, diagnostic{Name: k.name, Value: d})
			}
		}
	}

	if info, err := os.Stat(dir); err != nil {
		out = append(out, diagnostic{
			Name: "data directory state", Value: "missing", Status: statusWarn,
			Detail: "it is created on first run; nothing is wrong if you have not used tjek yet",
		})
		return out
	} else if !info.IsDir() {
		out = append(out, diagnostic{
			Name: "data directory state", Value: "not a directory", Status: statusFail,
			Detail: "a file is sitting where tjek's storage directory should be",
		})
		return out
	}

	path := dbPath()
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		out = append(out, diagnostic{
			Name: "database", Value: path, Status: statusWarn,
			Detail: "does not exist yet; it is created on first run",
		})
		return out
	case err != nil:
		out = append(out, diagnostic{Name: "database", Value: path, Status: statusFail, Detail: err.Error()})
		return out
	}
	out = append(out, diagnostic{
		Name:   "database",
		Value:  path,
		Status: statusOK,
		Detail: humanBytes(info.Size()),
	})
	// A WAL left behind is normal while tjek runs and after a crash; it is
	// folded back in on the next clean exit. Worth showing, never a failure.
	for _, sidecar := range []string{"-wal", "-shm"} {
		if si, err := os.Stat(path + sidecar); err == nil {
			out = append(out, diagnostic{
				Name: "database" + sidecar, Value: humanBytes(si.Size()),
				Detail: "normal while tjek is running; folded back in on exit",
			})
		}
	}

	db, err := openStoreAt(path)
	if err != nil {
		out = append(out, diagnostic{
			Name: "database open", Value: "failed", Status: statusFail, Detail: err.Error(),
		})
		return out
	}
	defer db.Close()

	if v, err := currentSchemaVersion(db); err != nil {
		out = append(out, diagnostic{Name: "schema version", Value: "unreadable", Status: statusFail, Detail: err.Error()})
	} else {
		out = append(out, diagnostic{Name: "schema version", Value: fmt.Sprintf("%03d", v), Status: statusOK})
	}
	out = append(out, integrityCheck(db))
	out = append(out, taskCounts(db)...)

	if _, err := os.Stat(getStoragePath()); err == nil {
		out = append(out, diagnostic{
			Name: "legacy tasks.json", Value: "present",
			Detail: "read only to seed a fresh database; safe to keep or archive",
		})
	}
	return out
}

// integrityCheck runs SQLite's own consistency check. It is the one probe
// that can tell a user their file is damaged rather than merely surprising.
func integrityCheck(db *sql.DB) diagnostic {
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return diagnostic{Name: "integrity check", Value: "could not run", Status: statusFail, Detail: err.Error()}
	}
	if strings.EqualFold(strings.TrimSpace(result), "ok") {
		return diagnostic{Name: "integrity check", Value: "ok", Status: statusOK}
	}
	return diagnostic{
		Name: "integrity check", Value: "failed", Status: statusFail,
		Detail: result + "; restore from a -pre-migration backup in " + tjekDir(),
	}
}

func taskCounts(db *sql.DB) []diagnostic {
	var pending, done, deleted int
	err := db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN deleted = 0 AND status != ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN deleted = 0 AND status =  ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN deleted = 1 THEN 1 ELSE 0 END), 0)
		FROM todos`, int(todo.Done), int(todo.Done)).Scan(&pending, &done, &deleted)
	if err != nil {
		return []diagnostic{{Name: "task counts", Value: "unreadable", Status: statusWarn, Detail: err.Error()}}
	}
	return []diagnostic{{
		Name:   "tasks",
		Value:  fmt.Sprintf("%d pending, %d done, %d deleted", pending, done, deleted),
		Status: statusOK,
		Detail: "deleted rows are tombstones kept so the deletion syncs",
	}}
}

func diagnoseSettings() []diagnostic {
	path := settingsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []diagnostic{{Name: "settings", Value: "defaults (no settings.json)", Status: statusOK}}
	}
	settings, err := loadSettings()
	if err != nil {
		return []diagnostic{{
			Name: "settings", Value: path, Status: statusFail,
			Detail: err.Error() + "; tjek falls back to defaults, and saving will overwrite the file",
		}}
	}
	out := []diagnostic{{
		Name:   "settings",
		Value:  path,
		Status: statusOK,
		Detail: fmt.Sprintf("theme=%s language=%s", orDefault(settings.Theme, "default"), orDefault(settings.Language, "en")),
	}}
	// A rejected keybinding is silent at startup apart from one toast, so it
	// is exactly the kind of thing a user reports as "my key does nothing".
	if len(settings.Keys) > 0 {
		_, problems := sanitizeKeyOverrides(settings.Keys)
		if len(problems) > 0 {
			out = append(out, diagnostic{
				Name: "key overrides", Value: fmt.Sprintf("%d rejected", len(problems)),
				Status: statusWarn, Detail: strings.Join(problems, "; "),
			})
		} else {
			out = append(out, diagnostic{
				Name: "key overrides", Value: fmt.Sprintf("%d active", len(settings.Keys)), Status: statusOK,
			})
		}
	}
	return out
}

func diagnoseSync() []diagnostic {
	cfg := loadSyncConfig()
	var out []diagnostic

	if cfg.URL == "" && cfg.ServerListen == "" {
		return []diagnostic{{Name: "sync", Value: "not configured", Status: statusOK}}
	}
	if cfg.URL != "" {
		status := statusOK
		detail := "token set"
		if cfg.Token == "" {
			// Never print the token itself — only whether one exists.
			status, detail = statusWarn, "no token set, so syncs will be refused"
		} else if strings.HasPrefix(strings.ToLower(cfg.URL), "http://") && !isLoopbackURL(cfg.URL) {
			status = statusWarn
			detail = "plain http to a non-loopback host: the token travels unencrypted"
		} else if why := weakSyncToken(cfg.Token); why != "" {
			// A property of the token, never the token: this output is meant
			// to be pasteable into a bug report.
			status, detail = statusWarn, "weak token: "+why
		}
		out = append(out, diagnostic{Name: "sync client", Value: cfg.URL, Status: status, Detail: detail})
		out = append(out, diagnostic{
			Name:  "auto-sync",
			Value: map[bool]string{true: "on", false: "off"}[autoSyncEnabled(cfg)],
		})
	}
	if cfg.ServerListen != "" {
		status, detail := statusOK, "token set"
		if cfg.ServerToken == "" {
			status, detail = statusWarn, "no server token set, so the endpoint will refuse to start"
		} else if why := weakSyncToken(cfg.ServerToken); why != "" {
			status, detail = statusWarn, "weak token: "+why
		}
		out = append(out, diagnostic{Name: "sync server", Value: cfg.ServerListen, Status: status, Detail: detail})
	}
	if st, ok, err := readSyncState(); err == nil && ok {
		out = append(out, diagnostic{
			Name:   "last sync",
			Value:  st.LastSync.Local().Format("2006-01-02 15:04"),
			Detail: fmt.Sprintf("%d sent, %d received, %d conflicts", st.Sent, st.Received, st.Conflicts),
		})
	} else {
		out = append(out, diagnostic{Name: "last sync", Value: "never"})
	}
	if _, err := os.Stat(syncLogPath()); err == nil {
		out = append(out, diagnostic{
			Name: "sync.log", Value: "present", Status: statusWarn,
			Detail: "local edits that lost a conflict were recorded; see `tjek sync --recover`",
		})
	}
	return out
}

func diagnoseEditor() diagnostic {
	cmd := resolveEditorCmd()
	if cmd == "" {
		return diagnostic{
			Name: "editor", Value: "none found", Status: statusWarn,
			Detail: "set $EDITOR to edit task notes",
		}
	}
	return diagnostic{Name: "editor", Value: cmd, Status: statusOK, Detail: "used for task notes"}
}

// diagnoseFFmpeg reports whether ffmpeg is on PATH — required to record a
// meeting (audiorecorder.go), optional otherwise, so its absence is a
// warning rather than a failure.
func diagnoseFFmpeg() diagnostic {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return diagnostic{
			Name: "ffmpeg", Value: "not found", Status: statusWarn,
			Detail: "needed to record Meetings; " + ffmpegInstallHint(),
		}
	}
	return diagnostic{Name: "ffmpeg", Value: "found", Status: statusOK, Detail: "used to record Meetings"}
}

// isLoopbackURL reports whether a sync URL points at this machine, where
// plain http carries no exposure worth warning about.
func isLoopbackURL(raw string) bool {
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"http://localhost", "http://127.0.0.1", "http://[::1]"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func printDiagnostics(w *os.File, report []diagnostic) {
	width := 0
	for _, d := range report {
		if len(d.Name) > width {
			width = len(d.Name)
		}
	}
	var failures, warnings int
	for _, d := range report {
		symbol := " "
		switch d.Status {
		case statusOK:
			symbol = "✓"
		case statusWarn:
			symbol = "!"
			warnings++
		case statusFail:
			symbol = "✗"
			failures++
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", symbol, width, d.Name, d.Value)
		if d.Detail != "" {
			fmt.Fprintf(w, "  %-*s  %s\n", width, "", d.Detail)
		}
	}
	fmt.Fprintln(w)
	switch {
	case failures > 0:
		fmt.Fprintf(w, "%s found. Include this output in a bug report:\n", plural(failures, "problem"))
		fmt.Fprintln(w, "https://github.com/Iliorn/tjek/issues/new")
	case warnings > 0:
		fmt.Fprintf(w, "No problems. %s worth a look, marked !\n", plural(warnings, "thing"))
	default:
		fmt.Fprintln(w, "No problems found.")
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// diagnoseLiveReload reports whether the filesystem watcher will run. It is in
// the doctor because it is the one switch a user is asked to flip when input
// feels laggy, and "did the variable actually reach the program" is otherwise
// invisible — an exported-in-the-wrong-shell mistake looks exactly like "the
// watcher was not the problem".
func diagnoseLiveReload() diagnostic {
	if watcherDisabled() {
		return diagnostic{
			Name: "live reload", Value: "off (TJEK_NO_WATCH)",
			Detail: "another shell's changes appear on the next reload, not immediately",
		}
	}
	return diagnostic{Name: "live reload", Value: "on", Detail: "watching the data directory for changes from other processes"}
}
