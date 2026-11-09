#!/usr/bin/env python3
"""Pursae vs WordPress benchmark (`make bench`). See docs/developer/benchmarks.md.

Builds both stacks from bench/wp and bench/pursae, imports the same content (bench/content/gen.py),
then for each setup measures single-request TTFB and DB queries (cold and warm), k6 load
(req/s, TTFB and latency percentiles), container memory (idle and peak), cold start, image and
install size and a Lighthouse score. Raw output goes to bench/results/<date>/, the medians of
--runs repetitions to summary.json and summary.md.

    python3 bench/run.py                       # everything (about an hour)
    python3 bench/run.py --setups wp-stock,pursae-cache --runs 1 --duration 5s   # smoke test
    python3 bench/run.py --reuse               # keep the seeded stacks from a previous run

Needs Docker (Compose v2), Python 3 (standard library) and, for Lighthouse, Node + Chrome.
"""
import argparse
import concurrent.futures as cf
import datetime as dt
import glob
import html
import json
import os
import platform
import re
import shutil
import statistics
import subprocess
import sys
import threading
import time
import uuid

BENCH = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BENCH)
from wpsite import Site  # noqa: E402

ADMIN_USER, ADMIN_PASS = "admin", "bench-admin-pass"
K6_IMAGE = "grafana/k6:1.3.0"
LIGHTHOUSE = "lighthouse@12.8.2"
SEARCH = "/?s=garden"
IMAGES = sorted(glob.glob(os.path.join(BENCH, "..", "web/themes/twentytwentyfive/assets/images/*.webp")))[:12]

# WordPress.org plugins of the "real-site" and "tuned" setups, pinned (bench/wp/compose.yaml pins core).
WP_PLUGINS = {
    "woocommerce": "11.2.2", "wordpress-seo": "28.8", "polylang": "3.9.2", "advanced-custom-fields": "6.8.11",
    "elementor": "4.3.6", "contact-form-7": "6.2.4.1", "redis-cache": "3.0.0", "wp-super-cache": "3.1.5",
    "wordpress-importer": "0.9.7",
}
WP_REAL_SITE = ["woocommerce", "wordpress-seo", "polylang", "advanced-custom-fields", "elementor", "contact-form-7"]
# The Pursae built-in plugins that cover the same ground, in the same order.
PURSAE_REAL_SITE = ["goocommerce", "seo-go", "golylang", "acf-go", "web-builder", "gontact-form"]

SIDES = {
    "wp": dict(dir="wp", port=18081, php=True, internal="http://nginx", app=["php", "nginx"],
               admin_urls={"admin-dashboard": "/wp-admin/", "admin-posts": "/wp-admin/edit.php"}),
    "pursae": dict(dir="pursae", port=18082, php=False, internal="http://wpgo:8080", app=["wpgo"],
                   admin_urls={"admin-dashboard": "/wp-admin/", "admin-posts": "/wp-admin/edit"}),
}
SETUPS = {
    "wp-stock": dict(side="wp", label="WordPress stock (OPcache)", env={}, plugins=[]),
    "wp-tuned": dict(side="wp", label="WordPress tuned (OPcache + Redis + WP Super Cache)",
                     env={"WP_NGINX": "supercache", "WP_FPM_POOL": "tuned",
                          "WP_DB_ARGS": "--innodb-buffer-pool-size=512M"},
                     profiles=["tuned"], plugins=["redis-cache", "wp-super-cache"]),
    "wp-plugins": dict(side="wp", label="WordPress + 6 plugins (Woo, Yoast, Polylang, ACF, Elementor, CF7)",
                       env={}, plugins=WP_REAL_SITE),
    "pursae-nocache": dict(side="pursae", label="Pursae, Pursae Cache off", plugins=[]),
    "pursae-cache": dict(side="pursae", label="Pursae, Pursae Cache on", plugins=["wpgo-cache"]),
    "pursae-plugins": dict(side="pursae", label="Pursae + 6 built-ins (Commerce, SEO, Multi Language, Custom Fields, Web Builder, Contact Form)",
                           plugins=PURSAE_REAL_SITE),
}
ENDPOINTS = ["home", "single", "archive", "product", "search", "rest-posts", "admin-dashboard", "admin-posts"]


def log(*a):
    print(time.strftime("%H:%M:%S"), *a, flush=True)


