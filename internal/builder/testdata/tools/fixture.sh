#!/bin/bash
# fixture.sh <spec.json> <outdir>: publishes the spec as a page in the lab and saves
# <name>.data.json (_elementor_data), <name>.html (document markup), <name>.css (post-ID.css), <name>.settings.json
set -e
L=${WPGO_LAB:-/private/tmp/claude-501/-Users-boradev-Desktop-WordpressGO/04483e33-c6f0-47da-8819-4a227b809af0/scratchpad/elementor-lab}
SPEC=$(cd "$(dirname "$1")" && pwd)/$(basename "$1"); mkdir -p "$2"; OUT=$(cd "$2" && pwd)
NAME=$(python3 -I -c "import json,sys;print(json.load(open(sys.argv[1]))['name'])" "$SPEC")
TOOLS=$(cd "$(dirname "$0")" && pwd)
mkdir -p $L/wp/wpgo-dump/specs
cp "$TOOLS/make-fixture.php" $L/wp/wpgo-dump/make-fixture.php
cp "$SPEC" $L/wp/wpgo-dump/specs/$NAME.json
cd $L
ID=$(docker compose run --rm -T cli wp --user=labadmin eval-file wpgo-dump/make-fixture.php wpgo-dump/specs/$NAME.json 2>/dev/null | grep -o 'WPGOID:[0-9]*' | cut -d: -f2)
echo "page id $ID"
docker compose run --rm -T cli wp --skip-plugins post meta get $ID _elementor_data 2>/dev/null > $OUT/$NAME.data.json
docker compose run --rm -T cli wp --skip-plugins post meta get $ID _elementor_page_settings --format=json 2>/dev/null > $OUT/$NAME.settings.json || true
curl -sL "http://localhost:8095/?page_id=$ID" > /private/tmp/claude-501/-Users-boradev-Desktop-WordpressGO/04483e33-c6f0-47da-8819-4a227b809af0/scratchpad/fx-tmp-$NAME.html
python3 -I - "$ID" /private/tmp/claude-501/-Users-boradev-Desktop-WordpressGO/04483e33-c6f0-47da-8819-4a227b809af0/scratchpad/fx-tmp-$NAME.html "$OUT/$NAME.html" <<'PY'
import sys,re
pid, src, dst = sys.argv[1], sys.argv[2], sys.argv[3]
h = open(src).read()
start = h.index('<div data-elementor-type="wp-page" data-elementor-id="%s"' % pid)
# find matching closing div
i = start; depth = 0
tag = re.compile(r'<(/?)div\b[^>]*>')
for m in tag.finditer(h, start):
    if m.group(1): depth -= 1
    else: depth += 1
    if depth == 0:
        open(dst, 'w').write(h[start:m.end()]); break
PY
docker compose run --rm -T cli wp --skip-plugins post meta get $ID _elementor_page_assets --format=json 2>/dev/null > $OUT/$NAME.assets.json || true
[ -s $OUT/$NAME.assets.json ] || rm -f $OUT/$NAME.assets.json
cp $L/wp/wp-content/uploads/elementor/css/post-$ID.css $OUT/$NAME.css 2>/dev/null || echo -n > $OUT/$NAME.css
echo "$ID" > $OUT/$NAME.id
rm -f /private/tmp/claude-501/-Users-boradev-Desktop-WordpressGO/04483e33-c6f0-47da-8819-4a227b809af0/scratchpad/fx-tmp-$NAME.html
