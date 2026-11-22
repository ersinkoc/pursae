#!/usr/bin/env bash
# Upgrade-path test: a site created and filled by an older release must keep its data when the
# current checkout (HEAD) takes it over. For each release under test:
#
#   1. build that tag's binary (git archive of the tag, go build -tags nodynamic ./cmd/wpgo);
#   2. on a fresh database, run its `migrate` and `serve`, install the site through its installer and
#      seed it over HTTP the way that release supports (tools/upgradetest seed): users, terms, media,
#      posts, pages, a custom field, comments, a menu, settings, a Web Builder page; snapshot it;
#   3. stop it, record the row count of every table, run HEAD's `migrate` and fail if a table
#      vanished or lost rows;
#   4. run HEAD's `serve` and check /healthz and /readyz, the seeded users' logins with their old
#      passwords, the old Application Password, every snapshotted field, the uploaded file byte for
#      byte, the custom field, the front page and every permalink (tools/upgradetest verify).
#
# The releases: v0.2.0 and the three newest release tags, not counting a tag on HEAD itself or one
# newer than HEAD's version (internal/server/server.go). UPGRADE_TAGS="v0.2.5 v0.2.8" overrides.
#
# Environment: UPGRADE_ADMIN_DSN, a database the test connects to in order to create its own
# (default the compose database, postgres://wpgo:wpgo@localhost:5432/wpgo); UPGRADE_DB_PREFIX names
# them (default wpgo_upg, one per tag: wpgo_upg_v0_2_8); UPGRADE_PORT (default 18490);
# UPGRADE_WORK keeps the build trees, logs and snapshots (default a temporary directory, removed on
# success). Run it with `make upgrade-test`.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

admin_dsn=${UPGRADE_ADMIN_DSN:-postgres://wpgo:wpgo@localhost:${PURSAE_DB_PORT:-${WPGO_DB_PORT:-5432}}/wpgo?sslmode=disable}
db_prefix=${UPGRADE_DB_PREFIX:-wpgo_upg}
port=${UPGRADE_PORT:-18490}
site="http://127.0.0.1:$port"
work=${UPGRADE_WORK:-}
keep_work=1
if [[ -z $work ]]; then
	work=$(mktemp -d "${TMPDIR:-/tmp}/pursae-upgrade.XXXXXX")
	keep_work=0
fi
mkdir -p "$work"

log() { printf '\n== %s\n' "$*"; }

# dsn_for <db>: admin_dsn with the database name replaced.
dsn_for() {
	local base=${admin_dsn%%\?*} query=""
	[[ $admin_dsn == *\?* ]] && query="?${admin_dsn#*\?}"
	printf '%s/%s%s' "${base%/*}" "$1" "$query"
}

head_version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/server/server.go)
[[ -n $head_version ]] || { echo "upgrade-test: no Version in internal/server/server.go" >&2; exit 1; }

# ver_le a b: a <= b in version order.
ver_le() { [[ $(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1) == "$1" ]]; }

if [[ -n ${UPGRADE_TAGS:-} ]]; then
	read -r -a tags <<<"$UPGRADE_TAGS"
