#!/usr/bin/env bash
# Builds the Pursae core for every platform and publishes it as a GitHub release, which
# Pursae Manager then imports (Core › Import): it signs the binaries' hashes and points sites
# at the GitHub downloads. See docs/developer/operations/releases.md.
#
#   tools/release.sh 0.2.0                 build, tag v0.2.0, push the tag, create the release
#   tools/release.sh 0.3.0-beta.1 --beta   the same, marked as a pre-release
#   tools/release.sh 0.2.0 --dry-run       only build and check into dist/release/v0.2.0
#
# Environment:
#   RELEASE_REPO     GitHub repository of the release (default pursae/pursae; must be public)
#   RELEASE_REMOTES  git remotes the tag is pushed to (default: public, i.e. github.com/pursae/pursae)
#   TARGETS          platforms (default: linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64)
#   NOTES_FILE       release notes (markdown); default: GitHub's generated notes
#   YES=1            do not ask before publishing
set -euo pipefail

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

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
(cd "$OUT" && shasum -a 256 pursae-* >SHA256SUMS)
echo
cat "$OUT/SHA256SUMS"
du -sh "$OUT" | awk '{print "Total: " $1}'

if [[ $DRY == 1 ]]; then
	echo
	echo "Dry run: the binaries are in $OUT; nothing was tagged or published."
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
if [[ -n "${NOTES_FILE:-}" ]]; then
	args+=(--notes-file "$NOTES_FILE")
else
	args+=(--generate-notes)
fi
gh "${args[@]}" "$OUT"/pursae-* "$OUT/SHA256SUMS"

echo
echo "Published https://github.com/$REPO/releases/tag/$TAG"
echo "Next: Pursae Manager › Core › Import, tag $TAG, channel $([[ $BETA == 1 ]] && echo beta || echo stable)."
