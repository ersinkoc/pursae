# HeadlessGo: feature inventory

HeadlessGo is a native (compiled-in) WordPress Go plugin that turns the site into a **headless CMS**:
a Content-Type Builder, a Content Manager and a delivery API for front-end apps (Next.js, Nuxt,
Astro, mobile apps…). Its model and query language follow the conventions of the popular open-source
headless CMSs (collection types, single types, components, dynamic zones, `filters[field][$op]`,
`populate`, `documentId`), so front-end code written for them needs few changes. Like every native
plugin it is **off on a fresh install** (`native_headlessgo`); while it is off its routes answer 404,
its menu is gone and the front end renders as usual. Schemas, entries, tokens and webhooks stay in
the database when it is deactivated.

Legend: **✓** implemented · **≈** implemented differently (reason given) · **✗** left out (reason given).

## 1. Storage

- **Schemas** (types and components) are JSON documents in `hg_schemas` (migration
  `0020_headlessgo.sql`). The registry is cached per server and dropped on every schema write.
- **Entries** of every type live in one table, `hg_entries`: one row per document and locale, with
  `data` (the draft: the latest saved values) and `published_data` (the published snapshot, NULL
  while unpublished) as JSONB, plus typed columns `document_id`, `type_uid`, `locale`,
  `published_at`, `created_at`, `updated_at`, `created_by`, `updated_by`. GIN indexes on both JSONB
  columns serve the unique checks and `@>` lookups.
- **No DDL per content type.** Schema changes migrate the stored data instead (§3).
- Relations and media are stored as lists of ids whatever their cardinality (document ids of
  entries, numeric ids of posts, pages, users and attachments); components and dynamic zones as
  nested objects (dynamic zone items carry `__component`, component values an `id`).
- Settings: the option `headlessgo_settings` (headless mode, CORS, public permissions, preview
  secret, page sizes, GraphQL switch). API tokens: `hg_api_tokens` (SHA-256 only). Webhooks:
  `hg_webhooks`, deliveries `hg_webhook_deliveries` (last 200 per webhook).

## 2. Headless mode

| Feature | Status |
|---|---|
| While active the front end does not render: JSON 404 `{"data":null,"error":{"status":404,"name":"NotFoundError",…}}` | ✓ |
| Setting: plain-text 404, or a 302 to the front-end app's URL (optionally keeping path and query), or keep the site's front end | ✓ |
| wp-admin, wp-login, `/wp-json` (core REST and `?rest_route=`), `/wp-content/` (uploads, assets), `/wp-includes/`, wp-cron, robots.txt and the favicon keep working | ✓ |
| Logged-in users who can edit posts still see the front end (setting, on by default) so previews and theme work go on | ✓ |
| Runs in `Server.Handler` before the page cache (`headlessServe`); answers are `no-store` and `noindex` | ✓ |

## 3. Content-Type Builder

| Feature | Status |
|---|---|
| Collection types (many entries), single types (one entry), components (category.name), dynamic zones | ✓ |
| Field types: text (short/long), rich text (blocks or Markdown), number (integer, big integer, decimal, float), boolean, date, date-time, time, email, password, enumeration, media (single/multiple, allowed types), relation, JSON, UID (derived from a text field), component (single/repeatable), dynamic zone | ✓ |
| Relations: one-to-one, one-to-many, many-to-one, many-to-many, to content types (self included) and to WordPress posts, pages and users | ✓ (one-way; ≈ no inverse "mappedBy" side: the relation lives on the owning type and is filtered/populated from there) |
| One-to-one / one-to-many exclusivity (a target belongs to one entry) | ✓ |
| Options: required, unique, min/max (length, value or item count), default, private (never in the API), regex (text, UID, email) | ✓ |
| Type options: draft & publish, internationalization, preview URL template, description | ✓ |
| Builder page: type list, settings, field table with edit / delete / reorder, add-field panel with a type picker and per-type options | ✓ |
| Validation: kebab-case API ids, unique singular/plural ids, reserved names (`id`, `documentId`, `locale`, …), valid targets, no component cycles, no dynamic zone in components | ✓ |
| Schema changes migrate data: renamed fields (the builder sends `renamedFrom`) move their values; removed fields are dropped — in the type's entries, or inside every component value when a component changes (also nested and in dynamic zones); i18n on/off converts locales; draft & publish off publishes every draft | ✓ |
| Delete: a type (with its entries) or a component; refused while another schema uses or relates to it | ✓ |
| Export all schemas as JSON; import (file or paste): components first in dependency order, then types in two passes so they may relate to each other | ✓ |
| Changing a field's type converts stored values | ✗ values stay; they are re-validated on the next save |

