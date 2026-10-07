package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// syncclient.go is the `tjek sync` side: it pushes the local task set (including
// tombstones) to a `tjek serve` endpoint, applies the authoritative merged set
// that comes back, and logs any local edit that lost a conflict so it stays
// recoverable. It is fail-soft — a network/server error leaves the local store
// untouched.

type syncConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// AutoSync gates the automatic syncs (TUI launch/periodic/exit and after CLI
	// mutations). nil means "default on" — set "auto_sync": false in sync.json to
	// keep sync manual-only.
	AutoSync *bool `json:"auto_sync,omitempty"`

	// Adopted records that the one-time first-sync question has been answered
	// on this device (syncadopt.go). It lives here rather than with the sync
	// state because the answer must outlive a lost state directory.
	Adopted bool `json:"adopted,omitempty"`

	// Server side: this machine acting as a sync hub. ServerOn runs the endpoint
	// in-process while the TUI is open (the always-on case still uses the
	// headless `tjek serve`). ServerListen/ServerToken are its bind address and
	// the token clients must present.
	ServerListen string `json:"server_listen,omitempty"`
	ServerToken  string `json:"server_token,omitempty"`
	ServerOn     bool   `json:"server_on,omitempty"`
}

func (c syncConfig) ready() bool { return c.URL != "" && c.Token != "" }

// autoSyncEnabled reports whether automatic syncs should fire: only when a
// server is configured, and not explicitly disabled.
func autoSyncEnabled(c syncConfig) bool {
	return c.ready() && (c.AutoSync == nil || *c.AutoSync)
}

// maybeAutoSyncCLI runs one fail-soft sync after a mutating CLI command, so a
// shell edit (tjek add/done/…) propagates without the TUI being open. Silent
// on failure — a network blip must not fail the command the user actually ran.
func maybeAutoSyncCLI() {
	cfg := loadSyncConfig()
	if !autoSyncEnabled(cfg) {
		return
	}
	if err := openStore(); err != nil {
		return
	}
	// Stale-device guard: auto-sync must never be the thing that resurrects
	// long-deleted tasks. Manual `tjek sync --accept-stale` is the way back in.
	if gap, stale := staleSyncGap(time.Now()); stale {
		fmt.Fprintf(os.Stderr, "tjek sync: auto-sync paused: %s; run `tjek sync --accept-stale` to rejoin\n", staleSyncNotice(gap))
		return
	}
	// First-sync guard: nor may it be the thing that pushes a device's
	// pre-fleet tasks to everyone. The choice is a person's, so this path can
	// only decline and say where to make it.
	if firstSyncNeedsChoice(cfg, db) {
		n, _ := countLiveTasks(db)
		fmt.Fprintf(os.Stderr, "tjek sync: auto-sync paused: %s; run `tjek sync` to choose\n", firstSyncNotice(n))
		return
	}
	board := storedBoard()
	sum, err := runClientSync(db, cfg, 10*time.Second, storedBiases(), board.wire())
	if err == nil {
		board.adoptFromSync(sum.board)
	}
}

func syncConfigPath() string {
	return paths.For(paths.Config, "sync.json")
}

