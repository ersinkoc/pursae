---
title: Forum themes (Pursae Forum)
description: "How to make a theme for Pursae Forum, as a package or with Pursae Web Builder: the forum.html wrapper, view overrides and their data, template functions, CSS tokens and classes, demo content and testing."
weight: 85
section: themes
---

[Pursae Forum](../../forum-features.md) draws every forum page (the index, forums, topics, profiles,
the member list, login, sign-up, account) inside the **active theme**. Any theme shows the forum
without changes. A theme made for the forum can go further, one step at a time:

| Step | What you write | What you get |
|------|----------------|--------------|
| 1. Nothing | — | The forum is drawn in your `page.html` chain with its own stylesheet (`forum.css`) |
| 2. Colours and type | `--pf-*` tokens in `style.css` | Every forum view uses your palette, fonts and corner radius |
| 3. Wrapper | `supports: {"pursae-forum": true}` and `templates/forum.html` | Your own page layout for forum views (a sidebar, a wide column). The forum prints its own title and breadcrumbs |
| 4. Theme widgets | `forum_user_menu`, `forum_recent_topics`, `forum_stats`, `forum_url` in the header, front page and sidebar | Log in / Join in the header, the latest topics on the home page |
| 5. View overrides | `templates/forum/<view>.html` | Your own markup for a view (for example, category cards on the forum home) |
| 6. Demo | a `"forum"` section in `demo/demo.json` | One click fills the site with members, forums, topics and replies |

