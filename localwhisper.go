package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Iliorn/tjek/paths"

	tea "github.com/charmbracelet/bubbletea"
)

// localwhisper.go acquires the one local-transcription asset tjek doesn't
// already have a reason to own: the ggml model file whisper-cli reads.
// (The whisper-cli binary itself is a separate, unverified-across-platforms
// story — see cli_diagnose.go's diagnoseWhisperCPP and ARCHITECTURE.md.)
// The download mechanics mirror helpers.go's downloadReleaseAsset (stream
// to a file while hashing, verify, then move into place) but as their own
// function rather than a reuse of it: that one's checkAssetURL is pinned to
// GitHub hosts, and a model lives on Hugging Face.

// whisperModelFileName and whisperModelDownloadURL name the one model this
// feature supports — large-v3-turbo, q5_0 quantized, whisper.cpp's own
// "good balance" recommendation. whisperModelDownloadURL is a var, not a
// const, so a test can repoint it at an httptest.Server the same way
// helpers_test.go repoints releaseAPIBase.
const whisperModelFileName = "ggml-large-v3-turbo-q5_0.bin"

var whisperModelDownloadURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + whisperModelFileName

// whisperModelSizeBytes (display only, not verified against) and
// whisperModelSHA256 were not guessed: both were read off the real file
// (fetched by hand during implementation) rather than assumed from a model
// card, so a mismatch at download time means the upstream file actually
// changed, not that this value was wrong from the start. whisperModelSHA256
// is a var, like whisperModelDownloadURL, so a test can point it at a
// fixture's real digest instead of the real model's.
const whisperModelSizeBytes = 574041195

var whisperModelSHA256 = "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2"

// whisperDir is <data>/whisper — its own subdirectory, the same
// one-feature-one-subdirectory convention meetingops.go's
// segmentRecordingPath uses for <data>/recordings/<meetingID>/. Data, not
// Cache: paths/paths.go calls Cache "throwaway" (today only the $EDITOR
// scratch dir); a model the user explicitly chose to fetch for ongoing use
// belongs with the rest of tjek's real, intentional files even though it
// is, in principle, re-fetchable.
func whisperDir() (string, error) {
	dataDir, err := paths.Ensure(paths.Data)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "whisper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// whisperModelPath is the model's fixed location — never a Settings value,
// unlike WhisperBinOverride: there is exactly one file this feature ever
// wants, so there is nothing for a stored path to vary.
func whisperModelPath() (string, error) {
	dir, err := whisperDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, whisperModelFileName), nil
}

// whisperModelStatus reports whether the model is already on disk, and its
// size if so — for the Settings row (settingWhisperModelStatus) and for
// deciding whether turning settingUseLocalWhisper on needs to ask for a
// download first.
func whisperModelStatus() (present bool, size int64) {
	path, err := whisperModelPath()
	if err != nil {
		return false, 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false, 0
	}
	return true, fi.Size()
}

// checkModelURL is checkAssetURL's shape (helpers.go) pinned to Hugging
// Face instead of GitHub — its own function, not a relaxed version of the
// original: that check's narrowness is a deliberate property of its own
// callers and isn't something to loosen for a second, unrelated download.
func checkModelURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("refusing to download from an unparseable URL %q: %w", raw, err)
	}
	// Same test-server escape hatch checkAssetURL uses for releaseAPIBase:
	// defer to whatever whisperModelDownloadURL is pointed at, which is the
	// real Hugging Face host (scheme https) outside tests and a loopback
	// httptest.Server (scheme http) inside them. Matching on scheme too,
	// not just host, matters here in a way it doesn't for checkAssetURL:
	// whisperModelDownloadURL's host *is* the real production host
	// (huggingface.co), not a separate API-base host that never collides
	// with a real download URL's host the way releaseAPIBase's does — so
	// without the scheme match, this escape hatch would wave through a
	// plain-http huggingface.co URL for everyone, not just tests.
	if base, berr := url.Parse(whisperModelDownloadURL); berr == nil && base.Host != "" &&
		base.Host == u.Host && base.Scheme == u.Scheme {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to download the whisper model over %q: https only", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"huggingface.co", "hf.co"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("refusing to download the whisper model from %q: only huggingface.co and hf.co are trusted", u.Host)
}

