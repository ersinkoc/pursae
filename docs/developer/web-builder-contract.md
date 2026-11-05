---
title: Web Builder integration contract
description: The contract between the Pursae Web Builder editor (React) and the Go server — boot config, REST, snapshots, runtime attributes, storage, AI proxy and sanitizer.
weight: 900
section: built-in-plugins
---

This page is the single source of truth for how the Pursae Web Builder editor (the React app from
`github.com/pursae/Builder`, built into `web/web-builder/`) talks to the Go server. Every work
package of the builder integration cites it. It has two code halves that must match the tables
below field for field:

- Go: `internal/webbuilder/contract.go` (package `webbuilder`, types and constants only)
- TypeScript: `builder/host/contract.ts` (the doc model is imported from the pinned upstream
  `@/builder/types`, the rest is declared there)

A change to the contract changes this page and both files in one commit. Incompatible payload
changes bump `APIVersion`; changes to the static markup or the `data-wb-*` attributes bump
`RendererVersion`.

The plugin keeps its identity: slug `golementor`, option `native_golementor`, visible name
"Pursae Web Builder", edit URL `/wp-admin/post?post={id}&action=web-builder`.

## 1. Versions and handshake

| Constant | Value | Meaning |
|----------|-------|---------|
| `APIVersion` / `API_VERSION` | `1` | REST and boot schema |
| `RendererVersion` / `RENDERER_VERSION` | `1` | static markup + runtime attributes |
| `APIHeader` | `X-WB-Api` | sent by the editor on every `web-builder/v2` request |
| `UpstreamTypesSHA` | `22ff4976a1416accd3b7370e176d7206bf390aee` | upstream commit the Go doc types were copied from |

Rules:

- Every editor request to `web-builder/v2` sends `X-WP-Nonce: <wp_rest nonce>` and
  `X-WB-Api: <API_VERSION>`.
- A missing or different `X-WB-Api` answers **409** `wb_api_mismatch` with
  `data: {status: 409, reload: true, apiVersion: <server>}`. The editor stops autosaving, keeps
  the unsaved doc in memory and offers "Reload" (the draft on the server stays).
- Exempt from `X-WB-Api`: `GET fonts/css2` (loaded as a stylesheet; the nonce is in
  `?_wpnonce=`) and `POST forms/…` (public).
- A publish whose `render.renderer` is not the server's `RendererVersion` answers **422**
  `wb_invalid_doc` with an error of code `bad_renderer` (a stale editor bundle).

## 2. Editor boot config: `window.__WEB_BUILDER__`

The editor page (`wbEditor`) writes one `<script>window.__WEB_BUILDER__ = {…}</script>` before the
bundle. The JSON is encoded with `"</"` escaped as `"<\/"`. **No AI key, sealed value or secret is
ever part of it.**

| Field | Type | Notes |
|-------|------|-------|
| `apiVersion` | number | `APIVersion` |
| `rendererVersion` | number | `RendererVersion` |
| `document` | DocumentResponse | same shape as `GET documents/{id}` (§3.1) |
| `user` | `{id, name}` | the current user |
| `rest.root` | string | absolute, ends with `/wp-json/` |
| `rest.namespace` | string | `web-builder/v2` |
| `rest.nonce` | string | `wp_rest` nonce |
| `links.site` | string | home URL, no trailing slash |
| `links.permalink` | string | the post's permalink |
| `links.preview` | string | preview URL (`?preview=true`) |
| `links.exit` | string | logo / Exit target: the post type's list screen |
| `links.editPost` | string | the core editor for this post |
| `links.settings` | string | Settings › Web Builder; `""` without `manage_options` |
| `caps.publish` | bool | `publish_posts`/`publish_pages` for the type; false = publish becomes pending |
| `caps.uploadFiles` | bool | `upload_files` |
| `caps.unfilteredHtml` | bool | `unfiltered_html`: html sections and custom CSS are kept |
| `caps.editOthers` | bool | `edit_others_posts`: may delete others' library items |
| `locale` | string | WordPress locale (`en_US`, `tr_TR`); picks the i18n catalog |
| `fonts.cssUrl` | string | absolute `fonts/css2` URL with `_wpnonce` |
| `fonts.local` | string[] | families already localized under `uploads/web-builder/fonts/` |
| `media.maxUploadBytes` | number | upload size limit |
| `media.types` | string[] | accepted MIME types |
| `siteAssets` | string | absolute `/wp-content/web-builder/site/` |
| `ai` | BootAi | below; always present |

