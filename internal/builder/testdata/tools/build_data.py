#!/usr/bin/env python3
"""Builds internal/builder/data/*.json.gz from a controls dump (dump-controls.php) and the
Elementor plugin assets. Usage: build_data.py <controls.json> <elementor-plugin-dir> <out-dir>"""
import gzip, json, os, sys

dump, plugin, out = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(dump))

def fix(o):
    # selectors maps keep their order as [[selector, property], ...]
    if isinstance(o, dict):
        r = {}
        for k, v in o.items():
            if k in ('selectors', 'unit_selectors_dictionary_ordered') and isinstance(v, dict):
                r[k] = [[a, b] for a, b in v.items()]
            else:
                r[k] = fix(v)
        return r
    if isinstance(o, list):
        return [fix(x) for x in o]
    return o

data = fix(d)
with gzip.open(os.path.join(out, 'controls.json.gz'), 'wt', compresslevel=9) as f:
    json.dump(data, f, separators=(',', ':'))

icons = {}
for lib in ('solid', 'regular', 'brands'):
    j = json.load(open(os.path.join(plugin, 'assets/lib/font-awesome/json', lib + '.json')))
    icons['fa-' + lib] = {k: [v[0], v[1], v[4]] for k, v in j['icons'].items()}
e = json.load(open(os.path.join(plugin, 'assets/lib/eicons/eicons.json')))
icons['eicons'] = {k: [v['width'], v['height'], v['path']] for k, v in e.items()}
with gzip.open(os.path.join(out, 'icons.json.gz'), 'wt', compresslevel=9) as f:
    json.dump(icons, f, separators=(',', ':'))

shapes = {}
sd = os.path.join(plugin, 'assets/shapes')
for n in sorted(os.listdir(sd)):
    if n.endswith('.svg'):
        shapes[n[:-4]] = open(os.path.join(sd, n)).read()
with gzip.open(os.path.join(out, 'shapes.json.gz'), 'wt', compresslevel=9) as f:
    json.dump(shapes, f, separators=(',', ':'))
print('ok')
