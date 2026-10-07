#!/usr/bin/env bash
# Print the CHANGELOG section for a release tag, for use as the release body.
#
# `gh release create --generate-notes` lists commit subjects. Those describe the
# work to whoever wrote it, not to whoever is deciding whether to upgrade — and
# the CHANGELOG already has that second thing written, in the words it should be
# read in. Pulling the matching section out means the release page and the file
# cannot say different things about the same version.
#
# Usage:  packaging/release-notes.sh <version> [changelog]
#
# Exits non-zero when the version has no section, so the caller can fall back to
# generated notes rather than publishing a release with an empty body.

set -euo pipefail

version="${1:?usage: release-notes.sh <version> [changelog]}"
changelog="${2:-CHANGELOG.md}"
# CHANGELOG headings carry a bare version ("## [1.31.0] - 2026-08-15"); the
# release tag carries a "v".
bare="${version#v}"

# Everything between this version's heading and the next release heading.
#
# The heading itself is dropped — the release is already titled with the
# version, and repeating it wastes the first line of the page. Blank lines are
# trimmed at both ends but kept in the middle: `pending` holds them back until
# a non-blank line proves they were a separator rather than trailing slack, so
# the section ends cleanly whether or not the file does.
section="$(awk -v want="## [${bare}]" '
  index($0, want) == 1 { found = 1; next }
  found && /^## / { exit }
  found {
    if ($0 ~ /[^[:space:]]/) {
      while (pending-- > 0) print ""
      pending = 0
      print
    } else if (NR > 0 && started_at) {
      pending++
    }
    if ($0 ~ /[^[:space:]]/) started_at = 1
  }
' "$changelog")"

if [ -z "$section" ]; then
  exit 1
fi

# The link is the one thing generated notes gave that a section does not: a way
# to read the versions around this one without leaving the release page.
printf '%s\n\n**Full changelog:** https://github.com/Morteningemann86/xtratjek/blob/%s/CHANGELOG.md\n' \
  "$section" "$version"
