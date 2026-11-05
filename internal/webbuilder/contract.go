// Package webbuilder holds the Pursae Web Builder pieces that do not need the server: the
// integration contract (this file), doc validation, the snapshot and CSS sanitizers, the
// doc → fallback HTML generator and the form schema.
//
// contract.go is the Go half of the integration contract. The prose and the field tables live in
// docs/developer/web-builder-contract.md; the TypeScript half is builder/host/contract.ts. The
// three are kept in step by hand: a change to one is a change to all three, in one commit.
//
// The document model types (DocState and below) mirror the upstream editor's
// src/builder/types.ts at the SHA in UpstreamTypesSHA. They are used to read and validate a doc;
// the stored doc is the client's JSON with privileged fields removed at the JSON level, so fields
// a newer editor adds survive a round trip through an older server.
package webbuilder

// Versions and handshake.
const (
	// APIVersion is the REST/boot schema version. Every web-builder/v2 request from the editor
	// carries it in APIHeader; a mismatch answers 409 ErrAPIMismatch with data.reload=true. Bump it
	// on any incompatible change to a payload in this file.
	APIVersion = 1
	// RendererVersion is the version of the static markup and the data-wb-* runtime contract.
	// Snapshots with an older renderer are listed by Settings › Web Builder › Regenerate pages.
	RendererVersion = 1
	// APIHeader is the request header carrying APIVersion.
	APIHeader = "X-WB-Api"
	// NonceHeader is the core REST nonce header (action "wp_rest").
	NonceHeader = "X-WP-Nonce"
	// BootGlobal is the window property holding Boot on the editor page.
	BootGlobal = "__WEB_BUILDER__"
	// UpstreamTypesSHA is the pursae/Builder commit the doc model types were copied from.
	UpstreamTypesSHA = "22ff4976a1416accd3b7370e176d7206bf390aee"
)

// REST namespace and routes (relative to /wp-json/). {id} and {rid} are decimal integers,
// {section} matches IDPattern, {provider} is one of the AIProvider values and {path} one of
// AIProviderPaths.
const (
	RESTNamespace = "web-builder/v2"

	RouteDocument        = "/web-builder/v2/documents/{id}"                         // GET
	RouteAutosave        = "/web-builder/v2/documents/{id}/autosave"                // PUT, DELETE
	RoutePublish         = "/web-builder/v2/documents/{id}/publish"                 // POST
	RouteLock            = "/web-builder/v2/documents/{id}/lock"                    // POST
	RouteRevisions       = "/web-builder/v2/documents/{id}/revisions"               // GET
	RouteRevision        = "/web-builder/v2/documents/{id}/revisions/{rid}"         // GET
	RouteRevisionRestore = "/web-builder/v2/documents/{id}/revisions/{rid}/restore" // POST
	RouteLibrary         = "/web-builder/v2/library"                                // GET, POST
	RouteLibraryItem     = "/web-builder/v2/library/{id}"                           // DELETE
	RoutePrefs           = "/web-builder/v2/prefs"                                  // GET, PUT
	RouteFontsCSS        = "/web-builder/v2/fonts/css2"                             // GET (text/css)
	RouteForm            = "/web-builder/v2/forms/{id}/{section}"                   // POST, public
	RouteAIStatus        = "/web-builder/v2/ai/status"                              // GET, AI on only
	RouteAILLM           = "/web-builder/v2/ai/llm/{provider}/{path}"               // POST, AI on only
	RouteAIImages        = "/web-builder/v2/ai/images"                              // POST, AI on only
	MediaRoute           = "/wp/v2/media"                                           // existing core route
)

// Asset URL prefixes and embed directories (web/web-builder/...).
const (
	// EditorAssetsPath serves web/web-builder/editor/ (AI build). 404 when AI is off.
	EditorAssetsPath = "/wp-content/plugins/web-builder/dist/editor/"
	// EditorNoAIAssetsPath serves web/web-builder/editor-noai/.
	EditorNoAIAssetsPath = "/wp-content/plugins/web-builder/dist/editor-noai/"
	// SiteAssetsPath serves web/web-builder/site/ (public runtime and base CSS), immutable.
	SiteAssetsPath = "/wp-content/web-builder/site/"
	// EmbedDir is the directory under web/ that holds the built bundles.
	EmbedDir = "web-builder"
	// ManifestFile and BuildFile sit in EmbedDir.
	ManifestFile = "manifest.json"
	BuildFile    = "BUILD.json"
	// PageCSSDir is the uploads sub-directory for page CSS files above InlineCSSMax.
	PageCSSDir = "web-builder/css"
	// FontsDir is the uploads sub-directory of localized Google Fonts (shared with the old builder).
	FontsDir = "web-builder/fonts"
)

// Bundle names (keys of Manifest).
const (
	BundleEditor     = "editor"
	BundleEditorNoAI = "editor-noai"
	BundleSite       = "site"
)

// Post and user meta keys, options.
const (
	PostMarkerMeta = "_web_builder" // "1" on every post that has a web_builder_docs row
	EditLockMeta   = "_edit_lock"   // WordPress format "<unix>:<user id>"
	UserPrefsMeta  = "_web_builder_prefs"
	PluginSlug     = "golementor" // plugin identity is kept: slug, option and URL action
	PluginOption   = "native_golementor"
	EditAction     = "web-builder" // /wp-admin/post?post={id}&action=web-builder
)

