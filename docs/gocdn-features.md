# GoCDN: feature inventory

GoCDN is a native (compiled-in) WordPress Go plugin for object storage, CDN delivery and on-the-fly
images. It covers the ground of **WP Offload Media** (Delicious Brains: offload to S3-compatible storage,
rewrite URLs, remove local files, bulk tools, `AS3CF_SETTINGS`), an **image CDN** (imgix / Cloudflare
Images style resize, crop and WebP with signed URLs) and **CDN Enabler** (KeyCDN: rewrite theme, plugin
and core asset URLs to a pull CDN), with purging of **WPGo Cache** and **Cloudflare**. Like every
native plugin it is **off on a fresh install** (`native_gocdn`), and while it is off nothing of it runs:
its screens and routes answer 404, uploads are served from disk and no response is rewritten.

Legend: **✓** implemented · **≈** implemented differently (reason given) · **✗** left out (reason given).

## 1. Storage connection (GoCDN › Storage)

| Feature | | Notes |
|---|---|---|
| Providers: Amazon S3, Cloudflare R2, DigitalOcean Spaces, Backblaze B2, MinIO / any S3 API | ✓ | One S3 client (`internal/gocdn`), provider presets fill the endpoint, region and public URL. |
| Endpoint, region, bucket, access key, secret key | ✓ | Empty endpoint = the provider's for the region. R2 uses region `auto`. |
| Path-style / virtual-host-style addressing | ✓ | Toggle; MinIO and R2 default to path-style in the presets' help. |
| Key prefix ("Path in bucket") | ✓ | Default `wp-content/uploads/`, keys are `<prefix><year>/<month>/<file>`. |
| Object ACL `public-read` | ✓ | Toggle; off for buckets with ACLs disabled (the test explains AccessDenied). |
| Delivery domain (custom domain / CDN in front of the bucket) | ✓ | Mapped to the bucket root. Without it the provider's public bucket URL is used (R2 needs one). |
| Secret stored safely | ✓ | AES-GCM with a key derived from the server secret (`gocdnSeal`/`gocdnReveal`, the WPGO Mail SMTP approach); never printed into a form: the field shows "•••• saved — type to replace" with a Clear box. Same for the Cloudflare token. |
| Configuration from the environment | ✓ | `WPGO_CDN_SETTINGS` = JSON with the option's keys (`provider`, `endpoint`, `bucket`, `access-key-id`, `secret-access-key`, …, the names of `AS3CF_SETTINGS` where it has them). Keys set there override the database, are shown locked, and are never written to the database. |
| Test connection | ✓ | "Save and Test Connection" / Test: PUT a probe object, HEAD it (size check), GET it through the public delivery URL (warning only), DELETE it, ListObjectsV2. Each step is reported; S3 error codes get advice (SignatureDoesNotMatch → wrong secret, NoSuchBucket, AccessDenied/ACL, region mismatch, clock skew, unreachable endpoint). |
| SigV4 signer | ✓ | Own implementation (no AWS SDK), header signing and presigned URLs, checked against AWS's published S3 test vectors (GET object, PUT object, GET lifecycle, ListObjects, presigned GET). |

## 2. Media offload (GoCDN › Media Offload)

| Feature | | Notes |
|---|---|---|
| Copy new uploads and all generated sizes to the bucket | ✓ | Hooked after the attachment is inserted (async-upload, REST `wp/v2/media`, sideloads: WXR import, demo content, remote "copy" import) and after the image editor saves. Also the original of a `-scaled` image and the editor's backup file. `Cache-Control: public, max-age=31536000, immutable`, content type by extension. |
| Per-file offload state | ✓ | Table `gocdn_items` (migration `0021_gocdn.sql`): media id, uploads path, provider, region, bucket, key, size, MIME, `local_removed`. Rows go with the attachment (`ON DELETE CASCADE`). |
| Remove files from the server | ✓ | After every file of an attachment is in the bucket. |
| `/wp-content/uploads/…` for removed files | ✓ | Answered before the page cache: 302 to the delivery URL, a presigned URL (1 h, private buckets) or the file proxied through the site (with the uploads sandbox CSP). Files still on disk are served as before. |
| Server needs a removed file (image editor) | ✓ | `gocdnEnsureLocal` downloads it back before the editor's preview/save; the edited files are offloaded (and removed) again. |
| Delete attachment deletes remote objects | ✓ | Through `deleteAttachmentFiles`, used by every delete path. The URLs are sent to Cloudflare when automatic purge is on. |
| Bulk "Offload existing media" | ✓ | Background goroutine, one attachment at a time, progress in the option `gocdn_job` after each one; the screen polls `/wp-admin/gocdn?page=offload&action=progress` (JSON). Stop button. |
| Resumable | ✓ | A job interrupted by a restart is shown as interrupted; starting it again with "Resume" continues after the last attachment done. |
| "Download back to the server" / "Remove from the bucket" | ✓ | Same job runner. Remove downloads removed files first and refuses to delete the only copy. |
| Bucket changed later | ≈ | Items keep the bucket they were written to and are still served/deleted there; they are not moved. |
| Deactivating the plugin | ≈ | URLs go back to `/wp-content/uploads`; files removed from the server are then missing, so run "Download back to the server" before deactivating (as with WP Offload Media). |

## 3. URL rewriting

