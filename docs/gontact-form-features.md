# Gontact Form — Contact Form 7 feature inventory

Gontact Form is WordPress Go's native (compiled-in) clone of **Contact Form 7** (studied at CF7 6.2.3,
`rocklobster-in/contact-form-7`, plus contactform7.com docs). Like Classic Editor, WPGo Cache and Golementor it is
listed under Plugins, **off on a fresh install**, and while off it adds nothing: no menu, no screens (404/403), no
REST namespace, no shortcode, no block, no front-end assets.

Legend: ✅ implemented · ◐ implemented differently / partly · ✗ left out (with the reason).

## 1. Data model

| CF7 | Gontact Form |
|---|---|
| Custom post type `wpcf7_contact_form` | ✅ same post type (non-public `content_types` row created on activation) |
| Post meta `_form`, `_mail`, `_mail_2`, `_messages`, `_additional_settings`, `_locale`, `_hash` | ✅ same keys in the post's `meta` jsonb; `post_content` keeps the form template too |
| Default form, Mail, Mail (2), messages templates (`WPCF7_ContactFormTemplate`) | ✅ identical defaults |
| "Contact form 1" created on first activation | ✅ |
| Flamingo storage of submissions | ✗ Flamingo is a separate plugin; not part of CF7 |
| Capabilities `wpcf7_edit_contact_form(s)`, `wpcf7_read_contact_forms`, `wpcf7_delete_contact_form`, `wpcf7_manage_integration`, `wpcf7_submit` | ◐ mapped like CF7's `map_meta_cap` defaults: edit/delete → `publish_pages`, read → `edit_posts`, integration → `manage_options`, submit → `read` |

## 2. Admin screens (menu **Contact**, `dashicons-email`, after Comments)

| CF7 | Gontact Form |
|---|---|
| Contact › Contact Forms (`admin.php?page=wpcf7`): list table with Title, Shortcode, Author, Date; search; bulk Delete; row actions Edit / Duplicate; pagination | ✅ (`/wp-admin/admin?page=wpcf7`) |
| Contact › Add Contact Form (`page=wpcf7-new`) | ✅ prefilled with the default templates |
| Edit screen (`page=wpcf7&post=ID&action=edit`): title, shortcode box, Status box (Duplicate, Delete, Save), "Do you need help?" box | ✅ |
| Editor tabs **Form**, **Mail**, **Messages**, **Additional Settings** | ✅ same panels, field names (`wpcf7-form`, `wpcf7-mail[…]`, `wpcf7-mail-2[…]`, `wpcf7-messages[…]`, `wpcf7-additional-settings`) |
| Form tab tag generator buttons: text, email, URL, tel, number, date, text area, drop-down menu, checkboxes, radio buttons, acceptance, quiz, file, submit | ✅ dialog with Field type (required), Name, Default value / placeholder, options, id/class, "Insert Tag" |
| Mail tab: To, From, Subject, Additional headers, Message body, Exclude lines with blank mail-tags, Use HTML content type, File attachments; list of available mail-tags | ✅ (the mail-tag list refreshes on save, not live while typing) |
| Mail (2) with "Use Mail (2)" switch | ✅ |
| Messages tab: every message with its description | ✅ all messages of the active modules |
| Additional Settings | ✅ see §6 |
| Configuration validator (warnings on the edit screen) | ◐ the common checks: empty/invalid To, From not an e-mail, unknown mail-tags in the mail templates, file attachment tag missing, invalid additional headers |
| Help tabs (screen-meta) | ✗ the general WordPress Go help panel is shown instead |
| Contact › Integration (`page=wpcf7-integration`) | ✅ see §8 |
| Welcome panel / dashboard news | ✗ promotional |
| Legacy "Contact Form 7" admin REST (`/contact-forms` CRUD) | ✅ GET/POST `/contact-forms`, GET/POST(PUT)/DELETE `/contact-forms/{id}` for users with the caps |

## 3. Embedding

| CF7 | Gontact Form |
|---|---|
| `[contact-form-7 id="<hash or id>" title="…"]` and `[contact-form …]` | ✅ through the core shortcode parser (`wp.DoShortcode`) in `theContent` |
| Shortcode attributes `html_id`, `html_name`, `html_title`, `html_class`, `output="raw_form"` | ✅ |
| 7-char hash ids (CF7 5.8+), legacy numeric ids, lookup by title | ✅ |
| Block `contact-form-7/contact-form-selector` | ✅ registered in Gotenberg while on (select a form); saved markup is CF7's: a `wp-block-contact-form-7-contact-form-selector` div holding the shortcode |
| Widgets / text widget shortcode | ◐ wherever WordPress Go renders `the_content` |

## 4. Form-tags

Syntax `[type* name option:value "value" 'value']`, escaped `[[tag]]`, enclosing `[acceptance x]…[/acceptance]`,
pipes `"Label|value"`, `default:get` / `default:post_meta` / `default:user_*` / `default:1_2`. Unknown tags are left as is.