## 4. Content Manager

| Feature | Status |
|---|---|
| Home with a card per type (entry counts, API route); side navigation | ✓ |
| Entries list: search (text fields and document id), status filter (draft / published / modified), locale filter, sortable columns, pagination | ✓ |
| Edit form generated from the schema for every field type: block editor for rich text (paragraph, headings, quote, code, lists, image; untouched blocks keep their inline formatting), media picker (WordPress media library), relation picker with search, nested components and dynamic zones with add / remove / reorder / collapse, JSON editor with validation, UID regenerate | ✓ |
| Validation per field options; a rejected save shows the errors next to the fields and keeps the input (passwords are never echoed back) | ✓ |
| Draft & publish: Save draft, Publish (validates required fields), Unpublish, Discard changes (back to the published version), status badges | ✓ |
| i18n: with Golylang active its languages are the locales; each document has one version per locale, linked by `documentId`; the edit screen lists the locales, opens a translation empty or filled from the current locale. With Golylang off there is one default locale (setting) | ✓ |
| Delete one locale or every locale | ✓ |
| Bulk actions, history / versions, editing lock | ✗ not in this version |

## 5. Delivery REST API — `/wp-json/headlessgo/v1`

| Feature | Status |
|---|---|
| `GET/POST /{pluralApiId}`, `GET/PUT/PATCH/DELETE /{pluralApiId}/{documentId}`, single types `GET/PUT/DELETE /{singularApiId}` | ✓ |
| `POST …/{documentId}/publish` and `/unpublish` (needs `update`) | ✓ (extension) |
| Response `{data, meta}`; entries carry `id`, `documentId`, attributes, `createdAt`, `updatedAt`, `publishedAt`, `locale`; errors `{data:null, error:{status, name, message, details}}` with `ValidationError` details `{errors:[{path, message}]}` | ✓ |
| `filters[field][$eq|$eqi|$ne|$nei|$lt|$lte|$gt|$gte|$in|$notIn|$contains|$notContains|$containsi|$notContainsi|$startsWith(i)|$endsWith(i)|$null|$notNull|$between]`, `$and`, `$or`, `$not`, implicit `$eq` | ✓ |
| Filtering through relations (`filters[author][name][$eq]`, nested), components (single and repeatable), media (`id`, `name`, `mime`), WordPress pages/posts (`id`, `slug`, `title`, `status`) and users (`id`, `username`, `slug`, `name`); `$null` on relations, media, components | ✓ |
| `sort=a:asc,b:desc`, `sort[0]=…`, `sort[a]=desc` on scalar attributes and system fields | ✓ (sorting through relations ✗) |
| `pagination[page]&pagination[pageSize]` or `pagination[start]&pagination[limit]` (mixing is a 400), `pagination[withCount]=false`; default/max page size are settings (25/100) | ✓ |
| `populate=*` (one level), `populate=a,b`, `populate=a.b`, `populate[a][populate]…`, `[fields]`, `[filters]`, `[sort]`, dynamic zones `populate[zone][on][comp.uid]…`, `populate=localizations` | ✓ |
| `fields[0]=title` / `fields=title,slug` | ✓ |
| `locale=` (default: the default locale), `status=draft|published` (drafts need a token allowed to read them) | ✓ |
| Writes: `{"data": {...}}`; `?status=published` publishes, `?locale=` writes that locale's version (a new translation of an existing document); partial updates; relations as id, list or `{connect, disconnect, set}`; media as id or list | ✓ |
| Password fields: hashed (argon2id), never returned; an empty value keeps the hash | ✓ |
| Private fields: never returned, not filterable or sortable, but writable | ✓ |
| WordPress content: posts, pages and media are in the core REST API (`/wp-json/wp/v2/posts`, `/pages`, `/media`) and stay reachable in headless mode; HeadlessGo adds the missing menus: `GET /wp/menus` (menus and theme locations) and `GET /wp/menus/{id|location|slug}` (published items as a tree with resolved URLs) | ✓ |
| `GET /` lists the routes the caller may read | ✓ |
| `?rest_route=/headlessgo/v1/…` | ✓ |

