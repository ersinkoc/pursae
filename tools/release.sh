#!/usr/bin/env bash
# Builds the Pursae core for every platform and publishes it as a GitHub release, which
# Pursae Manager then imports (Core › Import): it signs the binaries' hashes and points sites
# at the GitHub downloads. Each binary also ships in an archive with the license files
# (pursae-<os>-<arch>.tar.gz, .zip on Windows). See docs/developer/operations/releases.md.
#
#   tools/release.sh 0.2.0                 build, tag v0.2.0, push the tag, create the release
#   tools/release.sh 0.3.0-beta.1 --beta   the same, marked as a pre-release
#   tools/release.sh 0.2.0 --dry-run       only build and check into dist/release/v0.2.0
#
# Before building, a release is refused unless:
#   - the commit has a successful run of the CI workflow on GitHub, every job green;
#   - it is made from main (a --beta may come from another branch, e.g. a release branch);
#   - CHANGELOG.md has a non-empty "## [x.y.z]" section, which becomes the release notes
#     (a --beta without one falls back to GitHub's generated notes).
# A dry run only reports these checks.
#
# Environment:
#   RELEASE_REPO     GitHub repository of the release and of the CI runs (default pursae/pursae; must be public)
#   RELEASE_REMOTES  git remotes the tag is pushed to (default: public, i.e. github.com/pursae/pursae)
#   TARGETS          platforms (default: linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64)
#   NOTES_FILE       release notes (markdown); default: the version's CHANGELOG.md section
#   RELEASE_SKIP_CI_CHECK=1  publish without a green CI run (emergencies only; prints a warning)
#   YES=1            do not ask before publishing
set -euo pipefail

usage() { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

VERSION="${1:-}"
[[ -n "$VERSION" && "$VERSION" != -* ]] || usage
shift
BETA=0 DRY=0
for a in "$@"; do
	case "$a" in
	--beta) BETA=1 ;;
	--dry-run) DRY=1 ;;
	*) usage ;;
	esac
done

VERSION="${VERSION#v}"
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	echo "release: the version must look like 0.2.0 or 0.3.0-beta.1, not $VERSION" >&2
	exit 1
fi
if [[ "$VERSION" == *-* && $BETA == 0 ]]; then
	echo "release: $VERSION is a pre-release version; add --beta" >&2
	exit 1
fi
TAG="v$VERSION"
REPO="${RELEASE_REPO:-pursae/pursae}"
TARGETS="${TARGETS:-linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64}"

cd "$(git rev-parse --show-toplevel)"
[[ -d cmd/wpgo ]] || { echo "release: run this inside the Pursae repository" >&2; exit 1; }

# The release is exactly a commit: no local changes, and the tag (if it exists) is HEAD.
# Untracked files count: go build compiles stray .go files and //go:embed takes every file in web/,
# migrations/ and docs/, so they would end up in a binary the tag does not describe.
if [[ -n "$(git status --porcelain)" ]]; then
	if [[ $DRY == 0 ]]; then
		echo "release: commit, stash or remove your changes and untracked files first (git status)" >&2
		exit 1
	fi
	echo "release: note: uncommitted changes are included in this dry run."
fi
HEAD="$(git rev-parse HEAD)"
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
	if [[ "$(git rev-parse "$TAG^{commit}")" != "$HEAD" ]]; then
		echo "release: tag $TAG exists and points to another commit than HEAD" >&2
		exit 1
	fi
fi
src_version="$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/server/server.go)"
# The version in the source is what a build from the repository (docker compose, make build) reports;
# publishing sets it to this release in a "Pursae x.y.z" commit, which the tag then points to.
bump=0
if [[ "$src_version" != "$VERSION" ]] && ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
	bump=1
fi
branch="$(git symbolic-ref --quiet --short HEAD || true)"

# refuse MESSAGE: stops a release, but only reports in a dry run, which publishes nothing.
refuse() {
	if [[ $DRY == 1 ]]; then
		echo "release: note: $1 (a real release would be refused)"
	else
		echo "release: $1" >&2
		exit 1
	fi
}