| Tag | Options | Status |
|---|---|---|
| `text`, `email`, `url`, `tel` (+`*`) | `size:`/`maxlength:`/`minlength:` (`40/400`), `placeholder`/`watermark`, `default:`, `id:`, `class:`, `autocomplete:`, `readonly`, `tabindex:`, `list:` | ✅ |
| `textarea` | `cols/rows` (`40x10`), `maxlength` (2000), `minlength`, placeholder, default, enclosed content as default | ✅ |
| `number`, `range` | `min:`, `max:`, `step:`, placeholder | ✅ |
| `date` | `min:`, `max:` (YYYY-MM-DD or `today`, `today+7days`…), `step:` | ✅ |
| `select` | `multiple`, `include_blank`, `first_as_label`, `default:N`, `data:` | ◐ `data:` lists (countries etc.) not shipped |
| `checkbox`, `radio` | `label_first`, `use_label_element`, `exclusive`, `free_text`, `default:` | ✅ |
| `acceptance` | `optional`, `invert`, enclosed condition text | ✅ |
| `quiz` | `"Question|answer"` pairs, random question, hashed answer | ✅ |
| `file` | `limit:` (1mb default, `kb`/`mb`), `filetypes:` (`jpg|pdf|…` or `image/*`) | ✅ |
| `submit` | value, id, class | ✅ (`has-spinner`) |
| `hidden` | default | ✅ |
| `count` (character counter) | `down`, `id` of the target | ✅ |
| `response` (custom response output position) | | ✅ |
| `captchac` / `captchar` (Really Simple CAPTCHA) | | ✗ requires a separate plugin |
| `stripe` | | ✗ payments are out of scope |
| `reflection`, `listo` data lists | | ✗ rarely used, would need data files |

Front-end markup matches CF7 exactly: `div.wpcf7.no-js#wpcf7-f{id}-p{post}-o{n}` with `data-wpcf7-id`, the
`screen-reader-response` block, `form.wpcf7-form.init` with `data-status`, the hidden `fieldset.hidden-fields-container`
(`_wpcf7`, `_wpcf7_version`, `_wpcf7_locale`, `_wpcf7_unit_tag`, `_wpcf7_container_post`, `_wpcf7_posted_data_hash`,
`_wpnonce` for logged-in users), `span.wpcf7-form-control-wrap[data-name]`, `wpcf7-form-control wpcf7-text
wpcf7-validates-as-required …` classes, `wpcf7-not-valid-tip`, `wpcf7-response-output`, `wpcf7-list-item(-label)`,
`wpcf7-spinner`. Forms are wrapped with `wpcf7_autop` (paragraphs/line breaks) like CF7 (`WPCF7_AUTOP`).

## 5. Submission, validation and response

