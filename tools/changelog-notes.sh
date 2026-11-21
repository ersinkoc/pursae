#!/usr/bin/env bash
# Prints the body of one version's section of a Keep a Changelog file: the lines after
# "## [VERSION]" (optionally followed by " - date") up to the next "## " heading or the link
# reference definitions at the end, without leading and trailing blank lines. Prints nothing
# when the section is missing or empty. tools/release.sh uses it for the release notes.
#
#   tools/changelog-notes.sh 0.3.0 [CHANGELOG.md]
set -euo pipefail

[[ $# -ge 1 && $# -le 2 ]] || { echo "usage: $0 VERSION [CHANGELOG.md]" >&2; exit 2; }
version="${1#v}"
file="${2:-CHANGELOG.md}"
[[ -f "$file" ]] || exit 0

awk -v head="## [$version]" '
	# The heading is exactly "## [x.y.z]", or "## [x.y.z] - date"; "## [x.y.z-beta.1]" is another version.
	!found && (index($0, head) == 1) {
		rest = substr($0, length(head) + 1)
		if (rest == "" || rest ~ /^[ \t]/) { found = 1; next }
	}
	found && /^## / { exit }
	found && /^\[[^]]+\]: / { exit }
	found { lines[++n] = $0 }
	END {
		first = 1; last = n
		while (first <= last && lines[first] ~ /^[ \t]*$/) first++
		while (last >= first && lines[last] ~ /^[ \t]*$/) last--
		for (i = first; i <= last; i++) print lines[i]
	}
' "$file"