// Limits.
const (
	MaxDocBytes         = 4 << 20   // request body of autosave / publish / restore
	MaxSnapshotHTML     = 2 << 20   // RenderResult.HTML
	MaxCSSBytes         = 1 << 20   // utility CSS + section CSS together
	InlineCSSMax        = 24 << 10  // page CSS at or below this is inlined in <style id="wb-css">
	MaxSections         = 200       // top-level sections
	MaxItems            = 500       // items per section
	MaxDepth            = 4         // Item.Sub nesting
	MaxStringBytes      = 64 << 10  // any content / item string
	MaxHTMLSectionLen   = 256 << 10 // content["html"] of a 'html' section
	MaxDataURIBytes     = 32 << 10  // data:image/* URI in src / url()
	MaxLibraryItems     = 500       // saved components per site
	MaxPrefsBytes       = 16 << 10  // all prefs of one user, JSON-encoded
	MaxRevisionsListed  = 50        // per page of GET revisions
	MaxFormBodyBytes    = 64 << 10
	MaxAIBodyBytes      = 8 << 20
	LockTTLSeconds      = 150 // same as WordPress' wp_check_post_lock window
	AutosaveMinMillis   = 2000
	LockRefreshSeconds  = 60
	AIMaxTokens         = 8192 // max_tokens cap on proxied LLM requests
	AIRatePerMinute     = 30   // per-user LLM requests per minute
	AITimeoutSeconds    = 120  // whole proxied request
	AIFirstByteSeconds  = 30
	AIImageMaxBytes     = 10 << 20
	AIImageMaxCount     = 12
	AIImageDefaultCount = 6
	FormMinMillis       = 2000 // wb_t below this = spam
	FormNameMax         = 200
	FormEmailMax        = 254
	FormPhoneMax        = 50
	FormMessageMax      = 5000
	FormOtherMax        = 200                     // date, choice
	IDPattern           = `^[A-Za-z0-9_-]{1,64}$` // section, item and saved-component ids
	PrefKeyPattern      = `^[a-z0-9:._-]{1,64}$`
)

// REST error codes (WordPress error shape: {"code","message","data":{"status",...}}).
const (
	ErrAPIMismatch  = "wb_api_mismatch" // 409, data.reload=true, data.apiVersion
	ErrRevConflict  = "wb_rev_conflict" // 409, data.rev, data.modified, data.modifiedBy
	ErrDetached     = "wb_detached"     // 409, publish without reattach on a detached page
	ErrLocked       = "wb_locked"       // 409, data.lock (another user holds the edit lock)
	ErrTooLarge     = "wb_too_large"    // 413, data.limit
	ErrInvalidDoc   = "wb_invalid_doc"  // 422, data.errors []ValidationError
	ErrNotFound     = "wb_not_found"    // 404
	ErrNotBuilder   = "wb_not_enabled"  // 404, post type not enabled for the builder / plugin off
	ErrFormInvalid  = "wb_form_invalid" // 422, data.fields map[field]reason
	ErrRateLimited  = "wb_rate_limited" // 429, Retry-After header
	ErrAIBudget     = "wb_ai_budget"    // 429, the site's daily token budget is spent
	ErrAIPath       = "wb_ai_path"      // 400, path not allowlisted for the provider
	ErrAIModel      = "wb_ai_model"     // 422, model not allowlisted
	ErrAINoKey      = "wb_ai_no_key"    // 503, provider has no key / URL configured
	ErrAIUpstream   = "wb_ai_upstream"  // 502, provider unreachable or rejected the server key
	ErrAITimeout    = "wb_ai_timeout"   // 504
	ErrForbidden    = "rest_forbidden"  // 401 / 403, core code
	ErrInvalidNonce = "rest_cookie_invalid_nonce"
)

// Environment variables (read once by internal/config).
const (
	EnvAI                = "WPGO_BUILDER_AI" // unset/""/1/true/on/yes = on; 0/false/off/no = off; anything else = off + warning
	EnvAIAnthropic       = "WPGO_BUILDER_AI_ANTHROPIC_KEY"
	EnvAIOpenAI          = "WPGO_BUILDER_AI_OPENAI_KEY"
	EnvAIGemini          = "WPGO_BUILDER_AI_GEMINI_KEY"
	EnvAIMistral         = "WPGO_BUILDER_AI_MISTRAL_KEY"
	EnvAIOllamaURL       = "WPGO_BUILDER_AI_OLLAMA_URL"
	EnvAIModels          = "WPGO_BUILDER_AI_MODELS"       // "provider:model,provider:model"; unset = DefaultAIModels
	EnvAIDailyBudget     = "WPGO_BUILDER_AI_DAILY_BUDGET" // tokens (input+output) per site per UTC day; unset = DefaultAIDailyBudget; 0 = no cap
	DefaultAIDailyBudget = 1_000_000
)

// AIProvider ids are the upstream ids (providers.ts); "google" is Gemini.
type AIProvider string

const (
	AIAnthropic AIProvider = "anthropic"
	AIOpenAI    AIProvider = "openai"
	AIGoogle    AIProvider = "google"
	AIMistral   AIProvider = "mistral"
	AIOllama    AIProvider = "ollama"
)

// AIProviderBases are the fixed upstream bases; Ollama's comes from EnvAIOllamaURL.
var AIProviderBases = map[AIProvider]string{
	AIAnthropic: "https://api.anthropic.com",
	AIOpenAI:    "https://api.openai.com",
	AIGoogle:    "https://generativelanguage.googleapis.com",
	AIMistral:   "https://api.mistral.ai",
}

