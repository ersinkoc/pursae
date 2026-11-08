<p align="center">
  <img src="web/wp-includes/images/brand/logo-256.png" alt="Pursae" width="104">
</p>

<h1 align="center">Pursae</h1>

<p align="center"><b>WordPress, rewritten in Go.</b><br>
One binary · PostgreSQL · the admin you already know · 16 built-in plugins.</p>

<p align="center">
  <img alt="Go 1.27" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL 17" src="https://img.shields.io/badge/PostgreSQL-17-336791?logo=postgresql&logoColor=white">
  <img alt="Plugins: Wasm" src="https://img.shields.io/badge/plugins-WebAssembly-654FF0?logo=webassembly&logoColor=white">
  <img alt="Docker" src="https://img.shields.io/badge/run-docker%20compose-2496ED?logo=docker&logoColor=white">
  <img alt="License GPL" src="https://img.shields.io/badge/license-GPL--2.0--or--later-blue">
</p>

<p align="center">
  <a href="#-quick-start">Quick start</a> ·
  <a href="#-tour">Tour</a> ·
  <a href="#-built-in-plugins">Plugins</a> ·
  <a href="#-what-pursae-sends-to-hubpursaecom">Privacy</a> ·
  <a href="docs/GUIDE.md">Full guide</a> ·
  <a href="docs/developer/README.md">Developer docs</a>
</p>

<p align="center">
  <img src="docs/readme/hero.webp" alt="Pursae admin, block editor, Web Builder and a site" width="100%">
</p>

## ⚡ Quick start

You need Docker (with Compose v2), git and make.

**On a server** (production build, updates from hub.pursae.com, behind your TLS proxy):

```sh
git clone https://github.com/pursae/pursae.git && cd pursae
cp docker/.env.example docker/.env    # set WPGO_DB_PASSWORD, WPGO_SITE_URL (https://…), SMTP
make prod-up                          # → 127.0.0.1:8088; point Caddy or nginx at it
```

Open your `https://` address right away and finish the installer: until then anyone who reaches the
site can run it. Updates arrive in **Dashboard › Updates** (one click); data lives in Docker volumes.

**On your machine** (development build to try it out; it does not talk to hub.pursae.com):

```sh
make docker-up                        # → http://localhost:8088
```

Ports, env vars, proxies, backups: **[docs/GUIDE.md](docs/GUIDE.md)**.

## ✨ Why Pursae

<table>
<tr>
<td width="25%" align="center"><h3>🐹</h3><b>One Go binary</b><br><sub>No PHP, no FPM, no extensions.</sub></td>
<td width="25%" align="center"><h3>🐘</h3><b>PostgreSQL 17</b><br><sub>Typed schema, JSONB, migrations on start.</sub></td>
<td width="25%" align="center"><h3>🧩</h3><b>Wasm plugins</b><br><sub>Signed, sandboxed, permission-gated.</sub></td>
<td width="25%" align="center"><h3>🚀</h3><b>Fast by default</b><br><sub>Built-in page cache, static builder output.</sub></td>
</tr>
</table>

## 🧭 Tour

<table>
<tr>
<td width="50%"><img src="docs/readme/dashboard.webp" alt="Dashboard"><p align="center"><b>Dashboard</b> — the WordPress admin, 1:1</p></td>
<td width="50%"><img src="docs/readme/block-editor.webp" alt="Pursae Editor"><p align="center"><b>Pursae Editor</b> — the real block editor</p></td>
</tr>
<tr>
<td><img src="docs/readme/web-builder-editor.webp" alt="Pursae Web Builder"><p align="center"><b>Pursae Web Builder</b> — visual pages, static output</p></td>
<td><img src="docs/readme/front-home.webp" alt="Twenty Twenty-Five"><p align="center"><b>Twenty Twenty-Five</b> — demo content in one click</p></td>
</tr>
</table>

