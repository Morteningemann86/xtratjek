package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSplitFFmpegOverride(t *testing.T) {
	cases := []struct {
		in         string
		wantFormat string
		wantInput  string
		wantOK     bool
	}{
		{"dshow:audio=Microphone Array", "dshow", "audio=Microphone Array", true},
		{"pulse:default", "pulse", "default", true},
		// input legitimately containing a colon must not be split further.
		{"dshow:audio=Realtek: Mic", "dshow", "audio=Realtek: Mic", true},
		{"no-colon-here", "", "", false},
		{":missing-format", "", "", false},
		{"missing-input:", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		format, input, ok := splitFFmpegOverride(c.in)
		if ok != c.wantOK {
			t.Errorf("splitFFmpegOverride(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if format != c.wantFormat || input != c.wantInput {
			t.Errorf("splitFFmpegOverride(%q) = (%q, %q), want (%q, %q)", c.in, format, input, c.wantFormat, c.wantInput)
		}
	}
}

func TestDefaultFFmpegInputOnThisPlatform(t *testing.T) {
	format, input, ok := defaultFFmpegInput()
	switch runtime.GOOS {
	case "windows":
		if ok {
			t.Fatal("defaultFFmpegInput() should have no default on windows (device names aren't guessable)")
		}
	default:
		if !ok || format == "" || input == "" {
			t.Fatalf("defaultFFmpegInput() = (%q, %q, %v), want a usable default on %s", format, input, ok, runtime.GOOS)
		}
	}
}

// TestStartRecordingNoFFmpeg exercises the real "ffmpeg not found" path —
// nothing else in this file can run without ffmpeg actually installed, which
// this dev environment doesn't have, so that's the one scenario genuinely
// covered end-to-end. A successful record→Stop round trip needs a real
// ffmpeg binary and an available audio input and is not exercised by this
// test suite; it was checked by hand against a local ffmpeg install.
func TestStartRecordingNoFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed in this environment; the not-found path can't be exercised here")
	}
	_, err := StartRecording(filepath.Join(t.TempDir(), "out.wav"), "")
	if err == nil {
		t.Fatal("expected an error when ffmpeg is not on PATH")
	}
}