# Stable releases are cut from main; a pre-release may come from a release branch.
if [[ $BETA == 0 && "$branch" != main ]]; then
	refuse "a stable release is made from main, not ${branch:-a detached HEAD}; check out main, or add --beta for a pre-release"
fi

# Release notes: NOTES_FILE, or this version's section of CHANGELOG.md.
notes_file="${NOTES_FILE:-}"
if [[ -n "$notes_file" ]]; then
	[[ -r "$notes_file" ]] || { echo "release: NOTES_FILE $notes_file cannot be read" >&2; exit 1; }
else
	notes="$(tools/changelog-notes.sh "$VERSION" CHANGELOG.md)"
	# Only "### Added"-style headings and blank lines is empty too.
	if grep -qvE '^(#.*|[[:space:]]*)$' <<<"$notes"; then
		notes_file="$(mktemp "${TMPDIR:-/tmp}/pursae-release-notes.XXXXXX")"
		trap 'rm -f "$notes_file"' EXIT
		printf '%s\n' "$notes" >"$notes_file"
		echo "Release notes: CHANGELOG.md [$VERSION] ($(wc -l <"$notes_file" | tr -d ' ') lines)"
	elif [[ $BETA == 1 ]]; then
		echo "release: note: CHANGELOG.md has no [$VERSION] section; the release gets GitHub's generated notes"
	else
		refuse "CHANGELOG.md has no \"## [$VERSION]\" section, or it is empty; move the Unreleased entries under \"## [$VERSION] - $(date +%F)\" on main first (or set NOTES_FILE)"
	fi
fi

# ci_run SHA: prints the URL of a successful CI run on SHA whose jobs (latest attempt) all
# succeeded; fails when there is none.
ci_run() {
	local id url counts
	while read -r id url; do
		[[ -n "$id" ]] || continue
		counts="$(gh api "repos/$REPO/actions/runs/$id/jobs?per_page=100" \
			--jq '([.jobs[] | select(.conclusion != "success")] | length), (.jobs | length)' | tr '\n' ' ')" || continue
		# "0 N": no job that did not succeed, out of N > 0 jobs.
		if [[ "$counts" == "0 "* && "$counts" != "0 0 " ]]; then
			echo "$url"
			return 0
		fi
	done < <(gh api "repos/$REPO/actions/workflows/ci.yml/runs?head_sha=$1&status=success&per_page=20" \
		--jq '.workflow_runs[] | "\(.id) \(.html_url)"')
	return 1
}

# CI gate: the commit being released must be green on GitHub. When HEAD is a "Pursae x.y.z" commit
# that only sets the version (a release that stopped after the bump), the run on its parent counts.
# The bump commit this script makes below changes only that line, so CI's run on HEAD covers it.
if [[ "${RELEASE_SKIP_CI_CHECK:-}" == 1 ]]; then
	echo
	echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
	echo "!! RELEASE_SKIP_CI_CHECK=1: NOT checking that CI passed on $(git rev-parse --short=12 HEAD)."
	echo "!! This release may ship a build that fails its tests. Use it only in an emergency."
	echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
	echo
elif ! command -v gh >/dev/null; then
	refuse "install the GitHub CLI (gh) and run gh auth login; it checks that CI passed on the release commit"
else
	ci_sha="$HEAD"
	ci_url="$(ci_run "$ci_sha" || true)"
	if [[ -z "$ci_url" && "$(git log -1 --format=%s HEAD)" == "Pursae $VERSION" ]] &&
		[[ "$(git diff --name-only HEAD^ HEAD)" == internal/server/server.go ]]; then
		ci_sha="$(git rev-parse HEAD^)"
		ci_url="$(ci_run "$ci_sha" || true)"
	fi
	if [[ -n "$ci_url" ]]; then
		echo "CI passed on $(git rev-parse --short=12 "$ci_sha"): $ci_url"
	else
		refuse "no successful CI run with every job green for $(git rev-parse --short=12 HEAD) on github.com/$REPO; push the commit and wait for CI (https://github.com/$REPO/actions/workflows/ci.yml), or rerun the failed jobs. RELEASE_SKIP_CI_CHECK=1 skips this check, for emergencies only"
	fi
