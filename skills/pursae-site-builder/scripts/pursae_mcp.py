#!/usr/bin/env python3
"""Pursae MCP harness: call a Pursae site's MCP server over HTTPS without an MCP client.

Environment: PURSAE_URL (site address or the full .../wp-json/pursae/v1/mcp URL), PURSAE_USER,
PURSAE_APP_PASSWORD (an Application Password). Standard library only.

  pursae_mcp.py check                     site, user, plugins, Web Builder state
  pursae_mcp.py tools [filter]            tool names and descriptions
  pursae_mcp.py describe <tool>           a tool's input schema
  pursae_mcp.py call <tool> [json|@file|-]
  pursae_mcp.py prompts                   the server's playbooks
  pursae_mcp.py prompt <name> [key=value ...]
  pursae_mcp.py resources                 the developer docs offered as resources
  pursae_mcp.py read <uri>                one resource
  pursae_mcp.py kit-upload <folder> <kit> [name=...]
                                          upload a design folder (CSS, JS, fonts, images; no HTML) as a
                                          design kit, in batches, then publish it
  pursae_mcp.py import-html <file.html> [key=value ...]
                                          HTML import. method=code (default for administrators) keeps the
                                          design's own markup 1:1: pass kit= and path= (the file inside the
                                          kit), templates=true for site-wide header/footer templates,
                                          link_map=@links.json. method=exact opens import_url in a browser
                                          (Playwright when installed, else the default browser; --no-open
                                          only prints it), waits for the measurements and publishes.
                                          key=value: title=, post_id=, status=, front_page=true, layout=,
                                          header_template=, footer_template=, site_name=
  pursae_mcp.py import-site <folder> kit=<kit> [status=... site_name=... front_page=false]
                                          import every HTML page of a folder whose files were uploaded as
                                          the kit (kit-upload <folder> <kit> first): pages, parents, links,
                                          header/footer templates with the active menu link, 404 template

Exit status: 0 ok, 1 tool or JSON-RPC error, 2 usage / connection problem.
"""
import base64
import json
import os
import sys
import urllib.error
import urllib.request

ENDPOINT_SUFFIX = "/wp-json/pursae/v1/mcp"


def die(msg, code=2):
    print(msg, file=sys.stderr)
    sys.exit(code)


def endpoint():
    url = os.environ.get("PURSAE_URL", "").strip().rstrip("/")
    if not url:
        die("Set PURSAE_URL (e.g. https://example.com), PURSAE_USER and PURSAE_APP_PASSWORD.")
    if not url.endswith(ENDPOINT_SUFFIX):
        url += ENDPOINT_SUFFIX
    if url.startswith("http://") and not any(h in url for h in ("://localhost", "://127.0.0.1", "://[::1]", ".localhost", ".test")):
        print("warning: plain http sends the password in clear text; use https", file=sys.stderr)
    return url


def auth_header():
    user = os.environ.get("PURSAE_USER", "").strip()
    pw = os.environ.get("PURSAE_APP_PASSWORD", "").strip()
    if not user or not pw:
        die("Set PURSAE_USER and PURSAE_APP_PASSWORD (an Application Password, not the login password).")
    return "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()


class Client:
    def __init__(self):
        self.url = endpoint()
        self.auth = auth_header()
        self.session = None
        self.seq = 0
        self.timeout = 180

    def rpc(self, method, params=None):
        self.seq += 1
        body = {"jsonrpc": "2.0", "id": self.seq, "method": method}
        if params is not None:
            body["params"] = params
        req = urllib.request.Request(self.url, data=json.dumps(body).encode(), method="POST")
        req.add_header("Content-Type", "application/json")
        req.add_header("Accept", "application/json")
        req.add_header("Authorization", self.auth)
        req.add_header("User-Agent", "pursae-mcp-harness/1")
        if self.session:
            req.add_header("Mcp-Session-Id", self.session)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                sid = resp.headers.get("Mcp-Session-Id")
                if sid:
                    self.session = sid
                raw = resp.read().decode()
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")[:600]
            if e.code == 401:
                die(f"401: the user name or Application Password was not accepted ({detail})")
            die(f"HTTP {e.code}: {detail}")
        except urllib.error.URLError as e:
            die(f"cannot reach {self.url}: {e.reason}")
        out = json.loads(raw)
        if out.get("error"):
            print(json.dumps(out["error"], ensure_ascii=False, indent=2))
            sys.exit(1)
        return out.get("result", {})

    def start(self):
        return self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                       "clientInfo": {"name": "pursae-mcp-harness", "version": "1"}})

    def call(self, tool, args):
        res = self.rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        return res.get("isError", False), text


