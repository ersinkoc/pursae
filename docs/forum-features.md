# Pursae Forum: feature inventory (0.1)

Pursae Forum is a native (compiled-in) Pursae plugin for discussion boards in the class of bbPress:
categories and forums, topics and replies, front-end sign-up with email confirmation and Google
sign-in, moderation, member levels and badges, profile photos and a member list. It is written
from scratch for Pursae. Like every native plugin it is **off on a fresh install**
(`native_pursae_forum`): while it is off every forum URL, the sign-in routes, the admin screen and
the `forum/v1` REST routes answer 404, the menu is gone and the theme functions print nothing.
Deactivating keeps the forums, topics, replies, roles and members.

## What it does

- **Forums in any theme.** Every view renders as a page of the active theme, with the forum's own
  stylesheet (`forum.css`, scoped to `.pf`). Themes made for it declare
  `supports: {"pursae-forum": true}`, wrap the views in `templates/forum.html` and may replace any
  view (`docs/developer/themes/forum.md`).
- **Structure.** Categories hold forums (two levels). A forum can be closed to new topics.
  Topics can be pinned (sticky, first in their forum) and closed to replies.
- **Posting without JavaScript.** Plain forms, POST, then redirect. Text with basic HTML (links,
  bold, italic, lists, quotes, code), sanitized on save and again on output; links get `rel="nofollow ugc"`.
  Titles 3–120 characters, posts 2–20,000.
- **Search** over topics and replies (PostgreSQL full-text, `/forums/search/?q=`).
- **Profiles** (`/forums/u/<nicename>/`): photo, level and progress, badges, counts, bio, recent activity.
- **Member list** (`/forums/members/`): search by name, sort by newest, most active or name; cards
  with photo, level, badges and counts. It can be switched off (Settings › *Show the member list on the site*).
- **Shortcodes** for ordinary pages: `[pursae_forum]` (the forum list) and
  `[pursae_forum view="recent" limit="5"]` (recent topics).