`ai` (BootAi):

| Field | AI off (`WPGO_BUILDER_AI=0`) | AI on |
|-------|------------------------------|-------|
| `enabled` | `false` | `true` |
| `providers` | `[]` | configured providers only (`{id, models:[{id,name}]}`); `[]` when no key is set — the editor then runs only the demo engine and nothing leaves the browser |
| `llmBase` | `""` | absolute `…/wp-json/web-builder/v2/ai/llm/` |
| `imagesUrl` | `""` | absolute `…/wp-json/web-builder/v2/ai/images` |
| `allowCode` | `false` | `false` (AI tools may not write html sections or custom CSS) |

## 3. REST: namespace `web-builder/v2`

Authentication is the core one (`restAuthUser`): cookie + `X-WP-Nonce`, or an Application
Password. Routes exist only while the plugin is active; AI routes only while AI is on. Errors use
the WordPress shape `{"code", "message", "data": {"status", …}}`. Times are RFC 3339 strings.
Request bodies of document routes are capped at **4 MiB** (`413 wb_too_large`, `data.limit`).

| Method | Path | Capability | Body → Response |
|--------|------|------------|-----------------|
| GET | `/documents/{id}` | `edit_post` | — → DocumentResponse |
| PUT | `/documents/{id}/autosave` | `edit_post` | AutosaveRequest → AutosaveResponse |
| DELETE | `/documents/{id}/autosave` | `edit_post` | — → `{deleted: true}` |
| POST | `/documents/{id}/publish` | `edit_post` | PublishRequest → PublishResponse |
| POST | `/documents/{id}/lock` | `edit_post` | LockRequest → LockResponse |
| GET | `/documents/{id}/revisions?page=N` | `edit_post` | — → RevisionSummary[] (newest first, 50 per page, `X-WP-Total`, `X-WP-TotalPages`) |
| GET | `/documents/{id}/revisions/{rid}` | `edit_post` | — → RevisionResponse |
| POST | `/documents/{id}/revisions/{rid}/restore` | `edit_post` | RestoreRequest → DraftState |
| GET | `/library` | `edit_posts` | — → LibraryItem[] |
| POST | `/library` | `edit_posts` | LibraryCreateRequest → LibraryItem (201) |
| DELETE | `/library/{id}` | author, or `edit_others_posts` | — → `{deleted: true}` |
| GET | `/prefs` | logged in, `edit_posts` | — → Prefs |
| PUT | `/prefs` | logged in, `edit_posts` | Prefs (merged; `null` deletes a key) → Prefs |
| GET | `/fonts/css2?family=…&display=swap&_wpnonce=…` | `edit_posts` | — → `text/css` |
| POST | `/forms/{id}/{section}` | public | form fields → FormResponse or 303 |
| GET | `/ai/status` | `edit_posts` | — → AiStatusResponse |
| POST | `/ai/llm/{provider}/{path}` | `edit_posts` | provider-native JSON → provider response / SSE |
| POST | `/ai/images` | `edit_posts` + `upload_files` | AiImageRequest → AiImageResponse |

`{id}` must be a post of an enabled post type (pages by default, posts optional): otherwise
**404** `wb_not_enabled`. Trashed posts answer **404** `wb_not_found`.

### 3.1 Documents

DocumentResponse:

| Field | Type | Notes |
|-------|------|-------|
| `postId` | number | |
| `postType` | string | |
| `status` | string | core status |
| `rev` | number | published builder revision counter; `0` = never published from the builder |
| `doc` | DocState \| null | `null` = new page, the editor starts from its default doc |
| `page` | PageSettings | from the post when there is no doc yet (title, slug, excerpt, SEO noindex) |
| `draft` | DraftState \| null | the **current user's** autosave, only when newer than `doc` |
| `detached` | bool | `posts.content` was edited outside the builder (§6.3) |
| `modified` | string | `posts.modified_at` |
| `renderer` | number | renderer of the stored snapshot |
| `lock` | LockInfo \| null | another user's live lock |

DraftState: `{doc, page, baseRev, savedAt}`.

**Autosave** (`PUT`): body `{doc, page, baseRev}`. Writes the caller's row in
`web_builder_drafts`; the live page is untouched. The client debounces to at least 2 s
(`AutosaveMinMillis`). The doc is validated like a publish (422 on structure errors) and,
without `unfiltered_html`, html sections and `style.css` are stripped. Answers
`{savedAt, rev}`; `rev > baseRev` means someone else published since the editor loaded. Also
refreshes the caller's edit lock.

