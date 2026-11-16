# Playbook — forum / community (Pursae Forum)

Same steps as the server prompt `build_site kind=forum`.

1. **Plan** — `site_overview`. Ask: community topic, who joins, language, sections (categories →
   forums), rules, open registration or invite-only, moderation of first posts, brand color.
2. **Plugins** — ask to turn on Pursae Forum (`pursae-forum`), Pursae Web Builder, Pursae SEO.
3. **Forum**
   - `update_options {group: "forum", settings: {title, base: "forums", registration, require_verify, moderate_first_post, show_members}}`.
   - `forum_create {title, kind: "category"}` for each category, then `forum_create {title, parent: <category id>, description}` for each forum.
   - `forum_create_topic` in the main forum: a welcome post and the rules (the owner's words if given).
   - Google sign-in needs a client id/secret: leave it to the owner (Forums › Google sign-in).
4. **Pages** — Home: hero ("Join the …" → forum index URL from `forum_list`) → features (the main
   forums) → steps (Sign up → Introduce yourself → Ask & share) → quote → faq → cta. Rules / Code of
   conduct (text), About, Contact (form).
5. **Wire** — front page, menu (Home, Forums → index URL, Rules, About, Contact), SEO, `cache_purge`.

## From an HTML design (forum mockups)

The design's pages import as code sections (playbook-pages.md); the forum's own views take the
design's markup as **site templates** — Go html/template, the dot is the view's data:

1. Import one forum mockup with `templates: true, templates_only: true` so the site-wide header and
   footer are the design's. Point the header's links and search form at `/forums/…` (`/forums/login/`,
   `/forums/register/`, search `GET /forums/search/?q=`).
2. `forum_get_template {view}` for each view: its `default` shows the data (`.Groups`, `.Topics`,
   `.Topic`, `.Replies`, `.LeadPost`, `.URLs`, `.Totals`, `.Extra`) and the form fields.
3. `forum_set_template {view: "parts", source}` — shared `{{define}}`s (avatar disc, topic row,
   breadcrumbs, the design's sprite symbols that the header does not carry).
4. `forum_set_template {view, source, main: true}` for index, forum, topic, new-topic, edit, search,
   profile, members, login, register, verify, account: the mockup's `<main>` with its sample rows
   replaced by `range` over the data. Keep every form's `forum_action`, nonce (`.Nonce` /
   `.AuthNonce`), `{{.Traps}}`, `{{.Captcha}}`, field names, and `id="post-{{.ID}}"` on posts. Print
   `.Error` / `.Notice`. Leave out what the forum has no data for (view counts, "solved", online
   counts) instead of inventing numbers.
5. A save is parse- and render-checked and rolled back on error; profile, account and edit render
   only for a signed-in member, so open them in a browser after saving. Set the site language to the
   design's (`/wp/v2/settings` `language`) so times read "3 saat önce", not "3 hours ago".
