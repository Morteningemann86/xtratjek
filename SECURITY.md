# Security policy

## Reporting a vulnerability

Please report security issues **privately**, not as a public issue.

Use GitHub's private vulnerability reporting:
[**Report a vulnerability**](https://github.com/Morteningemann86/xtratjek/security/advisories/new).
It opens a private thread visible only to the maintainers.

Please include what `tjek doctor` prints (it never includes your sync token),
what you did, what happened, and what you expected. A proof of concept helps
but is not required to file.

tjek is a single-maintainer hobby project, so there is no paid bounty and no
guaranteed response time. Expect a first reply within about a week. If a
report is valid you will be credited in the release notes unless you would
rather not be.

## What is in scope

tjek is a local terminal application, so most of it has no attacker to defend
against: whoever can run `tjek` can already read its files. The parts where
that is not true:

- **The sync server** (`tjek serve`, or the in-process server enabled in
  Settings). It listens on a network socket and is the one component that
  accepts input from another machine. Authentication bypass, reading or
  writing another user's tasks, crashes triggered by a request, or anything
  reachable before the bearer-token check are all in scope.
- **The sync client**, including how it handles a hostile or compromised
  server's response.
- **The self-update path** on Linux and Windows, which downloads and replaces
  the running binary.
- **Token and file handling**: anything that writes a sync token somewhere
  it should not be, or that widens the permissions of a file tjek keeps.
- **Parsing of files tjek reads**: the SQLite database, `settings.json`,
  `sync.json`, an imported export file, and the legacy `tasks.json`.

## What is not in scope

- **Plain HTTP between client and server.** `tjek serve` speaks plain http
  unless it is given a certificate (`--tls-cert`/`--tls-key`). Without one, the
  bearer token travels in the clear unless you put it behind a tunnel, and tjek
  warns you about that in Settings and in `tjek doctor`. Run the sync server
  with a certificate, or over Tailscale, a VPN, or a reverse proxy that
  terminates TLS. A report that the token is readable on an unencrypted link you
  chose is documented behaviour, not a vulnerability.
- **A single shared token with full access.** The sync server is single-owner
  by design: one token, no multi-tenancy, no per-device revocation. Anyone
  holding the token can read and write every task.
- **Local access.** Another process running as your user can read tjek's
  files directly; tjek does not try to prevent that.
- **Denial of service against your own server** by a client holding a valid
  token.

## The update path

On Linux and Windows, Settings → "Update to latest release" downloads a binary
from GitHub and replaces the running one. What that does and does not
guarantee, precisely, because the difference is easy to overstate in either
direction:

**What is checked.** The release metadata is read over HTTPS from
`api.github.com` with the standard certificate validation of Go's `net/http`.
The asset URL that comes back is confined to `github.com` and
`githubusercontent.com` over TLS, on the initial request *and* on every
redirect. The download is staged in a private temporary directory (not a
predictable path another local user could write to), hashed as it is written,
and compared against the `SHA256SUMS` published with that release. It fails
closed: no sums file, no entry for this platform, or a mismatch means nothing
is installed and the staged file is removed. The install itself is an atomic
rename. Nothing updates silently; it is a button behind a confirmation.

**What is not checked by the updater: who published it.** `SHA256SUMS` lives in
the same release as the binary, so anyone able to publish a release can publish
matching checksums. The checksum proves your download was not corrupted or
swapped in transit. It does not prove the release itself is honest. A
compromised repository, a stolen token, or a malicious change to the release
workflow would produce a consistent, verifiable, malicious release.

**What closes that, if you check it yourself.** Since v1.35.0 the release
workflow attests every published binary with
[Sigstore](https://www.sigstore.dev/) via GitHub's artifact attestation: a
signed statement that these exact bytes were produced by this repository's
release workflow, at a named commit, recorded in a public transparency log.
The signing certificate is short-lived and minted from the workflow's own OIDC
identity, so there is no key for anyone, me included, to steal or misuse,
and the log entry cannot be withdrawn after the fact. Verify a download with:

```sh
gh attestation verify tjek --repo Morteningemann86/xtratjek
```

That checks the provenance, not just the bytes: it names the workflow, the
commit and the run that built the file in front of you. The in-app updater
does **not** perform this check (verifying an attestation needs the Sigstore
verification stack, which tjek does not carry), so it remains a checksum-level
guarantee, and this is the check to run by hand when that is not enough.

If that trade is not one you want to make, do not use the in-app updater:

- `go install github.com/Morteningemann86/xtratjek@latest` verifies against
  `sum.golang.org`, an append-only transparency log. A recorded hash cannot be
  changed afterwards, including by the maintainer. This is the strongest
  guarantee tjek offers.
- Homebrew and Scoop each add their own distribution checks,
  and tjek refuses to overwrite a Homebrew-managed install.
- Release builds are reproducible (`-trimpath`, `CGO_ENABLED=0`, the Go version
  pinned in `go.mod`), so you can rebuild a tag and compare hashes yourself.

## Choosing a sync token

The sync server authenticates with a single shared bearer token, compared in
constant time and stored in `sync.json` (config directory) with mode `0600`. Every one of
those precautions is downstream of a secret you chose: a guessable token is the
likeliest realistic compromise of a sync setup, and no amount of care further
down compensates for it.

`tjek serve --new-token` mints one from the system CSPRNG (32 bytes,
URL-safe), stores it, and prints it. In Settings, `ctrl+g` on the server-token
row does the same.

Whether a weak token is **refused** or merely **flagged** depends on whose
choice it is:

- **Settings → Server token is refused.** This is the token for the endpoint on
  this machine, which tjek gets to choose, and `ctrl+g` is one keystroke away, so a
  weak value is rejected with the text left in the field to fix.
- **Settings → Sync token is accepted as typed.** That one has to equal
  whatever the *server* already uses. Refusing a short one would not make
  anything safer; it would make a server that uses one unreachable.
- **`tjek serve --token` / `TJEK_SYNC_TOKEN` warn but run.** A deployment's
  token may come from a secret manager, a reverse proxy, or a unit file written
  a year ago. Breaking a running service to make a point about entropy is the
  wrong trade, and the warning is on stderr where an operator will see it.
- **`tjek doctor` reports it** for both tokens, naming the property and never
  the token.

The rule behind all four: tjek refuses only where it can offer the alternative
in the same breath.

There is no rate limiting on failed authentication. With a generated token that
is irrelevant; with a short one it is not, which is the other reason the
warning exists.

## Handling of secrets

The only secret tjek stores is the sync bearer token, in `sync.json` in the
config directory, with mode `0600`. It is never written to `sync.log`, never included in
`tjek doctor` output, and never logged. If you believe you have found a path
where it is exposed, that is in scope and worth reporting.

## Supported versions

Fixes land on `main` and go out in the next release. Only the latest release
is supported; there are no backports to older tags.
