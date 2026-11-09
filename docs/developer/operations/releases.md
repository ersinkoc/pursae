---
title: Publishing a release
description: Build the core for every platform, publish it on GitHub and let Pursae Manager sign it so sites update with one click.
weight: 95
section: operations
---

A Pursae release is a set of static binaries, one per platform, built on the release machine and
published as a GitHub release. Pursae Manager imports that release: it signs each binary's hash
with its endorsed key, and every site sees the version under **Dashboard › Updates**. The
Manager never compiles and keeps no copy of the files; sites download them from GitHub.

## 1. Build and publish

On a clean checkout of the commit you want to ship:

```sh
make release VERSION=0.2.0             # stable
make release VERSION=0.3.0-beta.1 BETA=1   # beta channel (a GitHub pre-release)
make release VERSION=0.2.0 DRY_RUN=1   # only build and check, into dist/release/v0.2.0
```

`tools/release.sh` (what `make release` runs):

1. refuses uncommitted changes or untracked files (they would be compiled or embedded), or a tag `v0.2.0` that points to another commit;
2. builds `linux-amd64`, `linux-arm64`, `darwin-arm64`, `darwin-amd64` and `windows-amd64` with
   `CGO_ENABLED=0 go build -trimpath -buildmode=exe -tags nodynamic -ldflags "-s -w -X wpgo/internal/server.Version=0.2.0"`,
   checks that the Linux binaries are static and that the binary for your machine reports the version;
3. writes `SHA256SUMS`;
4. asks before publishing, then sets `Version` in `internal/server/server.go` to `0.2.0` in a
   "Pursae 0.2.0" commit (so a build straight from the repository, e.g. `docker compose`, reports
   the release too), tags that commit `v0.2.0`, pushes both to the `public` remote (github.com/pursae/pursae)
   and creates the GitHub release in `pursae/pursae` with the binaries (`pursae-<os>-<arch>`,
   `.exe` on Windows) and `SHA256SUMS`.

It needs Go, git and the GitHub CLI (`gh auth login`). Settings: `RELEASE_REPO` (default
`pursae/pursae`; it must be public, sites download without credentials), `RELEASE_REMOTES`,
`TARGETS`, `NOTES_FILE` (release notes; default GitHub's generated notes) and `YES=1` (no
question). The release binaries get their version from `-ldflags`; the source keeps the latest release's version
for builds made from the repository.

## 2. Import it in Pursae Manager

**Core › Import**: tag `v0.2.0`, channel (stable, or beta for sites with a beta license), notes
(empty: the GitHub release notes), *Import Release*. The Manager:

- reads the release from the GitHub API and takes every `pursae-<os>-<arch>` asset;
- downloads each once, checks it against `SHA256SUMS` and that Linux binaries are static;
- signs `pursae-core-v1\n{version}\n{os}-{arch}\n{sha256}` with its active, endorsed key;
- stores the hash, the signature and the GitHub download URL, and publishes the release.

The log is on the import's page. A platform without a binary is not offered the release.

## 3. What sites do

At their next update check (daily, or on **Dashboard › Updates**) sites see the version, and
every admin screen shows the update notice. **Update to version 0.2.0** asks the Manager for the
platform's binary, follows the redirect to GitHub, checks the SHA-256 and the signature against a
key endorsed by the release root key built into Pursae, replaces the executable and restarts
([upgrading](../getting-started/upgrading.md)).

A file replaced on GitHub after the import fails that check, so sites refuse it. Never replace
the assets of an imported release; publish a new patch version instead (and consider turning on
GitHub's immutable releases for the repository).