**Publish** (`POST`) body PublishRequest:

| Field | Type | Notes |
|-------|------|-------|
| `baseRev` | number | the `rev` the editor started from |
| `status` | `publish` \| `draft` \| `pending` \| `private` | without `caps.publish`, `publish`/`private` become `pending` |
| `reattach` | bool | required (`true`) on a detached page, else 409 `wb_detached` |
| `doc` | DocState | source of truth |
| `page` | PageSettings | |
| `render` | RenderResult | §4 |
| `utilityCss` | string | Tailwind `compile()` of `render.classes` |

Server steps, in one transaction:

1. `X-WB-Api` check; `edit_post`; lock check (another live lock → **409** `wb_locked`,
   `data.lock`).
2. `baseRev != rev` → **409** `wb_rev_conflict` with `data: {rev, modified, modifiedBy}`.
3. Validate the doc (§8.1) → **422** `wb_invalid_doc`, `data.errors: ValidationError[]`.
4. Without `unfiltered_html`: drop html sections and every `style.css` from the doc **and** the
   snapshot, and drop `render.css`.
5. Sanitize `render.html` (§8.2) and `utilityCss` + `render.css` (§8.3).
6. Map PageSettings: `title` → post title, `slug` → `post_name`, `description` → excerpt and
   Pursae SEO description, `indexable=false` → Pursae SEO noindex.
7. `posts.content` = fallback HTML generated in Go from the doc (§6.2); store
   `content_hash` = sha256 hex of exactly that string. The core update creates a
   `post_revisions` row; a `web_builder_revisions` row is written for it.
8. Upsert `web_builder_docs` (`rev = rev + 1`, `detached = false`), set `_web_builder=1`,
   delete the caller's draft, purge WPGo Cache for the post.

PublishResponse: `{rev, status, permalink, revisionId, modified, stripped}`; `stripped` lists
what the server removed: `html-sections`, `custom-css`, `markup` (sanitizer changed the
snapshot), `css` (CSS rules dropped). The editor shows a notice when it is not empty.

**Lock**: WordPress format `_edit_lock = "<unix>:<user id>"`, 150 s TTL. `POST lock` with
`{}` takes a free or own lock; `{take: true}` takes another user's lock. Answers
`{lock: null | LockInfo (the other holder), expires}`. The editor refreshes every 60 s; autosave
also refreshes.

**Revisions**: RevisionSummary `{id (post_revisions.id), rev, author, date, title, renderer}`.
RevisionResponse `{id, rev, doc, page}`. **Restore** writes the revision as the caller's draft
(`baseRev` from the body) and returns that DraftState; the editor loads it and the user
publishes (a fresh snapshot needs the browser).

### 3.2 Library and prefs

LibraryItem = SavedComponent `{id, name, section}` + `{author, created}`. `id` is the
`web_builder_library.id` as a decimal **string** (upstream ids are strings). The section is
validated like a doc section; without `unfiltered_html` html sections and `style.css` are
refused/stripped. At most 500 items per site.

Prefs: a JSON object of the user's editor preferences (upstream localStorage keys such as
`pursae-builder:recent-fonts`, `pursae-builder:layout-width`), stored in user meta
`_web_builder_prefs`. Keys match `^[a-z0-9:._-]{1,64}$`; the encoded object is at most 16 KiB.

### 3.3 Fonts

`GET fonts/css2` takes the Google Fonts css2 query (`family=Inter:wght@400;700&display=swap`)
and answers CSS whose `src` URLs are under `/wp-content/uploads/web-builder/fonts/`. Downloads
happen only here (editor users), with a weight allowlist, one in-flight download per family and a
disk cap. Public pages link only fonts that are already local; an anonymous request never
triggers a download.

### 3.4 Media

The core routes: `GET /wp/v2/media?search=&page=&per_page=&media_type=image` and
`POST /wp/v2/media` (multipart field `file`). The host reads `id`, `source_url`, `alt_text`,
`mime_type`, `media_details.{width,height,sizes}`. Media are never stored as `blob:` URLs.

### 3.5 Forms

`POST /forms/{id}/{section}` is public. The schema comes from the **stored** doc (the section
with that id must be a `form` section of a published builder page), never from the request.