// AIProviderPaths is the path allowlist of RouteAILLM ({path} without the leading slash).
var AIProviderPaths = map[AIProvider][]string{
	AIAnthropic: {"v1/messages"},
	AIOpenAI:    {"v1/chat/completions"},
	AIGoogle:    {"v1beta/openai/chat/completions"},
	AIMistral:   {"v1/chat/completions"},
	AIOllama:    {"v1/chat/completions"},
}

// DefaultAIModels is the model allowlist when EnvAIModels is unset (upstream providers.ts catalog).
var DefaultAIModels = map[AIProvider][]string{
	AIAnthropic: {"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"},
	AIOpenAI:    {"gpt-4.1", "gpt-4.1-mini", "gpt-4o"},
	AIGoogle:    {"gemini-2.5-flash", "gemini-2.5-pro"},
	AIMistral:   {"mistral-large-latest", "mistral-small-latest"},
	AIOllama:    {"qwen2.5", "llama3.1"},
}

// Feature names in RenderResult.Features; each maps to one data-wb-* behaviour of the runtime.
type Feature string

const (
	FeatureMenu    Feature = "menu"
	FeatureAcc     Feature = "acc"
	FeatureTabs    Feature = "tabs"
	FeatureDismiss Feature = "dismiss"
	FeatureForm    Feature = "form"
	FeatureMotion  Feature = "motion"
	FeatureEmbed   Feature = "embed"
)

// Runtime attributes (see the contract doc, "data-wb-* runtime").
const (
	AttrRoot        = "data-wb-root"
	AttrMenu        = "data-wb-menu"
	AttrAcc         = "data-wb-acc"
	AttrAccSingle   = "data-wb-acc-single"
	AttrTabs        = "data-wb-tabs"
	AttrTab         = "data-wb-tab"
	AttrPanel       = "data-wb-panel"
	AttrAutoplay    = "data-wb-autoplay"
	AttrDismiss     = "data-wb-dismiss"
	AttrDismissBtn  = "data-wb-dismiss-btn"
	AttrForm        = "data-wb-form"
	AttrFormStatus  = "data-wb-form-status"
	AttrState       = "data-wb-state"
	AttrMotion      = "data-wb-motion"
	AttrDur         = "data-wb-dur"
	AttrDelay       = "data-wb-delay"
	AttrEase        = "data-wb-ease"
	AttrRepeat      = "data-wb-repeat"
	AttrStagger     = "data-wb-stagger"
	AttrStaggerItem = "data-wb-stagger-item"
	AttrParallax    = "data-wb-parallax"
	AttrParallaxOn  = "data-wb-parallax-on"
	AttrScrollFade  = "data-wb-scroll-fade"
	AttrScrollScale = "data-wb-scroll-scale"
	AttrHover       = "data-wb-hover"
	AttrHoverOn     = "data-wb-hover-on"
	AttrSticky      = "data-wb-sticky"
	AttrEmbed       = "data-wb-embed"
	ClassJS         = "wb-js" // set on <html> before first paint when the runtime will run
	ClassIn         = "wb-in" // set by the runtime when a motion element has entered
	FormHoneypot    = "wb_hp"
	FormTiming      = "wb_t"
	FormSentQuery   = "wb_form" // ?wb_form=sent|error after a no-JS submit (303 back to the page)
)

// Form field names a data-wb-form may carry (input name attributes).
var FormFields = []string{"name", "email", "phone", "message", "date", "choice"}

// ---------------------------------------------------------------------------------------------
// Document model (upstream src/builder/types.ts). Optional scalars are pointers so that an
// explicit 0 / false survives; optional strings use omitempty.

type Box4 struct {
	T float64 `json:"t"`
	R float64 `json:"r"`
	B float64 `json:"b"`
	L float64 `json:"l"`
}

type CellBox struct {
	Width   *float64 `json:"width,omitempty"`
	Padding *Box4    `json:"padding,omitempty"`
	Bg      string   `json:"bg,omitempty"`
	Radius  *float64 `json:"radius,omitempty"`
	Valign  string   `json:"valign,omitempty"` // start | center | end
	Halign  string   `json:"halign,omitempty"` // start | center | end
}

type MotionSettings struct {
	Entrance       string   `json:"entrance,omitempty"` // EntranceId
	Duration       *float64 `json:"duration,omitempty"`
	Delay          *float64 `json:"delay,omitempty"`
	Easing         string   `json:"easing,omitempty"` // smooth | ease | ease-out | ease-in-out | spring | linear
	Repeat         *bool    `json:"repeat,omitempty"`
	Stagger        *bool    `json:"stagger,omitempty"`
	StaggerDelay   *float64 `json:"staggerDelay,omitempty"`
	Parallax       *float64 `json:"parallax,omitempty"`       // -100…100
	ParallaxTarget string   `json:"parallaxTarget,omitempty"` // bg | content
	ScrollFade     *bool    `json:"scrollFade,omitempty"`
	ScrollScale    *bool    `json:"scrollScale,omitempty"`
	Sticky         *bool    `json:"sticky,omitempty"`
	Hover          string   `json:"hover,omitempty"`       // none | lift | glow | scale
	HoverTarget    string   `json:"hoverTarget,omitempty"` // self | items
}

