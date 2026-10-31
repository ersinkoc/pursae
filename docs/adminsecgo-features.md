# AdminSecGo — feature inventory

AdminSecGo is WordPress Go's native (compiled-in) login-security plugin. It covers the feature
level of **Wordfence Login Security**, **Two Factor**, **WPS Hide Login** and the login, user and
hardening parts of **Solid Security** — without a firewall service or malware scanner.

Like the other native plugins it is listed on **Plugins › Installed Plugins**, starts **inactive**
and adds nothing while inactive: no menu, no route, no header, no login step, no log row. Every
hook in the core starts with `adminsecOn()` (one cached option read). **Deactivating keeps
users' factors** (they are simply not asked for), keeps the settings and the log, and brings
`/wp-login` back.

Legend: ✅ implemented · ◐ implemented with a WordPress Go specific difference · — left out (reason given).

## 1. How it plugs into WordPress Go

| | WordPress Go | |
|---|---|---|
| Activation switch | Option `native_adminsecgo`; settings in one JSON option `adminsecgo_settings` (parsed once per stored version) | ✅ |
| Storage | Migration `0014_adminsecgo.sql`: `adminsec_factors`, `adminsec_challenges`, `adminsec_devices`, `adminsec_lockouts`, `adminsec_known_ips`, `adminsec_log` (+ an index on `login_attempts(lower(login))`). Failed and successful logins keep going to the core `login_attempts` table | ✅ |
| Code | `internal/server/adminsec*.go`: `adminsec.go` (settings, switch, front gate, custom login URL, headers, menu, CLI), `adminsec_login.go` (login hooks, challenges, lockouts, devices), `adminsec_account.go` (Users › Security, profile block, Users column), `adminsec_admin.go` (admin screens), `adminsec_audit.go` (log, alerts, purge), `adminsec_password.go` (policy, strength), `adminsec_totp.go` (TOTP, sealing), `adminsec_qr.go` (QR encoder). Templates `web/admin/templates/pages/adminsecgo*.html` | ✅ |
| Menu | Top-level **AdminSecGo** (Dashboard, Settings, Lockouts, Activity Log) for `manage_options`, right after Users; **Users › Security** (or **Profile › Security**) for every user — only while active | ✅ |
| Daily job | `adminsecgo_daily` (scheduled on activation): purges log rows older than the retention, expired challenges and devices, lockouts older than 30 days | ✅ |

## 2. The two second factors (owner's rules)

| Rule | Implementation | |
|---|---|---|
| **MFA = e-mailed one-time code**, usable on its own | Users › Security › *E-mailed codes (MFA)* | ✅ |
| MFA needs working e-mail | `asMailReady()`: WPGO Mail SMTP active with a non-default mailer whose required field (SMTP host / API key / client ID …) is set and *Do Not Send* off, **or** the core `WPGO_SMTP_HOST`. Otherwise the card is greyed out with the reason and a link to WPGO Mail SMTP (or to the Plugins screen when it is not active); the server refuses the setup too | ✅ |
| Setup verifies the address | A 6-digit code is mailed to the address entered (default: the account e-mail); only after it is entered are codes switched on | ✅ |
| **2FA = authenticator app (TOTP)** | RFC 6238 / RFC 4226: HMAC-SHA1, 6 digits, 30-second steps, 160-bit random secret, ±1 step drift, **replay protection** (the last accepted step is stored; an equal or older step is refused, updated atomically) | ✅ |
| QR code generated server-side in pure Go | `adminsec_qr.go`: ISO 18004 byte mode, level M, versions 1–40, Reed–Solomon, all 8 masks with penalty scoring; rendered as inline SVG. Checked with the browser's `BarcodeDetector` | ✅ |
| Manual key | Shown in groups of four next to the QR code (`otpauth://totp/Issuer:login?secret=…&issuer=…`) | ✅ |
| 2FA requires MFA | *Set up the app* is disabled until e-mailed codes are on; the server refuses `totp_start` without MFA; MFA cannot be switched off while the app is on; a 2FA role requirement is only met with both | ✅ |
| Secrets at rest | TOTP secret AES-256-GCM sealed with a key derived from the server secret (`asg:v1:`); e-mailed codes and backup codes stored only as HMAC-SHA256 hashes | ✅ |

