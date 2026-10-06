package aiprovider

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// LocalWhisperProvider shells out to a local whisper.cpp build
// ("whisper-cli", or the older "main" name) rather than calling an API —
// no key, no network call, nothing leaving the machine. The main package
// resolves BinPath/ModelPath (whisper-cli-or-main on PATH or a configured
// override; a model file downloaded once under <data>/whisper/) before
// constructing this; this package stays as config-free as every other
// provider here and just runs what it's given.
type LocalWhisperProvider struct {
	BinPath   string
	ModelPath string
	// Language is a whisper.cpp language code (e.g. "da"). Empty lets
	// whisper.cpp auto-detect, at some cost to both accuracy and speed
	// relative to naming it.
	Language string
}

func (p *LocalWhisperProvider) Name() string { return "Local Whisper" }

// Transcribe runs whisper-cli once over audioPath and returns its plain-
// text output. Unlike notify.go's shelling-out (CombinedOutput, where
// stdout and stderr are both just diagnostic text), stdout here *is* the
// transcript — -nt/-np keep it to plain recognized text with no
// timestamps or progress chatter, and only stdout is read as data; stderr
// is read solely to build a useful error message on a non-zero exit.
func (p *LocalWhisperProvider) Transcribe(ctx context.Context, audioPath string) (string, error) {
	args := []string{"-m", p.ModelPath, "-f", audioPath, "-nt", "-np"}
	if p.Language != "" {
		args = append(args, "-l", p.Language)
	}
	cmd := exec.CommandContext(ctx, p.BinPath, args...)
	out, err := cmd.Output()
	if err != nil {
		// ExitError means the process actually ran and exited non-zero —
		// everything else (including a PATH lookup failure, caught at
		// construction via cmd.Err, or an absolute path that doesn't
		// exist, caught only here) means it never started at all.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("whisper-cli: %s", firstUsefulLine(exitErr.Stderr))
		}
		return "", fmt.Errorf("whisper-cli not found at %q: %w", p.BinPath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// firstUsefulLine picks the first non-blank line out of a failed process's
// stderr, clipped — the same "one readable line, not a dump" treatment
// notify.go's notifyFailureReason gives a failed notifier's output.
func firstUsefulLine(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 160 {
				line = line[:160]
			}
			return line
		}
	}
	return "exited with an error and no output"
}
