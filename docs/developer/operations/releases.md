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

## 1. Prepare: CHANGELOG and CI

Every pull request that changes behavior adds a line under `## [Unreleased]` in `CHANGELOG.md`
(Keep a Changelog format). To prepare a release, open a pull request that moves those lines under a
new heading for the version and merge it once CI is green:

```markdown
## [Unreleased]

## [0.3.0] - 2026-11-21

### Added
- ...
```

That section, without the heading, becomes the GitHub release notes. Then check out `main`, pull,
and release that commit.

## 2. Build and publish

On a clean checkout of the commit you want to ship:

```sh
make release VERSION=0.2.0             # stable
make release VERSION=0.3.0-beta.1 BETA=1   # beta channel (a GitHub pre-release)
make release VERSION=0.2.0 DRY_RUN=1   # only build and check, into dist/release/v0.2.0
```

`tools/release.sh` (what `make release` runs):

1. refuses uncommitted changes or untracked files (they would be compiled or embedded), or a tag `v0.2.0` that points to another commit;
2. runs the release gate (below) and refuses a release that fails it;
3. builds `linux-amd64`, `linux-arm64`, `darwin-arm64`, `darwin-amd64` and `windows-amd64` with
   `CGO_ENABLED=0 go build -trimpath -buildmode=exe -tags nodynamic -ldflags "-s -w -X wpgo/internal/server.Version=0.2.0"`,
   checks that the Linux binaries are static and that the binary for your machine reports the version;
4. packs each binary into an archive for manual installs, `pursae-<os>-<arch>.tar.gz` (`.zip` on
   Windows), with the binary as `pursae` (`pursae.exe`), `LICENSE`, `NOTICE`, `LICENSES/` and a short
   `README.txt`, and writes `SHA256SUMS` over the binaries and the archives;
5. asks before publishing, then sets `Version` in `internal/server/server.go` to `0.2.0` in a
   "Pursae 0.2.0" commit (so a build straight from the repository, e.g. `docker compose`, reports
   the release too), tags that commit `v0.2.0`, pushes both to the `public` remote (github.com/pursae/pursae)
   and creates the GitHub release in `pursae/pursae` with the binaries (`pursae-<os>-<arch>`,
   `.exe` on Windows), the archives and `SHA256SUMS`. Pursae Manager imports only the bare binaries
   (assets named exactly `pursae-<os>-<arch>[.exe]`); the archives are for people
   [installing by hand](../getting-started/install-binary.md#install-a-release).

It needs Go, git and the GitHub CLI (`gh auth login`). Settings: `RELEASE_REPO` (default
`pursae/pursae`; it must be public, sites download without credentials; the CI runs are looked up
there too), `RELEASE_REMOTES`, `TARGETS`, `NOTES_FILE` (release notes; default the version's
CHANGELOG section), `RELEASE_SKIP_CI_CHECK=1` (below) and `YES=1` (no question). The release
binaries get their version from `-ldflags`; the source keeps the latest release's version for
builds made from the repository.

### The release gate

Before building, `tools/release.sh` refuses to publish unless:

- **CI passed on the commit.** `gh` must find a successful run of the `CI` workflow
  (`.github/workflows/ci.yml`) on github.com/pursae/pursae for HEAD, with every job of its latest
  attempt green. Push the commit (normally: merge to `main`) and wait for the run; rerun failed jobs
  if they were flaky. The "Pursae 0.2.0" commit the script adds only sets the version string, so
  the run on the commit before it is the one checked; if a release stopped after that commit was
  made, HEAD is that commit and the run on its parent counts.
- **It comes from `main`.** A stable release from any other branch, or a detached HEAD, is refused.
  A pre-release (`--beta`) may come from another branch, such as a release branch.
- **CHANGELOG.md has the version.** Without `NOTES_FILE`, a stable release needs a non-empty
  `## [0.2.0]` section (a `- date` after the heading is fine). A beta without one gets GitHub's
  generated notes.

`--dry-run` builds anyway and only reports what a real release would refuse.

**Escape hatch.** `RELEASE_SKIP_CI_CHECK=1 make release VERSION=0.2.1` skips the CI check (only that
one), with a loud warning before building and again before publishing. Use it only when shipping
cannot wait for CI, for example a security fix while GitHub Actions is down, and run the tests
locally (`make test`) first.

## 3. Import it in Pursae Manager

**Core › Import**: tag `v0.2.0`, channel (stable, or beta for sites with a beta license), notes
(empty: the GitHub release notes), *Import Release*. The Manager:

- reads the release from the GitHub API and takes every `pursae-<os>-<arch>` asset (not the archives);
- downloads each once, checks it against `SHA256SUMS` and that Linux binaries are static;
- signs `pursae-core-v1\n{version}\n{os}-{arch}\n{sha256}` with its active, endorsed key;
- stores the hash, the signature and the GitHub download URL, and publishes the release.

The log is on the import's page. A platform without a binary is not offered the release.

## 4. What sites do

At their next update check (daily, or on **Dashboard › Updates**) sites see the version, and
every admin screen shows the update notice. **Update to version 0.2.0** asks the Manager for the
platform's binary, follows the redirect to GitHub, checks the SHA-256 and the signature against a
key endorsed by the release root key built into Pursae, replaces the executable and restarts
([upgrading](../getting-started/upgrading.md)).

A file replaced on GitHub after the import fails that check, so sites refuse it. Never replace
the assets of an imported release; publish a new patch version instead (and consider turning on
GitHub's immutable releases for the repository).
