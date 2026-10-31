#!/usr/bin/env bash
# Publishes the theme library (library/themes/<slug>/) to Pursae Manager.
#
# The library is not part of the Pursae binary: sites list, preview, install and update its themes
# from Pursae Manager (docs/theme-package.md, "Theme library"). This script runs the Manager's bulk
# import against the library directories:
#
#   pursae-manager import -kind theme -publish [-preinstall slug,slug|all] [-access pro] DIR...
#
# from a pursae-manager checkout, so the import uses that checkout's environment (DATABASE_URL,
# PURSAE_MANAGER_DATA_DIR, … — see pursae-manager/API.md, "Configuration"). A version the Manager
# already has is skipped, so the script can be run again after adding themes or bumping versions.
#
# Usage:
#   PURSAE_MANAGER_DIR=/path/to/pursae-manager tools/publish-themes.sh [import flags] [theme-dir ...]
#   make publish-themes PURSAE_MANAGER_DIR=/path/to/pursae-manager PREINSTALL=slug,slug ACCESS=pro
#
# Without theme directories every directory of library/themes is imported. Flags are passed on to
# the import (e.g. -preinstall twentytwentyfive-blog,inkwell, -preinstall all, -access pro); -kind
# theme and -publish are always set. PURSAE_MANAGER (a pursae-manager binary) may be given instead of
# PURSAE_MANAGER_DIR; then the command runs from the current directory.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

flags=()
dirs=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -preinstall|-access)
      flags+=("$1" "$2")
      shift 2
      ;;
    -preinstall=*|-access=*)
      flags+=("$1")
      shift
      ;;
    -*)
      echo "unknown flag $1 (allowed: -preinstall, -access)" >&2
      exit 2
      ;;
    *)
      d="$1"
      [[ "$d" = /* ]] || d="$PWD/$d"
      dirs+=("${d%/}")
      shift
      ;;
  esac
done

if [[ ${#dirs[@]} -eq 0 ]]; then
  for d in "$root"/library/themes/*/; do
    [[ -f "$d/theme.json" ]] && dirs+=("${d%/}")
  done
fi
if [[ ${#dirs[@]} -eq 0 ]]; then
  echo "no theme directories to publish" >&2
  exit 1
fi
for d in "${dirs[@]}"; do
  if [[ ! -f "$d/theme.json" ]]; then
    echo "$d: no theme.json" >&2
    exit 1
  fi
done

cmd=(import -kind theme -publish ${flags[@]+"${flags[@]}"} "${dirs[@]}")
if [[ -n "${PURSAE_MANAGER:-}" ]]; then
  exec "$PURSAE_MANAGER" "${cmd[@]}"
fi
if [[ -z "${PURSAE_MANAGER_DIR:-}" ]]; then
  echo "set PURSAE_MANAGER_DIR to a pursae-manager checkout (or PURSAE_MANAGER to its binary)" >&2
  exit 2
fi
cd "$PURSAE_MANAGER_DIR"
exec go run ./cmd/pursae-manager "${cmd[@]}"