| Field (input `name`) | Rule |
|----------------------|------|
| `name` | ≤ 200 chars |
| `email` | valid address, ≤ 254 |
| `phone` | ≤ 50 |
| `message` | ≤ 5000 |
| `date`, `choice` | ≤ 200 |
| `wb_hp` | honeypot; must be empty (non-empty → silently "sent", nothing mailed) |
| `wb_t` | ms between page load and submit, set by the runtime; `< 2000` → treated as spam |
| `cf-turnstile-response`, `g-recaptcha-response` | checked when Pursae Contact Form has a captcha configured |

Body is `application/x-www-form-urlencoded` or `multipart/form-data` (no files), at most 64 KiB.
Per-IP rate limit → **429** `wb_rate_limited` with `Retry-After`. Invalid → **422**
`wb_form_invalid`, `data.fields: {field: "required"|"invalid"|"too_long"}`. Mail goes to the
admin email through the core mailer.

- `Accept: application/json` (runtime) → `200 {ok: true, message}`.
- Otherwise (no JS) → **303** to the page with `?wb_form=sent` (or `?wb_form=error`) and
  `#<section id>`.

## 4. `renderStatic` result

The upstream live renderer (`src/builder/live/`) runs in the editor:
`renderStatic(doc, opts: RenderOptions): RenderResult`.

RenderOptions: `{postId, formAction(sectionId) → absolute URL, siteUrl}` — the renderer reads
no globals.

| Field | Type | Notes |
|-------|------|-------|
| `html` | string | exactly one root `<div class="wb-page …" data-wb-root style="--brand:…">…</div>`; brand/palette CSS variables are on the root; no editor attributes (`data-label`, selection ids), no inline `opacity:0`; ≤ 2 MiB |
| `css` | string | section custom CSS (`style.css`, the word `selector` already replaced by `#<cssId>`); privileged |
| `classes` | string[] | Tailwind candidate classes in `html` |
| `fonts` | FontNeed[] | `{family, weights: number[], italic}` |
| `features` | Feature[] | sorted, unique subset of `menu acc tabs dismiss form motion embed`; empty = no runtime |
| `renderer` | number | `RENDERER_VERSION` |

`hideOn` becomes container-query classes; the root is the `canvas` container
(`.wb-page{container-type:inline-size;container-name:canvas}` in the base CSS).

## 5. `data-wb-*` runtime

The public runtime (`site/runtime.<hash>.js`, IIFE, no dependencies, ≤ 8 KB gzip) is loaded with
`defer` only when `features` is not empty. The base CSS (`site/wb.<hash>.css`) is linked on every
builder page. **Content must be readable with JavaScript off**: every hiding rule is scoped to
`.wb-js`, a class the page sets on `<html>` with a one-line inline script in `<head>` only when
the runtime will load. The runtime honours `prefers-reduced-motion: reduce` (no animation,
everything shown at once). All storage access is wrapped in try/catch.

| Feature | Markup | Behaviour (with JS) | Without JS |
|---------|--------|---------------------|------------|
| `menu` | `<details data-wb-menu><summary aria-label="Menu">…</summary><div>links</div></details>` (narrow-width markup; wide links are shown by container query) | closes on Escape, outside click and link click; syncs `aria-expanded` on the summary | native `<details>` toggle |
| `acc` | `<details data-wb-acc>` items, `<summary>` question; first item `open`; the group element carries `data-wb-acc-single` when only one may be open | closes siblings in a `data-wb-acc-single` group | native `<details>` |
| `tabs` | container `[data-wb-tabs]`; buttons `role=tab data-wb-tab="<id>" aria-controls aria-selected`; panels `role=tabpanel data-wb-panel="<id>"`; inactive panels carry `data-wb-inactive`; optional `data-wb-autoplay="<ms>"` on the container (slides) | switches panels, arrow/Home/End keys, autoplay paused on hover/focus and off with reduced motion | `.wb-js` scoping: all panels visible, stacked |
| `dismiss` | element `[data-wb-dismiss="<key>"]` containing a `button[data-wb-dismiss-btn]` | hides the element and remembers it in `sessionStorage["wb-dismiss:<postId>:<key>"]` | stays visible |
| `form` | `<form data-wb-form method="post" action="<formAction>">`, inputs named per §3.5, a visually hidden `wb_hp` input (`tabindex=-1 autocomplete=off`), a hidden `wb_t`, an `[data-wb-form-status]` element with `aria-live="polite"` | fills `wb_t`, posts with `fetch` + `Accept: application/json`, sets `data-wb-state="sending|sent|error"` on the form, shows the message | plain POST, 303 back |
| `motion` | `data-wb-motion="<entrance>"` plus optional `data-wb-dur`, `data-wb-delay` (ms), `data-wb-ease`, `data-wb-repeat`, `data-wb-stagger="<ms>"` (children `[data-wb-stagger-item]`), `data-wb-parallax="-100…100"` + `data-wb-parallax-on="bg|content"`, `data-wb-scroll-fade`, `data-wb-scroll-scale` | adds `wb-in` when in view (IntersectionObserver), scroll effects on rAF | visible, static |
| `embed` | `<iframe data-wb-embed="<id>" sandbox="allow-scripts" srcdoc="…">` (html sections); the srcdoc posts `{__wb: "<id>", h: <px>}` to the parent | sets the iframe height from messages whose `__wb` matches and whose source is that iframe | default height |

