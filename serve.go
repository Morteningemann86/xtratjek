package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	"github.com/Morteningemann86/xtratjek/rank"
	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// defaultServerListen is the bind address used when none is configured —
// localhost only, so an accidental "Server: On" never exposes tasks beyond this
// machine until the user deliberately sets a reachable address.
const defaultServerListen = "127.0.0.1:8765"

// startSyncServer launches the sync endpoint in a background goroutine and
// returns the running server so the caller can stop it. net.Listen runs
// synchronously so a bind failure (e.g. address already in use) is reported now
// rather than vanishing into the goroutine. A token is mandatory — the endpoint
// must never serve unauthenticated.
func startSyncServer(listen, token string) (*http.Server, func(), error) {
	if token == "" {
		return nil, nil, fmt.Errorf("a server token is required")
	}
	if listen == "" {
		listen = defaultServerListen
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, nil, err
	}
	srv := newAppSyncServer(token)
	// Watch the store so out-of-process writes (a CLI tjek add on this host)
	// also push to clients. Non-fatal if it can't start.
	stopWatch := func() {}
	if stop, werr := startChangeWatcher(srv.Hub, tjekDir()); werr == nil {
		stopWatch = stop
	}
	// Addr is informational here (Serve uses ln); it reflects the actually-bound
	// address, which matters when the configured port was 0 (OS-assigned).
	httpServer := &http.Server{Addr: ln.Addr().String(), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		// Serve blocks until the listener fails or Shutdown/Close is called;
		// ErrServerClosed is the expected stop signal, anything else is a real
		// failure that would otherwise vanish into this goroutine.
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("tjek serve: %v", err)
		}
	}()
	return httpServer, stopWatch, nil
}

// serve.go implements `tjek serve`: a small self-hosted HTTP endpoint that
// merges task sets pushed by `tjek sync` clients. It is tjek in another mode —
// it reuses the exact storage and merge code of the app and persists to its own
// tasks.db. One endpoint, POST /v1/sync, does push+pull in a single
// round trip: the client sends its full task set (tombstones included), the
// server merges it into the authoritative set, persists the result, and returns
// the merged set for the client to apply.
//
// It is single-owner by design: one shared bearer token, not multi-tenant.
// Anyone can run their own instance; the transport (Tailscale IP, localhost
// behind a reverse proxy, LAN) is a deployment choice via --listen, and
// https is --tls-cert/--tls-key (servetls.go).

func cliServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8765",
		"address to bind (e.g. a Tailscale IP like 100.x.y.z:8765, or 127.0.0.1:8765 behind a reverse proxy)")
	token := fs.String("token", os.Getenv("TJEK_SYNC_TOKEN"),
		"shared bearer token clients must present (or set TJEK_SYNC_TOKEN)")
	newToken := fs.Bool("new-token", false,
		"mint a strong token, store it as this machine's server token, print it, and exit")
	tlsCert := fs.String("tls-cert", "",
		"serve https with this PEM certificate (e.g. from `tailscale cert` or Let's Encrypt; re-read when renewed)")
	tlsKey := fs.String("tls-key", "", "the PEM private key for --tls-cert")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *newToken {
		return cliNewServerToken()
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "tjek serve: a token is required (--token or TJEK_SYNC_TOKEN); refusing to run unauthenticated")
		fmt.Fprintln(os.Stderr, "tjek serve: `tjek serve --new-token` mints one and stores it")
		return 2
	}
	// A warning, not a refusal: a working deployment must keep working, and
	// the person who put a short token behind a Tailscale-only listener is
	// better placed to judge it than a length check is.
	if why := weakSyncToken(*token); why != "" {
		fmt.Fprintf(os.Stderr, "tjek serve: warning: the token is %s\n", why)
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		fmt.Fprintln(os.Stderr, "tjek serve: --tls-cert and --tls-key go together; pass both, or neither for plain http")
		return 2
	}
	// Loaded before the store is opened or the port bound, so a wrong path is
	// the first thing the operator sees rather than a client's failed handshake.
	var tlsConfig *tls.Config
	if *tlsCert != "" {
		certs, err := newCertReloader(*tlsCert, *tlsKey, log.Printf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tjek serve: %v\n", err)
			return 1
		}
		tlsConfig = serveTLSConfig(certs)
	}
	if err := openStore(); err != nil {
		fmt.Fprintf(os.Stderr, "tjek serve: open store: %v\n", err)
		return 1
	}
	srv := newAppSyncServer(*token)
	// Watch the store so out-of-process writes (a CLI tjek add on this host)
	// also push to clients in real time, not just client-initiated merges.
	if stop, werr := startChangeWatcher(srv.Hub, tjekDir()); werr != nil {
		fmt.Fprintf(os.Stderr, "tjek serve: change watcher unavailable (%v); out-of-process writes won't push in real time\n", werr)
	} else {
		defer stop()
	}

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsConfig,
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek serve: %v\n", err)
		return 1
	}
	// Graceful stop: SIGINT/SIGTERM (^C, systemctl stop) closes the listener
	// so Serve returns cleanly and the WAL gets checkpointed on the way out
	// instead of surviving as a multi-megabyte sidecar.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		_ = httpServer.Close()
	}()
	scheme := "http"
	if tlsConfig != nil {
		scheme = "https"
	}
	fmt.Fprintf(os.Stderr, "tjek serve: listening on %s://%s (POST /v1/sync)\n", scheme, *listen)
	if err := serveOn(httpServer, ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "tjek serve: %v\n", err)
		return 1
	}
	checkpointStore()
	return 0
}

