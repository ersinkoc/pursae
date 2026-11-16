---
title: Command line (pursae CLI)
description: The pursae / wpgo binary's commands, and the WP-CLI style command line that manages a site from a shell.
weight: 25
section: operations
---

## One binary, two names

The Pursae binary answers to both **`pursae`** and **`wpgo`**: `pursae serve` and `wpgo serve` do the
same thing. Release downloads are named `pursae-<os>-<arch>`; `make build` writes `bin/pursae` and a copy
`bin/wpgo`; the Docker image has both `/usr/local/bin/pursae` and `/usr/local/bin/wpgo`. Usage text and
log lines use the name the binary was started with. Without arguments the binary runs `serve`.

The operational commands are unchanged:

| Command | What it does |
|---------|--------------|
| `pursae serve` | run the site (the default) |
| `pursae launch` | container entrypoint (core updates, see [upgrading](../getting-started/upgrading.md#docker)) |
| `pursae migrate` | apply pending database migrations |
| `pursae version` | print the version |
| `pursae healthcheck [ready]` | container health check |
| `pursae setup-token` | print the install link with a new setup token |
| `pursae adminsec …` | Pursae Security recovery ([security](security.md)) |
| `pursae headless export\|import` | Pursae Headless entries as NDJSON |
| `pursae mcp` | stdio MCP bridge ([AI agents](ai-mcp.md)) |
| `pursae plugin new\|build\|keygen\|pack\|verify` | plugin packaging tool ([packaging](../plugins/packaging-and-signing.md)) |

## The WP-CLI style command line

Everything else is a WP-CLI style command line:

```bash
pursae <noun> <verb> [<args>...] [--flag=value] [--flag value] [--switch]
pursae help                    # every command
pursae help post               # the commands of one noun
pursae help post list          # one command: arguments, flags, examples
```

`--help` / `-h` after any command, including the operational ones above (`pursae setup-token --help`,
`pursae plugin pack -h`), shows its help and runs nothing.

Like WP-CLI loads WordPress, it works on the site's **database and files directly**: run it with the
same environment as the server (`DATABASE_URL`, `WPGO_UPLOADS_DIR`, …). With Docker:

```bash
docker compose exec wpgo pursae post list --post_type=page
```

In the container the server runs the binary installed in `WPGO_BIN_DIR` (`/data/bin`), which
Dashboard › Updates replaces, while `docker compose exec … pursae` starts the image's binary. When the
installed binary is newer, the command re-executes it, so the command line always runs the same code
as the server (`pursae core version` reports that version).

The database must be migrated (`pursae migrate`) and the site installed (`pursae core install`, or the
install screen) before most commands run; they say so otherwise. `core`, `db`, `backup`, `language`
and `option get` / `option list` also work on a site that is not installed yet.

### Global parameters

| Parameter | |
|-----------|---|
| `--format=table\|json\|csv\|yaml\|ids\|count` | output format of lists and records (default `table`) |
| `--fields=a,b` | the columns to show, in this order |
| `--field=x` | print only this field's value (one line per row) |
| `--yes`, `-y` | confirm destructive commands; without a terminal (scripts, cron, `docker exec` without `-t`, stdin from `/dev/null`) they refuse to run without it |
| `--quiet`, `-q` | no informational messages |
| `--user=<id\|login\|email>` | the user the command acts as (author, editor); default: the first administrator |
| `--help`, `-h` | help for the command |

Global parameters may also come before the command: `pursae --format=json post list` and
`pursae --user admin post create …` work.

Table, CSV and plain output (and `--field`) show control characters stored in the database as visible
escapes (`\x1b`), so text submitted by visitors (comment authors, user agents) cannot send escape
sequences to your terminal; JSON and YAML keep the exact value, escaped. CSV cells that start with
`=`, `+`, `-` or `@` (other than numbers) get a leading `'`, so a spreadsheet does not evaluate them.
An unknown `--field` or `--fields` name is an error. Machine formats (`json`, `csv`, `yaml`, `ids`,
`count`) print only data: summary lines such as `Success: …` go with `table` output.

Exit codes: `0` success, `1` failure (or "no" for checks such as `post exists`), `2` invalid command line.
An unknown command or flag gets a "did you mean" suggestion.

### What a running site sees

Changes reach a running server without a restart:

- **Settings** (options, plugin switches, theme switch) through the database's settings notification.
- **Page cache**: every change purges the affected pages on every running instance (the
  `wpgo_cache_purge` channel) and on a configured front proxy. A site that is not running keeps its
  persisted cache files; `pursae cache flush` removes them.
- **Users, sessions, application passwords and roles** are read per request.
- **Wasm plugins** are loaded or unloaded by the running server within about 20 seconds.

Some effects stay in the process that makes them, so the command says when a restart is needed:
page cache configuration, newly installed theme packages, trusted plugin keys and language packs a
running server already loaded.

The command line runs no Wasm plugin hooks (like `wp --skip-plugins`); the built-in plugins' effects
(SEO redirects, Multi Language, Web Builder, Pursae CDN) do happen.

### Installing a site from the shell

`pursae core install` does what the install screen does, without a browser or setup token (like
`wp core install`), so a site can be provisioned by a script:

```bash
pursae migrate
pursae core install --url=https://example.com --title="My Site" \
  --admin_user=admin --admin_email=me@example.com [--admin_password=…] [--locale=de_DE]
pursae core is-installed && echo installed   # exit code 0 or 1
```

Without `--admin_password` a password is generated and printed once. Installing an installed site is a
no-op that says so.