CSS-only, no runtime needed: `data-wb-sticky` (position: sticky) and `data-wb-hover="lift|glow|scale"`
+ `data-wb-hover-on="self|items"` (styles in the base CSS). Entrance values are the upstream
`EntranceId`s (`fade`, `fade-up`, `zoom-in`, …); easing the upstream `EasingId`s. A failsafe
in the base CSS reveals `.wb-js [data-wb-motion]:not(.wb-in)` after 3 s if the runtime never runs.

## 6. Storage

### 6.1 Migration `0027_web_builder.sql`

The next free number after `0026_plugins_source.sql`. Idempotent (`IF NOT EXISTS`), like the
other migrations; existing migration files are never edited.

```sql
-- Pursae Web Builder: the page doc (source of truth), its sanitized snapshot, per-user drafts,
-- doc revisions next to the core post revisions, saved components and AI usage.
CREATE TABLE IF NOT EXISTS web_builder_docs (
	post_id             bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
	doc                 jsonb   NOT NULL,
	page                jsonb   NOT NULL DEFAULT '{}',
	html                text    NOT NULL DEFAULT '',   -- sanitized snapshot (RenderResult.html)
	css                 text    NOT NULL DEFAULT '',   -- sanitized utility CSS + section CSS
	fonts               jsonb   NOT NULL DEFAULT '[]', -- FontNeed[]
	features            text[]  NOT NULL DEFAULT '{}',
	renderer            integer NOT NULL DEFAULT 0,
	rev                 integer NOT NULL DEFAULT 0,
	content_hash        text    NOT NULL DEFAULT '',   -- sha256 hex of the posts.content we wrote
	detached            boolean NOT NULL DEFAULT false,
	snapshot_user       bigint REFERENCES users(id) ON DELETE SET NULL,
	snapshot_unfiltered boolean NOT NULL DEFAULT false,
	created_at          timestamptz NOT NULL DEFAULT now(),
	updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS web_builder_drafts (
	post_id    bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	user_id    bigint  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	doc        jsonb   NOT NULL,
	page       jsonb   NOT NULL DEFAULT '{}',
	base_rev   integer NOT NULL DEFAULT 0,
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS web_builder_revisions (
	revision_id bigint  PRIMARY KEY REFERENCES post_revisions(id) ON DELETE CASCADE,
	post_id     bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	rev         integer NOT NULL,
	doc         jsonb   NOT NULL,
	page        jsonb   NOT NULL DEFAULT '{}',
	renderer    integer NOT NULL DEFAULT 0,
	created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_builder_revisions_post ON web_builder_revisions (post_id, rev DESC);

CREATE TABLE IF NOT EXISTS web_builder_library (
	id         bigserial PRIMARY KEY,
	name       text   NOT NULL,
	data       jsonb  NOT NULL,                           -- Section
	author_id  bigint REFERENCES users(id) ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

-- AI proxy: per-user, per-model token counts per UTC day (rate limit and daily budget).
CREATE TABLE IF NOT EXISTS web_builder_ai_usage (
	day           date   NOT NULL,
	user_id       bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider      text   NOT NULL,
	model         text   NOT NULL,
	requests      integer NOT NULL DEFAULT 0,
	input_tokens  bigint  NOT NULL DEFAULT 0,
	output_tokens bigint  NOT NULL DEFAULT 0,
	PRIMARY KEY (day, user_id, provider, model)
);
```