// serveOn serves s on ln, over TLS when s carries a TLS configuration. The
// certificate comes from TLSConfig.GetCertificate, which is why ServeTLS gets
// no file names: the reloader owns them.
func serveOn(s *http.Server, ln net.Listener) error {
	if s.TLSConfig != nil {
		return s.ServeTLS(ln, "", "")
	}
	return s.Serve(ln)
}

// cliNewServerToken mints a server token, stores it, and prints it. Rotating
// is the point of a separate flag rather than a prompt: the new token is only
// useful once every client has it, and that is a step the user has to take
// deliberately.
func cliNewServerToken() int {
	token, err := newSyncToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek serve: %v\n", err)
		return 1
	}
	cfg := loadSyncConfigFile()
	had := cfg.ServerToken != ""
	cfg.ServerToken = token
	if err := saveSyncConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "tjek serve: could not store the token: %v\n", err)
		return 1
	}
	// The token itself goes to stdout so it can be piped into a secret store;
	// everything else is commentary and goes to stderr.
	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "tjek serve: stored as this machine's server token in %s\n", syncConfigPath())
	if had {
		fmt.Fprintln(os.Stderr, "tjek serve: this replaced the previous token; every client needs the new one before it can sync again")
	}
	fmt.Fprintln(os.Stderr, "tjek serve: for the headless server, pass it as --token or TJEK_SYNC_TOKEN")
	return 0
}

// dbStore adapts the app's SQLite store to tasksync.Store: MergeIn is the
// transactional load+merge+save (mergeIntoStore), the one write path a sync
// is allowed to use. biases score the rows it writes; the zero value is
// the neutral default.
type dbStore struct {
	h      *sql.DB
	biases rank.Biases
}

func (d dbStore) MergeIn(incoming []todo.Todo) ([]todo.Todo, bool, error) {
	return mergeIntoStore(d.h, incoming, d.biases)
}

// newAppSyncServer wires a tasksync.Server to this app: the shared SQLite
// store, a fresh SSE hub, and the serve-state file (throttled) so
// `tjek sync --status` on this host can report the last client contact.
func newAppSyncServer(token string) *tasksync.Server {
	return &tasksync.Server{
		Token:        token,
		Store:        dbStore{db, storedBiases()},
		Board:        &boardStore{},
		Version:      appVersion,
		Hub:          tasksync.NewHub(),
		OnClientSync: noteClientSync,
	}
}

// noteClientSync records an inbound client sync for `sync --status`,
// throttled to once a minute. Best-effort — a write failure must never fail
// the sync that triggered it.
var (
	serveStateMu        sync.Mutex
	lastServeStateWrite time.Time
)

func noteClientSync(now time.Time) {
	serveStateMu.Lock()
	defer serveStateMu.Unlock()
	if now.Sub(lastServeStateWrite) < time.Minute {
		return
	}
	lastServeStateWrite = now
	_ = writeServeState(now)
}

// serveState records hub-side sync facts, currently just the last time any
// authenticated client completed a /v1/sync against this host. Written by the
// serve process (headless or in-process), read by `tjek sync --status`.
type serveState struct {
	LastClientSync time.Time `json:"last_client_sync"`
}

func serveStatePath() string {
	return paths.For(paths.State, "serve-state.json")
}

func writeServeState(now time.Time) error {
	if _, err := paths.Ensure(paths.State); err != nil {
		return err
	}
	b, err := json.MarshalIndent(serveState{LastClientSync: now.UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(serveStatePath(), b, 0600)
}

// readServeState returns the recorded state; ok is false when no client has
// ever synced (or the file is unreadable/corrupt — treated the same, since
// the only consumer is a status line).
func readServeState() (serveState, bool) {
	b, err := os.ReadFile(serveStatePath())
	if err != nil {
		return serveState{}, false
	}
	var st serveState
	if err := json.Unmarshal(b, &st); err != nil || st.LastClientSync.IsZero() {
		return serveState{}, false
	}
	return st, true
}
