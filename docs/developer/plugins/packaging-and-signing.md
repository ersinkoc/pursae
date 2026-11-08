---
title: Packaging and signing
description: The package format, the wpgo-pkg tool, Ed25519 signatures and how sites decide what to trust.
weight: 120
section: plugins
---

## Package layout

A package is a zip with these files at its root and nothing else:

| File | Required | Limit |
|------|----------|-------|
| `plugin.json` | yes | 64 KiB ([manifest](manifest.md)) |
| `plugin.wasm` | yes | 64 MiB; a WASI reactor built with `-buildmode=c-shared` |
| `readme.md` | no | 1 MiB; Markdown shown on the marketplace page |
| `icon.svg` | no | 256 KiB; the marketplace icon |
| `assets/…` | no | 2 MiB per file, 16 MiB total ([assets](assets.md)) |
| `signature` | if the site requires signatures | 1 KiB |

## wpgo-pkg

The tool lives in the Pursae repository:

```bash
go build -o wpgo-pkg ./internal/plugins/cmd/wpgo-pkg

wpgo-pkg keygen -out mykey [-comment "My plugins key"]
#   writes mykey.pub (publish this) and mykey.key (mode 0600; keep it private); existing files are kept
wpgo-pkg pack -dir ./my-plugin -wasm plugin.wasm -key mykey.key -out my-plugin-1.0.0.zip
#   packs plugin.json, readme.md, icon.svg and assets/** from -dir; omit -key for an unsigned package
wpgo-pkg verify -pub mykey.pub my-plugin-1.0.0.zip
#   verifies the signature and prints the manifest summary
```

Key files are base64 with optional `#` comment lines — the same format `WPGO_PLUGIN_KEYS` and
the `plugin_trusted_keys` option expect.

`tools/build-plugins.sh [plugin-dir …]` builds and packs the example plugins (or the directories
you pass) into `examples/plugins/dist/`, generating a development key there if none exists. Use
`WPGO_SIGNING_KEY=/path/to/key` to sign with your own.

## The signature

```text
digest    = SHA-256("wpgo-plugin-v1\n" || u64be(len(plugin.json)) || plugin.json || u64be(len(plugin.wasm)) || plugin.wasm)
signature = base64(Ed25519.Sign(privateKey, digest)) + "\n"
```

The length prefixes make the concatenation unambiguous; the domain prefix keeps a plugin
signature from being confused with any other signature made with the same key. Assets are not
in the digest directly: `pack` writes their SHA-256 into `plugin.json`, which is. `readme.md`
and `icon.svg` are not signed (they are display-only).

## What a site accepts

| Site configuration | Signed by a trusted key | Signed by an unknown key | Unsigned |
|--------------------|-------------------------|--------------------------|----------|
| default (keys configured, `plugin_allow_unsigned` off) | installs | rejected | rejected |
| `plugin_allow_unsigned` on, some trusted keys | installs | rejected (a bad signature is never accepted) | installs |
| `plugin_allow_unsigned` on, no trusted key at all | — | installs as unverified (there is no key to check against) | installs |
| no trusted keys, `plugin_allow_unsigned` off | — | rejected | rejected |

`plugin_allow_unsigned` is an option without a screen (set it with `wpgo` SQL access or the REST
settings of a development site); never turn it on in production.

Trusted keys come from `WPGO_PLUGIN_KEYS` (comma-separated), the `plugin_trusted_keys` option
(both read at start-up), the Manager's key list (replaced on every index refresh, so a key the
Manager stops listing stops being trusted; with a pinned release root key only keys the root
endorsed, see [Pursae Manager](../hub/index.md#root-of-trust)), and — in development only — the
`dev-key.pub` next to the example packages when `WPGO_TRUST_DEV_KEY=1`.

## Updates and the signer

The site records which key signed each installed plugin. A new version must come from the same key
(any two keys of Pursae Manager count as the same publisher, since the Manager re-signs every
package and rotates its key) and must not be older than the installed one:

- from Pursae Manager (Update now, Update Plugins) such a package is always refused;
- an upload stops and offers **Replace current with uploaded**, which installs it after you confirm.

Before a Manager update the previous package is kept as `<slug>.zip.prev`; if the new version cannot
be loaded, the previous one is put back and loaded with its permissions.

The install screen records the package digest; the Plugins screen shows the plugin's name,
version, author and permissions read from the package, and the last lines of its log.

## Versioning

Bump `version` for every release. The Hub refuses a version that already exists or is lower than
the latest published one; a site re-installing the same slug replaces the package file.
