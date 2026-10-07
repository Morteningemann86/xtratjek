package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Morteningemann86/xtratjek/paths"
)

// adoptFormerName carries an install made under the former name, taskr, over
// to tjek: its directories (paths.AdoptFormerDirs), and the binary itself when
// it is still called taskr, so an install that updated itself into this build
// answers to the new name from its next run. It runs before anything resolves
// a path, and says on stderr what it moved.
//
// The binary is left alone when a package manager owns it (renaming a file
// Homebrew or Scoop installed breaks their next upgrade) or when tjek already
// exists beside it.
func adoptFormerName() {
	moves, err := paths.AdoptFormerDirs()
	for _, mv := range moves {
		fmt.Fprintf(os.Stderr, "tjek: moved %s to %s\n", mv.From, mv.To)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek: could not move %s's files: %v\n", paths.FormerName, err)
	}

	exe, err := os.Executable()
	if err != nil || !paths.FormerExecutable(exe) || packageManagerFor(exe) != "" {
		return
	}
	target := filepath.Join(filepath.Dir(exe), "tjek"+filepath.Ext(exe))
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := os.Rename(exe, target); err == nil {
		fmt.Fprintf(os.Stderr, "tjek: %s is now called tjek (%s)\n", paths.FormerName, target)
	}
}
