#!/usr/bin/env bash
# inventory.sh: read-only report of every reference to the old (Elementor-derived) web builder.
#
# Lists each match of gwe*, golementor*, gowebeditor*, elementor*, internal/builder,
# hello-web-builder and web/gowebeditor in tracked files (Go, templates, docs, tests, Makefile,
# Dockerfile, theme packages, catalogs) as file:line, grouped by the removal plan:
#
#   R0          generic helpers that live in builder files and must move first (symbol report)
#   R1a         themes                      R1b  commerce / language / security / misc
#   R1c         routing / admin / front     R1d  plugin registry / settings
#   R2          files and trees deleted whole (plus web/web.go embed entry)
#   R2-tests    tests the plan fixes in R2
#   KEEP        allowed residue (old migrations stay unchanged)
#   DOCS        docs, README, NOTICE
#   I18N        translation catalogs (stale message ids, regenerated)
#   UNASSIGNED  files the plan does not name: the coordinator picks an owner
#
# Lines that are Go/JS comments are tagged [comment]. It never writes anything.
#
# Usage: tools/webbuilder/inventory.sh [--full] [--summary] [--check]
#   --full     also list every line inside R2 trees and I18N catalogs (default: per-file counts)
#   --summary  only print the per-group counts
#   --check    exit 1 when any reference remains outside KEEP (for the end of R2)
set -euo pipefail

FULL=0 SUMMARY=0 CHECK=0
for a in "$@"; do
	case "$a" in
	--full) FULL=1 ;;
	--summary) SUMMARY=1 ;;
	--check) CHECK=1 ;;
	-h | --help) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown option: $a" >&2; exit 2 ;;
	esac
done

ROOT=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
cd "$ROOT"

# Case-sensitive on purpose: a case-insensitive "gwe" hits hgWebhook, ImgWebP and minified JS.
PATTERN='[Ee]lementor|ELEMENTOR|[Gg]olementor|GOLEMENTOR|[Gg]o[Ww]eb[Ee]ditor|GOWEBEDITOR|Gwe|GWE|(^|[^A-Za-z0-9])gwe|internal/builder|hello-web-builder'
# Vendored WordPress bundles, fonts and the base64 logo only produce noise.
EXCLUDES=(':!web/wp-includes/js' ':!web/wp-includes/fonts' ':!web/wp-includes/images' ':!tools/webbuilder')

hits() {
	git grep -n -I -E "$PATTERN" -- . "${EXCLUDES[@]}" || true
	# Calls into package builder from files that import wpgo/internal/builder.
	local importers
	importers=$(git grep -l -F '"wpgo/internal/builder"' -- '*.go' ':!internal/builder' || true)
	if [ -n "$importers" ]; then
		# shellcheck disable=SC2086
		git grep -n -I -E '(^|[^A-Za-z0-9_.])builder\.[A-Z]' -- $importers || true
	fi
}

ALL=$(hits | LC_ALL=C sort -t: -k1,1 -k2,2n -u)

