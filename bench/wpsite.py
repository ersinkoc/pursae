"""A tiny HTTP client for a WordPress-compatible site (WordPress or Pursae): log in with the login
form, get a REST nonce from admin-ajax, call the REST API with cookie + X-WP-Nonce, upload media.
Standard library only, so `make bench` needs nothing but Python 3, Docker and (optionally) Chrome."""
import http.cookiejar
import json
import mimetypes
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


class Site:
    def __init__(self, base, php_urls):
        self.base = base.rstrip("/")
        self.php = ".php" if php_urls else ""  # WordPress: /wp-login.php; Pursae: /wp-login
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.raw = urllib.request.build_opener(NoRedirect)
        self.nonce = ""

    def url(self, path):
        return path if path.startswith("http") else self.base + path

    def request(self, method, path, body=None, headers=None, timeout=300):
        req = urllib.request.Request(self.url(path), data=body, method=method, headers=headers or {})
        try:
            with self.opener.open(req, timeout=timeout) as r:
                return r.status, r.read(), dict(r.headers)
        except urllib.error.HTTPError as e:
            return e.code, e.read(), dict(e.headers)

    def get(self, path, **k):
        return self.request("GET", path, **k)

    def ttfb(self, path, headers=None):
        """Seconds to the first response byte of one request without a cookie (anonymous), status."""
        req = urllib.request.Request(self.url(path), headers=headers or {})
        t0 = time.perf_counter()
        try:
            r = self.raw.open(req, timeout=120)
            r.read(1)
            dt, st = time.perf_counter() - t0, r.status
            r.read()
        except urllib.error.HTTPError as e:
            dt, st = time.perf_counter() - t0, e.code
        return dt, st

    def login(self, user, password):
        self.request("GET", f"/wp-login{self.php}")  # sets the test cookie on WordPress
        form = urllib.parse.urlencode({"log": user, "pwd": password, "wp-submit": "Log In", "testcookie": "1",
                                       "redirect_to": self.base + "/wp-admin/"}).encode()
        st, body, _ = self.request("POST", f"/wp-login{self.php}", form, {"Content-Type": "application/x-www-form-urlencoded"})
        if not any("logged_in" in c.name for c in self.jar):
            err = re.search(rb'login_error[^>]*>(.*?)</div>', body, re.S)
            raise RuntimeError(f"login failed ({st}): {(err.group(1) if err else body[:300])!r}")
        st, body, _ = self.get(f"/wp-admin/admin-ajax{self.php}?action=rest-nonce")
        self.nonce = body.decode().strip()
        if st != 200 or not re.fullmatch(r"[0-9a-f]{6,}", self.nonce):
            raise RuntimeError(f"no REST nonce ({st}): {body[:200]!r}")

    def cookie_header(self):
        return "; ".join(f"{c.name}={c.value}" for c in self.jar)

    def rest(self, method, route, data=None, query=None):
        path = "/wp-json" + route + ("?" + urllib.parse.urlencode(query) if query else "")
        body = json.dumps(data).encode() if data is not None else None
        st, out, h = self.request(method, path, body, {"Content-Type": "application/json", "X-WP-Nonce": self.nonce})
        if st >= 300:
            raise RuntimeError(f"{method} {route}: {st} {out[:300]!r}")
        return json.loads(out or b"null"), h

    def rest_all(self, route, query=None):
        out, page = [], 1
        while True:
            q = dict(query or {}, per_page=100, page=page)
            items, h = self.rest("GET", route, query=q)
            out += items
            total = next((v for k, v in h.items() if k.lower() == "x-wp-totalpages"), "1")
            if page >= int(total) or not items:
                return out
            page += 1

    def upload(self, path):
        with open(path, "rb") as f:
            data = f.read()
        name = os.path.basename(path)
        ctype = mimetypes.guess_type(name)[0] or "application/octet-stream"
        st, out, _ = self.request("POST", "/wp-json/wp/v2/media", data, {
            "Content-Type": ctype, "Content-Disposition": f'attachment; filename="{name}"', "X-WP-Nonce": self.nonce})
        if st >= 300:
            raise RuntimeError(f"upload {name}: {st} {out[:300]!r}")
        return json.loads(out)
