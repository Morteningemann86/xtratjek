# Installing tjek

The [README](../README.md#install) has the one-line install for each
platform. This page has the details.

## macOS (Homebrew)

```sh
brew install iliorn/tap/tjek
```

Homebrew builds the tagged source and installs the `tjek` command. Update
with `brew update && brew upgrade tjek`; tjek recognises a Homebrew install
and points you at that command rather than replacing Homebrew's files.

## Windows (Scoop)

```sh
scoop install https://github.com/Morteningemann86/xtratjek/releases/latest/download/tjek.json
```

That address always points at the newest release. Upgrade with
`scoop update tjek`.

Notes open in `EDITOR` if it is set (`setx EDITOR hx`), otherwise Notepad.

## A downloaded binary (Linux / Windows)

From the [Releases](https://github.com/Morteningemann86/xtratjek/releases) page:

| File | Platform |
|------|----------|
| `tjek` | Linux x64 |
| `tjek-linux-arm64` | Linux arm64 (Raspberry Pi, ARM servers) |
| `tjek.exe` | Windows x64 |
| `SHA256SUMS` | checksums for the three binaries |
| `tjek.json` | Scoop manifest (Windows) |

Put it somewhere on your `PATH` (for example `~/.local/bin`). Later updates
are one step: Settings → "Update to latest release", or `tjek update`. The
download is checked against the release's `SHA256SUMS`, and nothing is
installed if it doesn't match.

## With Go

```sh
go install github.com/Morteningemann86/xtratjek@latest
```

Builds from source on any platform Go supports, including ones the release
page doesn't carry. Go checks every module against `sum.golang.org`, a public
log that cannot be rewritten afterwards, which makes this the install with the
strongest integrity guarantee.

## From source

```sh
git clone https://github.com/Morteningemann86/xtratjek
cd tjek
go build -ldflags "-X main.appVersion=$(git describe --tags --abbrev=0)" -o tjek .
mv tjek ~/.local/bin/   # or anywhere on your PATH
```

## Recording meetings needs ffmpeg

The Meetings tab's recording (`r`) shells out to `ffmpeg`, which is a
separate install from tjek itself. Scoop pulls it in automatically as a
dependency; everywhere else, pressing `r` without it offers to install it
there and then (with a `y`/`n` confirmation first) — see
[Files and troubleshooting](troubleshooting.md#ffmpeg-not-found-when-recording-a-meeting)
for how that works and the manual command for each platform. `tjek doctor`
reports whether tjek can find it. Typing notes, summarizing and reviewing
action items all work without it.

## Checking a download

That a file matches what the release published:

```sh
curl -LO https://github.com/Morteningemann86/xtratjek/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

That it was built by this repository's release workflow. Every release
binary is signed through Sigstore and recorded in a public log:

```sh
gh attestation verify tjek --repo Morteningemann86/xtratjek
```

Release builds are reproducible: check out the tag, run the same `go build`
the [release workflow](../.github/workflows/release.yml) does, and the hashes
should match. [SECURITY.md](../SECURITY.md#the-update-path) explains what each
check does and does not prove.