// ElStyle merges every upstream `interface ElStyle` declaration.
type ElStyle struct {
	Margin        *Box4             `json:"margin,omitempty"`
	Mt            *float64          `json:"mt,omitempty"`
	Mb            *float64          `json:"mb,omitempty"`
	Ml            *float64          `json:"ml,omitempty"`
	Mr            *float64          `json:"mr,omitempty"`
	Padding       *Box4             `json:"padding,omitempty"`
	FontSize      *float64          `json:"fontSize,omitempty"`
	FontWeight    *float64          `json:"fontWeight,omitempty"`
	LineHeight    *float64          `json:"lineHeight,omitempty"`
	LetterSpacing *float64          `json:"letterSpacing,omitempty"`
	Color         string            `json:"color,omitempty"`
	Bg            string            `json:"bg,omitempty"`
	Radius        *float64          `json:"radius,omitempty"`
	Align         string            `json:"align,omitempty"` // left | center | right
	Width         *float64          `json:"width,omitempty"`
	WidthUnit     string            `json:"widthUnit,omitempty"` // px | % | vw
	MaxWidth      *float64          `json:"maxWidth,omitempty"`
	MaxWidthUnit  string            `json:"maxWidthUnit,omitempty"` // px | %
	Height        *float64          `json:"height,omitempty"`
	HeightUnit    string            `json:"heightUnit,omitempty"` // px | vh
	Fit           string            `json:"fit,omitempty"`        // cover | contain
	IconColor     string            `json:"iconColor,omitempty"`
	IconColors    map[string]string `json:"iconColors,omitempty"`
	Font          string            `json:"font,omitempty"`
	Border        string            `json:"border,omitempty"`
	Shadow        string            `json:"shadow,omitempty"`
	Transform     string            `json:"transform,omitempty"` // none | uppercase | lowercase | capitalize
	Italic        *bool             `json:"italic,omitempty"`
	Underline     *bool             `json:"underline,omitempty"`
	BgImage       string            `json:"bgImage,omitempty"`
	BgSize        string            `json:"bgSize,omitempty"`
	BgPos         string            `json:"bgPos,omitempty"`
	List          string            `json:"list,omitempty"`
	Opacity       *float64          `json:"opacity,omitempty"`
	IconSize      *float64          `json:"iconSize,omitempty"`
	Tile          *bool             `json:"tile,omitempty"`
	TileSize      *float64          `json:"tileSize,omitempty"`
	TileOpacity   *float64          `json:"tileOpacity,omitempty"`
	Hide          *bool             `json:"hide,omitempty"`
	Motion        *MotionSettings   `json:"motion,omitempty"`
	Removed       *bool             `json:"removed,omitempty"`
}

type NodeLayout struct {
	Dir     string   `json:"dir,omitempty"` // row | column | grid
	Cols    *float64 `json:"cols,omitempty"`
	Gap     *float64 `json:"gap,omitempty"`
	GapY    *float64 `json:"gapY,omitempty"`
	Align   string   `json:"align,omitempty"`   // start | center | end | stretch
	Justify string   `json:"justify,omitempty"` // start | center | end | space-between
	Wrap    *bool    `json:"wrap,omitempty"`
	Stack   *bool    `json:"stack,omitempty"`
	Clip    *bool    `json:"clip,omitempty"`
	Menu    *bool    `json:"menu,omitempty"`
	Self    string   `json:"self,omitempty"` // start | center | end | stretch
	Basis   *float64 `json:"basis,omitempty"`
	Ratio   *float64 `json:"ratio,omitempty"`
}

type Item struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Text   string      `json:"text"`
	Icon   string      `json:"icon,omitempty"`
	Box    *CellBox    `json:"box,omitempty"`
	Href   string      `json:"href,omitempty"`
	Node   string      `json:"node,omitempty"` // NodeType: box | heading | text | button | link | image | icon | input | divider
	Parent string      `json:"parent,omitempty"`
	Tag    string      `json:"tag,omitempty"`
	Lay    *NodeLayout `json:"lay,omitempty"`
	Sub    *Section    `json:"sub,omitempty"`
}

type ContainerBox struct {
	ContentWidth  string  `json:"contentWidth"` // boxed | full
	Width         float64 `json:"width"`
	WidthUnit     string  `json:"widthUnit"` // px | % | vw
	MinHeight     float64 `json:"minHeight"`
	MinHeightUnit string  `json:"minHeightUnit"` // px | vh
	Direction     string  `json:"direction"`     // row | column | row-reverse | column-reverse
	Justify       string  `json:"justify"`       // start | center | end | space-between | space-around | space-evenly
	Align         string  `json:"align"`         // stretch | start | center | end
	Wrap          bool    `json:"wrap"`
	StackMobile   bool    `json:"stackMobile"`
	GapX          float64 `json:"gapX"`
	GapY          float64 `json:"gapY"`
	GridCols      float64 `json:"gridCols"`
	GridRows      float64 `json:"gridRows"`
	Margin        Box4    `json:"margin"`
	Padding       Box4    `json:"padding"`
	BorderStyle   string  `json:"borderStyle"` // none | solid | dashed | dotted
	BorderWidth   float64 `json:"borderWidth"`
	BorderColor   string  `json:"borderColor"`
	Radius        float64 `json:"radius"`
	Shadow        string  `json:"shadow"` // none | sm | md | lg
	ZIndex        float64 `json:"zIndex"`
}