def sh(cmd, cwd=None, env=None, check=True, capture=True, timeout=1800):
    e = dict(os.environ, **(env or {}))
    r = subprocess.run(cmd, cwd=cwd, env=e, text=True, capture_output=capture, timeout=timeout)
    if check and r.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed ({r.returncode}):\n{r.stdout[-2000:] if r.stdout else ''}{r.stderr[-2000:] if r.stderr else ''}")
    return (r.stdout or "").strip()


class Stack:
    """One docker compose project (bench/wp or bench/pursae) with the env of the current setup."""

    def __init__(self, side, content):
        self.side, self.cfg = side, SIDES[side]
        self.dir = os.path.join(BENCH, self.cfg["dir"])
        self.env = {"BENCH_CONTENT": content}
        self.profiles = []
        self.base = f"http://localhost:{self.cfg['port']}"
        self.host = f"localhost:{self.cfg['port']}"
        self.project = "bench-" + side

    def compose(self, *args, **k):
        prof = [x for p in self.profiles for x in ("--profile", p)]
        return sh(["docker", "compose", *prof, *args], cwd=self.dir, env=self.env, **k)

    def set_env(self, env, profiles):
        defaults = {"WP_NGINX": "default", "WP_FPM_POOL": "stock", "WP_DB_ARGS": ""}
        self.env = {**self.env, **defaults, **env}
        self.profiles = profiles

    def up(self):
        self.compose("up", "-d", "--remove-orphans", "--wait", timeout=900)

    def containers(self):
        out = self.compose("ps", "--format", "{{.Name}}")
        return [x for x in out.splitlines() if x and "-cli-" not in x]

    def site(self):
        return Site(self.base, self.cfg["php"])

    def wait_http(self, path=None, timeout=180):
        path = path or f"/wp-login{'.php' if self.cfg['php'] else ''}"  # boots the app, never page-cached
        t0 = time.time()
        while time.time() - t0 < timeout:
            try:
                dt_, st = self.site().ttfb(path)
                if st in (200, 302):
                    return time.time() - t0
            except Exception:
                pass
            time.sleep(0.1)
        raise RuntimeError(f"{self.base}{path} did not answer in {timeout}s")

    # --- WordPress helpers ---
    def wp(self, *args, check=True):
        return self.compose("run", "--rm", "-T", "cli", "wp", *args, check=check, timeout=1800)

    def db_counter(self):
        if self.side == "wp":
            out = self.compose("exec", "-T", "db", "mariadb", "-uroot", "-proot", "-N", "-B", "-e", "SHOW GLOBAL STATUS LIKE 'Questions'")
            return int(out.split()[-1])
        out = self.compose("exec", "-T", "db", "psql", "-U", "wpgo", "-d", "wpgo", "-Atqc",
                           "CREATE EXTENSION IF NOT EXISTS pg_stat_statements; SELECT coalesce(sum(calls),0) FROM pg_stat_statements")
        return int(float(out))


# ---------------------------------------------------------------- seeding

def seed_media_and_products(st, products):
    s = st.site()
    s.login(ADMIN_USER, ADMIN_PASS)
    log(f"[{st.side}] uploading {len(IMAGES)} images")
    media = [s.upload(p)["id"] for p in IMAGES]
    posts = s.rest_all("/wp/v2/posts", {"_fields": "id", "status": "publish"})
    log(f"[{st.side}] featured images on {len(posts)} posts")

    def feature(i_p):
        i, p = i_p
        s.rest("POST", f"/wp/v2/posts/{p['id']}", {"featured_media": media[i % len(media)]})
    with cf.ThreadPoolExecutor(4) as ex:
        list(ex.map(feature, enumerate(posts)))
    log(f"[{st.side}] {len(products['products'])} products")
    cats = {}
    for name in products["categories"]:
        cats[name] = s.rest("POST", "/wc/v3/products/categories", {"name": name})[0]["id"]
    batch = []
    for i, p in enumerate(products["products"]):
        p = dict(p)
        p["categories"] = [{"id": cats[p.pop("category")]}]
        p["images"] = [{"id": media[i % len(media)]}]
        batch.append(p)
    for i in range(0, len(batch), 50):
        out, _ = s.rest("POST", "/wc/v3/products/batch", {"create": batch[i:i + 50]})
        errs = [x for x in out.get("create", []) if x.get("error")]
        if errs:
            raise RuntimeError(f"product batch errors: {errs[:2]}")