// loadSyncConfigFile reads sync.json alone, no env overlay. This is
// what `sync --save` must start from: persisting the runtime view would bake a
// one-off TJEK_SYNC_URL/TOKEN into the file, silently outliving the env var.
func loadSyncConfigFile() syncConfig {
	var c syncConfig
	if b, err := os.ReadFile(syncConfigPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// loadSyncConfig is the runtime view: the file overlaid with TJEK_SYNC_URL /
// TJEK_SYNC_TOKEN when set. Either source may be absent.
func loadSyncConfig() syncConfig {
	c := loadSyncConfigFile()
	if v := os.Getenv("TJEK_SYNC_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("TJEK_SYNC_TOKEN"); v != "" {
		c.Token = v
	}
	return c
}

func saveSyncConfig(c syncConfig) error {
	if _, err := paths.Ensure(paths.Config); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(syncConfigPath(), b, 0600)
}

// syncState is the outcome of the last successful sync, persisted so `tjek sync
// --status` can report it without touching the network. Only successful syncs
// update it, so a failed attempt never erases the last-known-good timestamp.
type syncState struct {
	LastSync  time.Time `json:"last_sync"`
	Sent      int       `json:"sent"`
	Received  int       `json:"received"`
	Conflicts int       `json:"conflicts"`
}

func syncStatePath() string {
	return paths.For(paths.State, "sync-state.json")
}

// writeSyncState records the outcome of a successful sync. Best-effort — callers
// ignore its error, since failing to note status must never fail the sync.
func writeSyncState(sum syncSummary) error {
	if _, err := paths.Ensure(paths.State); err != nil {
		return err
	}
	st := syncState{
		LastSync:  time.Now().UTC(),
		Sent:      sum.sent,
		Received:  sum.received,
		Conflicts: sum.conflicts,
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(syncStatePath(), b, 0600)
}

// readSyncState returns the last recorded sync outcome. ok is false (with a nil
// error) when no sync has ever been recorded.
func readSyncState() (st syncState, ok bool, err error) {
	b, err := os.ReadFile(syncStatePath())
	if os.IsNotExist(err) {
		return syncState{}, false, nil
	}
	if err != nil {
		return syncState{}, false, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return syncState{}, false, err
	}
	return st, true, nil
}

// ── Stale-device guard ────────────────────────────────────────────────────────
//
// The tombstone GC (pruneOldTombstones) hard-deletes deletion markers after
// tombstoneRetention. A device that last synced within that window is provably
// safe: every deletion made anywhere since its last sync happened after that
// sync, so its marker is younger than the window and still alive — nothing the
// device holds can resurrect. Past the window the proof is gone: the device
// may hold live copies of tasks whose deletion markers no longer exist, and a
// blind full-set merge would bring them back on every device. The guard turns
// that silent resurrection into an explicit, explained choice.

// staleSyncThreshold is tombstoneRetention minus two weeks of margin, covering
// the edge where a marker is pruned at the very moment the returning device
// syncs (both sides prune on open, so an exact-boundary race is real).
const staleSyncThreshold = tombstoneRetention - 14*24*time.Hour

// staleSyncGap reports how long ago the last successful sync was and whether
// that exceeds staleSyncThreshold. A device with no recorded sync is NOT
// stale: it has never exchanged tasks, so the rest of the fleet never saw
// (and so never deleted) anything it holds — first-sync onboarding must not
// be blocked. (Corollary: don't hand-delete sync-state.json on a device that
// HAS synced; that file is what this guard reasons from.)
func staleSyncGap(now time.Time) (time.Duration, bool) {
	st, ok, err := readSyncState()
	if !ok || err != nil {
		return 0, false
	}
	gap := now.Sub(st.LastSync)
	return gap, gap > staleSyncThreshold
}

// staleSyncNotice is the shared explanation, phrased for a human who has just
// plugged in a long-dormant machine.
func staleSyncNotice(gap time.Duration) string {
	return fmt.Sprintf("this device hasn't synced in %s, longer than the %s deletion-memory window, so tasks deleted elsewhere in the meantime could come back everywhere if it syncs blind",
		shortDur(gap), shortDur(staleSyncThreshold))
}

// printSyncStatus reports the configured server and the last successful sync,
// reading only local state (no network). Returns a process exit code.
func printSyncStatus(cfg syncConfig) int {
	// A hub host has server_listen/server_token in sync.json but usually no
	// client URL — without this line, `sync --status` on the machine actually
	// serving everyone reads like sync is broken ("none configured").
	if cfg.ServerListen != "" || cfg.ServerToken != "" || cfg.ServerOn {
		listen := cfg.ServerListen
		if listen == "" {
			listen = defaultServerListen
		}
		if st, ok := readServeState(); ok {
			fmt.Printf("serving: this machine is a sync server (%s); last client sync %s ago\n",
				listen, shortDur(time.Since(st.LastClientSync)))
		} else {
			fmt.Printf("serving: this machine is a sync server (%s); no client sync recorded yet\n", listen)
		}
	}
	if cfg.URL != "" {
		fmt.Printf("server: %s\n", cfg.URL)
	} else {
		fmt.Println("server: (none configured)")
	}
	st, ok, err := readSyncState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync: read state: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Println("last sync: never")
		return 0
	}
	fmt.Printf("last sync: %s (%s ago), sent %d, received %d, %d conflict(s)\n",
		st.LastSync.Local().Format("2006-01-02 15:04"), shortDur(time.Since(st.LastSync)),
		st.Sent, st.Received, st.Conflicts)
	return 0
}

func cliSync(args []string) int {
	// --recover is an optional-value flag: bare `--recover` lists dropped edits,
	// `--recover=<ref>` reapplies one. stdlib's flag package requires a value for
	// string flags, so we normalise bare `--recover` to `--recover=` before
	// parsing, then treat the empty-string case as "list mode".
	args = normaliseBareRecover(args)
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	url := fs.String("url", "", "sync server URL, e.g. http://100.x.y.z:8765 (or set TJEK_SYNC_URL)")
	token := fs.String("token", "", "shared bearer token (or set TJEK_SYNC_TOKEN)")
	save := fs.Bool("save", false, "persist --url/--token to sync.json for future syncs (tjek doctor shows where)")
	quiet := fs.Bool("quiet", false, "print nothing on success")
	status := fs.Bool("status", false, "print the last sync time/result and exit (local only, no network)")
	acceptStale := fs.Bool("accept-stale", false, "sync even though this device has been offline longer than the deletion-memory window (tasks deleted elsewhere may resurrect)")
	adoptLocal := fs.Bool("adopt-local", false, "first sync: keep this device's tasks and push them to the fleet")
	adoptRemoteFlag := fs.Bool("adopt-remote", false, "first sync: back up this device's tasks, clear them here, and pull the fleet's list")
	// recover is a string: empty = list dropped edits; non-empty = reapply that ref.
	// Both forms are pure local operations — no network contact.
	recoverRef := fs.String("recover", recoverAbsent, "list dropped edits (no value) or reapply one: --recover=<ref>")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := loadSyncConfig()
	if *url != "" {
		cfg.URL = *url
	}
	if *token != "" {
		cfg.Token = *token
	}
	// --recover is a local operation: list or reapply dropped edits from
	// sync.log. No network, no sync-server config required.
	if *recoverRef != recoverAbsent {
		if *recoverRef == "" {
			return printDroppedEdits(syncLogPath())
		}
		return reapplyDroppedEdit(syncLogPath(), *recoverRef)
	}
	// --status is a local read: report and exit before any config-required or
	// network path, so it works even when no server is configured yet.
	if *status {
		return printSyncStatus(cfg)
	}
	if *save {
		// Persist file values + explicit flags only — never the env overlay
		// sitting in cfg, which is runtime-only by nature.
		saved := loadSyncConfigFile()
		if *url != "" {
			saved.URL = *url
		}
		if *token != "" {
			saved.Token = *token
		}
		if err := saveSyncConfig(saved); err != nil {
			fmt.Fprintf(os.Stderr, "tjek sync: save config: %v\n", err)
			return 1
		}
		if w := tasksync.InsecureURLWarning(saved.URL); w != "" {
			fmt.Fprintln(os.Stderr, "tjek sync: "+w)
		}
	}
	if !cfg.ready() {
		fmt.Fprintln(os.Stderr, "tjek sync: missing url/token; pass --url/--token (optionally --save), or set TJEK_SYNC_URL/TJEK_SYNC_TOKEN")
		return 2
	}
	if err := openStore(); err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync: open store: %v\n", err)
		return 1
	}
	if gap, stale := staleSyncGap(time.Now()); stale && !*acceptStale {
		fmt.Fprintf(os.Stderr, `tjek sync: refusing: %s.
Options:
  tjek sync --accept-stale     merge anyway (long-deleted tasks may return; back up first with tjek export)
  or reset this device to re-pull clean: back up, then rm %s and sync again
`, staleSyncNotice(gap), dbPath())
		return 2
	}
	if rc := resolveFirstSync(cfg, *adoptLocal, *adoptRemoteFlag); rc != 0 {
		return rc
	}
	board := storedBoard()
	sum, err := runClientSync(db, cfg, 30*time.Second, storedBiases(), board.wire())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync: %v\n", err)
		return 1
	}
	if board.adoptFromSync(sum.board) {
		fmt.Fprintf(os.Stderr, "tjek sync: board columns updated from the fleet: %s\n", board.stagesDisplay())
	}
	if sum.versionGap != "" {
		// stderr, and outside the --quiet gate: --quiet suppresses the
		// routine "synced: sent 3, received 0" line, not a warning that the
		// two ends are drifting.
		fmt.Fprintln(os.Stderr, "tjek sync: warning: "+sum.versionGap)
	}
	if !*quiet {
		hint := ""
		if sum.conflicts > 0 {
			hint = " (dropped versions logged to " + syncLogPath() + ")"
		}
		fmt.Printf("synced: sent %d, received %d, %d conflict(s) resolved%s\n",
			sum.sent, sum.received, sum.conflicts, hint)
	}
	return 0
}

