# Developer documentation

The source of the docs served by the public site (`wpgo-public-site`, http://localhost:8091/docs/).
Edit them here, next to the code they describe; `make sync-docs` in `wpgo-public-site` copies this
folder to its `content/docs/` and adds the built-in plugin pages (its `run`, `dev`, `build` and `docker-up` targets do it first).

Start here when writing a theme or a plugin:

| Writing… | Read |
|----------|------|
| A plugin (Wasm, Go SDK `sdk/go/wpgo`) | `plugins/index.md`, `plugins/your-first-plugin.md`, `plugins/manifest.md`, `plugins/hooks.md`, `plugins/host-api.md`, `plugins/permissions.md`, `plugins/packaging-and-signing.md` |
| A theme | `themes/index.md`, `themes/your-first-theme.md`, `themes/templates.md`, `themes/theme-json.md`, `themes/styles-and-scripts.md`, and the package format in `../theme-package.md` |
| Pursae Web Builder (pages, the editor build, `builder/`) | `web-builder.md` (overview: editing, publishing, Theme Builder, data, REST, `make builder`, patches, limits), then `web-builder-contract.md` (field-by-field contract; §11 Theme Builder) |
| Theme Builder templates, Hello Pursae, dynamic widgets, display conditions | `web-builder-theme-builder.md` (user guide), `themes/hello-pursae.md` (how templates replace a theme's output), contract §11 |
| Ready-made full themes in the editor ("Browse theme library"), making a library theme importable | `web-builder-theme-library.md` |
| A built-in plugin (what it does, what it leaves out) | `built-in-plugins/index.md` is generated: the sources are `../<name>-features.md` (`goocommerce`, `seo-go`, `acf-go`, `headlessgo`, `gocdn`, `gontact-form`, `golylang`, `adminsecgo`, `gooike`, `maintenance-go`, `wpgo-mail-smtp`); edit them there, `make sync-docs` publishes them |
| REST clients / plugin routes | `rest-api/*.md` |
| Running a site | `getting-started/*.md`, `operations/*.md` |

Working examples: `examples/plugins/` and `examples/themes/`. Page format (front matter, sections):
`wpgo-public-site/CONTENT.md`. This README is not published: `sync-docs` leaves it out.