fi

OUT="dist/release/$TAG"
rm -rf "$OUT"
mkdir -p "$OUT"
host="$(go env GOOS)-$(go env GOARCH)"
echo "Building Pursae $VERSION ($(git rev-parse --short=12 HEAD)) for: $TARGETS"
for t in $TARGETS; do
	os="${t%-*}" arch="${t#*-}"
	name="pursae-$t"
	[[ "$os" == windows ]] && name="$name.exe"
	echo "== $name"
	# The same flags Pursae Manager's Rebuild used: a static binary without an ELF interpreter
	# (-buildmode=exe; -tags nodynamic keeps the AVIF decoder off purego), so it starts on any Linux.
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildmode=exe -tags nodynamic \
		-ldflags "-s -w -X wpgo/internal/server.Version=$VERSION" -o "$OUT/$name" ./cmd/wpgo
	if [[ "$os" == linux ]] && command -v file >/dev/null; then
		if file "$OUT/$name" | grep -q 'dynamically linked\|interpreter'; then
			echo "release: $name is dynamically linked; it would not start on every Linux" >&2
			exit 1
		fi
	fi
	if [[ "$t" == "$host" ]]; then
		got="$("$OUT/$name" version)"
		if [[ "$got" != *"$VERSION"* ]]; then
			echo "release: the built binary reports \"$got\", not $VERSION" >&2
			exit 1
		fi
	fi
done

# Next to each bare binary (what Pursae Manager imports and sites update from; it only takes assets
# named exactly pursae-<os>-<arch>[.exe]), an archive for people installing by hand: the binary as
# pursae(.exe), LICENSE, NOTICE, LICENSES/ and a short README. pursae-<os>-<arch>.tar.gz, .zip on
# Windows. Owner, group and extra attributes are left out so the archives say nothing of this machine.
root_dir="$PWD"
stage="$(mktemp -d "${TMPDIR:-/tmp}/pursae-release-stage.XXXXXX")"
cleanup() {
	rm -rf "$stage"
	if [[ -n "${notes_file:-}" && -z "${NOTES_FILE:-}" ]]; then rm -f "$notes_file"; fi
}
trap cleanup EXIT
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
	tar_owner=(--owner=0 --group=0 --numeric-owner)
else
	# bsdtar (macOS): also leave out extended attributes, ACLs and Mac metadata, which it would
	# otherwise store as LIBARCHIVE.xattr.* / SCHILY.xattr.* pax headers (GNU tar warns on them).
	tar_owner=(--uid 0 --gid 0 --uname root --gname root --no-xattrs --no-acls --no-mac-metadata)
fi
for t in $TARGETS; do
	os="${t%-*}"
	src="pursae-$t" exe=pursae
	[[ "$os" == windows ]] && src="$src.exe" exe=pursae.exe
	dir="$stage/pursae-$t"
	mkdir -p "$dir"
	cp "$OUT/$src" "$dir/$exe"
	cp LICENSE NOTICE "$dir/"
	cp -R LICENSES "$dir/LICENSES"
	cat >"$dir/README.txt" <<EOF
Pursae $VERSION ($t)
https://github.com/$REPO

Pursae is a WordPress-compatible CMS in one binary, backed by PostgreSQL 16 or 17.

  $exe version
      print the version
  $exe serve
      run the database migrations, then serve the site (settings from the environment:
      DATABASE_URL, PURSAE_SITE_URL, PURSAE_UPLOADS_DIR, ...; see the configuration reference)
  $exe setup-token
      print the install link of a site that is not installed yet

Install guide, systemd unit and configuration reference:
https://github.com/$REPO/blob/$TAG/docs/developer/getting-started/install-binary.md