type syncSummary struct {
	sent, received, conflicts int
	// versionGap is set when the sync succeeded against a server running a
	// different tjek build (tasksync.VersionGapWarning). It rides on the
	// summary rather than being printed here so both callers can place it:
	// the CLI on stderr, the TUI in the Settings footer.
	versionGap string
	// board is the fleet's column list as the server has it after this sync,
	// or nil when neither end shares one. It rides back rather than being
	// applied in here for the same reason versionGap does — and one better:
	// runClientSync runs on a background goroutine, and the stage list is a
	// package-level global the renderer reads, so the install has to happen on
	// the loop that owns it (handleSyncDone).
	board *tasksync.Board
}

// runClientSync pushes the local full task set (including tombstones) to the
// server, persists the merged set it returns, and logs any local edit that lost
// a conflict. On error nothing is applied locally, so the local store is left
// untouched.
// b scores the rows the merge writes; board is this device's column list as
// offered to the fleet (boardConfig.wire), nil to leave the fleet's alone.
func runClientSync(h *sql.DB, cfg syncConfig, timeout time.Duration, b rank.Biases, board *tasksync.Board) (syncSummary, error) {
	local, err := loadTodosForSync(h)
	if err != nil {
		return syncSummary{}, err
	}
	resp, err := tasksync.PostSync(cfg.URL, cfg.Token, appVersion, local, board, timeout)
	if err != nil {
		return syncSummary{}, err
	}
	merged := resp.Tasks
	// A skewed clock silently corrupts LWW conflict resolution and nothing
	// else in the protocol surfaces it — warn loudly on every sync until the
	// user fixes the clock.
	if w := tasksync.ClockSkewWarning(resp.ServerTime, time.Now()); w != "" {
		fmt.Fprintln(os.Stderr, "tjek sync: "+w)
	}
	// Record dropped local edits before we overwrite, for the recovery log.
	// The baseline is the last successful sync: only edits made here since then
	// can genuinely lose the merge. A missing/corrupt state file reads as zero
	// → log everything, the conservative recovery-net default.
	var lastSync time.Time
	if st, ok, _ := readSyncState(); ok {
		lastSync = st.LastSync
	}
	dropped := tasksync.DroppedLocalEdits(local, merged, lastSync)
	if err := logDroppedEdits(dropped); err != nil {
		fmt.Fprintf(os.Stderr, "tjek sync: warning: could not write sync log: %v\n", err)
	}
	// The round trip can take seconds, and anything written locally meanwhile
	// (the TUI's debounced save, another CLI command) is missing from `merged`.
	// Saving that blind would overwrite those rows — worse, saveChildren would
	// tombstone a just-added comment as "vanished", and that deletion would
	// then propagate to every device. mergeIntoStore re-merges against the
	// store as it is NOW, transactionally, so even a writer racing this exact
	// moment either lands before our snapshot or forces a retry; whatever the
	// server hasn't seen yet goes out on the next sync. Its no-op guard also
	// keeps the fs watcher from waking the TUI on an unchanged periodic pull.
	if _, _, err := mergeIntoStore(h, merged, b); err != nil {
		return syncSummary{}, err
	}
	// Count live tasks only: the wire sets include every tombstone ever made,
	// so raw lengths would overstate forever ("received 400" on a no-op sync).
	sum := syncSummary{
		sent:       countLive(local),
		received:   countLive(merged),
		conflicts:  len(dropped),
		versionGap: tasksync.VersionGapWarning(resp.ServerVersion, appVersion),
		board:      resp.Board,
	}
	// Record status for `tjek sync --status`. Best-effort: a write failure here
	// must not fail an otherwise-successful sync.
	_ = writeSyncState(sum)
	return sum, nil
}

