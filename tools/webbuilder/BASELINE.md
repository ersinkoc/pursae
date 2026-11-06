# Web builder replacement: baseline (W0)

Recorded on 2026-11-06 at `web-builder` 3f00c1f (branch `wb/w0`), go1.27.3 darwin/arm64,
before any change of the old-builder removal. Later packages compare against this.

## Build and vet

| Command | Result |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |

## Tests

Command (own database, Postgres on localhost:5432):

```sh
WPGO_TEST_ADMIN_DB='postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable' \
WPGO_TEST_DB='postgres://wpgo:wpgo@localhost:5432/wpgo_test_w0?sslmode=disable' \
go test -p 1 -count=1 ./internal/...
```

Exit code 0. **No test fails on baseline.** Any failure after a merge is new.

| Package | Result | Passed | Skipped |
|---|---|---|---|
| internal/auth | ok | 4 | 0 |
| internal/blocks | ok | 8 | 0 |
| internal/builder | ok | 35 | 1 |
| internal/builder/atomic | ok | 8 | 0 |
| internal/cache | ok | 51 | 0 |
| internal/caps | ok | 2 | 0 |
| internal/config | ok | 3 | 0 |
| internal/coreupdate | ok | 5 | 0 |
| internal/db | ok | 2 | 0 |
| internal/gocdn | ok | 26 | 0 |
| internal/i18n | ok | 11 | 0 |
| internal/links | ok | 2 | 0 |
| internal/mail | ok | 32 | 0 |
| internal/media | ok | 8 | 0 |
| internal/plugins | ok | 49 | 0 |
| internal/server | ok (464s) | 544 | 12 |
| internal/settings | ok | 3 | 0 |
| internal/store | ok | 1 | 0 |
| internal/wp | ok | 9 | 0 |
| internal/gocdn/s3fake, internal/mail/smtptest, internal/plugins/cmd/wpgo-pkg | no test files | – | – |

Counts are top-level tests (subtests not counted separately).

### Skipped on baseline (pre-existing, not failures)

| Test | Reason |
|---|---|
| builder TestProBDump | `PROB_DUMP` not set (manual dump helper) |
| server TestAdminUntranslated, TestStoreFrontUntranslated | `WPGO_I18N_MISSES` not set (manual report) |
| server TestHubMarketplaceAndUpdates, TestLicenseScreenAndHeaders, TestHubAccessBadges, TestHubEventsBatching, TestHubPluginUpdate, TestCoreSelfUpdate, TestManagerThemeLibrary, TestManagerThemeUpdate, TestManagerThemePreinstall | example packages not built: run `tools/build-plugins.sh` first (`examples/plugins/dist/*.zip` missing) |
| server TestSEOGoHeadHidesProtectedPostContent | SEO-Go emits no FAQ schema in this setup (test skips itself) |

Not run here: `go test ./cmd/... ./sdk/... ./examples/...` (outside `./internal/...`).

## Old-builder references

`tools/webbuilder/inventory.sh` (read-only; `--full`, `--summary`, `--check`) lists every
reference with file:line, grouped by the removal plan. Baseline totals (lines that match):

| Group | Files | Lines |
|---|---|---|
| R1a themes | 21 | 91 |
| R1b commerce / lang / security / misc | 18 | 126 |
| R1c routing / admin / front | 10 | 107 |
| R1d plugin registry / settings | 2 | 15 |
| R2 delete (whole files and trees, web/web.go) | 748 | 11348 |
| R2-tests (tests the plan fixes in R2) | 7 | 30 |
| KEEP (migrations 0005, 0006, 0007, 0025) | 4 | 30 |
| DOCS (docs, README, NOTICE) | 8 | 51 |
| I18N (web/languages catalogs) | 130 | 1409 |
| UNASSIGNED | 8 | 16 |

R0 is reported per symbol (the helpers that live in builder files and their outside callers);
those callers are counted in the groups above.

Notes for later packages:

- `Makefile` and `docker/Dockerfile` have no references.
- UNASSIGNED files the plan does not name: `render.go` (`gweShell`, real code, likely R1c),
  `i18n_adminjs.go` (R0 caller of `gweJSLiteral`/`gweJSHasWord`/`gweJSNotText`/`gweJSON`),
  `i18n.go:31` (the `"elementor"` slug in a WordPress plugin-slug list), `headlessgo_test.go:811`
  (asserts "Elementor" is absent), and comments in `gocdn_fetch.go`, `gontact_plugin.go`,
  `mailsmtp.go`, `seogo.go`.
- R0 callers outside the groups: `cspAllow` in `gocdn_rewrite.go` and `gontact_render.go`;
  `msSeal`/`msReveal` in `mailsmtp_admin.go`, `mailsmtp_log.go`, `mailsmtp_test.go`;
  `installedThemes` in `hub_telemetry.go`, `hub_updates.go`.
- The Google Fonts helpers (`gowebeditor_fonts*.go`) have no callers outside R2 files today.
- `internal/settings/settings.go:38-39` holds `native_golementor` and `golementor_previous_theme`
  (R1d).
- I18N: 130 catalogs carry stale old-builder message ids (e.g. `#gwe-add-new` links); they go when
  the catalogs are regenerated after R2.
- Outside this repo: `../wpgo-public-site` and the root `CLAUDE.md` "Page builder" decision.
