# Editor ↔ published parity

The measuring stick of the "editor = published site" work (EDITOR-CANLI plan, K10): a design imported
1:1 as code sections must look and behave the same in the Web Builder editor's canvas as on the
published page.

`parity.mjs` uploads `fixture/` as a design kit and imports `fixture/parity/index.html` as a code page
through the MCP endpoint (`/wp-json/pursae/v1/mcp`, Application Password): `web_builder_kit_upload`,
then `web_builder_import_site` (default; the kit is then also served at the site's root, so the
page-relative `fetch('data/items.json')` works on `/parity/`) or `web_builder_import_html` method `code`
(`--import html`). The page is published, then shot

- on the published page, and
- in the editor's canvas frame (`iframe[data-wb-frame]`) in three modes: `static` (Safe mode: the trust
  prompt answered "Not now"), `live` ("This session only") and `always` ("Always for this design": a
  second editor session must then open Live with no prompt; the trust is revoked with the bottom bar's
  button at the end of the mode, which must put the canvas back in Safe mode),

at 1440 / 768 / 390 px. For the pixel comparison the editor window is grown until the frame is the
device's width at zoom 1 and as tall as the published viewport (900 px), so only content differences
count. A second editor pass in a laptop-sized window (1440 × 900, device scale 2) records what an author
actually gets: the frame's zoom, its viewport, the hero's `100vh`, the canvas backing size.

Per width and mode the report has the share of differing pixels (top of the page, scrolled to the
slider, after the dark-mode toggle was clicked) and functional checks, editor vs published:

| check | what |
|---|---|
| scriptsRan | the inline body script ran |
| moduleAndImportmap | `js/app.js` (module) imported `mini3` through the page's import map |
| sceneDrawn / sceneSizedToContainer | the WebGL canvas has pixels; its backing size is container × devicePixelRatio |
| moduleRelativeAsset | `fetch(new URL('../data/scene.json', import.meta.url))` |
| jsonFetch | `fetch('data/items.json')` (relative to the page) |
| counterDrawn / sliderSlide1 | text and state only the script draws |
| revealVisible | `.reveal` → `.visible` through IntersectionObserver |
| webFont | the kit's `@font-face` loaded |
| hero100vh / fixedHeader / viewportWidth | `100vh` hero = viewport height, `position: fixed` header at the top, the frame's width |
| toggleClick / toggleStored | a click on the theme toggle sets `html.dark` and localStorage |
| sliderDotClick | a click on the second slider dot shows slide 2 |
| themeRememberedOnLoad | with `parity-theme=dark` stored, the early head script applies it on load |
| darkKeptAfterInteract / slideKeptAfterInteract | leaving Interact mode keeps what the clicks changed (`--clicks interact`) |
| alwaysReopensLive / revokeGoesSafe | `always` mode: Live at once in a new session; revoking goes back to Safe mode |

The clicks on the design (theme toggle, slider dot) go through **Interact** mode by default
(`--clicks interact`); `--clicks edit` clicks the canvas as an editor would (the click selects).

Output: `report.json`, `report.html` (side-by-side published / editor / diff images) and `import.json`
in `--out` (default `builder/.work/editor-parity`). Exit 1 when a shot is over `--target` % (default 0.5)
or a check differs; on today's editor that is expected.

Determinism: reduced motion, transitions and animations off, fonts loaded and the page scrolled through
before the shots, a seeded scene without time-based animation, no network (the fixture's font is copied
from `library/themes/archform` at upload time; the fixture itself is a few KB).

## Running it

Against a running Pursae (an administrator with `unfiltered_html`):

```sh
cd builder
npm ci                                   # @playwright/test
node test/editor-parity/parity.mjs --site http://127.0.0.1:8191 --user admin --pass '…' \
  [--setup] [--csp keep|report|enforce|off] [--import site|html] [--widths 1440,768,390] \
  [--modes static,live,always] [--clicks interact|edit] [--out dir] [--kit editor-parity] [--app-password '…'] [--target 0.5]
```

- `--setup` on a fresh site: activates Pursae Web Builder and the Hello Pursae theme (the site import
  needs it).
- `--csp` switches Settings › Security › `csp_mode` before the run (it stays switched).
- Without `--app-password` a throwaway Application Password (full access) is made through the REST API
  with the login and deleted at the end.
- Environment instead of flags: `PARITY_SITE`, `PARITY_USER`, `PARITY_PASS`, `PARITY_APP_PASSWORD`;
  `PW_CHROMIUM` = a Chromium / headless-shell binary (the same variable for `test/live/live-frame.mjs` and
  `test/security/editor-xss.mjs`) when Playwright's own build of the installed
  version is missing (e.g.
  `~/Library/Caches/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-mac-arm64/chrome-headless-shell`).
- `make builder` (scripts/build.mjs) deletes `builder/.work`: copy a report elsewhere before building.

A throwaway site for it (own database and port; the e2e suite does the install the same way):

```sh
docker exec wpgo-db-1 psql -U wpgo -d wpgo -c 'CREATE DATABASE wpgo_parity'
make build
DATABASE_URL='postgres://wpgo:wpgo@127.0.0.1:5432/wpgo_parity?sslmode=disable' PURSAE_ADDR=127.0.0.1:8191 \
  PURSAE_SITE_URL=http://127.0.0.1:8191 PURSAE_HUB=off PURSAE_UPLOADS_DIR=/tmp/parity-uploads PURSAE_WEB_DIR=web \
  bin/pursae serve
# install through the link `bin/pursae setup-token` prints (same env), then run parity.mjs with --setup
```