else
	git rev-parse -q --verify 'refs/tags/v0.2.0' >/dev/null || {
		echo "upgrade-test: no release tags; fetch them (git fetch --tags, or actions/checkout with fetch-depth: 0)" >&2
		exit 1
	}
	on_head=" $(git tag --points-at HEAD | tr '\n' ' ') "
	candidates=()
	while read -r t; do
		[[ $t =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue # releases only, no pre-release tags
		[[ $on_head == *" $t "* ]] && continue            # HEAD is that release
		ver_le "${t#v}" "$head_version" || continue       # a tag newer than this checkout
		candidates+=("$t")
	done < <(git tag --sort=-v:refname)
	tags=("${candidates[@]:0:3}")
	[[ " ${tags[*]} " == *" v0.2.0 "* ]] || tags+=(v0.2.0)
fi
head_label="HEAD ($head_version+$(git rev-parse --short HEAD))"
echo "upgrade-test: $head_label; releases: ${tags[*]}"

export CGO_ENABLED=0 GOFLAGS=${GOFLAGS:-} GOTOOLCHAIN=${GOTOOLCHAIN:-auto}
log "building HEAD and the test helper"
go build -tags nodynamic -o "$work/pursae-head" ./cmd/wpgo
go build -o "$work/upgradetest" ./tools/upgradetest
helper="$work/upgradetest"

server_pid=""
stop_server() {
	if [[ -n $server_pid ]]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
		server_pid=""
	fi
}
trap stop_server EXIT

# A step that fails outside verify (build, migrate, seed) stops the run: show where it was.
dir=""
on_error() {
	echo "upgrade-test: failed; build trees, logs and snapshots in $work" >&2
	for f in "$dir"/serve-*.log; do
		[[ -f $f ]] && { echo "--- $f (last 30 lines)" >&2; tail -30 "$f" >&2; }
	done
}
trap on_error ERR

# start_server <binary> <log>: serve in the background and wait for /healthz.
start_server() {
	if curl -fsS -o /dev/null "$site/healthz" 2>/dev/null; then
		echo "upgrade-test: something already answers on $site; set UPGRADE_PORT" >&2
		return 1
	fi
	"$1" serve >"$2" 2>&1 &
	server_pid=$!
	for _ in $(seq 1 120); do
		if curl -fsS -o /dev/null "$site/healthz" 2>/dev/null; then
			return 0
		fi
		if ! kill -0 "$server_pid" 2>/dev/null; then
			break
		fi
		sleep 0.5
	done
	echo "upgrade-test: $1 serve did not come up; its log:" >&2
	tail -50 "$2" >&2
	return 1
}

results=()
failed=0
for tag in "${tags[@]}"; do
	t0=$SECONDS
	name="${db_prefix}_$(tr -c 'a-z0-9\n' '_' <<<"$tag" | tr -d '\n')"
	dir="$work/$tag"
	rm -rf "$dir"
	mkdir -p "$dir/src" "$dir/uploads"
	log "$tag: build"
	git archive "$tag" | tar -x -C "$dir/src"
	(cd "$dir/src" && go build -tags nodynamic -o "$dir/pursae" ./cmd/wpgo)

	# The releases under test predate PURSAE_* (0.5.0) and read only the WPGO_* names. HEAD gets the same
	# environment, so the run also checks that a deployment keeping the old names still works (and that
	# HEAD names the new variables in its log, below).
	export DATABASE_URL WPGO_ADDR="127.0.0.1:$port" WPGO_SITE_URL="$site" WPGO_HUB=off \
		WPGO_UPLOADS_DIR="$dir/uploads" WPGO_GEOIP_UPDATE=off WPGO_SETUP_TOKEN="upgrade-test-setup-token-0123456789"
	DATABASE_URL=$(dsn_for "$name")
	"$helper" resetdb -admin "$admin_dsn" -db "$name"

	log "$tag: migrate, serve, install and seed with $tag"
	"$dir/pursae" migrate
	start_server "$dir/pursae" "$dir/serve-old.log"
	fixture=""
	for f in variants-hero layout behaviours; do
		if [[ -f "$dir/src/internal/webbuilder/testdata/render/$f.doc.json" && -f "$dir/src/internal/webbuilder/testdata/render/$f.render.json" ]]; then
			fixture="$dir/src/internal/webbuilder/testdata/render/$f"
			break
		fi
	done
	"$helper" seed -url "$site" -version "$tag" -token "$WPGO_SETUP_TOKEN" -wb-fixture "$fixture" -out "$dir/snapshot.json"
	stop_server

	log "$tag: HEAD migrate"
	"$helper" counts -dsn "$DATABASE_URL" -out "$dir/counts-before.json"
	"$work/pursae-head" migrate
	"$helper" counts -dsn "$DATABASE_URL" -compare "$dir/counts-before.json"

	log "$tag: HEAD serve and verify"
	start_server "$work/pursae-head" "$dir/serve-head.log"
	ok=0
	if ! grep -q 'deprecated environment variable.*new=PURSAE_SITE_URL' "$dir/serve-head.log"; then
		echo "upgrade-test: HEAD did not warn about WPGO_SITE_URL (deprecated name)" >&2
		results+=("$tag -> $head_label: FAILED (no deprecation warning)")
		failed=1
	elif "$helper" verify -url "$site" -snapshot "$dir/snapshot.json"; then
		ok=1
		results+=("$tag -> $head_label: ok ($((SECONDS - t0))s)")
	else
		results+=("$tag -> $head_label: FAILED")
		failed=1
		echo "upgrade-test: HEAD serve log (last 30 lines):" >&2
		tail -30 "$dir/serve-head.log" >&2
	fi
	stop_server
	rm -rf "$dir/src" # the build tree is large; the snapshot and logs stay
	if [[ $ok == 1 ]]; then # a failed upgrade keeps its database for a look
		"$helper" resetdb -drop -admin "$admin_dsn" -db "$name" || true
	else
		echo "upgrade-test: database $name kept" >&2
	fi
done

log "results"
printf '%s\n' "${results[@]}"
if [[ $failed == 0 && $keep_work == 0 ]]; then
	rm -rf "$work"
elif [[ $failed != 0 ]]; then
	echo "upgrade-test: logs and snapshots in $work" >&2
fi
exit $failed
