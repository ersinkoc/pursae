# Convert world-atlas countries-110m TopoJSON into equirectangular SVG paths keyed by ISO numeric id.
import json, sys
src, out = sys.argv[1], sys.argv[2]
t = json.load(open(src))
sx, sy = t['transform']['scale']; tx, ty = t['transform']['translate']
arcs = []
for a in t['arcs']:
    x = y = 0; pts = []
    for dx, dy in a:
        x += dx; y += dy
        pts.append((x * sx + tx, y * sy + ty))
    arcs.append(pts)
W, H = 1000, 470          # lon -180..180, lat 84..-58 (Antarctica dropped)
LAT_TOP, LAT_BOT = 84.0, -58.0
def proj(lon, lat):
    return ((lon + 180) / 360 * W, (LAT_TOP - lat) / (LAT_TOP - LAT_BOT) * H)
def ring(idxs):
    pts = []
    for i in idxs:
        a = arcs[i] if i >= 0 else list(reversed(arcs[~i]))
        pts.extend(a if not pts else a[1:])
    return pts
def unwrap(pts):
    out = [pts[0]]
    for lon, lat in pts[1:]:
        prev = out[-1][0]
        while lon - prev > 180: lon -= 360
        while lon - prev < -180: lon += 360
        out.append((lon, lat))
    return out
def path(rings):
    d = []
    for r in rings:
        ll = unwrap(ring(r))
        if len(ll) < 3: continue
        copies = [0]
        if max(p[0] for p in ll) > 180: copies.append(-360)
        if min(p[0] for p in ll) < -180: copies.append(360)
        for off in copies:
            pts = [proj(lon + off, lat) for lon, lat in ll]
            if all(p[1] > H for p in pts): continue
            d.append('M' + 'L'.join('%.0f,%.0f' % (x, min(max(y, 0), H)) for x, y in pts) + 'Z')
    return ''.join(d)
res = []
for g in t['objects']['countries']['geometries']:
    gid = g.get('id')
    if gid is None or g['type'] not in ('Polygon', 'MultiPolygon'): continue
    polys = [g['arcs']] if g['type'] == 'Polygon' else g['arcs']
    d = ''.join(path(p) for p in polys)
    if d and gid != '010':
        res.append((gid, g.get('properties', {}).get('name', ''), d))
with open(out, 'w') as f:
    f.write('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d">' % (W, H))
    for gid, name, d in res:
        f.write('<path data-m="%s" d="%s"><title>%s</title></path>' % (gid, d, name.replace('&', '&amp;').replace('<', '')))
    f.write('</svg>\n')
print(len(res), 'countries')