def seed_wp(st, products):
    t0 = time.time()
    st.wp("core", "install", f"--url={st.base}", "--title=Bench", f"--admin_user={ADMIN_USER}",
          f"--admin_password={ADMIN_PASS}", "--admin_email=admin@bench.invalid", "--skip-email")
    st.wp("rewrite", "structure", "/%postname%/")
    st.wp("plugin", "delete", "akismet", "hello", check=False)
    for slug, ver in WP_PLUGINS.items():
        st.wp("plugin", "install", slug, f"--version={ver}")
    st.wp("plugin", "activate", "wordpress-importer")
    t1 = time.time()
    st.wp("import", "/content/site.wxr", "--authors=skip", "--quiet")
    import_s = time.time() - t1
    st.wp("plugin", "deactivate", "wordpress-importer")
    # The real-site plugins get their first-run setup once, here, as a site owner would do it.
    st.wp("plugin", "activate", *WP_REAL_SITE)
    st.wp("option", "update", "woocommerce_coming_soon", "no")
    st.wp("eval", "$m = PLL()->model; if (!$m->get_language('en')) { $m->add_language(array('name'=>'English','slug'=>'en','locale'=>'en_US','rtl'=>false,'term_group'=>0,'flag'=>'us')); } $m->set_language_in_mass($m->get_language('en'));")
    seed_media_and_products(st, products)
    st.wp("eval", "$m = PLL()->model; $m->set_language_in_mass($m->get_language('en'));")  # products and media too
    st.wp("yoast", "index", "--reindex", check=False)
    st.wp("cron", "event", "run", "--due-now", check=False)
    st.wp("plugin", "deactivate", "--all")
    return {"seconds": round(time.time() - t0, 1), "wxr_import_seconds": round(import_s, 1)}


def pursae_form(s, page, action, values):
    st, body, _ = s.get(page)
    page_html = body.decode()
    form = {}
    for m in re.finditer(r'<input[^>]*type="hidden"[^>]*>', page_html):
        name, val = re.search(r'name="([^"]+)"', m.group(0)), re.search(r'value="([^"]*)"', m.group(0))
        if name:
            form[name.group(1)] = html.unescape(val.group(1)) if val else ""
    form.update(values)
    import urllib.parse
    st, body, _ = s.request("POST", action, urllib.parse.urlencode(form).encode(), {"Content-Type": "application/x-www-form-urlencoded"})
    if st >= 400:
        raise RuntimeError(f"POST {action}: {st} {body[:300]!r}")
    return body.decode(errors="replace")


def pursae_plugins(s):
    """{slug: (active, toggle URL)} from the Plugins screen."""
    st, body, _ = s.get("/wp-admin/plugins")
    page = html.unescape(body.decode())
    out = {}
    for m in re.finditer(r'(/wp-admin/plugins\?action=(activate|deactivate)&plugin=([a-z0-9-]+)&_wpnonce=[0-9a-f]+)', page):
        out[m.group(3)] = (m.group(2) == "deactivate", m.group(1))
    return out


def pursae_set_plugins(st, wanted):
    s = st.site()
    s.login(ADMIN_USER, ADMIN_PASS)
    for _ in range(3):
        cur = pursae_plugins(s)
        todo = [(slug, url) for slug, (active, url) in cur.items()
                if slug != "classic-editor" and active != (slug in wanted)]  # Classic Editor: Pursae's default, admin only
        if not todo:
            break
        for slug, url in todo:
            s.get(url)
    cur = pursae_plugins(s)
    bad = [x for x in wanted if not cur.get(x, (False,))[0]]
    if bad:
        raise RuntimeError(f"could not activate {bad}")
    # Web Builder switches to its blank-canvas theme on activation: keep Twenty Twenty-Five on both sides.
    st_, body, _ = s.get("/wp-admin/themes")
    m = re.search(r'(/wp-admin/themes\?action=activate&(?:amp;)?stylesheet=twentytwentyfive&(?:amp;)?_wpnonce=[0-9a-f]+)', body.decode())
    if m:
        s.get(html.unescape(m.group(1)))


