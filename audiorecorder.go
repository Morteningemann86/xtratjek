package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Recording goes through ffmpeg, already the standard tool for this and
// available on every platform tjek supports, rather than a cgo audio-capture
// binding — the same reasoning as notify.go's shell-outs: no cgo, no bundled
// library, just a well-known external program tjek expects the user to have
// installed (doctor.go can check for it the way it already checks the
// editor). Capture is 16kHz mono — enough for speech transcription, far
// smaller than a stereo music-rate file for an hour-long meeting.

const (
	recordSampleRate = "16000"
	recordChannels   = "1"
)

// ffmpegInstallHint names the one command that installs ffmpeg on this
// platform's most common package manager — shown by both the recording
// error and `tjek doctor`, so a missing ffmpeg is something to paste into a
// terminal rather than something to go search for.
func ffmpegInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install it with: brew install ffmpeg"
	case "windows":
		return "install it with: winget install ffmpeg (or scoop install ffmpeg, choco install ffmpeg)"
	default:
		return "install it with your package manager, e.g. sudo apt install ffmpeg (Debian/Ubuntu), sudo dnf install ffmpeg (Fedora), or sudo pacman -S ffmpeg (Arch)"
	}
}

// defaultFFmpegInput returns the -f/-i pair for this platform's default
// microphone. Windows has no stable default: DirectShow devices are named by
// the OS and vary machine to machine (discoverable with
// `ffmpeg -list_devices true -f dshow -i dummy`), so there is no guess to
// make — AISettings.FFmpegInput must be set there before recording works.
// aiSettingsOverride, when non-empty, replaces this entirely (any platform).
func defaultFFmpegInput() (format, input string, ok bool) {
	switch runtime.GOOS {
	case "darwin":
		// ":0" is avfoundation's "no video, default audio device" address.
		return "avfoundation", ":0", true
	case "windows":
		return "", "", false
	default:
		// PulseAudio and PipeWire's pulse-compat layer cover the large
		// majority of modern Linux/BSD desktops; ALSA-only systems need an
		// override (e.g. "alsa" / "default" or a specific hw: device).
		return "pulse", "default", true
	}
}

// splitFFmpegOverride splits "format:input" on the first colon — input may
// itself legitimately contain one (a Windows dshow device string is
// "audio=Name"), so this never splits past the first.
func splitFFmpegOverride(s string) (format, input string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 || i == 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// AudioRecorder is one in-progress recording, started by StartRecording.
type AudioRecorder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output string
	done   chan error
}

// OutputPath is the wav file the recording is being written to.
func (r *AudioRecorder) OutputPath() string { return r.output }

// StartRecording launches ffmpeg capturing the default microphone (or the
// override from Settings → AI, required on Windows) to a fresh wav file at
// outputPath. The returned *AudioRecorder is already recording; call Stop to
// finish it.
func StartRecording(outputPath, inputOverride string) (*AudioRecorder, error) {
	format, input, ok := defaultFFmpegInput()
	if inputOverride != "" {
		// Override is "format:input", e.g. "dshow:audio=Microphone Array".
		f, i, splitOK := splitFFmpegOverride(inputOverride)
		if !splitOK {
			return nil, fmt.Errorf("invalid audio input override %q (want \"format:input\", e.g. \"dshow:audio=Microphone\")", inputOverride)
		}
		format, input, ok = f, i, true
	}
	if !ok {
		return nil, errors.New("no default microphone input on this platform — set it in Settings → AI (ffmpeg -list_devices true -f dshow -i dummy lists Windows device names)")
	}

	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", format, "-i", input,
		"-ar", recordSampleRate, "-ac", recordChannels,
		outputPath,
	)
	if errors.Is(cmd.Err, exec.ErrNotFound) {
		return nil, fmt.Errorf("ffmpeg not found — %s", ffmpegInstallHint())
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}

	r := &AudioRecorder{cmd: cmd, stdin: stdin, output: outputPath, done: make(chan error, 1)}
	go func() { r.done <- cmd.Wait() }()
	return r, nil
}

// stopGraceWindow is how long Stop waits for ffmpeg to finalize the file
// after asking it to quit before giving up and killing it outright — a
// killed ffmpeg can leave a wav file with a truncated/missing header that
// some decoders reject, so the grace window is worth spending.
const stopGraceWindow = 5 * time.Second

// Stop asks ffmpeg to finish writing OutputPath and waits for it to exit.
// It writes "q" to ffmpeg's stdin — the one stop signal ffmpeg honors
// identically on Linux, macOS and Windows, unlike process signals (SIGINT
// isn't meaningfully deliverable to a Windows child via os.Process.Signal).
// A still-running process past stopGraceWindow is killed outright; the file
// up to that point is kept rather than discarded, so a stuck ffmpeg costs a
// few seconds of audio, not the whole recording.
func (r *AudioRecorder) Stop() error {
	_, writeErr := io.WriteString(r.stdin, "q\n")
	r.stdin.Close()
	select {
	case err := <-r.done:
		// ffmpeg's own clean-quit path still exits with a non-zero status on
		// some builds; what matters is that it exited at all; writeErr (stdin
		// already gone) is the more useful signal of a real problem.
		if writeErr != nil {
			return fmt.Errorf("stopping recording: %w", writeErr)
		}
		_ = err
		return nil
	case <-time.After(stopGraceWindow):
		_ = r.cmd.Process.Kill()
		<-r.done
		return nil
	}
}