## 3. Login

| Feature | | |
|---|---|---|
| 2FA user: authenticator code first | *Verify it's you* step after the password, with **“Can't use your app? Email me a code”** (MFA fallback) and **“Use a backup code”** | ✅ |
| MFA-only user | The code is mailed right away: 6 digits, **10-minute expiry, single use**, **resend with a 60-second cooldown** (countdown on the button), at most 5 sends and **5 wrong attempts** per login, then the login starts over | ✅ |
| Backup codes | 10 codes (`xxxxx-xxxxx`, no look-alike characters), generated with the first factor, **shown once** (copy / print), hashed, each usable once (conditional update), regenerate with the current password; remaining count on the profile and the Security screen | ✅ |
| Remember this device | Checkbox on the challenge for N days (setting, default 30, 0 = off). Cookie `wpgo_adminsec_device` = row id + random token + HMAC signature bound to the user; the token is stored hashed with an expiry; one cookie can remember up to 5 accounts. Revocable: forget one or all devices (Security screen), an admin reset, or turning off MFA | ✅ |
| Challenge state | `adminsec_challenges` row (15 minutes), token in the form, hashed in the database; carries Remember Me and `redirect_to` | ✅ |
| Wrong codes count as failed logins | They feed the IP/username lockouts and the log | ✅ |
| Application Passwords | REST Basic authentication with an Application Password **skips the second factor** (like the Two Factor plugin); the account password never works on the API. Lockouts and the block list apply to the API (429 `too_many_retries`) | ✅ |
| XML-RPC | — WordPress Go has no XML-RPC endpoint | — |

## 4. Enforcement per role

