# Changelog

All notable changes to Pursae are recorded here, in the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
format; versions follow [Semantic Versioning](https://semver.org/). Entries start with 0.3.0; for 0.2.x and
older, see the [GitHub releases](https://github.com/pursae/pursae/releases).

Every pull request that changes behavior adds a line under **Unreleased**. Releasing moves those lines
under a new `## [x.y.z] - YYYY-MM-DD` heading, which `tools/release.sh` publishes as the release notes
(see [Publishing a release](docs/developer/operations/releases.md)).

## [Unreleased]

## [0.4.0] - 2026-11-21

Editor gaps, complete personal-data tools, licensing notices and a security policy, plus WordPress
migration of passwords and WooCommerce products.

### Added

- REST `POST /wp/v2/media/{id}/edit`: the block editor's image crop, rotate and flip save a new
  "-edited" attachment, as in WordPress.
- Link previews in the block editor: `/wp-block-editor/v1/url-details` returns the page title,
  description, icon and image (cached for an hour).
- Patterns screen (`edit.php?post_type=wp_block`, Appearance › Patterns) with sync status, author,
  pattern categories and date; edit, trash, Export as JSON and Import from JSON. Patterns open in the
  block editor.
- `wp_pattern_category` taxonomy: REST CRUD, assignment through `/wp/v2/blocks`, the editor's category
  picker, an admin screen, and WXR export and import.
- Users moved from WordPress keep their passwords: WordPress 6.8+ bcrypt, phpass, plain bcrypt, legacy
  MD5 and PHP argon2i/argon2id hashes are verified and rehashed to argon2id at the first sign-in.
  `pursae user create --user_pass_hash` imports such a hash as-is.
- WordPress import: WooCommerce products and variations from a WXR file go into Pursae Commerce, with
  prices, stock, attributes, images, categories, tags, shipping classes, upsells, cross-sells, grouped
  products and reviews. Orders, refunds and coupons are counted in the report, not imported.
- `SECURITY.md`: supported versions, private vulnerability reporting through GitHub security advisories,
  response targets and disclosure policy.
- Security docs: every outbound connection of the core and the built-in plugins and how to turn each
  off, the core firewall (`WPGO_FIREWALL`), the setup token and the Web Builder trust model.
- `LICENSES/` with the full texts of every license used by bundled components; the Docker image ships
  `LICENSE`, `NOTICE` and `LICENSES/` in `/usr/share/doc/pursae/`. The Web Builder build writes
  `web/web-builder/THIRD_PARTY_LICENSES.txt`.

### Changed

- `NOTICE` lists every bundled third-party component (WordPress libraries, block editor packages,
  fonts, Web Builder, Flipbook, map and geolocation data, Go modules, Docker base image) with version,
  license, copyright holder and source.
- Web Builder (breaking): a code binding's custom field whose name no Pursae Custom Fields field group
  defines (other than a site import's `design_<n>` / `list<n>_<k>`) now renders empty instead of the
  post's plain meta; define a field group for it. `web_builder_bind` warns about such names.

### Fixed

- Personal data export and erasure now cover Pursae Contact Form entries, Web Builder form
  submissions, maintenance page subscribers, Pursae Forum data (posts, profile, likes, points,
  subscriptions, reports), Commerce billing/shipping fields, saved payment methods and gift cards,
  contact form notifications with the person as Reply-To, finished mail jobs and contact form webhook
  jobs; security logs are exported and reported as retained.
- `/wp-admin/edit.php?post_type=wp_block` no longer answers "Invalid post type."
- Patterns created as "Not synced" keep their sync status (`meta.wp_pattern_sync_status`).

### Security

- URL details are fetched only from public addresses (checked at connect time and on every redirect),
  on ports 80/443/8080, with a 10 s timeout and a 150 KB cap.
- Work factors of imported password hashes are capped, and a wrong password against a legacy hash takes
  as long as the unknown-user path, so response times do not reveal which accounts exist.
- Web Builder: a code binding no longer prints arbitrary non-underscore post meta of the posts a loop
  shows.

## [0.3.0] - 2026-11-21

Safe upgrades: every release is now gated on green CI, every pull request proves that upgrading from
older releases keeps the data, and an older binary no longer runs silently on a newer schema.

### Added

- Release gate: `tools/release.sh` refuses to publish a commit without a successful CI run on GitHub
  (every job green), a stable release from a branch other than `main`, or a stable version without a
  CHANGELOG section. `RELEASE_SKIP_CI_CHECK=1` overrides the CI check in an emergency, with a warning.
- Release notes come from this file's section for the version (`NOTES_FILE` still overrides them).
- golangci-lint runs in CI (new issues only) and as `make lint`.
- Upgrade-path test (`make upgrade-test`, CI job `upgrade`): installs v0.2.0 and the last three releases,
  seeds users, content, media, menus, settings and a Web Builder page through each release's own paths,
  then migrates to the current code and checks that every seeded value, login and file survived.
- Migration lint: new migrations need a unique `NNNN_name.sql` number, and destructive statements
  (DROP TABLE/COLUMN, ALTER … TYPE, RENAME) need a `-- pursae:contract` marker.
- Schema guard: the database records the oldest release that may run on it (`schema_meta`, floor
  0.2.4). A binary below the floor that finds migrations it does not know refuses to start with an
  actionable message; `WPGO_ALLOW_OLDER_BINARY=1` overrides it. `/readyz` reports the condition.
- Automatic core updates write a database backup (`pre-update-*.zip`, newest 3 kept) first and skip
  the update, with a notice in Dashboard › Updates, when the backup fails.

### Changed

- A core update is confirmed when `/readyz` answers 200 (database reachable, no pending migration,
  schema guard satisfied) instead of when `/wp-login` answers below 500.
- Automatic updates stay within the installed minor version; 0.3.x → 0.4.0 needs a click in
  Dashboard › Updates.
- Reinstall refuses to go back to a release below the schema's floor.
- Docs: `upgrading.md` describes the expand/contract policy for schema changes instead of claiming that
  releases only add schema (0028, 0038 and 0105 drop tables or columns).