<details>
<summary><b>📸 More core screens</b> — posts, media, themes, menus, plugins, users, health, settings</summary>
<br>
<table>
<tr>
<td width="50%"><img src="docs/readme/posts.webp" alt="Posts"><p align="center">Posts</p></td>
<td width="50%"><img src="docs/readme/media.webp" alt="Media Library"><p align="center">Media Library</p></td>
</tr>
<tr>
<td><img src="docs/readme/themes.webp" alt="Themes"><p align="center">Themes</p></td>
<td><img src="docs/readme/menus.webp" alt="Menus"><p align="center">Menus</p></td>
</tr>
<tr>
<td><img src="docs/readme/plugins.webp" alt="Plugins"><p align="center">Plugins</p></td>
<td><img src="docs/readme/users.webp" alt="Users"><p align="center">Users &amp; multi-role</p></td>
</tr>
<tr>
<td><img src="docs/readme/site-health.webp" alt="Site Health"><p align="center">Site Health</p></td>
<td><img src="docs/readme/settings.webp" alt="Settings"><p align="center">Settings</p></td>
</tr>
</table>
</details>

## 🧩 Built-in plugins

Native, off on a fresh install, one click under **Plugins**. Click a row to open it.

| | Plugin | In one line | Docs |
|---|---|---|---|
| 🛒 | **Pursae Commerce** | Store, cart, checkout, orders, premium modules | [→](docs/goocommerce-features.md) |
| 🎨 | **Pursae Web Builder** | Visual pages + Theme Builder | [→](docs/developer/web-builder.md) |
| 🔎 | **Pursae SEO** | Analysis, schema, sitemaps, redirects | [→](docs/seo-go-features.md) |
| ⚡ | **Pursae Cache** | Full-page cache + page optimization | [→](docs/developer/operations/caching.md) |
| 🛡️ | **Pursae Security** | 2FA, MFA codes, lockouts, activity log | [→](docs/adminsecgo-features.md) |
| ☁️ | **Pursae CDN** | S3/R2 offload, image service, purge | [→](docs/gocdn-features.md) |
| 🔌 | **Pursae Headless** | Content types, REST, GraphQL, webhooks | [→](docs/headlessgo-features.md) |
| 🍪 | **Pursae Cookie** | GDPR/KVKK banner, Consent Mode v2 | [→](docs/gooike-features.md) |
| 📈 | **Pursae Pixel** | Meta, GA4, Ads, TikTok + server-side | [→](docs/pixelgo-features.md) |
| ✉️ | **Pursae SMTP** | Mailers, email log, reports | [→](docs/wpgo-mail-smtp-features.md) |
| 🗂️ | **Pursae Custom Fields** | Field groups, CPTs, options pages | [→](docs/acf-go-features.md) |
| 🌍 | **Pursae Multi Language** | Languages, translations, hreflang | [→](docs/golylang-features.md) |
| 📝 | **Pursae Contact Form** | Forms, mail templates, spam guard | [→](docs/gontact-form-features.md) |
| 🚧 | **Pursae Maintenance** | Maintenance / coming-soon page | [→](docs/maintenance-go-features.md) |
| 💾 | **Pursae Backup Manager** | One-click backup &amp; migrate | [→](docs/backup-manager-features.md) |
| 📄 | **Classic Editor** | TinyMCE editing, per-user switch | — |

<details>
<summary><h3>🛒 Pursae Commerce</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/commerce-themes.webp" alt="Store Themes"><p align="center">Store themes with a full demo shop</p></td>
<td width="50%"><img src="docs/readme/commerce-extensions.webp" alt="Extensions"><p align="center">Premium modules, built in</p></td>
</tr><tr>
<td><img src="docs/readme/commerce-home.webp" alt="Commerce home"><p align="center">Home &amp; setup tasks</p></td>
<td><img src="docs/readme/commerce-analytics.webp" alt="Analytics"><p align="center">Analytics</p></td>
</tr></table>
<p align="right"><a href="docs/goocommerce-features.md">All features →</a></p>
</details>

