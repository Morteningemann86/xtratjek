#!/usr/bin/env bash
# Generate the package manifest for a release.
#
# The Scoop manifest pins a version and a checksum, so a copy kept by hand in
# the tree is wrong from the moment the next tag is pushed, and wrong in the
# way that matters, since the checksum is the thing users rely on. It is
# generated from the binaries the release job just built, and attached to the
# release itself. The version and the hash are then correct by construction,
# and there is no second place to remember to update.
#
# Usage:  packaging/generate-packages.sh <version> <outdir>
#
# Expects the Windows release binary in the current directory under the exact
# name the release publishes: tjek.exe.

set -euo pipefail

version="${1:?usage: generate-packages.sh <version> <outdir>}"
outdir="${2:?usage: generate-packages.sh <version> <outdir>}"
# Scoop wants a bare version; the release tag carries a "v".
bare="${version#v}"
base="https://github.com/Iliorn/tjek/releases/download/${version}"

mkdir -p "$outdir"

sha() { sha256sum "$1" | cut -d' ' -f1; }

sha_windows="$(sha tjek.exe)"

# ── Scoop (Windows) ─────────────────────────────────────────────────────────
# Installed with:
#   scoop install https://github.com/Iliorn/tjek/releases/latest/download/tjek.json
# That URL always resolves to the newest release, so it never needs bumping.
# checkver/autoupdate are there so the manifest also works unmodified inside a
# Scoop bucket, where the excavator maintains it. "depends": "ffmpeg" makes
# Scoop install ffmpeg from the main bucket first if it isn't already on the
# system — Meetings recording (audiorecorder.go) needs it on PATH, and this
# is the one install path that can pull it in automatically.
cat > "$outdir/tjek.json" <<EOF
{
    "version": "${bare}",
    "description": "A keyboard-driven task manager for the terminal that tells you what to do next",
    "homepage": "https://github.com/Iliorn/tjek",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "${base}/tjek.exe",
            "hash": "${sha_windows}"
        }
    },
    "depends": "ffmpeg",
    "bin": "tjek.exe",
    "checkver": {
        "github": "https://github.com/Iliorn/tjek"
    },
    "autoupdate": {
        "architecture": {
            "64bit": {
                "url": "https://github.com/Iliorn/tjek/releases/download/v\$version/tjek.exe"
            }
        },
        "hash": {
            "url": "https://github.com/Iliorn/tjek/releases/download/v\$version/SHA256SUMS"
        }
    }
}
EOF

echo "wrote $outdir/tjek.json for $version"