def load_args(spec):
    if spec is None or spec == "":
        return {}
    if spec == "-":
        spec = sys.stdin.read()
    elif spec.startswith("@"):
        with open(spec[1:], encoding="utf-8") as f:
            spec = f.read()
    try:
        v = json.loads(spec)
    except json.JSONDecodeError as e:
        die(f"arguments are not JSON: {e}")
    if not isinstance(v, dict):
        die("arguments must be a JSON object")
    return v


def pretty(text):
    try:
        return json.dumps(json.loads(text), ensure_ascii=False, indent=2)
    except (json.JSONDecodeError, TypeError):
        return text


def main(argv):
    if len(argv) < 2 or argv[1] in ("-h", "--help", "help"):
        print(__doc__)
        return 0
    cmd = argv[1]
    c = Client()
    init = c.start()
    if cmd == "check":
        info = init.get("serverInfo", {})
        print(f"server: {info.get('title', info.get('name'))} {info.get('version', '')}")
        bad, text = c.call("site_overview", {})
        print(pretty(text))
        return 1 if bad else 0
    if cmd == "tools":
        flt = argv[2].lower() if len(argv) > 2 else ""
        for t in c.rpc("tools/list").get("tools", []):
            if flt and flt not in t["name"].lower() and flt not in t.get("description", "").lower():
                continue
            print(f"{t['name']}: {t.get('description', '').split('. ')[0]}")
        return 0
    if cmd == "describe":
        if len(argv) < 3:
            die("usage: describe <tool>")
        for t in c.rpc("tools/list").get("tools", []):
            if t["name"] == argv[2]:
                print(json.dumps(t, ensure_ascii=False, indent=2))
                return 0
        die(f"no tool {argv[2]} (or this password's scopes hide it)", 1)
    if cmd == "call":
        if len(argv) < 3:
            die("usage: call <tool> [json | @file | -]")
        bad, text = c.call(argv[2], load_args(argv[3] if len(argv) > 3 else None))
        print(pretty(text))
        return 1 if bad else 0
    if cmd == "prompts":
        for p in c.rpc("prompts/list").get("prompts", []):
            args = ", ".join(a["name"] + ("*" if a.get("required") else "") for a in p.get("arguments") or [])
            print(f"{p['name']}({args}): {p.get('description', '')}")
        return 0
    if cmd == "prompt":
        if len(argv) < 3:
            die("usage: prompt <name> [key=value ...]")
        args = dict(kv.split("=", 1) for kv in argv[3:] if "=" in kv)
        res = c.rpc("prompts/get", {"name": argv[2], "arguments": args})
        for m in res.get("messages", []):
            print(m.get("content", {}).get("text", ""))
        return 0
    if cmd == "resources":
        for r in c.rpc("resources/list").get("resources", []):
            print(f"{r['uri']}  {r.get('title', '')}")
        return 0
    if cmd == "read":
        if len(argv) < 3:
            die("usage: read <uri>")
        for x in c.rpc("resources/read", {"uri": argv[2]}).get("contents", []):
            print(x.get("text", ""))
        return 0
    if cmd == "import-html":
        return import_html(c, argv[2:])
    if cmd == "kit-upload":
        return kit_upload(c, argv[2:])
    if cmd == "import-site":
        return import_site(c, argv[2:])
    die(f"unknown command {cmd}; see --help")


KIT_EXTS = {".css", ".js", ".mjs", ".map", ".json", ".txt", ".woff2", ".woff", ".ttf", ".otf", ".eot", ".png", ".jpg",
            ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico", ".mp4", ".webm", ".mp3", ".ogg", ".wav", ".glb", ".gltf",
            ".bin", ".wasm", ".pdf"}