<details>
<summary><h3>🎨 Pursae Web Builder</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/web-builder-editor.webp" alt="Web Builder editor"><p align="center">Sections, design, devices</p></td>
<td width="50%"><img src="docs/readme/web-builder-theme.webp" alt="Theme Builder"><p align="center">Theme Builder: header, footer, templates</p></td>
</tr></table>
<p align="right"><a href="docs/developer/web-builder.md">All features →</a></p>
</details>

<details>
<summary><h3>🔎 Pursae SEO</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/seo.webp" alt="SEO dashboard"><p align="center">Scores &amp; modules</p></td>
<td width="50%"><img src="docs/readme/seo-titles.webp" alt="Search Appearance"><p align="center">Titles &amp; meta</p></td>
</tr></table>
<p align="right"><a href="docs/seo-go-features.md">All features →</a></p>
</details>

<details>
<summary><h3>⚡ Pursae Cache</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/cache.webp" alt="Cache dashboard"><p align="center">Dashboard &amp; purges</p></td>
<td width="50%"><img src="docs/readme/cache-optimize.webp" alt="Page Optimization"><p align="center">Page optimization</p></td>
</tr></table>
<p align="right"><a href="docs/developer/operations/caching.md">All features →</a></p>
</details>

<details>
<summary><h3>🛡️ Pursae Security</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/security.webp" alt="Security dashboard"><p align="center">Security score &amp; checklist</p></td>
<td width="50%"><img src="docs/readme/security-settings.webp" alt="Security settings"><p align="center">2FA per role</p></td>
</tr></table>
<p align="right"><a href="docs/adminsecgo-features.md">All features →</a></p>
</details>

<details>
<summary><h3>☁️ Pursae CDN</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/cdn.webp" alt="CDN storage"><p align="center">S3-compatible storage</p></td>
<td width="50%"><img src="docs/readme/cdn-images.webp" alt="Image service"><p align="center">On-the-fly WebP image service</p></td>
</tr></table>
<p align="right"><a href="docs/gocdn-features.md">All features →</a></p>
</details>

<details>
<summary><h3>🔌 Pursae Headless</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/headless-builder.webp" alt="Content-Type Builder"><p align="center">Content-Type Builder</p></td>
<td width="50%"><img src="docs/readme/headless-graphql.webp" alt="GraphQL"><p align="center">GraphQL playground</p></td>
</tr></table>
<p align="right"><a href="docs/headlessgo-features.md">All features →</a></p>
</details>

<details>
<summary><h3>🍪 Pursae Cookie</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/cookie-banner.webp" alt="Banner themes"><p align="center">14 banner themes</p></td>
<td width="50%"><img src="docs/readme/cookie.webp" alt="Consent dashboard"><p align="center">Consent dashboard</p></td>
</tr></table>
<p align="right"><a href="docs/gooike-features.md">All features →</a></p>
</details>

<details>
<summary><h3>📈 Pursae Pixel</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/pixel.webp" alt="Pixel dashboard"><p align="center">All platforms at a glance</p></td>
<td width="50%"><img src="docs/readme/pixel-pixels.webp" alt="Pixels and APIs"><p align="center">Pixels &amp; Conversions APIs</p></td>
</tr></table>
<p align="right"><a href="docs/pixelgo-features.md">All features →</a></p>
</details>

<details>
<summary><h3>✉️ Pursae SMTP</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/smtp.webp" alt="SMTP settings"><p align="center">Pick a mailer</p></td>
<td width="50%"><img src="docs/readme/smtp-reports.webp" alt="Email reports"><p align="center">Email reports</p></td>
</tr></table>
<p align="right"><a href="docs/wpgo-mail-smtp-features.md">All features →</a></p>
</details>