def seed_pursae(st, content, products):
    t0 = time.time()
    import urllib.parse
    s = st.site()
    form = {"weblog_title": "Bench", "user_name": ADMIN_USER, "admin_password": ADMIN_PASS, "admin_password2": ADMIN_PASS,
            "admin_email": "admin@bench.invalid", "blog_public": "1"}
    stc, body, _ = s.request("POST", "/wp-admin/install", urllib.parse.urlencode(form).encode(),
                             {"Content-Type": "application/x-www-form-urlencoded"})
    if b"Success" not in body:
        raise RuntimeError("install failed: " + body.decode()[:400])
    s.login(ADMIN_USER, ADMIN_PASS)
    pursae_form(s, "/wp-admin/options-permalink", "/wp-admin/options-permalink",
                {"selection": "/%postname%/", "permalink_structure": "/%postname%/", "category_base": "", "tag_base": ""})
    # WXR import through Tools › Import (multipart upload, like the browser).
    _, body, _ = s.get("/wp-admin/import")
    nonce = re.search(r'name="_wpnonce" value="([0-9a-f]+)"', body.decode()).group(1)
    with open(os.path.join(content, "site.wxr"), "rb") as f:
        wxr = f.read()
    bnd = uuid.uuid4().hex
    parts = (f"--{bnd}\r\nContent-Disposition: form-data; name=\"_wpnonce\"\r\n\r\n{nonce}\r\n"
             f"--{bnd}\r\nContent-Disposition: form-data; name=\"import\"; filename=\"site.wxr\"\r\nContent-Type: text/xml\r\n\r\n").encode() + wxr + f"\r\n--{bnd}--\r\n".encode()
    t1 = time.time()
    stc, body, _ = s.request("POST", "/wp-admin/import", parts, {"Content-Type": f"multipart/form-data; boundary={bnd}"})
    import_s = time.time() - t1
    if stc != 200 or b"error" in body.lower() and b"All done" not in body:
        m = re.search(rb'class="error[^"]*"[^>]*>(.*?)</div>', body, re.S)
        if m:
            raise RuntimeError("import failed: " + m.group(1).decode()[:400])
    pursae_set_plugins(st, ["goocommerce"])
    seed_media_and_products(st, products)
    pursae_set_plugins(st, PURSAE_REAL_SITE)  # first-run setup of each built-in, then everything off again
    pursae_set_plugins(st, [])
    return {"seconds": round(time.time() - t0, 1), "wxr_import_seconds": round(import_s, 1)}


# ---------------------------------------------------------------- setups

def apply_setup(st, name):
    cfg = SETUPS[name]
    st.set_env(cfg.get("env", {}), cfg.get("profiles", []))
    if st.side == "wp":
        if "tuned" not in st.profiles:  # Redis only runs in the tuned setup
            sh(["docker", "compose", "--profile", "tuned", "rm", "-sf", "redis"], cwd=st.dir, env={**os.environ, **st.env})
        st.up()
        st.wp("plugin", "deactivate", "--all", check=False)
        st.wp("config", "set", "WP_CACHE", "false", "--raw", check=False)
        st.compose("exec", "-T", "-u", "0", "php", "sh", "-c",
                   "rm -rf /var/www/html/wp-content/cache /var/www/html/wp-content/advanced-cache.php /var/www/html/wp-content/object-cache.php /var/www/html/wp-content/wp-cache-config.php")
        if cfg["plugins"]:
            st.wp("plugin", "activate", *cfg["plugins"])
        if "redis-cache" in cfg["plugins"]:
            st.wp("redis", "enable", "--force")
        if "wp-super-cache" in cfg["plugins"]:
            # Settings › WP Super Cache › "Caching On" (the plugin's recommended setting), files served by nginx.
            st.wp("eval", "wp_cache_enable(); wp_super_cache_enable(); wpsc_init(); wp_cache_setting('wp_cache_mod_rewrite', 0); wp_cache_setting('wp_cache_not_logged_in', 2); wp_cache_setting('cache_compression', 1);")
            st.wp("config", "set", "WP_CACHE", "true", "--raw")
        st.wp("cache", "flush", check=False)
        st.compose("restart", "php", "nginx")
    else:
        st.up()
        pursae_set_plugins(st, cfg["plugins"])
        st.compose("restart", "wpgo")  # a clean process: empty page cache and memory
    st.wait_http()