def kit_upload(c, rest):
    if len(rest) < 2:
        die("usage: kit-upload <folder> <kit> [name=...]")
    root, kit = rest[0], rest[1]
    name = next((kv.split("=", 1)[1] for kv in rest[2:] if kv.startswith("name=")), kit)
    files = []
    for d, dirs, names in os.walk(root):
        dirs[:] = sorted(x for x in dirs if not x.startswith(".") and x != "node_modules")
        for n in sorted(names):
            if n.startswith(".") or os.path.splitext(n)[1].lower() not in KIT_EXTS:
                continue
            full = os.path.join(d, n)
            files.append((os.path.relpath(full, root).replace(os.sep, "/"), full))
    if not files:
        die("no web asset files under " + root)
    batch, size, first, sent = [], 0, True, 0

    def flush(commit):
        nonlocal batch, size, first, sent
        args = {"kit": kit, "name": name, "files": batch, "commit": commit}
        if first:
            args["fresh"] = True
        bad, text = c.call("web_builder_kit_upload", args)
        if bad:
            print(text)
            sys.exit(1)
        sent += len(batch)
        print(f"sent {sent}/{len(files)}", file=sys.stderr)
        batch, size, first = [], 0, False
        return text

    for rel, full in files:
        with open(full, "rb") as f:
            raw = f.read()
        if size + len(raw) > 6 * 1024 * 1024 and batch:
            flush(False)
        batch.append({"path": rel, "data": base64.b64encode(raw).decode()})
        size += len(raw)
    print(pretty(flush(True)))
    return 0


def import_site(c, rest):
    if not rest:
        die("usage: import-site <folder> kit=<kit> [status=… site_name=… front_page=false]")
    root, args = rest[0], {}
    for kv in rest[1:]:
        if "=" in kv:
            k, v = kv.split("=", 1)
            args[k] = (v == "true") if k == "front_page" else v
    if not args.get("kit"):
        die("import-site needs kit=<the kit the folder was uploaded as>")
    pages = []
    for d, dirs, names in os.walk(root):
        dirs[:] = sorted(x for x in dirs if not x.startswith(".") and x != "node_modules")
        for n in sorted(names):
            if n.startswith(".") or os.path.splitext(n)[1].lower() not in (".html", ".htm"):
                continue
            full = os.path.join(d, n)
            with open(full, encoding="utf-8") as f:
                pages.append({"path": os.path.relpath(full, root).replace(os.sep, "/"), "html": f.read()})
    if not pages:
        die("no .html files under " + root)
    args["pages"] = pages
    c.timeout = 1800
    print(f"importing {len(pages)} page(s)", file=sys.stderr)
    bad, text = c.call("web_builder_import_site", args)
    print(pretty(text))
    return 1 if bad else 0


def open_measuring_browser(url):
    """Open the import page where it can run: headless Playwright when installed, else the default browser."""
    try:
        from playwright.sync_api import sync_playwright  # optional dependency
    except ImportError:
        import webbrowser
        if webbrowser.open(url):
            print("opened the import page in your browser; keep it open until it says Done", file=sys.stderr)
            return True
        return False
    with sync_playwright() as p:
        b = p.chromium.launch()
        page = b.new_page(viewport={"width": 1280, "height": 900})
        page.goto(url)
        page.wait_for_function("() => /done|failed/.test(document.title)", timeout=120000)
        print(page.title(), file=sys.stderr)
        b.close()
    return True


def import_html(c, rest):
    if not rest:
        die("usage: import-html <file.html> [title=… post_id=… status=… front_page=true] [--no-open]")
    path, opts = rest[0], [a for a in rest[1:] if a != "--no-open"]
    with open(path, encoding="utf-8") as f:
        html = f.read()
    args = {"html": html}
    for kv in opts:
        if "=" in kv:
            k, v = kv.split("=", 1)
            if k in ("post_id", "header_template", "footer_template"):
                args[k] = int(v)
            elif k in ("front_page", "templates"):
                args[k] = v == "true"
            elif k in ("link_map", "font_map", "site", "page"):
                args[k] = load_args(v)
            else:
                args[k] = v
    if "title" not in args and "post_id" not in args:
        args["title"] = os.path.splitext(os.path.basename(path))[0]
    bad, text = c.call("web_builder_import_html", args)
    if bad:
        print(text)
        return 1
    job = json.loads(text)
    url = job.get("import_url")
    if not url:  # method=quick published right away
        print(pretty(text))
        return 0
    print("import_url:", url, file=sys.stderr)
    if "--no-open" in rest or not open_measuring_browser(url):
        print("Open this link in a browser, then press Enter:", url, file=sys.stderr)
        try:
            input()
        except EOFError:
            pass
    for _ in range(6):
        bad, text = c.call("web_builder_import_finish", {"job": job["job"], "wait": 100})
        if bad:
            print(text)
            return 1
        out = json.loads(text)
        if out.get("status") != "waiting_for_browser":
            print(pretty(text))
            return 0
    print("the import page did not report; open the link again and rerun with the same job", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