Not in `posts.meta`: that column is read with every post and carries a GIN index, so 2 s
autosaves there would be costly. The post only gets the marker meta `_web_builder = "1"`, so
list screens can show "Pursae Web Builder" cheaply. Old builder data is removed by a separate
migration (`0028_drop_old_builder.sql`, package R3); 0027 does not touch it.

### 6.2 Fallback HTML (`posts.content`)

Generated in Go from the doc (`internal/webbuilder`), never from the snapshot. Semantic and
readable: per visible section, headings (`h1` for the first hero title, `h2` for section titles,
`h3` for items), paragraphs, lists, `<img>` with alt, links and buttons as `<a>`, quotes as
`<blockquote>`, FAQ as `<h3>` + `<p>`; nav and footer link rows as `<ul>`. Forms, html sections,
decor and motion are omitted. It is what REST `content.rendered`, Pursae Headless, feeds, search,
sitemaps and the theme fallback (plugin off or detached) show.

### 6.3 Drift (detached)

A save from outside the builder (block or classic editor, `/wp/v2/posts`) that makes
`sha256(posts.content) != content_hash` sets `web_builder_docs.detached = true`. The front then
renders `posts.content` in the theme; the editor shows "Edited outside the builder" with two
choices: publish with `reattach: true` (discard the outside edits) or leave the builder for this
page.

## 7. Assets and build output

| URL prefix | Served from | Rules |
|------------|-------------|-------|
| `/wp-content/plugins/web-builder/dist/editor/` | `web/web-builder/editor/` | AI build; **404 when `WPGO_BUILDER_AI=0`**; plugin active only |
| `/wp-content/plugins/web-builder/dist/editor-noai/` | `web/web-builder/editor-noai/` | no-AI build (no provider strings, no `/ai/` paths); plugin active only |
| `/wp-content/web-builder/site/` | `web/web-builder/site/` | public `runtime.<hash>.js` and `wb.<hash>.css`; `Cache-Control: public, max-age=31536000, immutable` |
| `/wp-content/uploads/web-builder/css/` | uploads | page CSS above 24 KiB: `post-<id>-<rev>.css` |
| `/wp-content/uploads/web-builder/fonts/` | uploads | localized Google Fonts |

None of these overlap the old builder's `/wp-content/plugins/web-builder/assets/`.

`web/web-builder/manifest.json` (Manifest; paths relative to each bundle directory):

```json
{
  "editor":      { "js": "assets/index-3f9a1c.js", "css": ["assets/index-8b2e4d.css"] },
  "editor-noai": { "js": "assets/index-77c0aa.js", "css": ["assets/index-8b2e4d.css"] },
  "site":        { "runtime": "runtime.1d2e3f.js", "css": "wb.4a5b6c.css" }
}
```

`web/web-builder/BUILD.json` (BuildInfo):
`{"upstreamSha": "<40 hex>", "patches": {"0001-host-seam.patch": "<sha256>", …}, "apiVersion": 1, "rendererVersion": 1}`.
A Go test compares it with `builder/UPSTREAM`, the patch files and the constants in
`contract.go`.

## 8. Trust: validation and sanitizer allowlist

The snapshot and the CSS come from the browser and are **untrusted**. The doc is the source of
truth. Allowlists are the Go variables in `contract.go`; the policy in `internal/webbuilder` is
built from them.

### 8.1 Doc validation

| Rule | Limit / allowed |
|------|-----------------|
| Request body | ≤ 4 MiB |
| Top-level sections | ≤ 200 |
| Items per section | ≤ 500 |
| `Item.sub` nesting | ≤ 4 |
| Ids (section, item, saved component) | `^[A-Za-z0-9_-]{1,64}$`, unique per doc |
| `kind` | `nav hero banner features media steps quote faq cta footer text image form layout html custom` |
| Strings | ≤ 64 KiB; `content.html` of a `html` section ≤ 256 KiB |
| Rich text in content strings | upstream rich.ts subset: `b i u s code a(href) br ul ol li` |
| `href` | `http`, `https`, `mailto`, `tel`, `#…`, relative |
| Image URLs (`image`, `bgImage`, item image) | `http`, `https`, relative, `data:image/*` ≤ 32 KiB |
| `bg.decorSvg` | sanitized to the SVG subset below |
| CSS strings (`style.css`, `els.*.bgImage`, `border`, `shadow`) | no `</style`, CSS rules of §8.3 |
| Privileged (`unfiltered_html` only) | `html` sections, `style.css` |