| CF7 | Gontact Form |
|---|---|
| REST `POST /wp-json/contact-form-7/v1/contact-forms/{id}/feedback` (multipart only, 415 otherwise; 404 `wpcf7_not_found`; 400 `wpcf7_unit_tag_not_found`) | ✅ |
| Response `{contact_form_id, status, message, invalid_fields:[{field,message,idref,error_id}], posted_data_hash, into, demo_mode?}` | ✅ |
| Statuses `validation_failed`, `acceptance_missing`, `spam`, `aborted`, `mail_sent`, `mail_failed` | ✅ |
| `GET …/feedback/schema` (SWV rules), `GET …/refill` | ✅ schema with `required`, `email`, `url`, `tel`, `number`, `date`, `minlength`/`maxlength`, `file`, `minnumber`/`maxnumber`, `mindate`/`maxdate`, `enum`; refill returns quiz refresh |
| Server-side validation and messages (`invalid_required`, `invalid_too_long/short`, `invalid_email/url/tel/number/date`, `number_too_small/large`, `date_too_early/late`, `quiz_answer_not_correct`, `upload_*`, `accept_terms`) | ✅ |
| Non-JS fallback (plain POST back to the page, `#unit-tag` action) with "hangover" values and inline errors | ✅ |
| `posted_data_hash` | ✅ |
| Mail-tags `[field]`, `[_raw_field]`, `[_format_field "Y-m-d"]` (date), pipes, special tags `_remote_ip`, `_user_agent`, `_url`, `_date`, `_time`, `_invalid_fields`, `_contact_form_title`, `_serial_number`, `_post_id/_name/_title/_url/_author/_author_email`, `_site_title/_description/_url/_domain/_admin_email`, `_user_login/_email/_url/_first_name/_last_name/_nickname/_display_name` | ✅ (`_serial_number` = submission count kept in meta) |
| Mail sent through `wp_mail()` | ✅ the core mailer queue (`sendMail`), From/Reply-To/Cc/Bcc from the template |
| Exclude blank lines, HTML mail (body wrapped in CF7's HTML template) | ✅ |
| File attachments (`[your-file]` in the attachments field, plus `uploads/…` paths) | ✅ attached to the queued mail; temporary files removed afterwards |
| Upload dir `wpcf7_uploads` with deny-all, random sub-dirs, file name sanitizing | ✅ under `<data>/gontact_uploads` (never web-served) |
| Hooks `wpcf7_before_send_mail`, `wpcf7_spam`, `wpcf7_validate_*`, `wpcf7_mail_sent`… | ◐ the Wasm plugin filters `wpcf7_spam` and `wpcf7_mail_sent` actions are fired through the core plugin registry |

## 6. Additional settings

| Setting | Status |
|---|---|
| `demo_mode: on` (no mail, `demo` class, `demo_mode: true`) | ✅ |
| `skip_mail: on` | ✅ |
| `subscribers_only: true` | ✅ form replaced with the notice; REST answers `error` for visitors; spam checks skipped for members |
| `acceptance_as_validation: on` | ✅ |
| `do_not_store: true` | ✅ accepted (nothing is stored anyway) |
| `flamingo_*`, `akismet: …`, `on_sent_ok`/`on_submit` (removed in CF7 5.0) | ✗ |

## 7. JavaScript (own file, same behaviour and DOM events)

`wpcf7` global (`api.root`, `api.namespace`, `cached`), `no-js` → `js`, AJAX submit with `FormData`, status classes
(`init`, `submitting`, `invalid`, `unaccepted`, `spam`, `aborted`, `sent`, `failed`, `resetting`, `custom-*`),
`data-status`, events `wpcf7beforesubmit`, `wpcf7submit`, `wpcf7invalid`, `wpcf7unaccepted`, `wpcf7spam`,
`wpcf7mailsent`, `wpcf7mailfailed`, `wpcf7aborted`, `wpcf7statuschanged`, `wpcf7reset` (all bubbling, CF7's `detail`
shape), inline tips + screen-reader list, reset after `mail_sent`, acceptance disabling the submit button, exclusive
checkboxes, free text, URL auto-prefix, character counters, quiz refresh, spinner, `inert-on-*`, live validation on
change via the schema. ✅

## 8. Spam protection & integrations

| CF7 | Gontact Form |
|---|---|
| Short User-Agent → spam; nonce check for logged-in users | ✅ |
| Disallowed comment keys (Settings › Discussion `disallowed_keys`) | ✅ |
| Quiz | ✅ |
| reCAPTCHA v3 (Integration: site key / secret, score threshold 0.5, `wpcf7_recaptcha_response`) | ✅ keys on Integration, server verification against Google |
| Cloudflare Turnstile (site key / secret, `_wpcf7_turnstile_response`) | ✅ |
| Akismet (`akismet:author` …) | ✗ no Akismet in WordPress Go |
| Constant Contact, Brevo (Sendinblue), Stripe | ✗ third-party marketing/payment accounts, not relevant to a contact form clone here |
| Really Simple CAPTCHA | ✗ separate plugin |

## 9. Not ported (summary)

Flamingo, Akismet, Constant Contact, Brevo, Stripe, Really Simple CAPTCHA, `data:` option lists, reflection/listo,
translations (UI is English), CF7's welcome panel and promotional notices, multisite bits.

## 10. Implementation map

| Piece | Where |
|---|---|
| On/off gate, activation (post type row, secret, "Contact form 1"), form model, Contact menu | `internal/server/gontact_plugin.go` |
| Form-tag scanner and options | `internal/server/gontact_formtags.go` |
| Shortcode, `form_html()`, tag handlers, asset tags | `internal/server/gontact_render.go` |
| Submission, validation, spam, uploads, REST (feedback, schema, refill, CRUD), non-JS fallback | `internal/server/gontact_submit.go` |
| Mail-tags and mail composition (queued through the core `wp_mail` job) | `internal/server/gontact_mail.go` |
| Admin screens, config validator, Integration, block assets | `internal/server/gontact_admin.go`, `web/admin/templates/pages/gontact-*.html` |
| Front-end and admin CSS/JS, Gotenberg block | `web/admin/assets/gontact/` (served at `/wp-content/gontact-form/` only while on) |
| Tests | `internal/server/gontact_test.go` |

Core changes (small hooks): `internal/mail` `Message` gained `From`, `Cc`, `Bcc` and `Attachments` (multipart/mixed);
the REST cookie check also reads `_wpnonce` from multipart bodies (WordPress reads `$_REQUEST`); the site handler lets
a non-JS form POST render the page; plus one-line registrations in `plugins.go`, `admin.go`, `wpgocache.go`
(`pluginAdmin`), `rest.go` (namespace hidden while off), `site_view.go`, `server.go` and `editor_block.go`.
Options: `native_gontact_form`, `gontact_form_installed`, `gontact_form_secret`, `gontact_recaptcha_sitekey/secret`,
`gontact_turnstile_sitekey/secret`.