type SectionStyle struct {
	Palette       string          `json:"palette"` // brand | dark | light | soft | sand
	Align         string          `json:"align"`   // left | center
	Padding       float64         `json:"padding"`
	TitleSize     float64         `json:"titleSize"`
	TitleWeight   *float64        `json:"titleWeight,omitempty"`
	LetterSpacing *float64        `json:"letterSpacing,omitempty"`
	TitleColor    string          `json:"titleColor,omitempty"`
	BodySize      *float64        `json:"bodySize,omitempty"`
	Bg            string          `json:"bg,omitempty"`
	HideOn        []string        `json:"hideOn"` // Device: desktop | tablet | mobile
	CSSID         string          `json:"cssId,omitempty"`
	CSS           string          `json:"css,omitempty"`  // privileged (unfiltered_html)
	Anim          string          `json:"anim,omitempty"` // legacy: none | fade | up | zoom | left
	Motion        *MotionSettings `json:"motion,omitempty"`
}

// GridSettings is upstream Partial<GridSettings>: every field optional.
type GridSettings struct {
	Cols        *float64 `json:"cols,omitempty"`
	ColsT       *float64 `json:"colsT,omitempty"`
	ColsM       *float64 `json:"colsM,omitempty"`
	GapX        *float64 `json:"gapX,omitempty"`
	GapY        *float64 `json:"gapY,omitempty"`
	Pad         *float64 `json:"pad,omitempty"`
	Radius      *float64 `json:"radius,omitempty"`
	Bg          string   `json:"bg,omitempty"`
	Border      *bool    `json:"border,omitempty"`
	BorderW     *float64 `json:"borderW,omitempty"`
	BorderColor string   `json:"borderColor,omitempty"`
	Shadow      string   `json:"shadow,omitempty"` // none | sm | md | lg
	ShowIcon    *bool    `json:"showIcon,omitempty"`
	IconSize    *float64 `json:"iconSize,omitempty"`
	IconColor   string   `json:"iconColor,omitempty"`
}

// BgSettings is upstream Partial<BgSettings>: every field optional.
type BgSettings struct {
	Type         string   `json:"type,omitempty"` // color | gradient | image
	GradFrom     string   `json:"gradFrom,omitempty"`
	GradTo       string   `json:"gradTo,omitempty"`
	GradAngle    *float64 `json:"gradAngle,omitempty"`
	Image        string   `json:"image,omitempty"`
	Overlay      *float64 `json:"overlay,omitempty"`
	Decor        string   `json:"decor,omitempty"`    // none | globe | rings | waves | blob | dots | grid | custom
	DecorSVG     string   `json:"decorSvg,omitempty"` // sanitized to the SVG subset
	DecorColor   string   `json:"decorColor,omitempty"`
	DecorOpacity *float64 `json:"decorOpacity,omitempty"`
	DecorScale   *float64 `json:"decorScale,omitempty"`
	DecorX       *float64 `json:"decorX,omitempty"`
	DecorY       *float64 `json:"decorY,omitempty"`
	DecorRotate  *float64 `json:"decorRotate,omitempty"`
	DecorAnim    string   `json:"decorAnim,omitempty"` // none | spin | float | pulse
}

type Section struct {
	ID      string             `json:"id"`
	Kind    string             `json:"kind"` // one of SectionKinds
	Name    string             `json:"name"`
	Hidden  bool               `json:"hidden"`
	Content map[string]string  `json:"content"`
	Items   []Item             `json:"items"`
	Style   SectionStyle       `json:"style"`
	Box     *ContainerBox      `json:"box,omitempty"`
	Els     map[string]ElStyle `json:"els,omitempty"`
	Grid    *GridSettings      `json:"grid,omitempty"`
	Bg      *BgSettings        `json:"bg,omitempty"`
}

type SiteSettings struct {
	Brand         string   `json:"brand"`
	FontPair      string   `json:"fontPair"` // elegant | modern | plain
	Corners       string   `json:"corners"`  // sharp | soft | round
	HeadingFont   string   `json:"headingFont,omitempty"`
	HeadingWeight *float64 `json:"headingWeight,omitempty"`
	BodyFont      string   `json:"bodyFont,omitempty"`
	BodyWeight    *float64 `json:"bodyWeight,omitempty"`
}

type DocState struct {
	Sections []Section    `json:"sections"`
	Site     SiteSettings `json:"site"`
}

type PageSettings struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Indexable   bool   `json:"indexable"`
}

// SavedComponent is a library item; ID is the web_builder_library id as a decimal string.
type SavedComponent struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Section Section `json:"section"`
}

// SectionKinds is the upstream SectionKind union. KindHTML sections are privileged.
var SectionKinds = []string{"nav", "hero", "banner", "features", "media", "steps", "quote", "faq", "cta", "footer", "text", "image", "form", "layout", "html", "custom"}

const (
	KindHTML   = "html"
	KindForm   = "form"
	KindCustom = "custom"
	KindLayout = "layout"
)

// ---------------------------------------------------------------------------------------------
// Render result (renderStatic in the editor) and publish.

// FontNeed is one font family the snapshot uses.
type FontNeed struct {
	Family  string `json:"family"`
	Weights []int  `json:"weights"`
	Italic  bool   `json:"italic"`
}

// RenderResult is what the upstream renderStatic(doc, opts) returns.
type RenderResult struct {
	HTML     string     `json:"html"`    // one root <div class="wb-page …" data-wb-root>…</div>
	CSS      string     `json:"css"`     // section custom CSS (style.css), scoped; privileged
	Classes  []string   `json:"classes"` // Tailwind candidate classes used by HTML
	Fonts    []FontNeed `json:"fonts"`
	Features []Feature  `json:"features"` // sorted, unique; empty = no runtime
	Renderer int        `json:"renderer"` // RendererVersion of the editor that rendered it
}