Enum fields with unknown values are a 422 `bad_value`. Fields the Go types do not know are kept
as sent (the stored doc is the client JSON with privileged fields removed at the JSON level).

### 8.2 Snapshot HTML

- Elements: `div section header footer nav main article aside h1–h6 p span a strong b em i u s
  small mark sub sup br hr ul ol li dl dt dd blockquote q cite figure figcaption img picture source
  video button form input textarea select option label fieldset legend details summary table thead
  tbody tfoot tr th td caption time address code pre kbd iframe` and the SVG subset `svg g path
  circle ellipse line polyline polygon rect defs linearGradient radialGradient stop clipPath mask
  use title desc symbol text tspan`.
- Global attributes: `class style id title role lang dir hidden tabindex`, `aria-*`,
  `data-wb-*`. Every other `data-*` and every `on*` attribute is dropped.
- Per element: see `ElementAttrs` / `SVGAttrs` in `contract.go`. `input type` ∈
  `text email tel date hidden checkbox radio submit`.
- `a target` only `_blank`, which forces `rel="noopener noreferrer"`.
- `form action` only the site's own `web-builder/v2/forms/{this post}/{section}` URL,
  `method="post"`.
- `style` attributes go through the CSS sanitizer.
- URLs: `href` per §8.1; `src`/`srcset`/`poster` `http`, `https`, relative or `data:image/*`
  (≤ 32 KiB); SVG `href`/`xlink:href`, `clip-path`, `mask` only same-document `#id` /
  `url(#id)`.
- `iframe`: either `src` on `www.youtube-nocookie.com`, `www.youtube.com`, `player.vimeo.com`
  (https, embed paths) for every author, or `sandbox="allow-scripts"` + `srcdoc` (never
  `allow-same-origin`) from `unfiltered_html` authors; anything else is dropped.
- Dropped with their content: `script style link meta base object embed applet frame frameset
  noscript template foreignObject animate animateMotion animateTransform set math portal`.
- Exactly one root `div.wb-page[data-wb-root]`; anything else → 422.
- Comparison with fixtures is by DOM equivalence, not bytes (there is no hydration, so
  normalization is harmless).

### 8.3 CSS

- At-rules kept: `@media @container @supports @keyframes @font-face @layer @property`; all
  others (`@import`, `@charset`, `@namespace`, …) are dropped.
- A declaration is dropped when, after unescaping, it contains `expression(`, `behavior`,
  `-moz-binding`, `javascript:`, `vbscript:`, `-o-link` or `@import`.
- `url()` only `http`, `https`, relative, `#id`, or `data:image/*` ≤ 32 KiB.
- `@font-face src` only under `/wp-content/uploads/web-builder/fonts/`.
- `</style` anywhere → 422 `bad_css`. Total CSS ≤ 1 MiB.
- Pages ≤ 24 KiB of CSS inline it in `<style id="wb-css">`, larger ones get a file (§7).

## 9. AI

### 9.1 Environment (read once by `internal/config` as `Config.BuilderAI` and friends)

| Variable | Values | Effect |
|----------|--------|--------|
| `WPGO_BUILDER_AI` | unset, `""`, `1`, `true`, `on`, `yes` → **on**; `0`, `false`, `off`, `no` → **off**; anything else → off with a startup warning | master switch |
| `WPGO_BUILDER_AI_ANTHROPIC_KEY` | API key | enables provider `anthropic` |
| `WPGO_BUILDER_AI_OPENAI_KEY` | API key | enables `openai` |
| `WPGO_BUILDER_AI_GEMINI_KEY` | API key | enables `google` (Gemini, OpenAI-compatible endpoint) |
| `WPGO_BUILDER_AI_MISTRAL_KEY` | API key | enables `mistral` |
| `WPGO_BUILDER_AI_OLLAMA_URL` | `http(s)://host:port` | enables `ollama` at that base |
| `WPGO_BUILDER_AI_MODELS` | `provider:model,provider:model` | model allowlist; unset = defaults (`DefaultAIModels`) |
| `WPGO_BUILDER_AI_DAILY_BUDGET` | integer tokens/day (input + output, site-wide, UTC day) | unset = 1 000 000; `0` = no cap |