| Feature | | Notes |
|---|---|---|
| Front end, feeds, sitemaps, REST API | ✓ | A gin middleware buffers HTML/XML/JSON responses and rewrites upload URLs (relative, or absolute on the site's own host) in `src`, `srcset`, `href`, CSS `url()` and JSON alike; another site's `/wp-content/uploads` URLs are untouched. Only offloaded files are rewritten (one batched lookup per page). |
| Custom domain | ✓ | The delivery domain. |
| Not in wp-admin, the login screen, or the REST API's `context=edit` | ✓ | So the editors keep and save local URLs. |
| Page cache | ✓ | The cache stores the rewritten page; every settings change, finished bulk job and import purges it. |
| CSP | ✓ | The delivery and asset CDN origins are added to the page's Content-Security-Policy when not covered by `https:`. |
| Reverse rewriting of content saved with CDN URLs | ✗ | Not needed while the editors get local URLs; content pasted with CDN URLs stays as typed. |

## 4. Remote storage browser (Media › Remote Storage)

| Feature | | Notes |
|---|---|---|
| Folder navigation (prefix + delimiter), breadcrumbs | ✓ | |
| Pagination | ✓ | 50 per page, ListObjectsV2 continuation tokens ("Next page", "First page"). |
| Thumbnails | ✓ | Image objects (≤ 20 MB) are fetched and shrunk to 160 px by an admin-only endpoint, so private buckets work too. |
| Import: Copy | ✓ | Downloaded like an upload (sizes generated; offloaded again under the library's own key when copying is on). |
| Import: Link (remote-only attachment) | ✓ | Registered with the key's path (`<key minus prefix>`, or `gocdn-remote/<key>`), width/height read from the first 512 KB, no local file, no sub-sizes; served through the uploads fallback. Only allowed upload types; duplicates and paths with a local file are skipped. |
| Use in the media picker | ≈ | Imported objects are ordinary attachments, so the media modal lists and inserts them; there is no separate "remote" tab inside the modal itself. |

## 5. Image service (GoCDN › Image Service)

| Feature | | Notes |
|---|---|---|
| Endpoint | ✓ | `/cdn-img/<path in uploads>?w=&h=&fit=&q=&fm=&s=`, and the same parameters on `/wp-content/uploads/<path>`. Aliases `width`, `height`, `quality`, `format`; fits `inside` (default, `contain`, `scale-down`), `cover` (`crop`), `fill`. Never upscales. |
| Output formats | ≈ | JPEG, PNG, GIF and **WebP** (libwebp transpiled to pure Go by `github.com/gen2brain/webp`, no cgo; a system libwebp is used instead when installed). **AVIF output is not implemented**: no pure-Go encoder of reasonable speed; `fm=avif` and AVIF-only browsers fall back to WebP or the source format. Animated GIFs are flattened to their first frame. |
| Format negotiation | ✓ | `fm=auto` sends WebP when the `Accept` header lists `image/webp` (q > 0), with `Vary: Accept`. |
| Quality | ✓ | Default quality setting, `q=` per request. |
| Signed URLs | ✓ | HMAC-SHA256 of the path and the canonical parameters with a random site key (stored sealed; "Make a new signing key" rotates it); parameter order and aliases don't matter. Unsigned / tampered requests get 403 when signing is on. |
| Abuse limits | ✓ | Max output width/height, max source megapixels (checked with `DecodeConfig` before decoding), max source 64 MB, one transformation per CPU at a time, path traversal refused. |
| Cache headers | ✓ | `Cache-Control: public, max-age=31536000, immutable`, strong `ETag`, `304` on `If-None-Match`. |
| Variant cache | ✓ | On disk (`<data>/gocdn-cache/`), optionally in the bucket (`gocdn-cache/…`, shared by several servers). Keyed by path, source version, parameters, quality and output format. "Empty the processed image cache". |
| Source offloaded / removed locally | ✓ | Read from the bucket. |
| Content images through the service | ✓ | Option: every upload image on the front end (src/srcset) becomes a signed `/cdn-img/` URL at a set quality, so browsers get WebP. |
| URL builder | ✓ | On the screen, for templates and testing; `gocdnImageURL` for code. |

## 6. Assets CDN (GoCDN › Assets CDN)

| Feature | | Notes |
|---|---|---|
| CDN base URL | ✓ | |
| Included folders | ✓ | Default `/wp-content/themes/`, `/wp-includes/`, `/wp-content/plugins/`, `/wp-admin/assets/`; other `/wp-admin/` paths and uploads are refused. |
| Included extensions, exclusions | ✓ | Comma list; substrings one per line. |
| Front-end HTML only, never wp-admin or JSON | ✓ | Relative and own-host absolute URLs; query strings (`?ver=`) kept. CDN origin added to `script-src`, `style-src`, `font-src`, `img-src`, `media-src`, `connect-src`. |

## 7. Purge (GoCDN › Purge)

| Feature | | Notes |
|---|---|---|
| WPGo Cache purge on settings changes, finished jobs, imports | ✓ | `purgeAll`. |
| Purge button | ✓ | WPGo Cache, Cloudflare (everything), processed image cache. |
| Cloudflare | ✓ | Zone ID + API token (Zone › Cache Purge; sealed). `purge_cache` with `purge_everything` or `files` (30 per request). Optional automatic purge with GoCDN's automatic purges and for deleted offloaded files. API errors are shown. |
| Other CDNs' purge APIs (CloudFront invalidations, Bunny, KeyCDN) | ✗ | Not implemented; immutable URLs make purges rarely necessary. |

## 8. Not done / limits

- AVIF encoding (see 5). Multipart uploads: files are sent in one PUT (uploads are capped at 64 MB).
- Server-side encryption headers, storage classes, object versioning (`object-versioning` of WP Offload
  Media adds a time stamp to keys), and copying between buckets on a bucket change.
- Customizer site-icon cropping and Golementor's server-side reads of uploads read the local file; with
  "Remove files from the server" they fail for removed files (the image editor is covered).
- Signed delivery of the *rewritten* URLs (presigned URLs in page HTML) is not offered: pages are cached;
  private buckets should use "presigned redirect" or "proxy" delivery for removed files.