// DraftState is a user's autosave.
type DraftState struct {
	Doc     DocState     `json:"doc"`
	Page    PageSettings `json:"page"`
	BaseRev int          `json:"baseRev"`
	SavedAt string       `json:"savedAt"` // RFC 3339
}

// PostStatus values the builder sends; the server may answer with any core status.
const (
	StatusPublish = "publish"
	StatusDraft   = "draft"
	StatusPending = "pending"
	StatusPrivate = "private"
)

type UserRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type LockInfo struct {
	User  UserRef `json:"user"`
	Since string  `json:"since"` // RFC 3339
}

// DocumentResponse answers GET RouteDocument (and is embedded in Boot).
type DocumentResponse struct {
	PostID   int64        `json:"postId"`
	PostType string       `json:"postType"`
	Status   string       `json:"status"`
	Rev      int          `json:"rev"`      // 0 = never published from the builder
	Doc      *DocState    `json:"doc"`      // null = new page, the editor starts from its default
	Page     PageSettings `json:"page"`     // from the post when there is no doc yet
	Draft    *DraftState  `json:"draft"`    // the current user's autosave, only if newer than the doc
	Detached bool         `json:"detached"` // posts.content was edited outside the builder
	Modified string       `json:"modified"` // RFC 3339, posts.modified_at
	Renderer int          `json:"renderer"` // renderer of the stored snapshot
	Lock     *LockInfo    `json:"lock"`     // another user's live lock, else null
}

// AutosaveRequest is the body of PUT RouteAutosave.
type AutosaveRequest struct {
	Doc     DocState     `json:"doc"`
	Page    PageSettings `json:"page"`
	BaseRev int          `json:"baseRev"`
}

// AutosaveResponse: Rev is the current published rev, so the editor can tell when someone
// else published since BaseRev.
type AutosaveResponse struct {
	SavedAt string `json:"savedAt"`
	Rev     int    `json:"rev"`
}

// PublishRequest is the body of POST RoutePublish.
type PublishRequest struct {
	BaseRev    int          `json:"baseRev"`
	Status     string       `json:"status"`             // publish | draft | pending | private
	Reattach   bool         `json:"reattach,omitempty"` // required on a detached page
	Doc        DocState     `json:"doc"`
	Page       PageSettings `json:"page"`
	Render     RenderResult `json:"render"`
	UtilityCSS string       `json:"utilityCss"` // Tailwind compile() of Render.Classes
}

// Stripped values name what the server removed from a publish or autosave.
const (
	StrippedHTMLSections = "html-sections"
	StrippedCustomCSS    = "custom-css"
	StrippedMarkup       = "markup" // the sanitizer changed the snapshot
	StrippedCSS          = "css"    // the CSS sanitizer dropped rules
)

type PublishResponse struct {
	Rev        int      `json:"rev"`
	Status     string   `json:"status"` // pending for users without publish_posts
	Permalink  string   `json:"permalink"`
	RevisionID int64    `json:"revisionId"`
	Modified   string   `json:"modified"`
	Stripped   []string `json:"stripped"`
}

type LockRequest struct {
	Take bool `json:"take,omitempty"` // take over another user's lock
}

type LockResponse struct {
	Lock    *LockInfo `json:"lock"`    // the other holder, or null when the caller holds it
	Expires string    `json:"expires"` // RFC 3339, refresh before this
}

type RevisionSummary struct {
	ID       int64   `json:"id"` // post_revisions.id
	Rev      int     `json:"rev"`
	Author   UserRef `json:"author"`
	Date     string  `json:"date"`
	Title    string  `json:"title"`
	Renderer int     `json:"renderer"`
}

type RevisionResponse struct {
	ID   int64        `json:"id"`
	Rev  int          `json:"rev"`
	Doc  DocState     `json:"doc"`
	Page PageSettings `json:"page"`
}

// RestoreRequest: restoring writes the revision as the caller's draft and returns it; the
// editor then publishes it (a fresh snapshot needs the browser).
type RestoreRequest struct {
	BaseRev int `json:"baseRev"`
}

type LibraryItem struct {
	SavedComponent
	Author  UserRef `json:"author"`
	Created string  `json:"created"`
}

type LibraryCreateRequest struct {
	Name    string  `json:"name"`
	Section Section `json:"section"`
}

// Prefs is a user's editor preferences (recent fonts, layout width, …); keys match PrefKeyPattern.
type Prefs map[string]any

type ValidationError struct {
	Path    string `json:"path"` // JSON pointer into the request, e.g. /doc/sections/3/items/0/href
	Code    string `json:"code"` // ValidationCodes
	Message string `json:"message"`
}

const (
	VErrRequired = "required"
	VErrKind     = "unknown_kind"
	VErrID       = "bad_id"
	VErrTooLong  = "too_long"
	VErrTooMany  = "too_many"
	VErrTooDeep  = "too_deep"
	VErrURL      = "bad_url"
	VErrCSS      = "bad_css" // e.g. "</style" in CSS
	VErrEnum     = "bad_value"
	VErrRenderer = "bad_renderer" // Render.Renderer != RendererVersion
)

// FormResponse answers a JSON (Accept: application/json) form submit.
type FormResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------------------------
// Editor boot config: window.__WEB_BUILDER__ (JSON, "</" escaped as "<\/").

