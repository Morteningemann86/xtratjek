package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/tasksync"
	"github.com/Morteningemann86/xtratjek/todo"
)

// writeSelfSigned writes a fresh self-signed pair for 127.0.0.1 to cert/key in
// dir and returns the paths and the certificate. serial tells two pairs apart
// when a test renews one.
func writeSelfSigned(t *testing.T, dir string, serial int64) (certPath, keyPath string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "tjek test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, cert
}

// bumpMtime moves both files' modification time forward, the way a renewal
// that rewrites them does. Filesystems with coarse timestamps would otherwise
// let a rewrite within the same second look unchanged.
func bumpMtime(t *testing.T, by time.Duration, paths ...string) {
	t.Helper()
	at := time.Now().Add(by)
	for _, p := range paths {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func servingSerial(t *testing.T, addr string) int64 {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // reading which certificate is served
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

// startTLSServe runs the real `tjek serve` handler over serveOn with a TLS
// configuration from the reloader, on a free port.
func startTLSServe(t *testing.T, certs *certReloader, token string) string {
	t.Helper()
	if err := openStore(); err != nil {
		t.Fatalf("open store: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{
		Handler:           newAppSyncServer(token).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         serveTLSConfig(certs),
	}
	go func() { _ = serveOn(s, ln) }()
	t.Cleanup(func() { _ = s.Close() })
	return ln.Addr().String()
}

func TestServeTLSFlagsGoTogether(t *testing.T) {
	setTestHome(t, t.TempDir())
	for _, args := range [][]string{
		{"--token", strings.Repeat("k", 40), "--tls-cert", "cert.pem"},
		{"--token", strings.Repeat("k", 40), "--tls-key", "key.pem"},
	} {
		var code int
		out := captureStderr(t, func() { code = cliServe(args) })
		if code != 2 || !strings.Contains(out, "go together") {
			t.Errorf("%v: exit %d, stderr %q; want 2 and the pairing message", args, code, out)
		}
	}
}

// A wrong path is a startup error naming the file, not a handshake failure a
// client later reports as a reset connection.
func TestServeRefusesAMissingCertificateAtStartup(t *testing.T) {
	setTestHome(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope.pem")
	var code int
	out := captureStderr(t, func() {
		code = cliServe([]string{"--token", strings.Repeat("k", 40), "--listen", "127.0.0.1:0",
			"--tls-cert", missing, "--tls-key", missing})
	})
	if code != 1 || !strings.Contains(out, "nope.pem") {
		t.Errorf("exit %d, stderr %q; want 1 naming the missing file", code, out)
	}
}

func TestServeTLSRejectsAMismatchedKey(t *testing.T) {
	certA, _, _ := writeSelfSigned(t, t.TempDir(), 1)
	_, keyB, _ := writeSelfSigned(t, t.TempDir(), 2)
	if _, err := newCertReloader(certA, keyB, t.Logf); err == nil {
		t.Fatal("a key that does not match the certificate was accepted")
	}
}

// The whole path a client takes: a sync over https to the real handler, with
// the token, merged into the real store. And nothing on that port answers in
// cleartext.
func TestServeSyncsOverTLS(t *testing.T) {
	setTestHome(t, t.TempDir())
	certPath, keyPath, cert := writeSelfSigned(t, t.TempDir(), 1)
	certs, err := newCertReloader(certPath, keyPath, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	const token = "a-long-enough-test-token-for-serve-tls"
	addr := startTLSServe(t, certs, token)

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	body, _ := json.Marshal(tasksync.Request{Tasks: []todo.Todo{todo.New("sent over tls")}, Protocol: tasksync.ProtocolVersion})
	req, _ := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/sync", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sync over https: %v", err)
	}
	defer resp.Body.Close()
	var out tasksync.Response
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, msg)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].Title != "Sent over tls" {
		t.Errorf("merged set = %+v", out.Tasks)
	}
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Errorf("negotiated %+v, want TLS 1.2 or newer", resp.TLS)
	}

	plain, err := http.Get("http://" + addr + "/v1/health")
	if err == nil {
		plain.Body.Close()
		if plain.StatusCode == http.StatusOK {
			t.Error("the TLS port answered plain http with a 200")
		}
	}
	// TLS 1.1 is refused: every tjek client is a Go binary, there is no old
	// peer to keep it for.
	old, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS11}) //nolint:gosec // probing the floor
	if err == nil {
		old.Close()
		t.Error("a TLS 1.1 handshake succeeded")
	}
}

// Renewal rewrites the files in place; the next handshake has to see the new
// pair without a restart. A renewal caught half-written must not take the
// server down with it.
func TestServeTLSPicksUpARenewedCertificate(t *testing.T) {
	setTestHome(t, t.TempDir())
	dir := t.TempDir()
	certPath, keyPath, _ := writeSelfSigned(t, dir, 1)
	var mu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	certs, err := newCertReloader(certPath, keyPath, logf)
	if err != nil {
		t.Fatal(err)
	}
	addr := startTLSServe(t, certs, "a-long-enough-test-token-for-serve-tls")
	if got := servingSerial(t, addr); got != 1 {
		t.Fatalf("serving serial %d, want 1", got)
	}

	writeSelfSigned(t, dir, 2)
	bumpMtime(t, time.Minute, certPath, keyPath)
	if got := servingSerial(t, addr); got != 2 {
		t.Fatalf("after renewal serving serial %d, want 2", got)
	}

	// Half-written: the certificate file is garbage for a moment.
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERT"), 0o600); err != nil {
		t.Fatal(err)
	}
	bumpMtime(t, 2*time.Minute, certPath)
	for i := 0; i < 3; i++ {
		if got := servingSerial(t, addr); got != 2 {
			t.Fatalf("during a broken renewal serving serial %d, want the last good one (2)", got)
		}
	}
	mu.Lock()
	failures := 0
	for _, l := range logged {
		if strings.Contains(l, "reload failed") {
			failures++
		}
	}
	mu.Unlock()
	if failures != 1 {
		t.Errorf("a broken renewal was logged %d times over three handshakes, want once:\n%s", failures, strings.Join(logged, "\n"))
	}
}

// The Settings row asks whether this machine already runs a server. A
// headless `tjek serve --tls-cert` answers plain http with a 400, which must
// still read as "something is running here".
func TestHealthProbeSeesPlainAndTLSServers(t *testing.T) {
	setTestHome(t, t.TempDir())
	plain := httptest.NewServer(newAppSyncServer("tok").Handler())
	t.Cleanup(plain.Close)
	if !healthAnswers(strings.TrimPrefix(plain.URL, "http://")) {
		t.Error("plain server not seen")
	}

	certPath, keyPath, _ := writeSelfSigned(t, t.TempDir(), 1)
	certs, err := newCertReloader(certPath, keyPath, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if addr := startTLSServe(t, certs, "a-long-enough-test-token-for-serve-tls"); !healthAnswers(addr) {
		t.Error("TLS server not seen")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	if healthAnswers(closed) {
		t.Error("a closed port read as a running server")
	}
}
