# The plugin ABI (language-independent)

A Pursae plugin is a WASI *reactor* module that speaks JSON over guest memory. Any language that
compiles to `wasm32-wasi` and can export functions can implement it; the Go SDK
(`wpgo/sdk/go/wpgo`) is the reference. Smaller runtimes (TinyGo, Rust, AssemblyScript) produce
packages a fraction of the size of a standard Go build, but only the standard Go toolchain is
tested by the project today.

## ABI (version 1)

The plugin is a WASI preview1 **reactor** module. In Go, build it with
`GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`. The default build mode
produces a command module (`_start`). Its runtime exits as soon as `main`
returns, so the host could not call its exports afterwards. `Load` rejects
command modules and tells the author to use `c-shared`.

Data crosses the boundary as UTF-8 JSON in guest linear memory. A *packed*
value is a `u64` equal to `ptr<<32 | len`.

Guest exports:

| Export | Signature | Purpose |
|--------|-----------|---------|
| `_initialize` | `() -> ()` | language runtime init (Go: runs package `init`s) |
| `wpgo_init` | `() -> ()` | called once after `_initialize`; registers hooks via `add_hook` |
| `wpgo_alloc` | `(size i32) -> i32` | allocate a buffer that the host writes into |
| `wpgo_free` | `(ptr, size i32)` | release a buffer returned by `wpgo_handle` |
| `wpgo_handle` | `(ptr, len i32) -> i64` | run a hook callback; returns packed response |

The host sends `wpgo_handle` this request:
`{"plugin":"slug","callback":"…","hook":"the_content","type":"filter","args":[…],"value":…}`.
The guest replies with `{"value":…}` or `{"error":"…"}`. The guest takes
ownership of the request buffer. `type` is `filter`, `action`, `shortcode`
(`hook` is the tag, `args` are `[attrs, content]`, the value must be a JSON
string) or `rest` (`hook` is the route path, `args` is `[request]`, the value is
`{"status":200,"body":…,"headers":{…}}`; see `RestRequest` / `RestResponse` in
`host.go`).

Host imports, module `wpgo`: every import has the signature
`(ptr, len i32) -> i64`. Each one takes a JSON request and returns a packed
pointer to a JSON envelope. The host allocates the envelope with `wpgo_alloc`,
and the guest then owns it. The envelope is either
`{"ok":true,"result":…}` or `{"ok":false,"code":"permission_denied|invalid_request|host_error","error":"…"}`.
The import returns `0` if no response could be delivered.

| Import | Request | Result | Permission |
|--------|---------|--------|------------|
| `add_hook` | `{"type":"filter\|action","hook":"…","priority":10,"callback":"…"}` | – | hook must be in manifest + its permission |
| `get_option` | `{"name":"…"}` | `{"found":bool,"value":…}` | `options:read` |
| `update_option` | `{"name":"…","value":…}` | – | `options:write` |
| `log` | `{"level":"debug\|info\|warning\|error","message":"…"}` | – | none |
| `http_request` | `{"method":"POST","url":"https://…","headers":{…},"body":"…"}` | same as `http_get` | `http:outbound` |
| `send_mail` | `{"to":["a@b.c"],"subject":"…","body":"…","html":"…","reply_to":"…"}` | `{"queued":true}` | `mail:send` |
| `http_get` | `{"url":"https://…"}` | `{"status":200,"header":{…},"body":"…"}` | `http:outbound` |
| `storage_get` | `{"key":"…"}` | `{"found":bool,"value":"…"}` | `storage` |
| `storage_set` | `{"key":"…","value":"…"}` | – | `storage` |
| `add_admin_notice` | `{"type":"info\|success\|warning\|error","message":"…"}` | – | `admin:notices` |
| `register_shortcode` | `{"tag":"…","callback":"…"}` | – | `content:filter` + tag in manifest `shortcodes` |
| `register_rest_route` | `{"namespace":"<slug>/v1","route":"/items/(?P<id>\\d+)","methods":["GET"],"callback":"…"}` | – | `rest:routes`; namespace is `<slug>` or `<slug>/vN` |
| `get_post` | `{"id":7}` | `{"found":bool,"post":{…}}` | `posts:read` |
| `query_posts` | `{"type","status","search","author","term","orderby","order","per_page"(≤100),"page"}` | `{"posts":[…],"total":n,"pages":n}` | `posts:read` |
| `insert_post` | `{"title","content","excerpt","status","type","slug","meta"}` | the post | `posts:write` |
| `update_post` | `{"id":7, …fields}` | the post | `posts:write` |
| `get_current_user` | `{}` | `{"found":bool,"user":{"id","login","display_name","roles","caps"}}` | `users:read` |
| `schedule_event` | `{"hook":"cron_x","interval":3600,"at":0,"args":{…}}` | – | `cron` + hook in manifest; `interval` 0 = one-off at `at` (unix) |
| `unschedule_event` | `{"hook":"cron_x","args":{…}?}` (args: only runs with exactly these args) | – | `cron` |
| `register_settings_page` | `{"page_title","menu_title","description","fields":[…]}` | `{"option_prefix":"plugin_<slug>_"}` | `admin:settings` |
| `transient_get` | `{"key":"…"}` | `{"found":bool,"value":…}` | `storage` |
| `transient_set` | `{"key":"…","value":…,"ttl":60}` | – | `storage`; `ttl` seconds, 0 = no expiry |
| `transient_delete` | `{"key":"…"}` | – | `storage` |
| `enqueue_style` | `{"handle","src":"style.css","deps":[…],"where":"front\|admin\|both","media"}` | – | `content:filter`; `src` must be a package asset |
| `enqueue_script` | `{"handle","src":"app.js","deps":[…],"where","in_footer":bool}` | – | `content:filter` |
| `translate` | `{"text":"…","context":"…"}` | `{"text":"…"}` | none; text ≤10000 bytes, context ≤200 |

`translate` returns the text in the language of the current request when the
Host implements the optional `Translator` interface (the core does, through Pursae
Multi Language: the site's string translations, then the language packs; the
domain is forced to the plugin's slug and the string is listed under
"Plugin: *name*" on Languages › Translations). Any other host returns the text
unchanged, so the import is always safe to call.

Post objects are `{"id","title","content","excerpt","status","type","slug","date","modified","author","link","terms":{"category":[…],"post_tag":[…]},"meta":{…}}`.
Settings fields are `{"id","label","type":"text\|textarea\|number\|checkbox\|select","description","placeholder","default","options":[{"value","label"}],"min","max","rows"}`;
the core renders the page under Settings › *menu_title* and saves each field
in the option `plugin_<slug>_<id>` (dashes in the slug become underscores).
Registrations made with `register_*` and `enqueue_*` are kept by the runtime
(`Plugin.Shortcodes()`, `RestRoutes()`, `SettingsPage()`, `Assets()`) and
re-sent by the guest after a re-instantiation, so repeating one is a no-op.

The guest may call host imports while it is inside any export. The host may
call `wpgo_alloc` from inside a host import.

