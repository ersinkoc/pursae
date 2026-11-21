# Security policy

Thank you for helping keep Pursae and the sites that run it safe. This page says which versions get
security fixes, how to report a vulnerability privately and what happens after you do.

## Supported versions

Pursae is pre-1.0 (`0.x`). Until 1.0, only the **latest minor release** gets security fixes. A fix
ships as a new patch release of that minor (for example `0.2.9` → `0.2.10`), announced in the
[changelog](CHANGELOG.md) and the GitHub release notes, and offered in **Dashboard › Updates**.
Older minors do not get backports: upgrade to the latest release
([upgrading](docs/developer/getting-started/upgrading.md)).

| Version | Security fixes |
|---------|----------------|
| Latest `0.x` minor (the newest release) | yes |
| Any older `0.x` minor | no, upgrade |
| `main` (unreleased) | fixed before the next release |

Pre-1.0 releases may change behaviour between minors when a fix needs it; the changelog says when
a fix changes what a site does (a stricter default, a refused request).

After 1.0 this page will name the supported release lines.

## Reporting a vulnerability

**Do not open a public issue, discussion or pull request for a vulnerability.**

Report it privately through GitHub: open
[**Report a vulnerability**](https://github.com/pursae/pursae/security/advisories/new) on the
repository's **Security** tab (GitHub private vulnerability reporting). Only you and the
maintainers see the report; we discuss it and prepare the fix in a private security advisory, and
can add you to it as a collaborator.

### What to include

- the affected version (`wpgo version`, or **Dashboard › Updates**) or commit, and
  whether it is a production build or a development (`devhub`) build;
- the component: core, a built-in plugin (which one), the Pursae Manager (Hub) client, the Web
  Builder, the CLI or the MCP server;
- the steps to reproduce, with a minimal proof of concept (a request, a payload, a small design or
  plugin package), and the role or capability the attacker needs (visitor, subscriber, author,
  administrator, an application password with which scope);
- what an attacker gains (data read, code run, privileges gained, a site taken down) and any
  setting or environment variable the issue depends on;
- whether the issue is already public or known to others, and how you would like to be credited.

Test only against sites you own or are allowed to test. Do not access or change other people's data,
do not degrade a service for others (no load or denial-of-service tests against sites you do not
run), and stop and report as soon as you have shown the issue.

## What happens next

| Step | Target |
|------|--------|
| Acknowledge the report | within **3 business days** |
| First assessment (confirmed or not, severity, affected versions) | within **10 business days** |
| Fix released, **critical** (unauthenticated code execution, account takeover, mass data exposure) | within **7 days** of confirmation |
| Fix released, **high** | within **30 days** |
| Fix released, **medium** | within **60 days** |
| Fix released, **low** | in a following regular release, within **90 days** |

Severity follows CVSS v3.1 as a guide, adjusted for Pursae's defaults (a flaw that needs an
administrator, or a setting that is off by default, rates lower). We keep you informed at least at
every step above and when a target slips, and say why.

## Disclosure

We follow coordinated disclosure:

1. We confirm the issue and agree on the severity with you.
2. We prepare the fix in a private advisory (and a private fork when needed), and request a CVE
   through GitHub when the issue warrants one.
3. We release the fixed version, then publish the advisory with the affected and fixed versions,
   the workaround if there is one, and credit to you unless you ask not to be named.
4. Please keep the details private until the advisory is published, or for 90 days after your
   report, whichever comes first. If we need longer, we will ask and explain why; if a
   vulnerability is being exploited in the wild, we may publish sooner.

## Scope

In scope:

- **Core**: the `wpgo` binary (`cmd/wpgo`, `internal/…`), its HTTP server, REST API, admin,
  installer, login and sessions, the block editor integration, the CLI and the MCP server, the
  Docker images and compose files in this repository.
- **Built-in plugins** shipped with the binary (Pursae Commerce, Web Builder, SEO, Cache, Security,
  CDN, Headless, Cookie, Pixel, Analytics, Forum, SMTP, Custom Fields, Multi Language, Contact Form,
  Maintenance, Backup Manager and Classic Editor; see the [README](README.md#-built-in-plugins)).
- **The Pursae Manager (Hub) client** inside the binary: update checks and core self-updates,
  package signatures and the pinned root of trust, licenses, marketplace downloads and the data a
  site sends ([Pursae Manager](docs/developer/hub/index.md)).
- **The WebAssembly plugin sandbox** itself: a plugin escaping its permissions, memory or time
  limits, or reading what it was not granted, is in scope.

Out of scope:

- **Third-party WebAssembly plugins** and themes not shipped in this repository: report those to
  their authors. (An escape from the sandbox they run in is in scope, see above.)
- The hosted Hub service at `hub.pursae.com` beyond the client in this repository: report it here
  too and we will route it, but it is not covered by the targets above.
- Issues that need an administrator to act against their own site where WordPress allows the same
  (an administrator with `unfiltered_html` publishing a script, importing a design whose scripts
  they chose to run: see the [trust model](docs/developer/operations/security.md#trust-model)),
  unless they cross a boundary Pursae documents.
- Findings from automated scanners without a demonstrated impact, missing headers that do not lead
  to an attack, self-XSS, clickjacking of pages with no state-changing action, rate limits on
  non-sensitive routes, and denial of service by sheer request volume.
- Sites running a development (`devhub`) build, `WPGO_TRUST_DEV_KEY=1`,
  `WPGO_ALLOW_UNSIGNED_PLUGINS=1` or other settings documented as development only.

The operator's checklist for a production site is in
[docs/developer/operations/security.md](docs/developer/operations/security.md).