def discover(st, setup):
    s = st.site()
    s.login(ADMIN_USER, ADMIN_PASS)
    strip = lambda u: re.sub(r"^https?://[^/]+", "", u)  # noqa: E731
    posts = [strip(p["link"]) for p in s.rest_all("/wp/v2/posts", {"_fields": "link", "status": "publish"})]
    cats = [strip(c["link"]) for c in s.rest_all("/wp/v2/categories", {"_fields": "link", "hide_empty": "true"})]
    posts.sort()
    cats.sort()
    # Load tests use a fixed sample (every 10th post, every category, every 4th product), crawled
    # once before the warm-up so a page cache is really warm; the cold single-request measurements
    # use other URLs of the same kind, never requested before.
    urls = {"home": ["/"], "single": posts[::10], "archive": cats, "search": [SEARCH], "rest-posts": ["/wp-json/wp/v2/posts"]}
    cold = {"home": ["/"], "single": posts[1::10][:3], "archive": cats[:3], "search": ["/?s=meadow", "/?s=river", "/?s=stone"],
            "rest-posts": ["/wp-json/wp/v2/posts?page=2", "/wp-json/wp/v2/posts?page=3", "/wp-json/wp/v2/posts?page=4"]}
    if set(SETUPS[setup]["plugins"]) & {"woocommerce", "goocommerce"}:
        prods = sorted(strip(p["permalink"]) for p in s.rest_all("/wc/v3/products", {"_fields": "permalink"}))
        urls["product"], cold["product"] = prods[::4], prods[1::4][:3]
    urls.update({k: [v] for k, v in st.cfg["admin_urls"].items()})
    cold.update({k: [v] for k, v in st.cfg["admin_urls"].items()})
    return urls, cold, s.cookie_header()


def check_pages(st, urls, cookie):
    """Every endpoint answers 200 and is the right page (not a coming-soon, login or error page)."""
    s = st.site()
    out = {}
    for ep, lst in urls.items():
        hdr = {"Cookie": cookie} if ep.startswith("admin") else {}
        stc, body, h = s.request("GET", lst[0], headers=hdr)
        title = re.search(rb"<title>(.*?)</title>", body, re.S)
        out[ep] = {"url": lst[0], "status": stc, "bytes": len(body), "title": html.unescape(title.group(1).decode().strip()) if title else None,
                   "cache": h.get("X-WPGo-Cache") or h.get("x-wpgo-cache")}
        if stc != 200:
            raise RuntimeError(f"{ep} {lst[0]} answered {stc}")
    return out


def measure_queries(st, urls, cookie):
    """DB statements and TTFB of the first (cold) and second (warm) request of 3 URLs per endpoint."""
    s = st.site()
    base = []
    for _ in range(3):
        a = st.db_counter()
        base.append(st.db_counter() - a)
    base = min(base)
    s.ttfb("/page-50/")  # process warm (OPcache, autoloaded options); the measured URLs are not requested yet
    out = {}
    for ep, lst in urls.items():
        hdr = {"Cookie": cookie} if ep.startswith("admin") else {}
        rows, seen = [], set()
        for u in (lst if len(lst) >= 3 else lst * 3):
            r = {"url": u}
            # "cold" is only the very first request of a URL (home and admin pages have one URL).
            for phase in (("warm",) if u in seen else ("cold", "warm")):
                a = st.db_counter()
                t, stc = s.ttfb(u, hdr)
                b = st.db_counter()
                r[phase] = {"queries": b - a - base, "ttfb_ms": round(t * 1000, 2), "status": stc}
            seen.add(u)
            rows.append(r)
        med = lambda ph, k: statistics.median(x[ph][k] for x in rows if ph in x)  # noqa: E731
        out[ep] = {"cold_queries": med("cold", "queries"), "warm_queries": med("warm", "queries"),
                   "cold_ttfb_ms": med("cold", "ttfb_ms"), "warm_ttfb_ms": med("warm", "ttfb_ms"), "samples": rows}
    return out


def mem_sample(names):
    out = sh(["docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}", *names])
    tot = 0.0
    for line in out.splitlines():
        used = line.split("\t")[1].split("/")[0].strip()
        num, unit = re.match(r"([\d.]+)\s*([KMG]i?B|B)", used).groups()
        tot += float(num) * {"B": 1 / 2**20, "KiB": 1 / 1024, "MiB": 1, "GiB": 1024, "kB": 1 / 1000, "MB": 1, "GB": 1000}[unit]
    return round(tot, 1)


class MemPeak(threading.Thread):
    def __init__(self, names):
        super().__init__(daemon=True)
        self.names, self.peak, self.stop = names, 0.0, threading.Event()

    def run(self):
        while not self.stop.is_set():
            try:
                self.peak = max(self.peak, mem_sample(self.names))
            except Exception:
                pass