Themes for Pursae Web Builder (Hello Pursae with Theme Builder parts) do steps 2 and 4 through the
parts file. See [Web Builder themes](#web-builder-themes).

The four forum themes in the library are working examples of everything on this page. See
[The shipped forum themes](#the-shipped-forum-themes).

## How a forum page is built

1. The request matches a forum URL (`/<base>/…`, default base `forums`).
2. The view loads its data into a `forumPage` (the *dot*, below).
3. The view body is rendered from the active theme's `templates/forum/<view>.html` when the theme
   has one, otherwise from the plugin's own template in `web/forum/templates/`.
4. The body becomes the content of a **synthetic page** (`.Q.Kind` `page`, post ID 0). The theme
   renders that page like any other: the page's own template `forum` (`templates/forum.html`), then
   `page.html`, `singular.html`, `index.html`.

What the theme sees in its site view on a forum page:

| Field | Value |
|-------|-------|
| `.Post.Title` | the view's title: the forum, topic or member name; "Log in", "Members", … |
| `.Post.Content` | the forum body (already HTML) |
| `.DocumentTitle` | `<view title> – <site name>` |
| `.BodyClass` | the page classes plus `pursae-forum` and `pf-view-<view>` (`pf-view-index`, `pf-view-topic`, …) |
| `forum_is_view .` | `true` |

The core prints the canonical URL (with `/page/N/` on later pages), `noindex` on private views, and
`forum.css` in `.Head`. The theme does not query anything.

## Declaring support

```json
"supports": {
  "pursae-forum": true,
  "title-tag": true
}
```

With the support declared, `.OwnTitle` is `true` on every forum view. The forum body then prints
its own `<h1 class="pf-title">`, breadcrumbs and **New topic** button, and your wrapper should not
print `.Post.Title`. Without it (every bundled Go theme, including Twenty Twenty-Five and Hello
Pursae), the theme prints the title as for any page and the body prints no H1.

## The wrapper: templates/forum.html

The synthetic page asks for the page template `forum`, so `templates/forum.html` (when the
package has it) renders every forum view. The file is picked even without the support flag, so
ship the two together. Without the file, the page chain applies: make sure `page.html` prints
`.Post.Content`.

```html
{{/* templates/forum.html: every Pursae Forum view. The body prints its own H1 (supports["pursae-forum"]). */}}
{{template "header" .}}
<main id="main" class="site-main porch-forum">
	<div class="porch-forum__main">{{with .Post}}{{.Content}}{{end}}</div>
	<aside class="porch-forum__side" aria-label="Community">
		<section class="porch-card"><h2>{{__ . "In numbers"}}</h2>{{forum_stats .}}</section>
		{{with forum_recent_topics . 5}}<section class="porch-card"><h2>{{__ . "Recently active"}}</h2>{{.}}</section>{{end}}
	</aside>
</main>
{{template "footer" .}}
```

The dot is the normal **site view**, so `__ .`, `theme_mod .` and every field from
[templates](templates.md#the-view) work here. To drop the sidebar on forms, use the body
classes: `.pf-view-login .porch-forum__side { display: none; }` (do the same for `register`,
`verify`, `account`, `new-topic` and `edit`).

## Template functions

### In theme templates (header, footer, front page, sidebar, the wrapper)

These take the site view (`.`, or `$` inside `range`/`with`). While the plugin is off, they print
nothing and `forum_on` is `false`, so a theme can use them unconditionally.

| Function | Returns |
|----------|---------|
| `forum_on` | `true` while Pursae Forum is active |
| `forum_is_view .` | `true` on a forum view |
| `forum_url . "<view>"` | the URL of `index` (default), `login`, `register`, `account`, `search`, `members` (index while the member list is off) or `new` (the new-topic form of the forum being viewed, the index elsewhere). Carries the language prefix under Pursae Multi Language |
| `forum_user_menu .` | `<nav class="pf-usermenu">`: **Log in** (`.pf-usermenu-login`) and **Join** (`.pf-usermenu-join.pf-button.pf-button--primary`, only while registration is open) for visitors; avatar and name linking the account page (`.pf-usermenu-account` › `.pf-avatar` + `.pf-usermenu-name`) and **Log out** (`.pf-usermenu-logout`) for members |
| `forum_recent_topics . n` | `<ul class="pf-recent">` of the `n` most recently active topics (default 5, max 50): `<li><a>title</a> <span class="pf-recent-meta">forum · <time></span></li>`; empty when there are none, so wrap it in `with` |
| `forum_stats .` | `<dl class="pf-stats"><div><dt>Forums</dt><dd>12</dd></div>…</dl>`: forums (categories not counted), topics, replies, members |

`forum_recent_topics` and `forum_stats` tag the page with the `forum` cache tag, so Pursae Cache
refreshes your front page when someone posts.

```html
<!-- header.html: account links, and a search box only while the forum is on -->
{{if forum_on}}<form class="porch-search" role="search" method="get" action="{{forum_url . "search"}}">
	<label class="screen-reader-text" for="porch-q">{{__ . "Search the forums"}}</label>
	<input type="search" id="porch-q" name="q" maxlength="100" />
</form>{{end}}
{{with forum_user_menu .}}<div class="site-header__account">{{.}}</div>{{end}}
```

### Everywhere (theme templates and view overrides)

These take plain values and are safe while the plugin is off.

| Function | Returns |
|----------|---------|
| `forum_format s` | stored post HTML, sanitized again and formatted (paragraphs, `rel="nofollow ugc"` on links) |
| `forum_date t` | date and time in the site's formats and time zone |
| `forum_iso t` | RFC 3339 UTC, for `<time datetime>` |
| `forum_ago t` | "5 mins" (English; in overrides use `.Ago`, which is translated) |
| `forum_avatar author size` | the member's photo `<img class="pf-avatar avatar">`, otherwise an initials disc `<span class="pf-avatar pf-avatar--initials">`. A `nil` author (a deleted account) gives an empty disc `pf-avatar--empty`. Size in px (default 40, max 512), also set as `--pf-av-size` |
| `forum_level level` | a level chip (empty for `nil`). The name is not translated: use `.LevelChip` in overrides |
| `forum_badge badge` | a badge chip with its description as `title`. Not translated: use `.BadgeChip` in overrides |
| `forum_icon "name" size` | one of the 43 line icons as inline SVG (`class="pf-icon pf-icon--<name>"`, `currentColor`); empty for an unknown name |
| `forum_icon_names` | the icon names, sorted: `anchor bolt book calendar camera chat check code coffee compass crown feather flag flame gamepad gem gift globe heart key leaf lightbulb lock mail medal moon mountain music paw pencil pin puzzle rocket seedling shield sparkles star sun target trophy user users verified` |

The core functions (`num`, `date`, `dict`, `add`, …) work in overrides as well.

## View overrides

Put a file at `templates/forum/<view>.html`. Package themes name each template by its path under
`templates/`, so `templates/forum/topic.html` becomes the template `forum/topic` (no `{{define}}`
needed). The file replaces the plugin's template for that view only. Other views keep the
plugin's markup.

Rules:

- **Self-contained.** The plugin's partials (`forum/part-head`, `forum/part-post`, …) are not
  visible to your theme. Copy what you need from `web/forum/templates/` into your file, or into
  your own `{{define "porch/post"}}` in any theme file (all files share one template set).
- **The dot is the `forumPage`**, not the site view. `forum_url`, `forum_user_menu`,
  `forum_recent_topics`, `forum_stats`, `__` and `theme_mod` need the site view, so they do not
  work here. Use `.URLs`, `.T` and `.Tf` instead.
- **A failing override falls back.** If the template returns an error while it runs (a typo in a
  field name, a wrong argument type), the server logs `forum: theme <slug> override forum/<view>:
  …` and serves the plugin's template. The visitor never sees a broken page, so watch the log
  while you develop.
- Keep the root element `<div class="pf …" data-view="<view>">`: the stylesheet scopes everything
  to `.pf`.
- Keep the form contract ([Forms](#forms-in-overrides)) when you override a view with forms.

### Override templates

| File | URL | Shown for | Data on the dot (besides the [common fields](#the-dot-forumpage)) |
|------|-----|-----------|----------------------------------------------------------------------|
| `forum/index.html` | `/forums/` | the forum home | `.Forums` (the tree), `.Groups`, `.Topics` (5 latest) |
| `forum/forum.html` | `/forums/f/<slug>/[page/N/]` | a forum or a category | `.Forum` (with `.Children` for a category), `.Topics` (sticky first), `.Pagination`, `.URLs.NewTopic` |
| `forum/topic.html` | `/forums/t/<id>/<slug>/[page/N/]` | a topic with its replies | `.Topic`, `.Forum`, `.Replies`, `.Pagination`, `.LeadPost`, `.ReplyPost`, `.CanEdit`, `.EditURL`, `.TopicURL`; Extra `page`, `page_url`, `open`, `can_reply` |
| `forum/new-topic.html` | `/forums/f/<slug>/new/` | the new-topic form | `.Forum`, `.Error`, `.FormValue`; Extra `action`, `no_form` |
| `forum/edit.html` | `/forums/edit/<id>/` | editing a topic or reply | `.Edit`, `.Topic`, `.Forum`; Extra `action`, `back`, `window` |
| `forum/search.html` | `/forums/search/?q=` | search results | `.Query`, `.Pagination`; Extra `hits`, `total` |
| `forum/profile.html` | `/forums/u/<nicename>/[page/N/]` | a member's profile and activity | `.Profile` (stats, level, badges), `.ProfileStats`, `.Pagination`; Extra `hits`, `total`, `bio`, `can_block`, `action`, `badge_admin`, `award`, `revoke` |
| `forum/members.html` | `/forums/members/[page/N/]?q=&sort=` | the member list (while it is switched on) | `.Members`, `.MembersTotal`, `.MemberQuery`, `.Query`, `.Pagination`; Extra `sorts`, `sort`, `action` |
| `forum/login.html` | `/forums/login/` | the login form | `.Form` (`log`, `rememberme`, `redirect`), `.Captcha`; Extra `action`, `google`, `verify` |
| `forum/register.html` | `/forums/register/` | the sign-up form, then "check your inbox" | `.Form` (`user_login`, `user_email`, `redirect`, `err_user_login`, `err_user_email`, `err_user_pass`), `.Traps`, `.Captcha`; Extra `action`, `google`, `verify`, `sent` |
| `forum/verify.html` | `/forums/verify/` | email confirmation and "send the link again" | `.Form` (`user_email`); Extra `failed`, `verify` |
| `forum/account.html` | `/forums/account/` | the member's account: photo, profile, password, Google | `.User` (with stats and level), `.Form` (`display_name`, `bio`, `email`, `login`, `google_email`); Extra `action`, `profile_url`, `need_current`, `password_set`, `google_linked` |
| `forum/shortcode-index.html` | any page with `[pursae_forum]` | the forum list embedded in a page | `.Forums`, `.Groups`. No nonces (the page may be cached) |
| `forum/shortcode-recent.html` | any page with `[pursae_forum view="recent" limit="5"]` | recent topics embedded in a page | `.Topics`. No nonces |

`/forums/r/<id>/` (a reply's short link) redirects to `…/t/<id>/<slug>/page/N/#post-<reply id>`,
so keep `id="post-{{.ID}}"` on every post.

### The dot (forumPage)

Common fields, set on every view:

| Field | Type | Meaning |
|-------|------|---------|
| `.View` | string | `index`, `forum`, `topic`, `new-topic`, `edit`, `search`, `profile`, `members`, `login`, `register`, `verify`, `account`, `shortcode-index`, `shortcode-recent` |
| `.Title` | string | the view title (print it as the H1 when `.OwnTitle`) |
| `.OwnTitle` | bool | the theme declares `supports["pursae-forum"]` |
| `.Crumbs` | []crumb | `.Label`, `.URL` (empty for the current page). The first is the forum home; the index has only that one |
| `.User` | *author | the logged-in member, `nil` for visitors. Stats, level and badges are filled only on the account view |
| `.CanPost` | bool | may start topics and reply (has `forum_participate`, not blocked, email confirmed when required). Closed forums and topics are checked separately |
| `.CanModerate` | bool | has `forum_moderate` |
| `.Unverified` | bool | the member has not confirmed their email yet |
| `.GoogleOn`, `.RegisterOn` | bool | Google sign-in is ready; registration is open |
| `.Nonce`, `.AuthNonce` | string | form nonces. Empty for visitors on cacheable views, so forms only render where they work |
| `.Notice`, `.Error` | string | plain text, already translated (print in `role="status"` / `role="alert"`) |
| `.URLs` | urls | `.Index`, `.Search`, `.Login`, `.Register`, `.Account`, `.Logout` (members only), `.LostPassword`, `.GoogleStart`, `.GoogleLink` (empty unless Google is ready), `.NewTopic` (forum view, logged-in members, on an open forum or for moderators; show it only when `.CanPost`) |
| `.Pagination` | HTML | `<nav class="pf-pagination">` or empty |
| `.Traps` | HTML | the honeypot and the signed time field (spam traps) |
| `.Captcha` | HTML | the login captcha (reCAPTCHA or Turnstile, when one is set up for the login form) on the login and register views, or empty |
| `.Form` | map | values to fill again after a failed POST; also `.FormValue "key"` |
| `.Extra` | map | per-view values (table below); `members_url` is set on every view while the member list is on |

View data: `.Forums`, `.Forum`, `.Topics`, `.Topic`, `.Replies`, `.Profile`, `.ProfileStats`
(`.Topics`, `.Replies`, `.Registered`), `.Query`, `.Edit` (`.ID`, `.IsTopic`, `.Title`,
`.Content`), `.Members`, `.MemberQuery` (`.Search`, `.Sort`, `.Page`), `.MembersTotal`.

Methods:

| Method | Returns |
|--------|---------|
| `.T "text"` | the forum's own strings in the visitor's language (the Pursae Multi Language language, else the site language). A phrase the catalog does not know prints as written |
| `.Tf "Joined %s" x` | the same with `%s` / `%1$s` filled |
| `.Count n "%s reply" "%s replies"` | singular or plural with the number |
| `.Ago t` | "5 mins ago", translated |
| `.Groups` | the index blocks: `.Category` (`nil` for forums outside categories, listed first) and `.Forums` |
| `.LeadPost` | the topic's opening post as a post view |
| `.ReplyPost reply` | a reply as a post view |
| `.CanEdit id`, `.EditURL id` | the viewer may edit post `id` (author within the edit window, or a moderator); its edit form |
| `.TopicURL` | the topic's URL (where the reply form and the mod bar post) |
| `.FormValue "key"` | a re-filled form value |
| `.LevelChip level`, `.LevelName level` | a level chip / name with the name translated (empty for `nil`) |
| `.BadgeChip badge`, `.BadgeText badge` | a badge chip with name and description translated; `[name, description]` |
| `.FewBadges author n`, `.MoreBadges author n` | the first `n` badges; how many were left out |
| `.BadgeIcon badge size` | the badge's Media Library image, else its icon |
| `.ChipStyle color`, `.ProgressStyle n` | `style` values: `--pf-chip:#hex` (validated) and `--pf-progress:NN%` |
| `.PointsToNext author` | points still needed for the next level (0 at the top) |
| `.MemberAuthor member` | the author of a member row (for `forum_avatar`) |

### Per-view Extra keys

| Key | View | Type | Meaning |
|-----|------|------|---------|
| `members_url` | all | string | the member list, while it is on |
| `page` | topic | int | the page number. Print `.LeadPost` only when it is 1 |
| `page_url` | topic | string | this page's URL |
| `open` | topic | bool | the topic and its forum accept replies |
| `can_reply` | topic | bool | show the reply form (a member who may post; moderators also on closed topics) |
| `action` | new-topic, edit, profile, members, login, register, account | string | where the view's forms post |
| `no_form` | new-topic | bool | the forum is closed: show `.Error` and a way back |
| `back` | edit | string | the post's URL (Cancel) |
| `window` | edit | int | the edit window in minutes (only when it applies to the viewer) |
| `hits` | search, profile | []hit | results: `.Topic`, `.Reply` (`nil` for a topic), `.Excerpt` (plain text), `.URL`, `.At`, `.Author` |
| `total` | search, profile | int | number of results |
| `bio` | profile | string | the member's bio, plain text |
| `can_block` | profile | bool | the viewer may block or unblock this member |
| `badge_admin`, `award`, `revoke` | profile | bool, []award, []award | administrators: badges to award and manual awards to take back; an award has `.Badge`, `.Name` (translated), `.Manual` |
| `sorts`, `sort` | members | []link, string | sort links (`.Key`, `.Label`, `.URL`, `.Current`) and the current key (`newest`, `active`, `name`) |
| `google` | login, register | string | the "Continue with Google" URL (Google ready only) |
| `verify` | login, register, verify | string | where "send the link again" posts |
| `sent` | register | bool | the account was created: show "check your inbox" |
| `failed` | verify | bool | the confirmation link did not work |
| `profile_url`, `need_current`, `password_set`, `google_linked` | account | string, bool, bool, bool | public profile; ask for the current password; the member has a password; Google is linked |

### The data types

**Author** (`.User`, `.Profile`, `.Author`, `.LastAuthor`, member rows):

| Field | Meaning |
|-------|---------|
| `.ID`, `.DisplayName`, `.Nicename`, `.ProfileURL` | identity. Print `@{{.Nicename}}` as the handle, never `.Login` (the login name) |
| `.AvatarURL`, `.HasPhoto`, `.Initials`, `.Hue` | the photo (or the default image), whether there is a real photo, initials and hue for the initials disc. Prefer `forum_avatar` |
| `.Moderator`, `.Blocked` | can moderate; may no longer post |
| `.Stats` | statistics are loaded (`false` on `.User` except on the account view) |
| `.Topics`, `.Replies`, `.Points` | published counts and points |
| `.Joined`, `.LastActive` | registration date; last published post (zero = never) |
| `.Verified`, `.Google` | email confirmed; Google linked |
| `.Level`, `.NextLevel`, `.Progress` | current level (`nil` while `.Stats` is false), the next one (`nil` at the top), 0–100 toward it |
| `.Badges` | earned and awarded badges, enabled only, in display order |

**Level**: `.ID`, `.Name`, `.Min` (points), `.Icon`, `.Color`, `.ImageURL`.
**Badge**: `.ID`, `.Name`, `.Description`, `.Icon`, `.Color`, `.ImageURL`, `.Rule`, `.Threshold`.

**Forum** (`.Forum`, items of `.Forums`): `.ID`, `.ParentID`, `.Slug`, `.Title`, `.Description`
(HTML), `.Kind` (`forum` or `category`), `.Closed`, `.TopicCount`, `.ReplyCount`, `.LastActive`,
`.LastTopicTitle`, `.LastTopicURL`, `.LastAuthor`, `.URL`, `.Children`. A category's counts add up
its forums.

**Topic**: `.ID`, `.Title`, `.Content` (print with `forum_format`), `.Status` (`publish`,
`pending`, `trash`), `.Spam`, `.Sticky`, `.Closed`, `.ReplyCount`, `.CreatedAt`, `.LastActive`,
`.EditedAt` (`nil` when never edited), `.Author`, `.LastAuthor`, `.URL`, `.ForumTitle`, `.ForumURL`,
`.IP` (moderators only).

**Reply**: `.ID`, `.TopicID`, `.Content`, `.Status`, `.Spam`, `.CreatedAt`, `.EditedAt`, `.Author`,
`.URL`, `.Position` (1-based among published replies), `.IP`.

**Post view** (`.LeadPost`, `.ReplyPost`): `.Page` (the forumPage), `.ID`, `.Lead`, `.Title`,
`.Author`, `.Content`, `.CreatedAt`, `.EditedAt`, `.Status`, `.Spam`, `.Sticky`, `.Closed`,
`.Position` (0 for the lead post and unpublished replies), `.IP`, `.Permalink`, `.ActionURL` (the
mod bar's target).

Moderators also see pending topics and replies in the forum and topic lists, and can open a spam or
trashed topic by its link, so draw the states (`.Status`, `.Spam`) when you override `forum`, `topic`
or a list.

### Forms in overrides

Every form is a plain POST (no JavaScript) with a `forum_action` and a nonce. Success redirects;
a validation error renders the view again with `.Error`, the values in `.Form` and status 422. A
field you leave out makes the action fail.

| Action (`forum_action`) | Posts to | Fields |
|-------------------------|----------|--------|
| `login` | `.Extra.action` | `_wpnonce` = `.AuthNonce`, `log`, `pwd`, `rememberme=1`, `redirect` (from `.Form.redirect`), `{{.Captcha}}` |
| `register` | `.Extra.action` | `_wpnonce` = `.AuthNonce`, `{{.Traps}}`, `user_login`, `user_email`, `user_pass`, `redirect`, `{{.Captcha}}` |
| `resend` | `.Extra.verify` (logged out), `.URLs.Account` (members) | `_wpnonce` = `.AuthNonce`, `user_email` (logged out) |
| `new_topic` | `.Extra.action` | `_wpnonce` = `.Nonce`, `{{.Traps}}`, `title` (3–120 characters), `content` (2–20,000) |
| `reply` | `.TopicURL` (add `#reply`) | `_wpnonce` = `.Nonce`, `{{.Traps}}`, `content` |
| `edit` | `.Extra.action` | `_wpnonce` = `.Nonce`, `title` (topics only), `content` |
| `moderate` | the post's `.ActionURL` | `_wpnonce` = `.Nonce`, `post_id`, `op` = `approve`, `stick`, `unstick`, `close`, `open` (these four on the lead post only), `spam`, `unspam`, `trash`, `restore` |
| `block`, `unblock` | `.Extra.action` (profile) | `_wpnonce` = `.Nonce` |
| `badge_award`, `badge_revoke` | `.Extra.action` (profile) | `_wpnonce` = `.Nonce`, `badge` (badge id); administrators only |
| `account` | `.Extra.action` | `_wpnonce` = `.Nonce`, `display_name` (≤ 60), `bio` (≤ 1000, plain text) |
| `password` | `.Extra.action` | `_wpnonce` = `.Nonce`, `current_pass` (when `.Extra.need_current`), `new_pass` (≥ 8 characters, or the Pursae Security password policy when it is on) |
| `avatar_upload` | `.Extra.action`, `enctype="multipart/form-data"` | `_wpnonce` = `.Nonce`, file `avatar` (JPEG, PNG, WebP or GIF, ≤ 2 MB) |
| `avatar_remove` | `.Extra.action` | `_wpnonce` = `.Nonce` |
| `google_unlink` | `.Extra.action` | `_wpnonce` = `.Nonce` (only offer it when `.Extra.password_set`) |

Searches are GET: `q` to `.URLs.Search`; the member list takes `q` and `sort` at `.Extra.action`.

## CSS: tokens and classes

`forum.css` is loaded on forum views and on pages embedding `[pursae_forum]`. Everything is
scoped under `.pf` (and the theme widgets `.pf-usermenu`, `.pf-recent`, `.pf-stats`), so it
leaves the rest of your theme alone. Restyle it in two layers: tokens first, then classes.

### Tokens

| Token | Default | Used for |
|-------|---------|----------|
| `--pf-accent` | `var(--wb-color-primary, #2271b1)` | links, primary buttons, focus ring |
| `--pf-accent-ink` | `#fff` | text on the accent |
| `--pf-bg` | `var(--wb-color-background, transparent)` | the forum's background |
| `--pf-surface` | `#fff` | cards, rows, posts |
| `--pf-text` | `var(--wb-color-text, inherit)` | text |
| `--pf-muted` | `#646970` | meta lines, counts |
| `--pf-border` | `#dcdcde` | borders |
| `--pf-radius` | `8px` | corners |
| `--pf-font`, `--pf-font-heading` | `var(--wb-font-body, inherit)`, `var(--wb-font-heading, inherit)` | body and heading fonts |
| `--pf-gap` | `16px` | spacing unit |
| `--pf-soft`, `--pf-line` | mixes of accent / border | hover and header tints, separators |
| `--pf-danger`, `--pf-success`, `--pf-warning` | `#b32d2e`, `#00753a`, `#996800` | notices and state badges (pick readable values on dark surfaces) |
| `--pf-shadow`, `--pf-focus` | a light shadow; a 2 px accent ring | cards; keyboard focus |

The `--wb-*` fallbacks mean a Pursae Web Builder site gets its brand colour and fonts (Web Builder ›
Settings) in the forum without any CSS.

**Set the tokens on `body .pf`, not on `:root`.** `forum.css` declares them on `.pf` itself, so a
value on `:root` never reaches the forum. Two patterns work:

```css
/* A: values right on the forum blocks */
body :is(.pf, .pf-usermenu, .pf-recent, .pf-stats) {
	--pf-accent: #b24723;
	--pf-radius: 14px;
	--pf-font: "Porch Sans", system-ui, sans-serif;
}

/* B: values on :root (so a Customizer css_var reaches them), inherited by the forum */
:root { --pf-accent: #b24723; --pf-surface: #fff; }
body :is(.pf, .pf-usermenu, .pf-recent, .pf-stats) { --pf-accent: inherit; --pf-surface: inherit; }
```

A Customizer colour with `"css_var": "--pf-accent"` prints on `:root` ([customizer](customizer.md)),
so a theme that offers an accent setting needs pattern B for that token.

### Classes

Use these to adjust details. The names are a contract: they stay stable.

| Area | Classes |
|------|---------|
| Frame | `.pf` (also a CSS container named `pf`: `@container pf (max-width: 680px) { … }`), `.pf--embed` (shortcodes), `.alignwide` (wide views), `.pf-head`, `.pf-crumbs`, `.pf-title`, `.pf-actions`, `.pf-toolbar`, `.pf-section-title`, `.pf-intro`, `.pf-empty`, `.pf-card`, `.pf-card-title`, `.pf-sr` (screen-reader only) |
| Index and forums | `.pf-forums`, `.pf-category` (`--loose`), `.pf-category-head`, `.pf-category-title`, `.pf-category-desc`, `.pf-forum-list`, `.pf-forum-row`, `.pf-forum-icon`, `.pf-forum-main`, `.pf-forum-name`, `.pf-forum-desc`, `.pf-subforums`, `.pf-count`, `.pf-count-num`, `.pf-count-label`, `.pf-last`, `.pf-last-text`, `.pf-last-title`, `.pf-last-meta`, `.pf-latest` |
| Topic lists | `.pf-topics` (`--compact`), `.pf-topic-row`, `.pf-topic-main`, `.pf-topic-title`, `.pf-topic-meta` |
| Topic page | `.pf-topic-state`, `.pf-topic-stats`, `.pf-posts`, `.pf-post` (`--lead`), `.pf-post-author`, `.pf-post-author-name`, `.pf-post-author-roles`, `.pf-post-author-level`, `.pf-post-author-badges`, `.pf-post-author-stats`, `.pf-post-main`, `.pf-post-meta`, `.pf-post-link`, `.pf-post-num`, `.pf-post-edited`, `.pf-post-ip`, `.pf-post-body`, `.pf-post-tools`, `.pf-modbar`, `.pf-reply`, `.pf-join`, `.pf-closed-note` |
| States | `.is-sticky`, `.is-closed`, `.is-pending`, `.is-spam`, `.is-trash` on rows and posts; `.is-current` on pagination and sort links |
| Labels | `.pf-badge` with `--sticky`, `--closed`, `--pending`, `--spam`, `--trash`, `--mod`, `--op` (topic author), `--blocked` |
| Levels and badges | `.pf-badge-chip` (+ `.pf-level.pf-level--<id>` or `.pf-badge.pf-badge--<id>`, colour in `--pf-chip`), `.pf-badge-chip__name`, `.pf-badge-chip__img`, `.pf-badge-more`, `.pf-badge-list`, `.pf-badge-item`, `.pf-level-progress` (`--pf-progress`), `.pf-level-next` |
| Avatars | `.pf-avatar` (size from `--pf-av-size`), `.pf-avatar--initials` (colour from `--pf-av`), `.pf-avatar--empty`, `.pf-avatar--sm`, `--lg`, `--xl` |
| Profile and members | `.pf-profile`, `.pf-profile-avatar`, `.pf-profile-name`, `.pf-profile-handle`, `.pf-profile-bio`, `.pf-profile-level`, `.pf-profile-badges`, `.pf-stats`, `.pf-activity`, `.pf-members`, `.pf-members-toolbar`, `.pf-sort`, `.pf-sort-link`, `.pf-member-grid`, `.pf-member-card` (`__avatar`, `__name`, `__meta`, `__badges`, `__joined`), `.pf-member-stats` |
| Search | `.pf-search`, `.pf-search-input`, `.pf-results`, `.pf-result`, `.pf-result-title`, `.pf-result-excerpt`, `.pf-result-meta`, `.pf-result-count` |
| Forms | `.pf-form` (`--inline`), `.pf-field`, `.pf-label`, `.pf-input`, `.pf-textarea`, `.pf-select`, `.pf-file`, `.pf-check`, `.pf-help`, `.pf-field-error`, `.pf-form-actions`, `.pf-auth`, `.pf-auth-lead`, `.pf-auth-foot`, `.pf-account`, `.pf-avatar-form`, `.pf-divider`, `.pf-compose` |
| Buttons and notices | `.pf-button` with `--primary`, `--ghost`, `--small`, `--danger`, `--google`, `--block`; `.pf-notice` with `--success`, `--error`, `--info`; `.pf-pagination`, `.pf-page` (`--prev`, `--next`, `--gap`) |
| Theme widgets | `.pf-usermenu`, `.pf-usermenu-login`, `.pf-usermenu-join`, `.pf-usermenu-account`, `.pf-usermenu-name`, `.pf-usermenu-logout`, `.pf-recent`, `.pf-recent-meta`, `.pf-stats` |
| Body | `.pursae-forum`, `.pf-view-<view>` |

Block themes print the page title in a narrower column. On wide views `forum.css` widens
`.wp-block-post-title` to match `.pf.alignwide` (up to 1080 px).

## A complete example

A small package, "Porch", with the wrapper, the account menu, the tokens and an override of the
forum home. It is enough to install, activate and use.

```text
porch/
  theme.json
  templates/
    index.html
    header.html
    forum.html
    forum/index.html
  assets/
    style.css
    screenshot.png
```

`theme.json`:

```json
{
  "slug": "porch",
  "name": "Porch",
  "version": "1.0.0",
  "description": "A small community theme for Pursae Forum.",
  "author": "Gopher Co.",
  "requires_core": "0.1.0",
  "screenshot": "assets/screenshot.png",
  "tags": ["Community", "Forum"],
  "menu_locations": [{"id": "primary", "name": "Primary Menu"}],
  "styles": [{"handle": "porch-style", "src": "assets/style.css"}],
  "supports": {"pursae-forum": true, "title-tag": true},
  "customizer": {"sections": [{"id": "porch", "title": "Porch", "settings": [
    {"id": "accent", "label": "Accent colour", "type": "color", "default": "#B24723", "transport": "postMessage", "css_var": "--pf-accent"}
  ]}]}
}
```

`templates/header.html` (both parts):

```html
{{define "header"}}<!doctype html>
<html lang="en-US">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.DocumentTitle}}</title>
{{.Head}}
</head>
<body class="{{.BodyClass}}">
{{.BodyOpen}}
<header class="porch-header">
	<a class="porch-brand" href="{{.HomeURL}}" rel="home">{{.SiteName}}</a>
	{{with index .Menus "primary"}}<nav aria-label="Primary"><ul class="porch-menu">
		{{range .}}<li{{if .Current}} class="is-current"{{end}}><a href="{{.URL}}">{{.Label}}</a></li>{{end}}
	</ul></nav>{{end}}
	{{if forum_on}}<a class="porch-forums-link" href="{{forum_url . "index"}}">{{__ . "Forums"}}</a>{{end}}
	{{forum_user_menu .}}
</header>
{{end}}

{{define "footer"}}
<footer class="porch-footer">{{.PrivacyLink}}</footer>
{{.Footer}}{{.AdminBar}}
</body>
</html>
{{end}}
```

`templates/index.html` (required; also used for pages here):

```html
{{template "header" .}}
<main id="main" class="porch-main">
{{with .Post}}
	<h1>{{.Title}}</h1>
	<div class="entry-content">{{.Content}}</div>
{{else}}
	{{range .Posts}}<article class="{{.Class}}"><h2><a href="{{.Permalink}}">{{.Title}}</a></h2>{{.Excerpt}}</article>
	{{else}}<p>Nothing found.</p>{{end}}
	{{.Pagination}}
	{{if forum_on}}<section class="porch-card"><h2>{{__ . "Latest from the forums"}}</h2>
		{{with forum_recent_topics . 5}}{{.}}{{else}}<p>{{__ . "No topics yet."}}</p>{{end}}</section>{{end}}
{{end}}
</main>
{{template "footer" .}}
```

`templates/forum.html` (the wrapper):

```html
{{template "header" .}}
<main id="main" class="porch-main porch-forum">
	<div class="porch-forum__main">{{with .Post}}{{.Content}}{{end}}</div>
	<aside class="porch-forum__side" aria-label="Community">
		<section class="porch-card"><h2>{{__ . "In numbers"}}</h2>{{forum_stats .}}</section>
	</aside>
</main>
{{template "footer" .}}
```

`templates/forum/index.html` (the forum home; the dot is the forumPage):

```html
<div class="pf alignwide porch-board" data-view="index">
	<header class="pf-head">
		{{if .OwnTitle}}<h1 class="pf-title">{{.Title}}</h1>{{end}}
		<form class="pf-search" role="search" method="get" action="{{.URLs.Search}}">
			<label class="pf-sr" for="porch-forum-q">{{.T "Search the forums"}}</label>
			<input class="pf-input pf-search-input" type="search" id="porch-forum-q" name="q" maxlength="100" placeholder="{{.T "Search topics and replies"}}" />
			<button class="pf-button pf-button--primary" type="submit">{{.T "Search"}}</button>
		</form>
		{{with .Extra.members_url}}<a class="pf-button pf-button--ghost" href="{{.}}">{{$.T "Members"}}</a>{{end}}
	</header>
	{{with .Error}}<div class="pf-notice pf-notice--error" role="alert">{{.}}</div>{{end}}
	{{with .Notice}}<div class="pf-notice pf-notice--success" role="status">{{.}}</div>{{end}}
	{{if and .User .Unverified}}<div class="pf-notice pf-notice--info pf-verify-banner">
		<p>{{.T "Please confirm your email address to start posting."}}</p>
		<form class="pf-form pf-form--inline" method="post" action="{{.URLs.Account}}">
			<input type="hidden" name="forum_action" value="resend" />
			<input type="hidden" name="_wpnonce" value="{{.AuthNonce}}" />
			<button type="submit" class="pf-button pf-button--ghost">{{.T "Send the link again"}}</button>
		</form>
	</div>{{end}}

	{{range .Groups}}<section class="pf-category porch-group">
		{{with .Category}}<header class="pf-category-head">
			<h2 class="pf-category-title"><a href="{{.URL}}">{{.Title}}</a></h2>
			{{with .Description}}<div class="pf-category-desc">{{.}}</div>{{end}}
		</header>{{end}}
		<ul class="pf-forum-list">
			{{range .Forums}}<li class="pf-forum-row{{if .Closed}} is-closed{{end}}">
				<span class="pf-forum-icon" aria-hidden="true">{{forum_icon "chat" 20}}</span>
				<div class="pf-forum-main">
					<h3 class="pf-forum-name"><a href="{{.URL}}">{{.Title}}</a>{{if .Closed}} <span class="pf-badge pf-badge--closed">{{$.T "Closed"}}</span>{{end}}</h3>
					{{with .Description}}<div class="pf-forum-desc">{{.}}</div>{{end}}
				</div>
				<div class="pf-count"><span class="pf-count-num">{{num .TopicCount}}</span> <span class="pf-count-label">{{if eq .TopicCount 1}}{{$.T "topic"}}{{else}}{{$.T "topics"}}{{end}}</span></div>
				<div class="pf-last">{{if .LastTopicURL}}{{with .LastAuthor}}{{forum_avatar . 32}}{{end}}
					<div class="pf-last-text"><a class="pf-last-title" href="{{.LastTopicURL}}">{{.LastTopicTitle}}</a>
					<span class="pf-last-meta"><time datetime="{{forum_iso .LastActive}}">{{$.Ago .LastActive}}</time></span></div>
				{{else}}<span class="pf-last-none">{{$.T "No topics yet"}}</span>{{end}}</div>
			</li>{{else}}<li class="pf-empty">{{$.T "No forums in this category yet."}}</li>{{end}}
		</ul>
	</section>{{else}}<p class="pf-empty">{{.T "There are no forums yet."}}</p>{{end}}

	{{with .Topics}}<section class="pf-latest" aria-labelledby="porch-latest">
		<h2 class="pf-section-title" id="porch-latest">{{$.T "Latest topics"}}</h2>
		<ul class="pf-topics">{{range .}}<li class="pf-topic-row">
			{{forum_avatar .Author 40}}
			<div class="pf-topic-main">
				<h3 class="pf-topic-title"><a href="{{.URL}}">{{.Title}}</a></h3>
				<p class="pf-topic-meta">{{with .Author}}<a href="{{.ProfileURL}}">{{.DisplayName}}</a> &middot; {{end}}<a href="{{.ForumURL}}">{{.ForumTitle}}</a> &middot; <time datetime="{{forum_iso .LastActive}}">{{$.Ago .LastActive}}</time></p>
			</div>
			<div class="pf-count"><span class="pf-count-num">{{num .ReplyCount}}</span> <span class="pf-count-label">{{if eq .ReplyCount 1}}{{$.T "reply"}}{{else}}{{$.T "replies"}}{{end}}</span></div>
		</li>{{end}}</ul>
	</section>{{end}}
</div>
```

`assets/style.css`:

```css
:root {
	--pf-accent: #B24723;               /* Customizer › Porch › Accent colour */
	--pf-surface: #fff;
	--pf-text: #2b2622;
	--pf-border: #eadfcf;
	--pf-radius: 12px;
	--pf-font: system-ui, sans-serif;
	--pf-font-heading: Georgia, serif;
}
/* forum.css declares its tokens on these blocks: inherit ours instead */
body :is(.pf, .pf-usermenu, .pf-recent, .pf-stats) {
	--pf-accent: inherit; --pf-surface: inherit; --pf-text: inherit; --pf-border: inherit;
	--pf-radius: inherit; --pf-font: inherit; --pf-font-heading: inherit;
}
body { margin: 0; background: #fbf6ee; color: #2b2622; font-family: system-ui, sans-serif; }
.porch-header, .porch-main, .porch-footer { max-width: 1180px; margin-inline: auto; padding: 16px; }
.porch-header { display: flex; flex-wrap: wrap; align-items: center; gap: 16px; }
.porch-header .pf-usermenu { margin-inline-start: auto; }
.porch-menu { display: flex; gap: 16px; list-style: none; margin: 0; padding: 0; }
.porch-forum { display: grid; grid-template-columns: minmax(0, 1fr) 280px; gap: 32px; }
.porch-card { background: #fff; border: 1px solid #eadfcf; border-radius: 12px; padding: 16px; }
:is(.pf-view-login, .pf-view-register, .pf-view-verify, .pf-view-account, .pf-view-new-topic, .pf-view-edit) .porch-forum { grid-template-columns: minmax(0, 1fr); }
:is(.pf-view-login, .pf-view-register, .pf-view-verify, .pf-view-account, .pf-view-new-topic, .pf-view-edit) .porch-forum__side { display: none; }
.porch-board .pf-category { border-top: 3px solid var(--pf-accent); }
@media (max-width: 760px) { .porch-forum { grid-template-columns: minmax(0, 1fr); } }
```

Zip it (`zip -r ../porch.zip theme.json templates assets`), upload it under Appearance › Themes ›
Add Theme › Upload Theme, activate it and open `/forums/`. Every view except the home uses the plugin's
templates with your tokens. Add `forum/topic.html` and the others the same way when you want
them.

## Demo content

A theme's `demo/demo.json` ([package format](../../theme-package.md)) may carry a `"forum"`
section. Importing the demo switches Pursae Forum on and creates the members, forums, topics and
replies. Each item is marked as demo content: a second import adds nothing, and **Remove Demo
Content** deletes them and restores the forum title (a demo forum that meanwhile holds the site's own
topics is kept, and so is a demo member who meanwhile owns other posts or media).

```json
"forum": {
  "title": "Porch Community",
  "members": [
    {"ref": "mara", "display_name": "Mara Lind", "role": "forum_participant", "bio": "Woodworker."},
    {"ref": "ozan", "display_name": "Ozan Kaya", "role": "forum_moderator"}
  ],
  "forums": [
    {"ref": "general", "title": "General", "slug": "general", "kind": "category", "order": 0},
    {"ref": "introductions", "title": "Introductions", "slug": "introductions", "parent": "general", "description": "Say hello.", "order": 1},
    {"ref": "announcements", "title": "Announcements", "slug": "announcements", "parent": "general", "closed": true}
  ],
  "topics": [
    {"ref": "welcome", "forum": "announcements", "author": "admin", "title": "Welcome to the porch",
     "content": "<p>Pull up a chair and introduce yourself.</p>", "sticky": true, "hours_ago": 96,
     "replies": [{"author": "mara", "content": "<p>Glad to be here.</p>", "hours_ago": 90}]}
  ]
}
```

| Key | Rules |
|-----|-------|
| `title` | optional, ≤ 80 characters: replaces the forum title (Forums › Settings) |
| `members` | ≤ 12. `ref` matches `^[a-z0-9][a-z0-9_-]{0,39}$`, unique, not `admin`. `display_name` required (≤ 80). `role` is `forum_participant` (default), `forum_moderator` or `subscriber`. `bio` ≤ 2000 bytes. Members get the login `demo-<ref>`, an `@example.invalid` address and an unusable password |
| `forums` | ≤ 30. `ref` unique, `title` required (≤ 200), `kind` `forum` (default) or `category`. `parent` must be a category of the demo; a category has no parent (two levels). `slug` optional (from the title otherwise), `description` HTML ≤ 20 KB, `order`, `closed` |
| `topics` | ≤ 50. `ref` unique; `forum` must be a forum of the demo (categories hold no topics); `author` is `admin` (the user importing) or a member ref; `title` 3–120 characters; `content` HTML, required, ≤ 20 KB; `sticky`, `closed`; `hours_ago` 0 to 43,800 (five years) |
| `replies` | in each topic; ≤ 300 in all; `author`, `content` (≤ 20 KB), `hours_ago` |

The package validator rejects a demo that breaks a rule and lists every problem (`forum.topics[2].forum
"x" is not a forum of the demo …`). Content goes through the post sanitizer like any post. Set
`slug` explicitly when your menus or Web Builder parts link `/forums/f/<slug>/`. A primary menu item
for the forum is a custom link: `{"title": "Forums", "url": "/forums/"}`.

## Web Builder themes

On a site with Pursae Web Builder, the bundled **Hello Pursae** theme is active and the Theme Builder
parts design the site ([Hello Pursae and the Theme Builder](hello-pursae.md)). On forum views:

- **Header and footer** parts render as on any page. A forum view counts as a page (post type
  `page`, ID 0), so conditions such as `include/general` or `include/singular/page` match it.
- **Main templates never render on a forum view** (Single Page, Archive, …): the forum prints its own
  body inside Hello Pursae's page layout. Hello Pursae does not declare `pursae-forum`, so it prints the
  title and the forum prints no H1.
- The forum's tokens fall back to the builder's site tokens (`--wb-color-primary`,
  `--wb-color-background`, `--wb-color-text`, `--wb-font-body`, `--wb-font-heading`, printed on `:root`
  on pages with builder parts). With no CSS at all, the forum follows Web Builder › Settings.

To make a library theme that also imports into the builder, add `"web_builder": "web-builder/parts.json"`
to `theme.json` and ship the parts ([theme library](../web-builder-theme-library.md)). For the forum:

1. **Link the forum in the header.** Builder parts have no template functions, so links are paths:
   `"linkHref": "/forums/login/"`, `"primaryHref": "/forums/register/"`, menu items to `/forums/`.
   These assume the default base `forums`. A site that changes the base must edit them.
2. **Carry the forum tokens in the header's custom CSS** (the section's `style.css`), so every forum
   view takes the theme's colours. The rule must pass the builder's CSS sanitizer unchanged:

   ```css
   body .pf{--pf-accent:#B6F23A;--pf-accent-ink:#1B1035;--pf-surface:#1B1035;--pf-text:#F4F1FF;--pf-muted:#B9B0D9;--pf-border:rgba(244,241,255,.14);--pf-radius:10px;--pf-font:Inter,system-ui,sans-serif;--pf-font-heading:'Chakra Petch',Inter,system-ui,sans-serif}
   body .pf .pf-input,body .pf .pf-textarea{background:#120A26;color:#F4F1FF}
   ```

   The header is on every page, so the rule reaches every forum view. Add `.pf-usermenu`,
   `.pf-recent` and `.pf-stats` to the selector if the parts print them.
3. **Link the demo forums** from front-page tiles with `/forums/f/<slug>/`, using the explicit
   `slug` of each demo forum.
4. Keep the native side of the package (`templates/`, `forum.html`, the `--pf-*` tokens in
   `style.css`) working too: the same package can be activated as a plain theme.

Guild Hall and Study Circle are built this way.

## Multi Language

With Pursae Multi Language on:

- Forum content is shared by all languages. Topics are not translated per language, and every
  language shows the same forums.
- Forum URLs carry the language prefix (`/en/forums/…`). `.URLs`, `forum_url`, the pagination
  and redirects (login, posting) keep it. A hard-coded path (`/forums/` in a menu or in builder
  parts) carries no prefix of its own, so prefer `.URLs` and `forum_url` wherever a template can.
- The language switcher on a forum view links the same forum page in the other language. Public
  views print `hreflang` alternates and, on their first page, `x-default`; private views (login, search, a member search,
  account) print none.
- The forum's own strings (`.T`) follow the page's language. Use `__ .` in the wrapper and other
  theme templates for your own text ([translatable strings](templates.md#translatable-strings)).
  Level and badge names are translated through `.LevelChip`, `.BadgeChip` and `.BadgeText`, not
  through `forum_level` / `forum_badge`.

## Accessibility

The plugin templates are a good baseline. Keep the following when you override them:

- Breadcrumbs in `<nav aria-label>` with `aria-current="page"` on the last item.
- Notices: `role="alert"` for `.Error`, `role="status"` for `.Notice`.
- A `<label>` for every field (`.pf-sr` hides it visually), `aria-describedby` for help text, and
  `aria-invalid` with the error from `.Form.err_<field>` on sign-up.
- Avatars are decorative (`alt=""`, `aria-hidden`): print the name next to them. The level progress
  bar carries `role="progressbar"` and `aria-valuenow`.
- Every action works without JavaScript (a configured login captcha is the exception); keyboard focus uses `--pf-focus`. Reduced motion is respected.
- Keep `id="post-<id>"` on posts and `id="reply"` on the reply box: links land on them.
- Check contrast after changing `--pf-accent`, `--pf-accent-ink`, `--pf-muted` and the state colours,
  particularly on dark surfaces.

## Testing checklist

- [ ] Activate the theme with Pursae Forum **off**: no forum markup, no empty boxes, no errors.
- [ ] Switch the forum on and import the demo (or add a category, a forum and a few topics).
- [ ] Visit as a visitor: index, a category, a forum, page 2 of a forum, a topic with replies, page 2 of a
      topic, search with and without results, a profile, the member list, login, register.
- [ ] Visit as a member: the reply box, new topic, edit within the window, the account page (photo
      upload and remove, profile, password), the unverified banner (Settings › *New members confirm their email address before they can post*).
- [ ] Visit as a moderator: pending posts and their badges, a spam and a trashed topic opened by its link,
      the mod bar, block on a profile.
- [ ] Check the title: exactly one H1 on every view (with `supports` the body prints it; without, the theme).
- [ ] Check that each override renders, not its fallback: no `forum: theme … override` line in the log.
- [ ] Narrow the window to 360 px; check a long topic title, a long word, an image and a code block in a post.
- [ ] Turn off the member list (Forums › Settings › *Show the member list on the site*): `forum_url . "members"` and `.Extra.members_url`
      fall back gracefully.
- [ ] With Pursae Multi Language: open a forum page under the second language's prefix and follow links.
- [ ] Remove Demo Content: the forum is empty again and the theme still looks right.

## The shipped forum themes

The themes in `library/themes/` of the repository are complete references:

| Theme | Kind | Shows |
|-------|------|-------|
| **Campfire** (`campfire`) | package | `supports`, a wrapper with an optional sidebar (Customizer select), the only override in the set (`forum/index.html`, category cards), tokens on `:root` handed to the forum blocks as derived values (a darkened Customizer accent) |
| **Devboard** (`devboard`) | package | a dense developer Q&A board: a forum search in the header (`forum_url . "search"`), a stats sidebar beside the forum pages (`forum_stats`, `forum_recent_topics`; on by default, a Customizer select turns it off), compact rows and code blocks |
| **Guild Hall** (`guild-hall`) | package + Web Builder parts | dark surfaces with readable state colours, token pattern B (`inherit`), the header CSS with `body .pf{…}` in the parts |
| **Study Circle** (`study-circle`) | package + Web Builder parts | the same structure as Guild Hall on a light palette (teal and yellow) for study groups |

Each one has a demo with a forum section and passes the package validator.
