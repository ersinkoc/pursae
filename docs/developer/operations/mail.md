---
title: Mail
description: How Pursae sends e-mail, SMTP settings, and the .eml drop folder for development.
weight: 60
section: operations
---

Pursae sends the same e-mails WordPress does: new-user notifications, password resets,
comment moderation and "new comment" notices, the admin e-mail change confirmation, personal
data export links. Messages are **queued** in the jobs table and delivered by the scheduler, so a
slow SMTP server never delays a request ([cron](cron.md)).

## SMTP

```bash
WPGO_SMTP_HOST=smtp.example.com
WPGO_SMTP_PORT=587            # 465 = implicit TLS; other ports use STARTTLS when offered
WPGO_SMTP_USER=postmaster@example.com
WPGO_SMTP_PASS=…
WPGO_MAIL_FROM='Example Blog <no-reply@example.com>'
```

With Docker, `docker/compose.yaml` passes these variables through to the server. For `make prod-up`
put them in `docker/.env` (copied from `docker/.env.example`) and run `make prod-up` again to apply
a change.

Without `WPGO_MAIL_FROM` the sender is `<Site title> <wordpress@host>`, the host taken from the
site URL — set a real address so receiving servers accept the mail (and set up SPF/DKIM for it).

Delivery failures are retried with back-off (1, 4, 9, 16 minutes); after five attempts the job is
marked failed and shows up in Tools › Site Health.

## Development: .eml files

Without `WPGO_SMTP_HOST` every message is written as an `.eml` file to `WPGO_MAIL_DIR` (default:
`mail/` next to the uploads directory, `/data/mail` in Docker, kept in the `mail` volume). Open the file in a mail client or
read it as text to see the password reset link.

```bash
docker compose -f docker/compose.yaml exec wpgo ls /data/mail
```

## From the admin

Settings › General holds the administration e-mail address; Settings › Discussion decides which
comment events send a notification. Pursae never contacts Gravatar or any other third
party on its own.