// downloadWhisperModel fetches whisperModelDownloadURL to a temp file,
// hashing as it streams (helpers.go's downloadReleaseAsset does the same),
// checks the digest against whisperModelSHA256, and only then moves it
// into place at whisperModelPath — a truncated or tampered download can
// never land where the app will actually look for it.
func downloadWhisperModel() error {
	if err := checkModelURL(whisperModelDownloadURL); err != nil {
		return err
	}
	finalPath, err := whisperModelPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(finalPath)
	tmpFile, err := os.CreateTemp(dir, whisperModelFileName+".download-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	req, err := http.NewRequest(http.MethodGet, whisperModelDownloadURL, nil)
	if err != nil {
		tmpFile.Close()
		return err
	}
	req.Header.Set("User-Agent", "tjek/"+appVersion)
	client := &http.Client{
		Timeout: whisperModelDownloadTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return checkModelURL(r.URL.String())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download failed: %s returned %s", req.URL.Host, resp.Status)
	}

	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmpFile, sum), io.LimitReader(resp.Body, maxWhisperModelBytes))
	if cerr := tmpFile.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if written == 0 {
		return fmt.Errorf("download failed: the model file was empty")
	}
	if written == maxWhisperModelBytes {
		return fmt.Errorf("download failed: the model is larger than the %d MB cap", maxWhisperModelBytes/(1024*1024))
	}
	if digest := hex.EncodeToString(sum.Sum(nil)); digest != whisperModelSHA256 {
		return fmt.Errorf("download failed: checksum mismatch (got %s, want %s) — the file at the download URL may have changed", digest, whisperModelSHA256)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("could not install the downloaded model: %w", err)
	}
	return nil
}

// resolveWhisperBinPath decides which whisper-cli to shell out to.
// override (Settings' WhisperBinOverride) wins unconditionally when set —
// a path the user typed in themselves, the same trust level ffmpegInput's
// override gets in audiorecorder.go. Otherwise, only the specific
// "whisper-cli" name is looked up via PATH: whisper.cpp's older "main"
// binary name is deliberately NOT auto-discovered this way, since "main"
// is a common enough name for some unrelated tool's own build output that
// finding one on PATH would be a coincidence, not a real signal — blindly
// shelling out to whatever that happens to be, with transcription-shaped
// arguments, is not a risk worth taking for a convenience fallback.
// Someone with an old whisper.cpp build actually named "main" can still
// use it via the override, which is a deliberate choice, not a guess.
func resolveWhisperBinPath(override string) (path string, found bool) {
	if override != "" {
		return override, true
	}
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p, true
	}
	return "", false
}

// whisperCLIInstallHint, unlike ffmpegInstallHint (audiorecorder.go), does
// not assert a package-manager command for every platform: Homebrew's
// whisper.cpp formula is a safe bet, but apt/dnf/pacman/winget coverage is
// unverified, and naming a package that might not exist is worse than
// pointing at the project itself.
func whisperCLIInstallHint() string {
	if runtime.GOOS == "darwin" {
		return "install it with: brew install whisper-cpp"
	}
	return "see https://github.com/ggml-org/whisper.cpp for prebuilt releases or build instructions"
}

// whisperModelDownloadDoneMsg reports downloadWhisperModelCmd finishing —
// err nil on success, in which case whisperModelStatus will now report the
// model present.
type whisperModelDownloadDoneMsg struct{ err error }

// downloadWhisperModelCmd runs downloadWhisperModel off the Update loop —
// it's a large, slow network write, the same reason every other network
// call in this app (an AI request, a sync push, self-update's own
// download) runs as a tea.Cmd rather than inline in a key handler.
func downloadWhisperModelCmd() tea.Cmd {
	return func() tea.Msg {
		return whisperModelDownloadDoneMsg{err: downloadWhisperModel()}
	}
}
