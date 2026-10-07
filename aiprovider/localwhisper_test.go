package aiprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeWhisperEnv names the role the test binary plays when it runs as
// whisper-cli (see TestMain): the provider is pointed at the test binary
// itself, so the fake runs on every OS without a shell or whisper.cpp.
const fakeWhisperEnv = "AIPROVIDER_FAKE_WHISPER_CLI"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeWhisperEnv) {
	case "":
		os.Exit(m.Run())
	case "transcript":
		fmt.Fprintln(os.Stderr, "whisper_init: loading model")
		fmt.Println("  Hej, dette er en test.  ")
	case "fail":
		fmt.Fprintln(os.Stderr, "error: failed to load model")
		os.Exit(1)
	case "args":
		fmt.Println(strings.Join(os.Args[1:], " "))
	}
	os.Exit(0)
}

// fakeWhisperCLI returns a whisper-cli stand-in that behaves as role:
// "transcript", "fail" or "args" (echo the arguments it was given).
func fakeWhisperCLI(t *testing.T, role string) string {
	t.Helper()
	t.Setenv(fakeWhisperEnv, role)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestLocalWhisperTranscribeSuccess(t *testing.T) {
	bin := fakeWhisperCLI(t, "transcript")
	p := &LocalWhisperProvider{BinPath: bin, ModelPath: "model.bin"}
	got, err := p.Transcribe(context.Background(), "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hej, dette er en test." {
		t.Fatalf("Transcribe() = %q", got)
	}
}

func TestLocalWhisperTranscribeNotFound(t *testing.T) {
	p := &LocalWhisperProvider{BinPath: filepath.Join(t.TempDir(), "does-not-exist"), ModelPath: "model.bin"}
	_, err := p.Transcribe(context.Background(), "audio.wav")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want it to mention not found", err)
	}
}

func TestLocalWhisperTranscribeNonZeroExitSurfacesStderr(t *testing.T) {
	bin := fakeWhisperCLI(t, "fail")
	p := &LocalWhisperProvider{BinPath: bin, ModelPath: "model.bin"}
	_, err := p.Transcribe(context.Background(), "audio.wav")
	if err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Fatalf("err = %v, want it to mention the failure", err)
	}
}

func TestLocalWhisperTranscribePassesLanguageFlag(t *testing.T) {
	bin := fakeWhisperCLI(t, "args")
	p := &LocalWhisperProvider{BinPath: bin, ModelPath: "model.bin", Language: "da"}
	got, err := p.Transcribe(context.Background(), "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "-l da") {
		t.Fatalf("args = %q, want -l da present", got)
	}

	p.Language = ""
	got, err = p.Transcribe(context.Background(), "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "-l") {
		t.Fatalf("args = %q, want no -l flag when Language is empty", got)
	}
}

// TestLocalWhisperTranscribePassesThreadFlag guards the fix for a real
// measured problem: whisper-cli's own default of 4 threads left most of a
// 28-thread machine idle, more than doubling encode time for no reason —
// see localwhisper.go's comment on the -t flag for the numbers.
func TestLocalWhisperTranscribePassesThreadFlag(t *testing.T) {
	bin := fakeWhisperCLI(t, "args")
	p := &LocalWhisperProvider{BinPath: bin, ModelPath: "model.bin"}
	got, err := p.Transcribe(context.Background(), "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "-t "+strconv.Itoa(runtime.NumCPU())) {
		t.Fatalf("args = %q, want -t %d present", got, runtime.NumCPU())
	}
}

func TestLocalWhisperName(t *testing.T) {
	if (&LocalWhisperProvider{}).Name() != "Local Whisper" {
		t.Fatal("Name() mismatch")
	}
}