classify='
function group(p, t,   b) {
	b = p; sub(/.*\//, "", b)
	# KEEP: residue that stays after R2 (each rule names why).
	# Old migrations stay unchanged; 0028 is the migration that drops the old builder data (R3).
	if (p ~ /^internal\/db\/migrations\/00(05|06|07|25|28)_/) return "KEEP"
	# Plugin identity kept by the new builder: slug "golementor", option "native_golementor".
	if (p == "internal/webbuilder/contract.go" || t ~ /native_golementor/) return "KEEP"
	# Old upload paths, old admin/editor URLs (301 table) and the one-time theme upgrade off the old builder.
	if (p ~ /^internal\/server\/(uploads_legacy|webbuilder_redirects|webbuilder_redirects_test|webbuilder_upgrade|webbuilder_upgrade_test)\.go$/) return "KEEP"
	# New-builder tests that assert old-builder traces (old actions, menu ids, asset paths) are absent.
	if (p ~ /^internal\/server\/webbuilder_(admin|editor|settings)_test\.go$/) return "KEEP"
	# Not the old builder: the Google font "Gwendolyn" and Welsh words ("gwe-": web, "gweinyddwr").
	if (t ~ /Gwendolyn/ || p == "web/languages/wpgo-cy.json") return "KEEP"
	# Demo copy of the upstream builder ("Golementor" in sample sections), its patch, fixtures and dist.
	if (p ~ /^(builder\/patches|internal\/webbuilder\/testdata|web\/web-builder)\//) return "KEEP"
	# Docs, README and NOTICE: rewritten by X2; NOTICE keeps the attribution for the old migrations.
	if (p ~ /^docs\// || p == "README.md" || p == "NOTICE") return "KEEP"
	if (p ~ /^(internal\/builder|internal\/server\/gowebeditor|web\/gowebeditor|web\/themes\/hello-web-builder|tools\/gowebeditor|tools\/goo-golementor-themes|internal\/server\/testdata\/golementor[^\/]*)\//) return "R2"
	if (p ~ /^internal\/server\/[^\/]*$/ && b ~ /^(gowebeditor|gwe_|golementor_|goo_gwe|theme_hellogowebeditor|i18n_golementor)/) return "R2"
	if (p ~ /^web\/admin\/templates\/pages\/(golementor|gowebeditor)-/) return "R2"
	if (p == "web/web.go" || p == "docs/golementor-studio.md" || p == "docs/developer/themes/golementor.md") return "R2"
	if (p ~ /^internal\/server\/(admin_bar_front|brand|plugins_bulk|security_hardening|server|gocdn_more|goo_ext_memberships)_test\.go$/) return "R2-tests"
	if (p ~ /^internal\/server\/[^\/]*$/) { sub(/_test\.go$/, ".go", b) }
	if (p ~ /^internal\/server\/[^\/]*$/ && b ~ /^(theme[^\/]*|customize_colors)\.go$/) return "R1a"
	if (p ~ /^(library\/themes|web\/themes)\// || p == "tools/build-themes.sh" || p == "web/admin/templates/pages/themes.html") return "R1a"
	if (p ~ /^internal\/server\/[^\/]*$/ && b ~ /^(adminsec[^\/]*|goo_[^\/]*|gooike_[^\/]*|golylang[^\/]*|acfgo[^\/]*|rest_[^\/]*|admin_misc|admin_tools2)\.go$/) return "R1b"
	if (p ~ /^internal\/i18n\// || p ~ /^web\/goocommerce\// || p ~ /^web\/admin\/templates\/pages\/(goo-|misc\.html)/) return "R1b"
	if (p ~ /^internal\/server\/[^\/]*$/ && b ~ /^(server|admin|admin_posts|rest|wpgocache[^\/]*|site[^\/]*)\.go$/) return "R1c"
	if (p == "web/admin/templates/pages/posts-list.html") return "R1c"
	if (p ~ /^internal\/server\/[^\/]*$/ && b ~ /^(plugins[^\/]*|settings)\.go$/) return "R1d"
	if (p ~ /^internal\/settings\//) return "R1d"
	if (p ~ /^docs\// || p == "README.md" || p == "NOTICE") return "DOCS"
	if (p ~ /^web\/languages\//) return "I18N"
	return "UNASSIGNED"
}
'

report_r0() {
	echo "## R0 generic helpers (move before detaching)"
	echo "Callers outside the defining file. These must keep compiling when the builder files go."
	local spec def syms s
	# spec: defining-file|symbol...
	for spec in \
		'internal/server/gwe_embed_csp.go|ctxCSPAllow cspAllow cspHas cspMerge cspWriter gweEmbedNeeds gweEmbedScan gweEmbedWalk gweEmbedCSP' \
		'internal/server/gowebeditor_legacy.go|gweLegacyUploadPath pluginURLSlug pluginSlugFromURL' \
		'internal/server/i18n_golementor.go|gweJSLiteral gweJSHasWord gweJSNotText' \
		'internal/server/gowebeditor.go|gweJSON' \
		'internal/server/gowebeditor_theme_custom.go|gweAbsURL' \
		'internal/server/mailsmtp.go|msSeal msReveal' \
		'internal/server/golementor_plugin.go|themeVisible installedThemes' \
		'internal/server/gowebeditor_fonts.go internal/server/gowebeditor_fonts_list.go|@fonts'; do
		def=${spec%%|*}
		syms=${spec#*|}
		if [ ! -e "${def%% *}" ]; then
			echo
			echo "### $def: deleted (R2)"
			continue
		fi
		if [ "$syms" = "@fonts" ]; then
			# shellcheck disable=SC2086
			syms=$(git grep -h -o -E '^(func (\([^)]*\) )?|var |const |type |	)gwe[A-Za-z0-9_]*' -- $def 2>/dev/null |
				sed -E 's/.*(gwe[A-Za-z0-9_]*)$/\1/' | LC_ALL=C sort -u | tr '\n' ' ')
		fi
		echo
		echo "### $def"
		for s in $syms; do
			local ex=() f
			for f in $def; do ex+=(":!$f"); done
			local uses
			uses=$(git grep -n -w -E "$s" -- '*.go' "${ex[@]}" || true)
			if [ -z "$uses" ]; then
				echo "- $s: no callers outside $def"
				continue
			fi
			echo "- $s:"
			printf '%s\n' "$uses" | awk "$classify"'
				{ i = index($0, ":"); p = substr($0, 1, i-1); r = substr($0, i+1); j = index(r, ":")
				  t = substr(r, j+1); gsub(/^[ \t]+/, "", t); if (length(t) > 140) t = substr(t, 1, 140) "..."
				  printf "    %s:%s [%s] %s\n", p, substr(r, 1, j-1), group(p, t), t }'
		done
	done
	echo
	echo "### internal/server/plugins.go positional rows (become lookups by slug)"
	git grep -n -E 'rows\[[0-9]+\]' -- internal/server/plugins.go | sed 's/^/    /' || true
	echo
}

total=$(printf '%s\n' "$ALL" | grep -c . || true)

printf '%s\n' "$ALL" | awk -v full="$FULL" -v summary="$SUMMARY" -v total="$total" -v root="$(git rev-parse --abbrev-ref HEAD)" -v head="$(git rev-parse --short HEAD)" "$classify"'
BEGIN { n = split("R1a R1b R1c R1d R2 R2-tests KEEP DOCS I18N UNASSIGNED", order, " ") }
NF == 0 { next }
{
	i = index($0, ":"); p = substr($0, 1, i-1); r = substr($0, i+1); j = index(r, ":")
	ln = substr(r, 1, j-1); t = substr(r, j+1)
	g = group(p, t)
	cnt[g]++
	if (!((g, p) in fc)) { files[g] = files[g] SUBSEP p; nf[g]++ }
	fc[g, p]++
	tag = (t ~ /^[ \t]*(\/\/|\/\*|\*|#)/) ? "[comment] " : ""
	gsub(/^[ \t]+/, "", t)
	if (length(t) > 160) t = substr(t, 1, 160) "..."
	lines[g] = lines[g] "\n  " p ":" ln " " tag t
}
END {
	printf "# Old web builder inventory (%s @ %s)\n\n", root, head
	printf "Total references: %d\n\n", total
	printf "| Group | Files | Lines |\n|---|---|---|\n"
	for (k = 1; k <= n; k++) { g = order[k]; printf "| %s | %d | %d |\n", g, nf[g], cnt[g] }
	printf "\n"
	if (summary) exit
	for (k = 1; k <= n; k++) {
		g = order[k]
		if (!cnt[g]) continue
		printf "## %s (%d files, %d lines)\n", g, nf[g], cnt[g]
		if ((g == "R2" || g == "I18N") && !full) {
			m = split(substr(files[g], 2), fl, SUBSEP)
			# collapse per top directory for big trees
			for (x = 1; x <= m; x++) {
				q = fl[x]
				if (q ~ /^(web\/gowebeditor|internal\/builder|internal\/server\/gowebeditor|web\/languages|tools\/gowebeditor|tools\/goo-golementor-themes|web\/themes\/hello-web-builder)\//) {
					match(q, /^(web\/gowebeditor|internal\/builder|internal\/server\/gowebeditor|web\/languages|tools\/gowebeditor|tools\/goo-golementor-themes|web\/themes\/hello-web-builder)\//)
					d = substr(q, 1, RLENGTH)
					if (!(d in tf)) { tord[++nt] = d }
					tf[d]++; tl[d] += fc[g, q]
				} else {
					printf "  %s (%d lines)\n", q, fc[g, q]
				}
			}
			for (x = 1; x <= nt; x++) { d = tord[x]; if (tf[d]) printf "  %s* (%d files, %d lines; --full lists them)\n", d, tf[d], tl[d]; delete tf[d] }
			nt = 0
		} else {
			printf "%s\n", substr(lines[g], 2)
		}
		printf "\n"
	}
}'

if [ "$SUMMARY" = 0 ]; then
	report_r0
	echo "Not in this repo: ../wpgo-public-site (docs site) and the root CLAUDE.md \"Page builder\" decision."
fi

if [ "$CHECK" = 1 ]; then
	left=$(printf '%s\n' "$ALL" | awk "$classify"'NF { i = index($0, ":"); p = substr($0, 1, i-1); r = substr($0, i+1); j = index(r, ":"); if (group(p, substr(r, j+1)) != "KEEP") c++ } END { print c+0 }')
	if [ "$left" -gt 0 ]; then
		echo "check: $left references remain outside KEEP" >&2
		exit 1
	fi
	echo "check: clean (only KEEP residue)" >&2
fi
