package tasksync

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Morteningemann86/xtratjek/todo"
)

// compress_test.go covers the gzip negotiation on /v1/sync. The mixed-version
// cases are the point: a compressed body toward an older server is a failed
// sync, so every path that could send one to a server that has not asked for
// it is pinned here.

// forgetGzip clears what this process has learned about which servers take a
// gzipped request, so a test starts from a first sync.
func forgetGzip(t *testing.T) {
	t.Helper()
	reset := func() {
		gzipAcceptingURLs.Range(func(k, _ any) bool { gzipAcceptingURLs.Delete(k); return true })
	}
	reset()
	t.Cleanup(reset)
}

// rawClient does not decompress on its own and does not ask for gzip unless a
// test sets the header, so the tests see the bytes the server actually sent.
var rawClient = &http.Client{Transport: &http.Transport{DisableCompression: true}}

func syncRequest(t *testing.T, url string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/sync", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func mustGzip(t *testing.T, b []byte) []byte {
	t.Helper()
	z, err := gzipBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func requestBody(t *testing.T, tasks ...todo.Todo) []byte {
	t.Helper()
	b, err := json.Marshal(Request{Tasks: tasks, Protocol: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every client that ever shipped asks for gzip through Go's transport, so the
// response side needs no negotiation — and a client that does not ask must
// still get plain JSON.
func TestSyncResponseIsGzippedOnlyWhenAsked(t *testing.T) {
	store := &fakeStore{tasks: []todo.Todo{newTask("on the server", time.Now().UTC())}}
	hs := testServer(t, &Server{Token: "tok", Store: store})

	resp := syncRequest(t, hs.URL, requestBody(t), map[string]string{"Accept-Encoding": "gzip"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("response is not gzip: %v", err)
	}
	var out Response
	if err := json.NewDecoder(zr).Decode(&out); err != nil {
		t.Fatalf("decode gzipped response: %v", err)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].Title != "On the server" {
		t.Errorf("tasks = %+v", out.Tasks)
	}

	for _, ae := range []string{"", "identity", "gzip;q=0"} {
		resp = syncRequest(t, hs.URL, requestBody(t), map[string]string{"Accept-Encoding": ae})
		if got := resp.Header.Get("Content-Encoding"); got != "" {
			t.Errorf("Accept-Encoding %q: got Content-Encoding %q, want none", ae, got)
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Errorf("Accept-Encoding %q: plain response did not decode: %v", ae, err)
		}
	}
}

// The advertisement is how a client learns it may compress, so it rides on
// every answer — a client whose first sync failed on a stale token has still
// learned something true.
func TestServerAdvertisesGzipRequestsOnEveryAnswer(t *testing.T) {
	hs := testServer(t, &Server{Token: "tok", Store: &fakeStore{}})
	ok := syncRequest(t, hs.URL, requestBody(t), nil)
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/v1/sync", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer wrong")
	unauth, err := rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer unauth.Body.Close()
	for name, resp := range map[string]*http.Response{"200": ok, "401": unauth} {
		if got := resp.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Errorf("%s: Accept-Encoding = %q, want gzip", name, got)
		}
	}
}

func TestServerMergesAGzippedRequest(t *testing.T) {
	store := &fakeStore{}
	hs := testServer(t, &Server{Token: "tok", Store: store})
	body := mustGzip(t, requestBody(t, newTask("sent compressed", time.Now().UTC())))
	resp := syncRequest(t, hs.URL, body, map[string]string{"Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, msg)
	}
	if len(store.tasks) != 1 || store.tasks[0].Title != "Sent compressed" {
		t.Errorf("store = %+v, want the compressed task merged", store.tasks)
	}
}

func TestServerRefusesBodiesItCannotDecode(t *testing.T) {
	store := &fakeStore{}
	hs := testServer(t, &Server{Token: "tok", Store: store})

	resp := syncRequest(t, hs.URL, requestBody(t), map[string]string{"Content-Encoding": "br"})
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("br: status %d, want 415", resp.StatusCode)
	}
	resp = syncRequest(t, hs.URL, []byte("not gzip at all"), map[string]string{"Content-Encoding": "gzip"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("corrupt gzip: status %d, want 400", resp.StatusCode)
	}
	if store.calls != 0 {
		t.Errorf("a body the server could not decode reached the merge %d time(s)", store.calls)
	}
}

// The 64 MB cap has to hold after decompression: a few kilobytes of gzip can
// expand into gigabytes. The body is valid JSON as far as it goes, so the
// decoder keeps reading and it is the cap, not a syntax error, that stops it.
func TestServerCapsTheDecompressedBody(t *testing.T) {
	store := &fakeStore{}
	hs := testServer(t, &Server{Token: "tok", Store: store})

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	write := func(b []byte) {
		if _, err := zw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write([]byte(`{"tasks":[],"pad":"`))
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for i := 0; i < maxSyncBody>>20+1; i++ {
		write(chunk)
	}
	write([]byte(`"}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() >= maxSyncBody {
		t.Fatalf("the bomb is %d bytes on the wire; it has to fit under the wire cap to test the other one", buf.Len())
	}

	resp := syncRequest(t, hs.URL, buf.Bytes(), map[string]string{"Content-Encoding": "gzip"})
	msg, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(msg), "too large") {
		t.Errorf("status %d, body %q; want 400 naming the size", resp.StatusCode, msg)
	}
	if store.calls != 0 {
		t.Error("an over-cap body reached the merge")
	}
}

func TestCappedReaderAllowsExactlyTheCap(t *testing.T) {
	read := func(s string, limit int64) (string, error) {
		b, err := io.ReadAll(&cappedReader{r: strings.NewReader(s), left: limit})
		return string(b), err
	}
	if got, err := read("12345", 5); err != nil || got != "12345" {
		t.Errorf("exactly at the cap: %q, %v", got, err)
	}
	if _, err := read("123456", 5); !errors.Is(err, errBodyTooLarge) {
		t.Errorf("one byte over: err = %v, want errBodyTooLarge", err)
	}
	if got, err := read("12", 5); err != nil || got != "12" {
		t.Errorf("under the cap: %q, %v", got, err)
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"gzip":              true,
		"gzip, deflate, br": true,
		"br;q=1.0, GZIP":    true,
		"gzip;q=0.5":        true,
		"gzip;q=0":          false,
		"gzip; q=0.0":       false,
		"":                  false,
		"identity":          false,
		"x-gzip-ish":        false,
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

// encodingRecorder sits in front of a handler and notes each request's
// Content-Encoding, so a test can see what PostSync actually put on the wire.
type encodingRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (e *encodingRecorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.seen = append(e.seen, r.Header.Get("Content-Encoding"))
		e.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (e *encodingRecorder) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.seen
	e.seen = nil
	return s
}

// A first sync is plain — it is the only way to meet a server whose build is
// unknown — and every sync after the server has said "gzip" is compressed.
func TestPostSyncCompressesOnceTheServerHasSaidSo(t *testing.T) {
	forgetGzip(t)
	store := &fakeStore{}
	rec := &encodingRecorder{}
	hs := httptest.NewServer(rec.wrap((&Server{Token: "tok", Store: store}).Handler()))
	t.Cleanup(hs.Close)

	for i, title := range []string{"first", "second", "third"} {
		resp, err := PostSync(hs.URL, "tok", "", []todo.Todo{newTask(title, time.Now().UTC())}, nil, 5*time.Second)
		if err != nil {
			t.Fatalf("sync %d: %v", i+1, err)
		}
		if len(resp.Tasks) != i+1 {
			t.Fatalf("sync %d: merged set has %d tasks, want %d", i+1, len(resp.Tasks), i+1)
		}
	}
	if got := rec.take(); strings.Join(got, ",") != ",gzip,gzip" {
		t.Errorf("request encodings = %q, want plain then gzip, gzip", got)
	}
}

// oldServer answers like a build from before compression: no advertisement,
// and a body decoded as JSON straight off the wire.
func oldServer(store *fakeStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		merged, _, _ := store.MergeIn(req.Tasks)
		if err := json.NewEncoder(w).Encode(Response{Tasks: merged, Protocol: ProtocolVersion}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

func TestPostSyncNeverCompressesTowardAnOlderServer(t *testing.T) {
	forgetGzip(t)
	rec := &encodingRecorder{}
	hs := httptest.NewServer(rec.wrap(oldServer(&fakeStore{})))
	t.Cleanup(hs.Close)
	for i := 0; i < 3; i++ {
		if _, err := PostSync(hs.URL, "tok", "", []todo.Todo{newTask("x", time.Now().UTC())}, nil, 5*time.Second); err != nil {
			t.Fatalf("sync %d against an older server: %v", i+1, err)
		}
	}
	if got := rec.take(); strings.Join(got, ",") != ",," {
		t.Errorf("request encodings = %q, want three plain", got)
	}
}

// The same URL can stop taking gzip — a server rolled back to an older
// build. The compressed attempt fails there; the sync must not.
func TestPostSyncFallsBackWhenTheServerStopsTakingGzip(t *testing.T) {
	forgetGzip(t)
	store := &fakeStore{}
	current := (&Server{Token: "tok", Store: store}).Handler()
	older := oldServer(store)
	var rolledBack bool
	var mu sync.Mutex
	rec := &encodingRecorder{}
	hs := httptest.NewServer(rec.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := current
		if rolledBack {
			h = older
		}
		mu.Unlock()
		h.ServeHTTP(w, r)
	})))
	t.Cleanup(hs.Close)
	post := func(title string) {
		t.Helper()
		if _, err := PostSync(hs.URL, "tok", "", []todo.Todo{newTask(title, time.Now().UTC())}, nil, 5*time.Second); err != nil {
			t.Fatalf("sync %q: %v", title, err)
		}
	}

	post("learn") // plain; the answer says gzip
	rec.take()
	mu.Lock()
	rolledBack = true
	mu.Unlock()

	post("after rollback")
	if got := rec.take(); strings.Join(got, ",") != "gzip," {
		t.Errorf("rollback sync encodings = %q, want a gzip attempt then one plain retry", got)
	}
	post("steady")
	if got := rec.take(); strings.Join(got, ",") != "" {
		t.Errorf("after the fallback the client should stay plain, got %q", got)
	}
	if len(store.tasks) != 3 {
		t.Errorf("store holds %d tasks, want all three syncs merged", len(store.tasks))
	}
}