<details>
<summary><h3>🗂️ Custom Fields · 🌍 Multi Language · 📝 Contact Form</h3></summary>
<table><tr>
<td width="33%"><img src="docs/readme/acf.webp" alt="Field Groups"><p align="center"><a href="docs/acf-go-features.md">Custom Fields →</a></p></td>
<td width="33%"><img src="docs/readme/languages.webp" alt="Languages"><p align="center"><a href="docs/golylang-features.md">Multi Language →</a></p></td>
<td width="33%"><img src="docs/readme/contact-edit.webp" alt="Contact form"><p align="center"><a href="docs/gontact-form-features.md">Contact Form →</a></p></td>
</tr></table>
</details>

<details>
<summary><h3>🚧 Maintenance · 💾 Backup Manager</h3></summary>
<table><tr>
<td width="50%"><img src="docs/readme/maintenance.webp" alt="Maintenance"><p align="center"><a href="docs/maintenance-go-features.md">Maintenance →</a></p></td>
<td width="50%"><img src="docs/readme/backup.webp" alt="Backup Manager"><p align="center"><a href="docs/backup-manager-features.md">Backup Manager →</a></p></td>
</tr></table>
</details>

## 🔒 What Pursae sends to hub.pursae.com

Every production build talks to **Pursae Manager at https://hub.pursae.com**. It is how a site gets
updates, the theme and plugin marketplace and licenses. Here is everything it sends, plainly:

| When | What |
|------|------|
| Once a day (and a minute after a core update) | the site's URL, Pursae version, language, OS and CPU architecture, license key (if you entered one), active theme, and the list of installed plugins and themes with their versions, whether they are active, and where they came from (built in, from the Hub, uploaded) |
| As it happens (sent in batches) | what was done with packages: a plugin installed, updated, activated, deactivated or deleted; a theme installed, updated, activated, previewed or deleted; a demo imported; the core updated — each with the slug, version and time |
| On every request to the Hub | the site's URL and license key as headers; the Hub also sees the server's IP address, as any web server does |

The Hub uses this for the numbers you see on it (theme and plugin download and install counts, which
versions sites run) and to decide what a license unlocks. **It never receives your content, users,
e-mail addresses, passwords, visitors, orders or anything else from your database.**

What a site gets back:

- **Updates**: the newest Pursae release, offered under **Dashboard › Updates** and installed with
  one click. Every core binary is signed by the Hub with a key endorsed by the Pursae release key
  built into your binary; the site refuses anything else.
- **Plugins**: Wasm plugins from the marketplace, each signed; by default a site installs only
  packages whose signature it can verify.
- **Themes**: the theme library with demo content, checked against their SHA-256.
- **Licenses** for pro and beta packages, and the product logo.

**Turning it off.** Set `WPGO_HUB=off` in the environment (with Docker: in `docker/.env`) and restart;
the site then sends nothing to hub.pursae.com. **We do not recommend it**: the site gets no update
notices or one-click updates — security fixes included —, no marketplace, no signed plugins and no
licenses, and you have to replace the binary yourself for every release. While it is off, every admin
page says so; remove `WPGO_HUB` and restart to get updates again.

## 🛠️ Build on it

| | |
|---|---|
| 🧩 **Plugins** — Go → signed Wasm, hooks, REST, settings | [docs/developer/plugins](docs/developer/plugins/index.md) |
| 🎨 **Themes** — Go templates + theme.json | [docs/developer/themes](docs/developer/themes/index.md) |
| 🔗 **REST API** — `wp/v2` + plugin routes | [docs/developer/rest-api](docs/developer/rest-api/index.md) |
| 🏪 **Hub** — marketplace, updates, licenses | [docs/developer/hub](docs/developer/hub/index.md) |
| ⚙️ **Operations** — config, TLS, backups, cron | [docs/developer/operations](docs/developer/operations/configuration.md) |

## 📚 More

[Full guide](docs/GUIDE.md) (install, configuration, troubleshooting, project layout) ·
[Developer docs](docs/developer/README.md) · [License](LICENSE) · [Notice](NOTICE)

<sub>GPL-2.0-or-later. "WordPress" is a trademark of its owner; Pursae is not affiliated with it.</sub>