def k6(st, outdir, name, urls, vus, duration, cookie=""):
    os.makedirs(outdir, exist_ok=True)
    with open(os.path.join(outdir, "urls-tmp.json"), "w") as f:
        json.dump(urls, f)
    cmd = ["docker", "run", "--rm", "--network", f"{st.project}_default", "--cpus", "4",
           "-v", f"{os.path.join(BENCH, 'k6')}:/scripts:ro", "-v", f"{outdir}:/out",
           "-e", f"TARGET={st.cfg['internal']}", "-e", f"HOST={st.host}", "-e", "URLS=/out/urls-tmp.json",
           "-e", f"VUS={vus}", "-e", f"DURATION={duration}", "-e", f"OUT={name}.json", "-e", f"COOKIE={cookie}",
           K6_IMAGE, "run", "--quiet", "--no-usage-report", "/scripts/load.js"]
    sh(cmd, timeout=900)
    with open(os.path.join(outdir, name + ".json")) as f:
        d = json.load(f)
    m = d["metrics"]
    v = lambda k, s: round(m[k]["values"][s], 2)  # noqa: E731
    return {"rps": round(m["http_reqs"]["values"]["rate"], 1), "requests": m["http_reqs"]["values"]["count"],
            "ttfb_p50": v("http_req_waiting", "med"), "ttfb_p95": v("http_req_waiting", "p(95)"), "ttfb_p99": v("http_req_waiting", "p(99)"),
            "p50": v("http_req_duration", "med"), "p95": v("http_req_duration", "p(95)"), "p99": v("http_req_duration", "p(99)"),
            "failed": round(m["http_req_failed"]["values"]["rate"], 4),
            "non200": round(1 - m["checks"]["values"]["rate"], 4) if "checks" in m else None}


def cold_start(st):
    """Seconds from `docker compose start` of the stopped stack to the first answer of the home page."""
    st.compose("stop")
    t0 = time.time()
    st.compose("start")
    st.wait_http("/", timeout=300)
    full = time.time() - t0
    st.compose("stop", *st.cfg["app"])
    t0 = time.time()
    st.compose("start", *st.cfg["app"])
    st.wait_http("/", timeout=300)
    return {"full_stack_s": round(full, 2), "app_only_s": round(time.time() - t0, 2)}


def lighthouse(st, outdir, runs):
    if not shutil.which("npx"):
        return {"skipped": "npx not found"}
    scores = []
    for i in range(runs):
        path = os.path.join(outdir, f"lighthouse-{i + 1}.json")
        r = subprocess.run(["npx", "-y", LIGHTHOUSE, st.base + "/", "--quiet", "--output=json", f"--output-path={path}",
                            "--only-categories=performance,accessibility,best-practices,seo",
                            "--chrome-flags=--headless=new --no-sandbox"], capture_output=True, text=True, timeout=600)
        if r.returncode != 0 or not os.path.exists(path):
            return {"skipped": "lighthouse failed: " + r.stderr[-300:]}
        with open(path) as f:
            d = json.load(f)
        keep = {"lighthouseVersion": d["lighthouseVersion"], "fetchTime": d["fetchTime"], "finalUrl": d.get("finalDisplayedUrl"),
                "categories": {k: v["score"] for k, v in d["categories"].items()},
                "audits": {k: d["audits"][k].get("numericValue") for k in
                           ("first-contentful-paint", "largest-contentful-paint", "total-blocking-time", "cumulative-layout-shift",
                            "speed-index", "server-response-time", "total-byte-weight") if k in d["audits"]}}
        with open(path, "w") as f:
            json.dump(keep, f, indent=1)  # the full report is ~1 MB; keep the scores and metrics
        scores.append(keep)
    return {"version": scores[0]["lighthouseVersion"],
            "performance": statistics.median(x["categories"]["performance"] for x in scores),
            "accessibility": statistics.median(x["categories"]["accessibility"] for x in scores),
            "best_practices": statistics.median(x["categories"]["best-practices"] for x in scores),
            "seo": statistics.median(x["categories"]["seo"] for x in scores),
            "lcp_ms": statistics.median(x["audits"]["largest-contentful-paint"] for x in scores),
            "page_bytes": statistics.median(x["audits"]["total-byte-weight"] for x in scores)}