type BootREST struct {
	Root      string `json:"root"`      // absolute, ends with "/wp-json/"
	Namespace string `json:"namespace"` // RESTNamespace
	Nonce     string `json:"nonce"`     // wp_rest
}

type BootLinks struct {
	Site      string `json:"site"` // home URL, no trailing slash
	Permalink string `json:"permalink"`
	Preview   string `json:"preview"`
	Exit      string `json:"exit"`     // where the logo / Exit goes (the post's list screen)
	EditPost  string `json:"editPost"` // core editor of this post
	Settings  string `json:"settings"` // Settings › Web Builder, "" without manage_options
}

type BootCaps struct {
	Publish        bool `json:"publish"` // publish_posts / publish_pages for the type
	UploadFiles    bool `json:"uploadFiles"`
	UnfilteredHTML bool `json:"unfilteredHtml"` // html sections and custom CSS
	EditOthers     bool `json:"editOthers"`     // edit_others_posts: library delete of others' items
}

type BootFonts struct {
	CSSURL string   `json:"cssUrl"` // RouteFontsCSS absolute, with _wpnonce
	Local  []string `json:"local"`  // families already localized
}

type BootMedia struct {
	MaxUploadBytes int64    `json:"maxUploadBytes"`
	Types          []string `json:"types"` // accepted MIME types
}

type AIModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AIProviderInfo struct {
	ID     AIProvider `json:"id"`
	Models []AIModel  `json:"models"`
}

// BootAI is present in every Boot. With AI off (WPGO_BUILDER_AI=0) it is
// {"enabled":false,"providers":[],"llmBase":"","imagesUrl":"","allowCode":false}.
type BootAI struct {
	Enabled   bool             `json:"enabled"`
	Providers []AIProviderInfo `json:"providers"` // configured providers only; empty = demo engine
	LLMBase   string           `json:"llmBase"`   // absolute .../web-builder/v2/ai/llm/ ; "" when off
	ImagesURL string           `json:"imagesUrl"` // absolute RouteAIImages; "" when off
	AllowCode bool             `json:"allowCode"` // always false for now
}

// Boot is window.__WEB_BUILDER__.
type Boot struct {
	APIVersion      int              `json:"apiVersion"`
	RendererVersion int              `json:"rendererVersion"`
	Document        DocumentResponse `json:"document"`
	User            UserRef          `json:"user"`
	REST            BootREST         `json:"rest"`
	Links           BootLinks        `json:"links"`
	Caps            BootCaps         `json:"caps"`
	Locale          string           `json:"locale"` // WordPress locale, e.g. en_US, tr_TR
	Fonts           BootFonts        `json:"fonts"`
	Media           BootMedia        `json:"media"`
	SiteAssets      string           `json:"siteAssets"` // absolute SiteAssetsPath
	AI              BootAI           `json:"ai"`
}

// ---------------------------------------------------------------------------------------------
// AI proxy.

type AIStatusResponse struct {
	Enabled   bool             `json:"enabled"`
	Providers []AIProviderInfo `json:"providers"`
	Budget    AIBudget         `json:"budget"`
}

type AIBudget struct {
	Daily int64 `json:"daily"` // 0 = no cap
	Used  int64 `json:"used"`  // today, UTC
}

// AIImageRequest / AIImageResponse match upstream ai/images.ts (ImageQuery / ImageResult).
type AIImageRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count,omitempty"`       // 1…12, default 6
	Orientation string `json:"orientation,omitempty"` // landscape | portrait | square
}