**AI off (`=0`)**: only `editor-noai` is served and `dist/editor/` answers 404; no
`/web-builder/v2/ai/*` route is registered (404); no AI settings UI; no key is read; zero outbound
requests; `ai.enabled=false`; Site Health shows "AI off".

**AI on, no key**: the editor bundle with AI is served, `ai.providers = []`, the client runs only
its rule-based demo engine and nothing leaves the server or the browser.

Keys live only in the environment. They are never sent to the browser, logged or echoed.

### 9.2 `POST /ai/llm/{provider}/{path}`

- `{provider}` ∈ `anthropic openai google mistral ollama` with a key/URL configured, else
  **503** `wb_ai_no_key` (**404** for an unknown provider).
- `{path}` must be in the provider allowlist, else **400** `wb_ai_path`:

  | Provider | Fixed base | Paths |
  |----------|------------|-------|
  | `anthropic` | `https://api.anthropic.com` | `v1/messages` |
  | `openai` | `https://api.openai.com` | `v1/chat/completions` |
  | `google` | `https://generativelanguage.googleapis.com` | `v1beta/openai/chat/completions` |
  | `mistral` | `https://api.mistral.ai` | `v1/chat/completions` |
  | `ollama` | `WPGO_BUILDER_AI_OLLAMA_URL` | `v1/chat/completions` |

- Request body: the provider-native JSON the upstream `ai/llm.ts` already builds (Anthropic
  Messages or OpenAI chat completions), ≤ 8 MiB. `model` must be in the allowlist (**422**
  `wb_ai_model`); `max_tokens` is capped at 8192. Browser-sent `authorization`, `x-api-key`,
  `anthropic-*` and `cookie` headers are dropped; the server adds the key
  (`x-api-key` + `anthropic-version: 2023-06-01` for Anthropic, `Authorization: Bearer` for the
  others, none for Ollama). Upstream's `anthropic-dangerous-direct-browser-access` header is
  never sent.
- Response: without `"stream": true` the provider's JSON is passed through with its status.
  With `"stream": true` the response is `Content-Type: text/event-stream`, the provider's SSE
  bytes passed through unchanged and flushed per event (`data: …\n\n`; OpenAI-style streams end
  with `data: [DONE]`). Usage is read from the JSON `usage` or from the final SSE events and
  added to `web_builder_ai_usage`.
- Provider `401`/`403` become **502** `wb_ai_upstream` ("the provider rejected the server key");
  other provider errors pass through. Network failure → **502** `wb_ai_upstream`; timeout
  (120 s total, 30 s to first byte) → **504** `wb_ai_timeout`. Redirects are never followed.
- Per-user rate limit (default 30 requests/minute) → **429** `wb_rate_limited`;
  daily budget spent → **429** `wb_ai_budget`. Each call is written to the audit log
  (`web_builder.ai`: user, provider, model, tokens; never the prompt or key).
- Ollama: the base comes only from the environment (operator-trusted) and the request never
  chooses a host. Loopback and private ranges are allowed (Ollama usually runs there);
  link-local and cloud metadata addresses (`169.254.0.0/16`, `fe80::/10`, `fd00:ec2::254`) are
  refused even if configured.

`GET /ai/status` → `{enabled, providers, budget: {daily, used}}`.

### 9.3 `POST /ai/images`

Body/response match upstream `ai/images.ts` (`ImageQuery` → `ImageResult`) plus `mediaId`:
`{query, count? (1–12, default 6), orientation?}` → `{images: [{url, mediaId, width, height,
alt, credit, license, source}], error?}`. Searches Openverse and Wikimedia only
(`api.openverse.org`, `commons.wikimedia.org`, `upload.wikimedia.org`), image MIME types only,
≤ 10 MiB per file; results are sideloaded into the Media Library and `url` is the local URL.
Site cloning is not ported (no endpoint; the tool reports itself unavailable).

## 10. Front-end page (for reference)

No React for visitors. A builder page is a canvas document: `<!doctype html>`, `lang`, the
non-theme head (SEO, analytics, cookie, plugin heads), page CSS (inline ≤ 24 KiB or file), local
`@font-face` with a preload of the first face, the base CSS, the `.wb-js` inline script when
`features` is not empty, the body-open hooks, the stored snapshot (images through
`filterContentTags`), `<script defer src="…/site/runtime.<hash>.js">` when `features` is not
empty, the admin bar and the footer hooks. Plugin off or detached → the theme renders
`posts.content`.
