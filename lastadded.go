package main

import (
	"os"
	"strings"

	"github.com/Morteningemann86/xtratjek/paths"
)

// The "last added" pointer backs the `dep:^` / `--depends ^` shorthand: a
// one-line sidecar holding the ID of the most recently created top-level
// task, written on every successful add from either surface (CLI or TUI) so
// a follow-up add can chain onto it without the user looking up a ref.
// It's a sidecar rather than a settings.json field so a TUI session and a
// CLI call can't clobber each other's unrelated settings, and best-effort on
// both ends — a missing file just means "nothing to chain to yet".

func lastAddedPath() string {
	return paths.For(paths.State, "last-added")
}

func saveLastAddedID(id string) {
	if _, err := paths.Ensure(paths.State); err != nil {
		return
	}
	_ = writeFileAtomic(lastAddedPath(), []byte(id+"\n"), 0o644)
}

func loadLastAddedID() string {
	b, err := os.ReadFile(lastAddedPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
