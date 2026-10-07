package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// whisperModelServer stands in for Hugging Face, the same way
// helpers_test.go's releaseServer stands in for GitHub: it repoints
// whisperModelDownloadURL at a loopback server for the duration of one
// test, restoring the real URL (and the real checksum, which callers set
// to match their fixture content) when it ends.
func whisperModelServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	prevURL, prevSum := whisperModelDownloadURL, whisperModelSHA256
	whisperModelDownloadURL = srv.URL + "/" + whisperModelFileName
	t.Cleanup(func() {
		whisperModelDownloadURL, whisperModelSHA256 = prevURL, prevSum
		srv.Close()
	})
	return srv
}

func TestDownloadWhisperModelSuccess(t *testing.T) {
	setTestHome(t, t.TempDir())
	payload := []byte("fake ggml model bytes")
	sum := sha256.Sum256(payload)
	whisperModelSHA256 = hex.EncodeToString(sum[:])
	whisperModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})

	if present, _ := whisperModelStatus(); present {
		t.Fatal("model should not be present before downloading")
	}
	if err := downloadWhisperModel(); err != nil {
		t.Fatal(err)
	}
	present, size := whisperModelStatus()
	if !present || size != int64(len(payload)) {
		t.Fatalf("whisperModelStatus() = %v, %d; want true, %d", present, size, len(payload))
	}
	path, err := whisperModelPath()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("installed file content = %q, want %q", got, payload)
	}
}

func TestDownloadWhisperModelChecksumMismatchRejected(t *testing.T) {
	setTestHome(t, t.TempDir())
	whisperModelSHA256 = strings.Repeat("0", 64) // never matches real content
	whisperModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some bytes that don't match"))
	})

	if err := downloadWhisperModel(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch error", err)
	}
	if present, _ := whisperModelStatus(); present {
		t.Fatal("a checksum mismatch must not install the file")
	}
}

func TestDownloadWhisperModelHTTPErrorRejected(t *testing.T) {
	setTestHome(t, t.TempDir())
	whisperModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if err := downloadWhisperModel(); err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func TestCheckModelURLRejectsNonHuggingFaceHost(t *testing.T) {
	if err := checkModelURL("https://evil.example.com/" + whisperModelFileName); err == nil {
		t.Fatal("expected an error for a non-Hugging-Face host")
	}
	if err := checkModelURL("http://huggingface.co/" + whisperModelFileName); err == nil {
		t.Fatal("expected an error for plain http even on the right host")
	}
	if err := checkModelURL("https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + whisperModelFileName); err != nil {
		t.Fatalf("real Hugging Face URL rejected: %v", err)
	}
	if err := checkModelURL("https://hf.co/ggerganov/whisper.cpp/resolve/main/" + whisperModelFileName); err != nil {
		t.Fatalf("hf.co short-domain URL rejected: %v", err)
	}
}

func TestWhisperModelStatusAbsent(t *testing.T) {
	setTestHome(t, t.TempDir())
	if present, size := whisperModelStatus(); present || size != 0 {
		t.Fatalf("whisperModelStatus() = %v, %d; want false, 0 on a fresh home", present, size)
	}
}