type AIImage struct {
	URL     string `json:"url"` // Media Library URL after sideload
	MediaID int64  `json:"mediaId"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Alt     string `json:"alt"`
	Credit  string `json:"credit"`
	License string `json:"license"`
	Source  string `json:"source"` // original page URL
}

type AIImageResponse struct {
	Images []AIImage `json:"images"`
	Error  string    `json:"error,omitempty"`
}

// AIImageHosts are the only hosts /ai/images talks to (search APIs and image CDNs).
var AIImageHosts = []string{"api.openverse.org", "commons.wikimedia.org", "upload.wikimedia.org"}

// ---------------------------------------------------------------------------------------------
// Build output.

// ManifestEntry: paths are relative to the bundle directory.
type ManifestEntry struct {
	JS  string   `json:"js"`
	CSS []string `json:"css"`
}

// Manifest is web/web-builder/manifest.json.
type Manifest struct {
	Editor     ManifestEntry `json:"editor"`
	EditorNoAI ManifestEntry `json:"editor-noai"`
	Site       SiteManifest  `json:"site"`
}

type SiteManifest struct {
	Runtime string `json:"runtime"` // e.g. runtime.3f9a1c.js
	CSS     string `json:"css"`     // e.g. wb.8b2e4d.css (base CSS: .wb-page container, .wb-js rules, motion, hover)
}

// BuildInfo is web/web-builder/BUILD.json.
type BuildInfo struct {
	UpstreamSHA     string            `json:"upstreamSha"`
	Patches         map[string]string `json:"patches"` // file name → sha256 hex
	APIVersion      int               `json:"apiVersion"`
	RendererVersion int               `json:"rendererVersion"`
}

// ---------------------------------------------------------------------------------------------
// Sanitizer allowlist for snapshot HTML (S2 builds its bluemonday policy from these).

// AllowedElements are the HTML elements a snapshot may contain.
var AllowedElements = []string{
	"div", "section", "header", "footer", "nav", "main", "article", "aside",
	"h1", "h2", "h3", "h4", "h5", "h6", "p", "span", "a", "strong", "b", "em", "i", "u", "s",
	"small", "mark", "sub", "sup", "br", "hr", "ul", "ol", "li", "dl", "dt", "dd",
	"blockquote", "q", "cite", "figure", "figcaption", "img", "picture", "source", "video",
	"button", "form", "input", "textarea", "select", "option", "label", "fieldset", "legend",
	"details", "summary", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption",
	"time", "address", "code", "pre", "kbd", "iframe",
}

// AllowedSVGElements is the SVG subset (snapshot icons, decor layers, BgSettings.DecorSVG).
var AllowedSVGElements = []string{
	"svg", "g", "path", "circle", "ellipse", "line", "polyline", "polygon", "rect", "defs",
	"linearGradient", "radialGradient", "stop", "clipPath", "mask", "use", "title", "desc",
	"symbol", "text", "tspan",
}

// GlobalAttrs are allowed on every element, plus aria-* and data-wb-* (AttrPrefixes).
var GlobalAttrs = []string{"class", "style", "id", "title", "role", "lang", "dir", "hidden", "tabindex"}

var AttrPrefixes = []string{"aria-", "data-wb-"}

// ElementAttrs are per-element attributes on top of GlobalAttrs.
var ElementAttrs = map[string][]string{
	"a":        {"href", "target", "rel"},
	"img":      {"src", "srcset", "sizes", "alt", "width", "height", "loading", "decoding", "fetchpriority"},
	"source":   {"srcset", "sizes", "type", "media"},
	"video":    {"src", "poster", "controls", "autoplay", "muted", "loop", "playsinline", "preload", "width", "height"},
	"input":    {"type", "name", "placeholder", "required", "autocomplete", "value", "maxlength", "checked", "disabled", "readonly"},
	"textarea": {"name", "rows", "placeholder", "required", "maxlength"},
	"select":   {"name", "required"},
	"option":   {"value", "selected"},
	"label":    {"for"},
	"button":   {"type", "disabled"},
	"form":     {"method", "action", "novalidate"},
	"details":  {"open"},
	"time":     {"datetime"},
	"td":       {"colspan", "rowspan"},
	"th":       {"colspan", "rowspan", "scope"},
	"ol":       {"start", "reversed"},
	"iframe":   {"src", "srcdoc", "sandbox", "title", "loading", "allow", "allowfullscreen", "width", "height", "referrerpolicy"},
	"svg":      {"viewBox", "xmlns", "width", "height", "fill", "stroke", "stroke-width", "preserveAspectRatio", "focusable"},
}

// SVGAttrs are allowed on every AllowedSVGElements element.
var SVGAttrs = []string{
	"d", "cx", "cy", "r", "rx", "ry", "x", "y", "x1", "y1", "x2", "y2", "points", "width", "height",
	"transform", "opacity", "fill", "fill-opacity", "fill-rule", "clip-rule", "stroke",
	"stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-opacity", "stroke-dasharray",
	"offset", "stop-color", "stop-opacity", "gradientUnits", "gradientTransform", "viewBox",
	"preserveAspectRatio", "clip-path", "mask", "href", "font-size", "font-weight", "text-anchor",
}

// InputTypes are the allowed <input type> values.
var InputTypes = []string{"text", "email", "tel", "date", "hidden", "checkbox", "radio", "submit"}

// URLSchemes are allowed in href; src/srcset/poster allow only http, https, relative and
// data:image/* (DataImageTypes, at most MaxDataURIBytes). svg href/xlink:href, clip-path and
// mask allow only same-document references ("#id" / "url(#id)").
var URLSchemes = []string{"http", "https", "mailto", "tel"}

var DataImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml"}

// VideoEmbedHosts are the iframe src hosts allowed for every author (https only, path /embed/… or /video/…).
var VideoEmbedHosts = []string{"www.youtube-nocookie.com", "www.youtube.com", "player.vimeo.com"}

// EmbedSandbox is the only sandbox value allowed on a srcdoc iframe (html sections; unfiltered_html
// authors only). allow-same-origin is never allowed.
const EmbedSandbox = "allow-scripts"

// ForbiddenElements are dropped with their content wherever they appear.
var ForbiddenElements = []string{"script", "style", "link", "meta", "base", "object", "embed", "applet", "frame", "frameset", "noscript", "template", "foreignObject", "animate", "animateMotion", "animateTransform", "set", "math", "portal"}

// RichTextElements are the inline tags upstream rich.ts keeps in content strings (a: href only).
var RichTextElements = []string{"b", "i", "u", "s", "code", "a", "br", "ul", "ol", "li"}

// CSS sanitizer rules.
var (
	// CSSAllowedAtRules; anything else (@import, @charset, @namespace, @document, …) is dropped.
	CSSAllowedAtRules = []string{"media", "container", "supports", "keyframes", "font-face", "layer", "property"}
	// CSSForbiddenTokens drop the whole declaration (matched case-insensitively after unescaping).
	CSSForbiddenTokens = []string{"expression(", "behavior", "-moz-binding", "javascript:", "vbscript:", "-o-link", "@import"}
)

// CSSFontFacePrefix: @font-face src url() must start with this (after the uploads base URL).
const CSSFontFacePrefix = "/wp-content/uploads/web-builder/fonts/"