def sizes(st):
    imgs = {}
    for c in st.containers():
        img = sh(["docker", "inspect", "-f", "{{.Config.Image}}", c])
        imgs[img] = int(sh(["docker", "image", "inspect", "-f", "{{.Size}}", img]))
    out = {"images_mb": {k: round(v / 1e6, 1) for k, v in imgs.items()}, "images_total_mb": round(sum(imgs.values()) / 1e6, 1)}
    if st.side == "wp":
        k = [int(st.compose("exec", "-T", "php", "du", "-sk", d).split()[0])
             for d in ("/var/www/html", "/var/www/html/wp-content/plugins", "/var/www/html/wp-content/uploads")]
        out.update(install_mb=round((k[0] - k[2]) / 1024, 1), plugins_mb=round(k[1] / 1024, 1), uploads_mb=round(k[2] / 1024, 1))
        db = st.compose("exec", "-T", "db", "mariadb", "-uroot", "-proot", "-N", "-B", "-e",
                        "SELECT sum(data_length+index_length) FROM information_schema.tables WHERE table_schema='wordpress'")
        out["db_mb"] = round(int(db) / 2**20, 1)
        out["db_tables"] = int(st.compose("exec", "-T", "db", "mariadb", "-uroot", "-proot", "-N", "-B", "-e",
                                          "SELECT count(*) FROM information_schema.tables WHERE table_schema='wordpress'"))
    else:
        out["install_mb"] = round(int(st.compose("exec", "-T", "wpgo", "stat", "-c", "%s", "/usr/local/lib/pursae/wpgo")) / 2**20, 1)
        out["uploads_mb"] = round(int(st.compose("exec", "-T", "wpgo", "du", "-sk", "/data/uploads").split()[0]) / 1024, 1)
        out["db_mb"] = round(int(st.compose("exec", "-T", "db", "psql", "-U", "wpgo", "-d", "wpgo", "-Atc", "SELECT pg_database_size('wpgo')")) / 2**20, 1)
        out["db_tables"] = int(st.compose("exec", "-T", "db", "psql", "-U", "wpgo", "-d", "wpgo", "-Atc",
                                          "SELECT count(*) FROM pg_tables WHERE schemaname='public'"))
    return out


def versions(st):
    if st.side == "wp":
        v = {"wordpress": st.wp("core", "version"),
             "php": st.compose("exec", "-T", "php", "php", "-r", "echo PHP_VERSION;"),
             "opcache": st.compose("exec", "-T", "php", "php", "-r", "echo ini_get('opcache.enable') ? 'on' : 'off';"),
             "mariadb": st.compose("exec", "-T", "db", "mariadb", "-uroot", "-proot", "-N", "-B", "-e", "SELECT VERSION()"),
             "nginx": "1.27 (nginx:1.27-alpine)",
             "plugins": json.loads(st.wp("plugin", "list", "--fields=name,version", "--format=json")),
             "theme": json.loads(st.wp("theme", "list", "--status=active", "--fields=name,version", "--format=json"))}
        return v
    return {"pursae": st.compose("exec", "-T", "wpgo", "/usr/local/lib/pursae/wpgo", "version"),
            "commit": sh(["git", "rev-parse", "--short", "HEAD"], cwd=BENCH),
            "postgres": st.compose("exec", "-T", "db", "psql", "-U", "wpgo", "-d", "wpgo", "-Atc", "SHOW server_version")}


# ---------------------------------------------------------------- main

