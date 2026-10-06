package aiprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWhisperCLI writes an executable shell script standing in for
// whisper-cli, the same "don't require the real external tool" approach
// helpers_test.go's TestDownloadReleaseAsset uses for a fake release
// binary — this package has no business depending on whisper.cpp actually
// being installed to run its tests.
func fakeWhisperCLI(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "whisper-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalWhisperTranscribeSuccess(t *testing.T) {
	bin := fakeWhisperCLI(t, `echo "whisper_init: loading model" >&2
echo "  Hej, dette er en test.  "
`)
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
	bin := fakeWhisperCLI(t, `echo "error: failed to load model" >&2
exit 1
`)
	p := &LocalWhisperProvider{BinPath: bin, ModelPath: "model.bin"}
	_, err := p.Transcribe(context.Background(), "audio.wav")
	if err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Fatalf("err = %v, want it to mention the failure", err)
	}
}

func TestLocalWhisperTranscribePassesLanguageFlag(t *testing.T) {
	bin := fakeWhisperCLI(t, `echo "$@"
`)
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

func TestLocalWhisperName(t *testing.T) {
	if (&LocalWhisperProvider{}).Name() != "Local Whisper" {
		t.Fatal("Name() mismatch")
	}
}