## 6. Permissions and API tokens

| Feature | Status |
|---|---|
| Public role (no token): a matrix of type × find / findOne / create / update / delete under Settings. **Default: nothing is public.** Public callers never see drafts | ✓ |
| API tokens: read-only (find/findOne on every type), full access (everything, drafts included), custom (the same matrix per token, plus "may read drafts") | ✓ |
| Duration 7 / 30 / 90 days or unlimited; expired tokens get 401; last-used time (recorded at most once a minute); regenerate; delete | ✓ |
| Only a SHA-256 of the token is stored; it is shown once (on the response page, never in a URL) with its last four characters in the list | ✓ |
| `Authorization: Bearer <token>`; a bad token is 401 `UnauthorizedError`, a missing permission 403 `ForbiddenError` | ✓ |
| Populated relations to a type the caller may not read are left out (not an error) | ✓ |
| Cookie / Application Password auth on this namespace | ✗ tokens only (the admin uses its own screens) |

## 7. CORS

- Allow-list of origins (one per line, `*` for any) under Settings. Allowed origins get
  `Access-Control-Allow-Origin` (the origin itself) and `Vary: Origin`; preflight `OPTIONS` requests
  are answered with 204, the methods and the `Authorization, Content-Type, Accept, X-Preview-Token`
  headers, and a one-day `Max-Age`. Other origins get no CORS headers. Applies to the HeadlessGo
  namespace only. ✓

## 8. Webhooks

| Feature | Status |
|---|---|
| Events `entry.create`, `entry.update`, `entry.publish`, `entry.unpublish`, `entry.delete`, and `schema.update` (setting) | ✓ |
| Per webhook: name, URL, events, custom headers (reserved ones refused), signing secret, enabled | ✓ |
| Payload `{event, createdAt, model, uid, entry}` (public scalar attributes; private fields never sent) | ✓ |
| `X-HeadlessGo-Signature: sha256=<hex HMAC-SHA256 of the body>`, `X-HeadlessGo-Event`, `X-HeadlessGo-Delivery` | ✓ |
| Sent in the background; three attempts (now, +5 s, +30 s) on a network error or a non-2xx answer | ✓ |
| Delivery log per webhook: date, event, attempt, status code, duration, the first 1 KB of the response or the error | ✓ |
| "Send test" (`trigger-test`, synchronous, result shown as a notice) | ✓ |
| Durable queue surviving a restart | ✗ retries live in memory |

## 9. Preview

- Per type a preview URL template with `{documentId}`, `{locale}`, `{status}`, `{secret}`,
  `{previewToken}`, `{uid}`, `{pluralApiId}`, `{singularApiId}` and any scalar field (`{slug}`); values
  are URL-encoded. The edit screen shows **Open preview**. ✓
- The preview secret (Settings, regenerable) is the `{secret}` front ends compare; it also keys the
  preview tokens. ✓
- `{previewToken}`: a signed, short-lived token (lifetime setting, 60 min by default) that reads one
  document's draft: `findOne` of that document, or `find` restricted to it, with `status=draft`. Sent
  as `Authorization: Bearer`, `X-Preview-Token` or `?previewToken=`. Rotating the secret revokes every
  preview token. ✓

## 10. GraphQL (read-only)

- `POST|GET /wp-json/headlessgo/v1/graphql` with `{query, variables}`. Root fields: `articles`
  (camelCase plural id), `article(documentId:)`, single types (`homepage`), `articles_connection { nodes
  pageInfo { total page pageSize pageCount } }`. Arguments `filters` (operators without `$`), `sort`,
  `pagination`, `locale`, `status`. Selections drive populate; aliases, variables, `__typename` and
  inline fragments on dynamic zones (`... on ComponentBlocksQuote`) work. Same permissions as REST. ✓
- Mutations, named fragments, directives and introspection ✗ (a minimal endpoint; switch it off under
  Settings).

## 11. Not built / limits

- Media upload through the delivery API (use the core `/wp-json/wp/v2/media` with an Application
  Password) ✗.
- Inverse relation fields, sorting by related fields, relation ordering beyond the stored order ✗.
- Content history, scheduled publishing, review workflows, bulk actions in the list ✗.
- Admin strings in the JavaScript editor/builder are English only (the server-rendered admin goes
  through the i18n layer).