def host_info():
    info = {"date": dt.date.today().isoformat(), "os": platform.platform(), "machine": platform.machine(),
            "docker": sh(["docker", "version", "--format", "{{.Server.Version}}"]),
            "docker_cpus": sh(["docker", "info", "--format", "{{.NCPU}}"]),
            "docker_mem_gb": round(int(sh(["docker", "info", "--format", "{{.MemTotal}}"])) / 2**30, 1),
            "k6": K6_IMAGE, "lighthouse": LIGHTHOUSE}
    if sys.platform == "darwin":
        info["cpu"] = sh(["sysctl", "-n", "machdep.cpu.brand_string"])
        info["host_mem_gb"] = round(int(sh(["sysctl", "-n", "hw.memsize"])) / 2**30)
    else:
        info["cpu"] = next((l.split(":", 1)[1].strip() for l in open("/proc/cpuinfo") if l.startswith("model name")), platform.processor())
    return info


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--setups", default=",".join(SETUPS))
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--duration", default="20s")
    ap.add_argument("--warmup", default="5s")
    ap.add_argument("--vus", type=int, default=10)
    ap.add_argument("--out", default=os.path.join(BENCH, "results", dt.date.today().isoformat()))
    ap.add_argument("--reuse", action="store_true", help="keep existing seeded stacks (skip down -v and seeding)")
    ap.add_argument("--keep", action="store_true", help="leave the stacks running at the end")
    ap.add_argument("--no-lighthouse", action="store_true")
    a = ap.parse_args()
    setups = [x for x in a.setups.split(",") if x]
    for x in setups:
        if x not in SETUPS:
            sys.exit(f"unknown setup {x}; known: {', '.join(SETUPS)}")
    os.makedirs(a.out, exist_ok=True)
    content = os.path.join(BENCH, "content", "out")
    sh([sys.executable, os.path.join(BENCH, "content", "gen.py"), content])
    with open(os.path.join(content, "products.json")) as f:
        products = json.load(f)
    summary_path = os.path.join(a.out, "summary.json")
    summary = json.load(open(summary_path)) if os.path.exists(summary_path) else {}
    summary.update({"host": host_info(), "params": {"runs": a.runs, "duration": a.duration, "warmup": a.warmup, "vus": a.vus}})
    summary.setdefault("setups", {})
    summary.setdefault("sides", {})
    for side in ("wp", "pursae"):
        mine = [x for x in setups if SETUPS[x]["side"] == side]
        if not mine:
            continue
        st = Stack(side, content)
        side_info = summary["sides"].setdefault(side, {})
        if not a.reuse:
            log(f"[{side}] fresh stack")
            st.set_env({}, ["tuned"] if side == "wp" else [])
            st.compose("down", "-v", "--remove-orphans", check=False)
            if side == "pursae":
                st.compose("build", "-q", timeout=1800)
            st.up()
            st.wait_http("/wp-admin/install" if side == "pursae" else "/wp-admin/install.php")
            log(f"[{side}] seeding")
            side_info["seed"] = seed_wp(st, products) if side == "wp" else seed_pursae(st, content, products)
            log(f"[{side}] seeded: {side_info['seed']}")
        for name in mine:
            log(f"== {name}")
            outdir = os.path.join(a.out, name)
            os.makedirs(outdir, exist_ok=True)
            apply_setup(st, name)
            urls, cold, cookie = discover(st, name)
            res = {"label": SETUPS[name]["label"], "pages": check_pages(st, urls, cookie)}
            log(f"[{name}] queries + single-request TTFB")
            # After a restart: the first request of each URL is cold (nothing cached), the second warm.
            st.compose("restart", *st.cfg["app"])
            st.wait_http()
            res["queries"] = measure_queries(st, cold, cookie)
            time.sleep(10)
            res["mem_idle_mb"] = mem_sample(st.containers())
            peak = MemPeak(st.containers())
            peak.start()
            res["load"] = {}
            for ep in ENDPOINTS:
                if ep not in urls:
                    continue
                ck = cookie if ep.startswith("admin") else ""
                for u in urls[ep]:  # crawl once: fills a page cache, compiles every template
                    st.site().ttfb(u, {"Cookie": ck} if ck else None)
                k6(st, os.path.join(outdir, "warmup"), ep, urls[ep], a.vus, a.warmup, ck)
                runs = [k6(st, outdir, f"k6-{ep}-run{i + 1}", urls[ep], a.vus, a.duration, ck) for i in range(a.runs)]
                med = {k: statistics.median(r[k] for r in runs) for k in runs[0] if runs[0][k] is not None}
                res["load"][ep] = {"median": med, "runs": runs}
                log(f"[{name}] {ep}: {med['rps']} req/s, TTFB p50 {med['ttfb_p50']} ms, p95 {med['p95']} ms, non-200 {med.get('non200')}")
            peak.stop.set()
            peak.join(timeout=10)
            res["mem_peak_mb"] = peak.peak
            shutil.rmtree(os.path.join(outdir, "warmup"), ignore_errors=True)
            for f in glob.glob(os.path.join(outdir, "urls-tmp.json")):
                os.remove(f)
            if not a.no_lighthouse:
                log(f"[{name}] lighthouse")
                st.wait_http("/")
                res["lighthouse"] = lighthouse(st, outdir, a.runs)
            log(f"[{name}] cold start")
            res["cold_start"] = [cold_start(st) for _ in range(a.runs)]
            res["cold_start_median"] = {k: statistics.median(x[k] for x in res["cold_start"]) for k in res["cold_start"][0]}
            res["sizes"] = sizes(st)
            res["versions"] = versions(st)
            with open(os.path.join(outdir, "result.json"), "w") as f:
                json.dump(res, f, indent=1)
            summary["setups"][name] = res
            with open(summary_path, "w") as f:
                json.dump(summary, f, indent=1)
        if not a.keep:
            st.compose("stop")
    with open(summary_path, "w") as f:
        json.dump(summary, f, indent=1)
    sh([sys.executable, os.path.join(BENCH, "report.py"), summary_path], capture=False)
    log("done:", a.out)


if __name__ == "__main__":
    main()