func countLive(ts []todo.Todo) int {
	n := 0
	for i := range ts {
		if !ts[i].Deleted {
			n++
		}
	}
	return n
}

func syncLogPath() string {
	return paths.For(paths.State, "sync.log")
}

// syncLogMaxBytes caps sync.log growth: past this size the file is
// rotated to sync.log.1 (replacing any previous .1) before the next append.
// The log is a recovery net for conflict-overwritten edits, so one full
// generation of history is plenty; unbounded append-forever is not.
const syncLogMaxBytes = 1 << 20 // 1 MiB

// logDroppedEdits appends one JSON line per dropped local edit to
// sync.log so a wrongly-overwritten edit can be recovered.
func logDroppedEdits(dropped []todo.Todo) error {
	if len(dropped) == 0 {
		return nil
	}
	if _, err := paths.Ensure(paths.State); err != nil {
		return err
	}
	if fi, err := os.Stat(syncLogPath()); err == nil && fi.Size() > syncLogMaxBytes {
		// Best-effort rotation — a failure must not block logging the drops.
		_ = os.Rename(syncLogPath(), syncLogPath()+".1")
	}
	f, err := os.OpenFile(syncLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, t := range dropped {
		line, err := json.Marshal(struct {
			At      string    `json:"at"`
			Note    string    `json:"note"`
			Dropped todo.Todo `json:"dropped"`
		}{now, "local edit superseded by sync (last-writer-wins)", t})
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}