| | | |
|---|---|---|
| Require MFA or 2FA per role | Settings › Two-Factor: per role *Not required / MFA / 2FA* (strictest of a user's roles wins). Can only be saved while e-mail can be delivered; while e-mail is broken requirements are not enforced (nobody could meet them) | ✅ |
| Grace period with a nag | Starts the first time a user is seen without the factor (user meta `adminsec_grace_start`, default 7 days); a warning notice on every admin screen with the days left | ✅ |
| After the grace period | Login succeeds but every admin screen redirects to Users › Security until the factor is set up (profile and heartbeat stay reachable); required factors cannot be removed | ✅ |
| Users list | **Two-Step** column: `2FA app + e-mail`, `MFA e-mail` or —, plus “2FA required” / “MFA required” when unmet | ✅ |
| Reset a user's factors | User-edit › *Reset two-step login*: removes factors, backup codes, remembered devices, pending challenges and restarts the grace period; logged | ✅ |
| Recovery from the command line | `wpgo adminsec reset-user <login>` | ✅ |

## 5. Custom login URL (WPS Hide Login)

| | | |
|---|---|---|
| Slug | Settings › Login: e.g. `my-login` → `https://site/my-login`. Validated: 3–64 of `a-z0-9-_`, not reserved (`wp-*`, `login`, `admin`, `feed`, …) and not an existing page slug | ✅ |
| Hidden URLs | For visitors who are not logged in, `/wp-login`, `/wp-login.php` and every `/wp-admin…` path (also `.php` spellings) answer the theme's **404 page**, or redirect to a configurable path/URL | ✅ |
| Still reachable | `/wp-admin/admin-ajax`, `/wp-admin/admin-post`, `/wp-admin/install`, admin assets, and `wp-login?action=postpass` (password-protected posts) | ✅ |
| How it is served | `adminsecFront` runs first in `Server.Handler()` — before Maintenance-Go and the page cache — and serves the slug as `/wp-login` internally, so the cache never stores it and maintenance mode never closes it | ✅ |
| Every core link uses it | `loginPath()` / template func `loginURL`: the login, lost-password, register and reset forms and their links, logout links (admin bar in wp-admin and on the front end, comment form, Login/out block, Maintenance-Go login form, Golementor login widget), `auth_redirect`, redirects after logout/reset/registration, the password-reset, new-user and privacy-confirmation e-mails, generic error links. A response wrapper also rewrites any `Location: /wp-login…` | ✅ |
| `authorize-application` | A visitor opening `/wp-admin/authorize-application` is sent to the new login with `redirect_to` | ✅ |
| Escape hatch | `WPGO_ADMINSEC_DISABLE_HIDE_LOGIN=1` in the environment brings `/wp-login` back immediately (the settings screen says so); `wpgo adminsec disable-hide-login` switches the setting off in the database | ✅ |
| Deactivation | Restores `/wp-login` at once | ✅ |

## 6. Login protection

| | | |
|---|---|---|
| Limit login attempts per IP | Default 5 failures in 15 minutes → 20-minute lockout | ✅ |
| Per username | Default 5 failures (counted against the account's login whether the username or the e-mail was typed) — stops attacks spread over many IPs | ✅ |
| Progressive lockouts | After N lockouts (default 4) within 24 hours the next one lasts the long duration (default 24 h) | ✅ |
| Replaces the core throttle | While active, the core's fixed 5-per-15-minutes IP throttle is replaced by these rules; while inactive it is unchanged | ✅ |
| IP allow list / block list | One IP or CIDR (IPv4/IPv6) per line, `#` comments. Allow list: never locked out. Block list: login, API, and optionally the whole site (403). The form refuses to block your own IP | ✅ |
| Lockouts screen | Active lockouts with release / release all, manual IP lockout, lockout history | ✅ |
| Generic error messages | “The username or password you entered is incorrect.” for unknown users and wrong passwords (default on) | ✅ |
| User enumeration | `/?author=N` → 404, REST `wp/v2/users` → 401 and an empty users sitemap for visitors. **The core does this by default** (and the login form gives one message for an unknown account and a wrong password); `WPGO_EXPOSE_USERS=1` opts out. While AdminSecGo is active its "block enumeration" and "generic errors" settings decide instead, so an administrator can relax them on purpose | ✅ |
| CAPTCHA on the login | — not included (Gontact Form has reCAPTCHA/Turnstile for forms; lockouts cover brute force) | — |

## 7. Password policy

| | | |
|---|---|---|
| Minimum length and strength per role | Defaults: administrator 12 / Strong, editor and author 10 / Medium, contributor 8 / Weak, subscriber 8 / any. A zxcvbn-like 0–4 score: character-pool entropy, cut for repeats, sequences, keyboard walks (`qwerty`, `asdf`, `7890`), a common password with a few characters around it, and the user's own name | ✅ |
| Common passwords | The 10,000 most common passwords (the list WordPress' bundled `zxcvbn.min.js` carries, de-obfuscated) in `adminsec_passwords.txt`, embedded | ✅ |
| Where it applies | Profile and user-edit, Add User, password reset, REST `wp/v2/users` create/update | ✅ |
| Force password change | Admin action on user-edit (*Require a password change at next login*), and automatically at login when the current password breaks the policy (setting). The user lands on the profile with a notice; every other admin screen redirects there until a compliant password is saved | ✅ |
| Password expiry | — not included (current NIST guidance advises against periodic changes) | — |

## 8. Sessions

| | | |
|---|---|---|
| Active sessions on the profile | Device (browser on OS), IP, last activity, start; *This session* marked; *Log out* per session | ✅ |
| Log out other sessions | The core *Log Out Everywhere Else* plus per-session buttons; administrators get *Log out all sessions* on user-edit | ✅ |
| Idle timeout | Settings › Sessions (minutes, 0 = off): `auth.Manager.Idle` ends a session unused for longer | ✅ |

## 9. Hardening

| | | |
|---|---|---|
| Security headers | X-Frame-Options (SAMEORIGIN/DENY), Referrer-Policy, Permissions-Policy, X-Content-Type-Options, HSTS (max-age, includeSubDomains, preload; HTTPS requests only). Set by a response wrapper, so cached pages get them too. **The core Content-Security-Policy (and its per-response allow-list) is left untouched** | ✅ |
| Disable the file editors | Theme File Editor and Plugin File Editor: menu entries removed, screens 403, `edit-theme-plugin-file` ajax refused (default on) | ✅ |
| Hide the version | No generator meta tag, feeds without the version, front-end `?ver=0.1.0` replaced by a site-specific hash (default on) | ✅ |
| REST only for logged-in users | Opt-in; visitors keep oEmbed and the contact-form routes plus configurable prefixes | ✅ |
| Alerts | E-mail to the admin (or a configured address) on an administrator's login from a new IP (not the very first login) and on every lockout | ✅ |
| Firewall, malware scan, file-change detection | — out of scope by design | — |

## 10. Activity log

| | | |
|---|---|---|
| Events | Logins (with the method: password, + app, + e-mailed code, + backup code, + remembered device), failed logins, lockouts and releases, blocked IPs, logouts, factor changes (MFA/2FA on/off, backup codes generated/used, devices remembered/forgotten, admin resets), sessions logged out, password changes and forced changes, role changes (user-edit, Users bulk, REST), users created/deleted, plugin activation/deactivation (including AdminSecGo itself), theme switches, Settings screens saved, AdminSecGo settings saved | ✅ |
| Screen | Filters: event, severity, user, IP, date range; 50 per page; user agent per row | ✅ |
| CSV export | The filtered rows, formula-safe cells | ✅ |
| Retention | Days (default 90, 0 = forever), purged by the daily job; option to skip logging failed logins on busy sites | ✅ |

## 11. Dashboard

Security score (weighted checklist, 0–100 %) with a *fix* link per item: e-mail delivery, your
own 2FA, 2FA required for administrators, every administrator enrolled, lockouts on, custom
login URL, generic errors, enumeration blocked, no `admin` username, administrator password
policy, common passwords refused, file editors disabled, security headers, HTTPS site address,
version hidden, idle timeout, alerts. Plus 2FA/MFA adoption, failed logins in 24 hours, active
lockouts, the login URL and recent activity.

## 12. Command line

```
wpgo adminsec disable-hide-login   # custom login URL off (restart a running server, or use the env var)
wpgo adminsec reset-user <login>   # remove a user's second factors and remembered devices
wpgo adminsec unlock-all           # release every lockout and forget failed attempts
wpgo adminsec deactivate           # switch the plugin off
```

## 13. Tests

`internal/server/adminsec_test.go` (httptest + PostgreSQL): RFC 6238 vectors, drift and replay;
QR structure and capacities; password strength; plugin off = unchanged behaviour (menus, headers,
detailed errors, core throttle, stored factors ignored) and deactivation; MFA disabled without
mail, MFA setup and login with the e-mailed code read from the queued `wp_mail` job, cooldown,
single use, attempt limit, backup codes; 2FA requires MFA, TOTP login with drift and replay,
e-mail fallback, remembered device, forget devices; role enforcement with grace period, gate and
reset; Application Passwords bypass 2FA on REST and lockouts apply to the API; custom login URL
(404s, open paths, every link, reset e-mail, logout, redirect mode, escape hatch, CLI); IP,
username, progressive lockouts, allow and block lists, alert e-mail; enumeration and REST
restriction; sessions list, revoke, log out others, idle timeout, new-IP alert; password policy
and forced change; activity log entries, filters, CSV and retention; all screens render.
