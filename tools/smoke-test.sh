#!/usr/bin/env bash
# Smoke test of a built Pursae binary (CI jobs smoke-linux and smoke-macos; the Windows job does the
# same in PowerShell): version, migrate, serve, healthcheck, the installer locked without the setup
# token and open with the link `setup-token` prints, an install through that form, and the front page.
#
#   tools/smoke-test.sh ./pursae
#
# Environment: DATABASE_URL (required; an empty database or one this test may install into),
# SMOKE_PORT (default 18480). The uploads and other data go to a temporary directory.
set -euo pipefail

bin=${1:?usage: tools/smoke-test.sh <binary>}
[[ -x $bin ]] || { echo "smoke: $bin is not an executable" >&2; exit 1; }
: "${DATABASE_URL:?smoke: set DATABASE_URL}"
port=${SMOKE_PORT:-18480}
site="http://127.0.0.1:$port"
work=$(mktemp -d "${TMPDIR:-/tmp}/pursae-smoke.XXXXXX")
export PURSAE_ADDR="127.0.0.1:$port" PURSAE_SITE_URL="$site" PURSAE_HUB=off PURSAE_UPLOADS_DIR="$work/data/uploads"

pid=""
cleanup() {
	[[ -n $pid ]] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT
fail() {
	echo "smoke: FAILED: $*" >&2
	[[ -f $work/serve.log ]] && { echo "--- serve log (last 40 lines)" >&2; tail -40 "$work/serve.log" >&2; }
	exit 1
}
step() { printf '== %s\n' "$*"; }

step version
"$bin" version || fail "version"

step migrate
"$bin" migrate >"$work/migrate.log" 2>&1 || { cat "$work/migrate.log" >&2; fail "migrate"; }

step serve
"$bin" serve >"$work/serve.log" 2>&1 &
pid=$!
ok=0
for _ in $(seq 1 60); do
	if "$bin" healthcheck >/dev/null 2>&1; then ok=1; break; fi
	kill -0 "$pid" 2>/dev/null || fail "serve exited"
	sleep 1
done
[[ $ok == 1 ]] || fail "healthcheck did not pass within 60 s"
code=$(curl -s -o /dev/null -w '%{http_code}' "$site/healthz")
[[ $code == 200 ]] || fail "/healthz: $code"

step "installer without token"
code=$(curl -s -o /dev/null -w '%{http_code}' "$site/wp-admin/install")
[[ $code == 403 ]] || fail "/wp-admin/install without the setup token: $code, want 403"

step "installer with the setup-token link"
link=$("$bin" setup-token | head -1)
[[ $link == *"/wp-admin/install?token="* ]] || fail "setup-token printed: $link"
jar="$work/cookies.txt"
body=$(curl -s -L -c "$jar" -b "$jar" "$link")
grep -q 'name="language"' <<<"$body" || fail "the install link did not open the installer"

step install
token=${link#*token=}
code=$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -b "$jar" \
	--data-urlencode "weblog_title=Smoke Test" --data-urlencode "user_name=admin" \
	--data-urlencode "admin_password=smoke-test-password-1" --data-urlencode "admin_password2=smoke-test-password-1" \
	--data-urlencode "pw_weak=on" --data-urlencode "admin_email=admin@example.com" \
	--data-urlencode "blog_public=0" --data-urlencode "language=en_US" --data-urlencode "token=$token" \
	"$site/wp-admin/install?token=$token")
[[ $code == 200 || $code == 302 || $code == 303 ]] || fail "install form: $code"
loc=$(curl -s -o /dev/null -w '%{redirect_url}' "$site/wp-admin/install")
[[ $loc == *wp-login* ]] || fail "the site is not installed (installer redirects to '$loc')"

step "front page"
code=$(curl -s -o "$work/front.html" -w '%{http_code}' "$site/")
[[ $code == 200 ]] || fail "front page: $code"
grep -q 'Smoke Test' "$work/front.html" || fail "front page does not show the site title"

echo "smoke: OK ($("$bin" version))"