- **Theme functions**: `forum_user_menu` (Log in / Join, or the member's avatar and Log out),
  `forum_recent_topics`, `forum_stats`, `forum_url`, `forum_is_view`, `forum_on`.
- **Demo content**: a theme's `demo/demo.json` may carry members, forums, topics and replies; importing
  it switches the forum on. Four library themes ship with one: Campfire, Devboard, Guild Hall and Study Circle.

URLs (default base `forums`, changeable; always with a trailing slash, also with plain permalinks):

| URL | View |
| --- | --- |
| `/forums/` | index: categories, forums, latest topics |
| `/forums/f/<slug>/[page/N/]` | a forum (or a category's forums); `/forums/f/<slug>/new/` new topic |
| `/forums/t/<id>/<slug>/[page/N/]` | a topic; a wrong or missing slug redirects (301) |
| `/forums/r/<id>/` | a reply: redirects to its page of the topic, `#post-<id>` |
| `/forums/edit/<id>/` | edit a topic or reply |
| `/forums/search/?q=`, `/forums/u/<nicename>/`, `/forums/members/` | search, profile, member list |
| `/forums/login/`, `/forums/register/`, `/forums/verify/`, `/forums/account/` | membership |

## Membership

- **Email sign-up** (`/forums/register/`): username, email, password (at least 8 characters, or
  the Pursae Security policy when it is on). A confirmation link is sent. It works for 48 hours and only
  its SHA-256 is stored. Unconfirmed members can log in and read; while *New members confirm their email
  address before they can post* is on (the default) they cannot post until they confirm. They can ask for a new link.
- **Login** on the forum's own page, with the core's throttling, the Pursae Security login rules and the
  login captcha when one is set up. Logging out returns to the forum.
- **Account page**: display name, bio, password (Google-only members choose one), profile photo,
  Google link and unlink.
- **Profile photos**: JPEG, PNG, WebP or GIF up to 2 MB. Larger than 6000×6000 or smaller than 16×16 is
  refused before decoding. The photo is turned upright (EXIF), cropped to a centred square of at
  most 512 px and stored in the Media Library, owned by the member. It is the member's site avatar
  too (`avatar_id`). Ten changes an hour. Removing it deletes only the forum's own upload. Members
  without a photo get an initials disc in a colour derived from their ID. No Gravatar.
- **Roles.** Activation adds *Forum Participant* (`forum_participate`) and *Forum Moderator*
  (`forum_participate`, `forum_moderate`). It also gives `forum_participate` to subscribers,
  contributors, authors and customers, and both caps to administrators, editors and shop managers.
  New members get Forum Participant (or Subscriber, a setting).
- **Spam guards** on the logged-out forms: a honeypot field, a signed time token, a captcha when one is
  set up, and rate limits (20 sign-up attempts per network in 10 minutes, 5 new accounts per network an
  hour, 3 confirmation mails per address and per network an hour). Posting: a flood interval (15 s by
  default), 30 posts an hour, the Discussion screen's disallowed words. Moderators skip the limits.
- **Enumeration.** While the core blocks user enumeration (the default), a profile is a 404 until
  the member has posted, and the member list shows only members who posted.

## Google sign-in

"Continue with Google" on the login and sign-up pages, and "Connect Google" on the account page:
OAuth 2.0 with PKCE and OpenID Connect, standard library only. The flow state travels in a short-lived
signed cookie, so nothing is stored on the server.

Setup (Forums › **Google sign-in**, which shows both values to copy):

1. In the Google Cloud Console, create an OAuth consent screen (External, scopes `openid`, `email`,
   `profile`) and an OAuth client of type **Web application**.
2. Add the site origin under *Authorized JavaScript origins* and
   `https://<your-site>/forum-auth/google/callback` under *Authorized redirect URIs*. The route is fixed
   and has no language prefix, so the URI never changes.
3. Paste the Client ID and Client secret, tick **Enable** and save. The secret is write-only: it is never
   shown again, and `/pursae/v1/options/forum` masks it.

Google requires HTTPS (except `localhost`). A Google account whose verified email matches an existing
member is linked to that member, unless the member can edit content, moderate or manage the site: they
log in with their password and connect Google from the account page. Linking an account that never
confirmed its email resets its password and sessions (protection against account pre-hijacking).
Administrators, and members with a Pursae Security second factor, always log in with their password.
While registration is closed, Google cannot create accounts.

## Levels and badges

- **Points** = published topics × topic points (3) + published replies × reply points (1). Nothing is
  stored per post; spam and trash do not count.
- **Levels** by points: Newcomer 0, Member 10, Regular 50, Contributor 150, Veteran 400, Legend 1000.
  Each level has a name, a minimum, an icon (43 line icons) or a Media Library image, and a colour. Up
  to 30 levels. Profiles show the progress to the next one.
- **Badges** with a rule: topics started, replies written, posts, points or days since joining (each
  with a threshold); verified email; Google account linked; forum moderator; or *awarded by hand*.
  Defaults: First Post, Conversation Starter (5 topics), Helpful (50 replies), Prolific (250 posts),
  One Year Club, Verified, Moderator, Founding Member (by hand). Each badge has a name, description, icon
  or image, colour and an on/off switch. Up to 100; the list order is the display order.
- Rule badges are computed from the member's numbers when they are shown, so they appear and disappear
  by themselves (behind the page cache, with its delay). Manual
  awards are stored per member (`forum_badges`). Administrators award and revoke them from the admin member list
  (bulk actions, or a member's **Manage** page) or from the member's forum profile.
- Levels and badges show next to every post (level and up to three badges), on profiles, member cards
  and in the admin. Default names are translated; custom names print as typed.

## Moderation

- **Front end**: moderators get a button bar under each post: approve, pin and unpin, close and reopen
  (topics), spam and not spam, trash and restore. They see pending posts in place and can open a spam or
  trashed topic by its link, with the author's IP address, and can block or unblock a member from the profile. Closed forums and
  topics do not stop them.
- **Admin › Forums › Moderation**: topics and replies by status (pending, published, spam, trash), with
  search, row actions and bulk actions.
- **Holding posts**: optional moderation of a member's first post; posts that hit the disallowed words
  are refused.
- **Blocking** takes `forum_participate` away (a per-user deny), so the member keeps the account but
  cannot post. Administrators cannot be blocked, and only an administrator can block a moderator.
- **Editing**: members edit their own posts within the edit window (30 minutes, 0 to turn it off,
  up to a week); moderators always can. Edited posts say so.

## The admin screen

**Forums** in the admin menu, after Comments, visible with `forum_moderate`:

| Tab | Who | What |
| --- | --- | --- |
| Overview | moderators | totals, the moderation queue (pending and spam, linked), a 30-day bar chart of new members, topics and replies (plain CSS, no script), top contributors, recent activity |
| Forums | administrators | categories and forums: add, edit, order, close, delete (only when empty) |
| Moderation | moderators | the queue described above |
| Members | moderators (roles and badges: administrators) | a list table with photo, level, badges, topics, replies, points, joined, last active, status and Google. Filters (unverified, blocked, moderators, Google), search (also by email), sorting; row actions (manage, view profile, edit user, verify, block or unblock); bulk actions to verify, block, unblock, award or take back a badge and set the forum role; a per-member **Manage** page |
| Badges & Levels | administrators | point weights, the levels table, the badge list with add, edit, reorder and delete |
| Settings | administrators | URL base, forum title, topics and replies per page, registration, require confirmation, default role, first-post moderation, edit window, flood interval, the member list |
| Google sign-in | administrators | setup steps, client ID and secret |

Settings are also readable and writable at `/pursae/v1/options/forum`.

## Multi Language

With Pursae Multi Language on, forum URLs answer under each language's prefix (`/en/forums/…`) and keep
it on every link, form and redirect. The forum's own text follows the page's language. The language
switcher on a forum page leads to the same forum page in the other language. Public views print
`hreflang` alternates, `x-default` (on the first page) and a canonical URL in the page's language; private views (login,
search, account) print none. The content itself (forums, topics, replies) is shared by every language.

## Caching and SEO

Pages a visitor sees are cacheable by Pursae Cache and tagged `forum`; every forum write purges the
tag (a new photo purges everything). A logged-in member's pages carry their forms and are sent
`no-store`. Login, sign-up, email confirmation, account, search, new topic, edit and a member search are `noindex` and
never cached. Forum views get a canonical URL (with `/page/N/`) and no shortlink. Pursae SEO does
not add its head to them.

## API

- `GET /wp-json/forum/v1/forums`, `/forums/<id>/topics`, `/topics/<id>`, `/topics/<id>/replies`: public,
  read-only, published content only (`docs/developer/rest-api/endpoints.md`).
- `/pursae/v1/options/forum`: the settings, like the other built-in plugins.

## Not in 0.1

- ✗ Subscriptions and email notifications for new replies.
- ✗ Per-language forums or translated topics: content is shared by every language.
- ✗ Guest posting, private or hidden forums, per-forum moderators, topic tags, reply threading,
  topic merge and split, favourites, revision history.
- ✗ Forum RSS feeds, sitemap entries for forums and topics, forum results in the site search.
- ✗ Web Builder widgets for forum content. Web Builder themes link the forum and style it through the
  `--pf-*` tokens; the forum body is not designed in the builder.
- ✗ Changing the email address from the forum (it is done on the core profile screen), and importers
  from bbPress or other boards.

## Files

| File | What it holds |
| --- | --- |
| `internal/server/forum.go` | constants, settings, activation and roles, plugin row, menu, routes |
| `internal/server/forum_types.go` | the domain types and `forumPage`, the data of every view template |
| `internal/server/forum_store.go` | data access: lists, create, moderate, recount, authors |
| `internal/server/forum_urls.go` | URL building, path parsing, the language switcher's forum URLs |
| `internal/server/forum_render.go` | the synthetic page, theme overrides, shortcodes, pagination, messages |
| `internal/server/forum_front.go`, `forum_views.go`, `forum_post.go` | the front end: intercept, views, forms, moderation |
| `internal/server/forum_members_front.go` | the public member list |
| `internal/server/forum_account.go`, `forum_mail.go`, `forum_google.go` | login, sign-up, confirmation mail, account, photo upload, Google sign-in |
| `internal/server/forum_gamify.go`, `forum_icons.go`, `forum_avatar.go` | points, levels, badges, member queries, the overview, icons, photos and initials |
| `internal/server/forum_funcs.go` | the template functions for themes |
| `internal/server/forum_admin.go`, `web/admin/templates/pages/forum*.html` | the admin screen |
| `internal/server/forum_demo.go` | the theme demo's `forum` section |
| `internal/server/rest_forum.go` | `forum/v1` |
| `internal/db/migrations/0097_forum.sql` | the `forum`, `topic` and `reply` content types, their indexes, one account per Google subject |
| `web/forum/templates/*.html`, `web/forum/assets/forum.css` | the default view templates and the stylesheet |
| `library/themes/{campfire,devboard,guild-hall,study-circle}` | the forum themes |
| `internal/server/forum_*_test.go`, `theme_library_forum_*_test.go` | unit and integration tests |
