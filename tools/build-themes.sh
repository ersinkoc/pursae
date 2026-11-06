#!/usr/bin/env bash
# Zips theme packages (docs/theme-package.md):
#
#   examples/themes/dist/<slug>.zip
#
# A package is theme.json, templates/ and assets/ (plus readme.md and, for the theme library,
# demo/, acf-json/ and blocks/) at the zip root. Nothing is compiled or signed: themes carry no executable
# code. The SHA-256 of each package is printed so it can be published next to it.
#
# Without arguments the examples are zipped; give library/themes/<slug> to zip a library theme. The
# library is published to Pursae Manager with tools/publish-themes.sh (make publish-themes), which
# zips the directories itself.
#
# Usage: tools/build-themes.sh [theme-dir ...]
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

dist="examples/themes/dist"
mkdir -p "$dist"

if [[ $# -gt 0 ]]; then
  themes=("$@")
else
  themes=(examples/themes/starter)
fi

for dir in "${themes[@]}"; do
  dir="${dir%/}"
  if [[ ! -f "$dir/theme.json" ]]; then
    echo "$dir: no theme.json" >&2
    exit 1
  fi
  slug="$(sed -n 's/.*"slug"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$dir/theme.json" | head -n1)"
  version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$dir/theme.json" | head -n1)"
  out="$root/$dist/$slug.zip"
  echo "==> $slug $version"
  rm -f "$out"
  (
    cd "$dir"
    files=(theme.json templates assets)
    [[ -f readme.md ]] && files+=(readme.md)
    for extra in demo acf-json blocks; do
      [[ -d "$extra" ]] && files+=("$extra")
    done
    zip -q -r -X "$out" "${files[@]}" -x '*.DS_Store' -x '__MACOSX/*'
  )
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$out"
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$out"
  fi
done

echo "packages in $dist"