Pursae is licensed under the GNU General Public License, version 2 or (at your option) any
later version (LICENSE). The third-party components it bundles and their licenses are listed in
NOTICE; the license texts are in LICENSES/.
EOF
	chmod 0755 "$dir/$exe"
	if [[ "$os" == windows ]]; then
		command -v zip >/dev/null || { echo "release: zip is needed for the Windows archive" >&2; exit 1; }
		(cd "$stage" && zip -qrX "$root_dir/$OUT/pursae-$t.zip" "pursae-$t")
	else
		(cd "$stage" && COPYFILE_DISABLE=1 tar "${tar_owner[@]}" -czf "$root_dir/$OUT/pursae-$t.tar.gz" "pursae-$t")
	fi
	echo "== pursae-$t.$([[ "$os" == windows ]] && echo zip || echo tar.gz)"
done
(cd "$OUT" && shasum -a 256 pursae-* >SHA256SUMS)
echo
cat "$OUT/SHA256SUMS"
du -sh "$OUT" | awk '{print "Total: " $1}'

if [[ $DRY == 1 ]]; then
	echo
	echo "Dry run: the binaries and archives are in $OUT; nothing was tagged or published."
	exit 0
fi

command -v gh >/dev/null || { echo "release: install the GitHub CLI (gh) and run gh auth login" >&2; exit 1; }
if gh release view "$TAG" -R "$REPO" >/dev/null 2>&1; then
	echo "release: $REPO already has a release $TAG; delete it on GitHub first if you mean to replace it" >&2
	exit 1
fi
remotes="${RELEASE_REMOTES:-public}"
push_to=()
for r in $remotes; do
	git remote get-url "$r" >/dev/null 2>&1 && push_to+=("$r")
done
[[ ${#push_to[@]} -gt 0 ]] || { echo "release: none of the remotes \"$remotes\" exists" >&2; exit 1; }

echo
echo "About to publish Pursae $VERSION:"
if [[ $bump == 1 ]]; then
	echo "  commit \"Pursae $VERSION\" on ${branch:-HEAD} setting internal/server/server.go from $src_version to $VERSION"
	echo "  tag $TAG on that commit; tag${branch:+ and $branch} pushed to: ${push_to[*]}"
else
	echo "  tag $TAG on $(git rev-parse --short=12 HEAD), pushed to: ${push_to[*]}"
fi
echo "  GitHub release $REPO $TAG$([[ $BETA == 1 ]] && echo ' (pre-release)') with $(ls "$OUT" | wc -l | tr -d ' ') files"
echo "  notes: ${NOTES_FILE:-$([[ -n "$notes_file" ]] && echo "CHANGELOG.md [$VERSION]" || echo "GitHub's generated notes")}"
[[ "${RELEASE_SKIP_CI_CHECK:-}" == 1 ]] && echo "  WARNING: the CI check was skipped (RELEASE_SKIP_CI_CHECK=1)"
if [[ "${YES:-}" != 1 ]]; then
	ok=""
	read -r -p "Publish? [y/N] " ok || true
	[[ "$ok" == y || "$ok" == Y ]] || { echo "Cancelled; the binaries stay in $OUT."; exit 1; }
fi

if [[ $bump == 1 ]]; then
	sed -i.bak "s/^var Version = \".*\"$/var Version = \"$VERSION\"/" internal/server/server.go && rm -f internal/server/server.go.bak
	git commit -q -m "Pursae $VERSION" -- internal/server/server.go
fi
git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || git tag -a "$TAG" -m "Pursae $VERSION"
for r in "${push_to[@]}"; do
	if [[ $bump == 1 && -n "$branch" ]]; then
		git push "$r" "refs/heads/$branch" "refs/tags/$TAG"
	else
		git push "$r" "refs/tags/$TAG"
	fi
done

args=(release create "$TAG" -R "$REPO" --verify-tag --title "Pursae $VERSION")
[[ $BETA == 1 ]] && args+=(--prerelease) || args+=(--latest)
if [[ -n "$notes_file" ]]; then
	args+=(--notes-file "$notes_file")
else
	args+=(--generate-notes)
fi
gh "${args[@]}" "$OUT"/pursae-* "$OUT/SHA256SUMS"

echo
echo "Published https://github.com/$REPO/releases/tag/$TAG"
echo "Next: Pursae Manager › Core › Import, tag $TAG, channel $([[ $BETA == 1 ]] && echo beta || echo stable)."
