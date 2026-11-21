package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"wpgo/internal/plugins"
	"wpgo/internal/webbuilder"
)

// Plugin setup guides ---------------------------------------------------------------------------
//
// Every plugin can come with a step-by-step setup guide: the built-in ones below, a Wasm package
// through the "setup" steps of its plugin.json. The first time a plugin with a guide is activated
// from the Plugins screen, the admin is asked whether to start with the guide or go straight to
// the settings (plugin-setup?plugin=…&welcome=1). The guide stays one click away afterwards: the
// Plugins screen shows a "Setup guide" link on the row of every active plugin that has one.
//
// Two built-in plugins ship a full setup wizard of their own (Pursae SMTP, Pursae SEO): for them
// the guide is that wizard, so "Start the setup guide" opens it.

const pluginSetupPath = "/wp-admin/plugin-setup"

// setupGuideStep is one step of a setup guide. Title and Body are msgids of the admin catalog
// for the built-in guides (the page translator finds them as whole text nodes, so they hold no
// markup); a Wasm package's steps are shown as written.
type setupGuideStep struct {
	Title, Body, URL string
}

type setupGuide struct {
	Slug, Name  string
	Intro       string // one sentence under the title
	Steps       []setupGuideStep
	WizardURL   string // the plugin's own setup wizard, which replaces the step list
	SettingsURL string // "Go straight to the settings"
	Native      bool   // texts are catalog msgids
}

// pluginSetupState is what the guide remembers per plugin, in the plugin_setup_<slug> option.
type pluginSetupState struct {
	Asked    bool  // the first-activation question was answered
	Done     []int // finished steps (0-based)
	Finished bool  // the admin finished the guide
}

func (st pluginSetupState) done(i int) bool {
	for _, d := range st.Done {
		if d == i {
			return true
		}
	}
	return false
}

// nativeSetupGuides are the guides of the built-in plugins, by stored slug.
func nativeSetupGuides() map[string]setupGuide {
	g := map[string]setupGuide{
		"classic-editor": {Intro: "Bring back the classic editor in two short steps.", Steps: []setupGuideStep{
			{"Choose the default editor", "Under Settings › Writing, pick the classic or the block editor as the default for everyone and decide whether users may switch between them.", "/wp-admin/options-writing"},
			{"Open a post in the classic editor", "Open any post: it now opens in the editor you picked. If switching is allowed, each post remembers the editor it was last edited in.", "/wp-admin/edit"},
		}},
		"wpgo-cache": {Intro: "Make the site fast with the page cache in four steps.", Steps: []setupGuideStep{
			{"Tell the cache what runs in front of Pursae", "On the Cache screen, pick your web server or proxy (LiteSpeed, nginx, a CDN) or leave it on auto-detect, and switch page caching on.", "/wp-admin/admin?page=wpgo-cache-settings"},
			{"Turn on page optimizations", "Minify and combine CSS and JavaScript, lazy-load images and defer scripts. Turn them on one by one and check the site after each.", "/wp-admin/admin?page=wpgo-cache-optimize"},
			{"Warm up the cache", "In the Toolbox, run the crawler so visitors get cached pages right away, and purge everything whenever you need a clean start.", "/wp-admin/admin?page=wpgo-cache-toolbox"},
			{"Check the hit ratio", "The dashboard shows the hit ratio and the cached pages. A high hit ratio after a day of traffic means the cache works.", "/wp-admin/admin?page=wpgo-cache"},
		}},
		webbuilder.PluginSlug: {Intro: "Build your first visual page and the site's header and footer.", Steps: []setupGuideStep{
			{"Choose what the Web Builder edits", "Under Settings › Web Builder, tick the post types (pages, posts, products…) that open in the visual builder.", wbSettingsURL},
			{"Design the header and footer", "Under Appearance › Theme Builder, create a header and a footer from ready-made sections and set where they are shown.", "/wp-admin/themes?page=web-builder-templates"},
			{"Build your first page", "Add a new page and open it in the Web Builder. Drop in sections, edit the texts and images, then publish: the page is saved as fast, static HTML.", "/wp-admin/post-new?post_type=page"},
			{"Import an existing HTML site (optional)", "Have a ready HTML template? Upload it on the import screen and its pages become editable Web Builder pages.", "/wp-admin/web-builder-import"},
		}},
		msSlug: {Intro: "Make sure the site's emails reach the inbox."},
		gontactSlug: {Intro: "Put a working contact form on the site in four steps.", Steps: []setupGuideStep{
			{"Make sure emails are delivered", "Contact forms send email. Set up Pursae SMTP (or check that the server can send mail) so messages do not end up in spam.", "/wp-admin/plugins"},
			{"Edit the contact form", "A contact form was created for you. Open it, adjust the fields and the email it sends, and note its shortcode.", "/wp-admin/admin?page=wpcf7"},
			{"Turn on spam protection", "Under Integration, connect reCAPTCHA or Cloudflare Turnstile so bots cannot fill in the form.", "/wp-admin/admin?page=wpcf7-integration"},
			{"Place the form on a page", "Paste the form's shortcode into a page (for example Contact), publish it and send a test message.", "/wp-admin/edit?post_type=page"},
		}},
		acfgoSlug: {Intro: "Add your own fields to posts, pages and more.", Steps: []setupGuideStep{
			{"Create a field group", "Add a field group, give it a name and add fields: text, image, repeater, relationship and many more.", "/wp-admin/acf-field-group"},
			{"Choose where the fields appear", "Under Location Rules, pick the post types, pages, taxonomies or options pages that show the group.", "/wp-admin/acf-field-groups"},
			{"Add custom post types and taxonomies (optional)", "Need a Portfolio or a Team section? Register a post type or a taxonomy without code.", "/wp-admin/acf-post-types"},
			{"Fill in and show the fields", "Edit a post to fill in the new fields, then show them in your theme or in the Web Builder's dynamic tags.", "/wp-admin/edit"},
		}},
		golylangSlug: {Intro: "Make the site multilingual in five steps.", Steps: []setupGuideStep{
			{"Add your languages", "Add the site's default language first, then every language you want to translate into.", "/wp-admin/admin?page=mlang"},
			{"Choose how languages appear in URLs", "In the settings, pick a directory (/en/), a subdomain or a separate domain per language.", "/wp-admin/admin?page=mlang_settings"},
			{"Translate your content", "Open a post or page and use the language column to add its translations. Do the same for categories and tags.", "/wp-admin/edit"},
			{"Translate the site's strings", "Translate the site title, tagline, widget titles and other theme strings.", "/wp-admin/admin?page=mlang_strings"},
			{"Add a language switcher", "Add the language switcher to a menu or a widget area so visitors can change the language.", "/wp-admin/nav-menus"},
		}},
		seogoSlug: {Intro: "Get the site ready for search engines."},
		gooikeSlug: {Intro: "Set up a GDPR and KVKK ready cookie banner in five steps.", Steps: []setupGuideStep{
			{"Pick the banner's look and texts", "Choose a theme, the position and the texts of the cookie banner and the preferences center.", "/wp-admin/gooike?page=banner"},
			{"Scan the site for cookies", "Run the scanner to find the cookies the site sets and sort them into categories.", "/wp-admin/gooike?page=categories&tab=scan"},
			{"Block trackers until consent", "In the script manager, add analytics, ad and embed scripts so they run only after the visitor agrees.", "/wp-admin/gooike?page=scripts"},
			{"Choose the consent rules", "In the settings, pick the law that applies (GDPR, KVKK, CCPA), turn on Google Consent Mode v2 if you use Google tags, and link your privacy policy.", "/wp-admin/gooike?page=settings"},
			{"Preview the banner", "Look at the banner as a visitor sees it, then check the consent log after the first visits.", "/wp-admin/gooike?page=preview"},
		}},
		pixelSlug: {Intro: "Connect your ad and analytics tags in four steps.", Steps: []setupGuideStep{
			{"Add your pixels", "Enter the IDs of the platforms you use: Meta Pixel, Google Analytics 4, Google Ads, TikTok, Pinterest, Microsoft Advertising.", "/wp-admin/pixelgo?page=pixels"},
			{"Choose the events to send", "Pick the standard events (page view, view content, add to cart, purchase…) each platform receives.", "/wp-admin/pixelgo?page=events"},
			{"Turn on server-side delivery (optional)", "Add the Conversions API access tokens so events are also sent from the server, with deduplication.", "/wp-admin/pixelgo?page=settings"},
			{"Check the delivery log", "Visit the site and check the delivery log: every event shows whether it reached the platform.", "/wp-admin/pixelgo?page=logs"},
		}},
		analyticsSlug: {Intro: "Start collecting privacy-friendly statistics.", Steps: []setupGuideStep{
			{"Review the settings", "Choose what is tracked (outbound links, downloads, forms, 404s, Core Web Vitals) and exclude your own visits.", "/wp-admin/analytics?tab=settings"},
			{"Open the dashboard", "Visit the site in another browser, then open the dashboard: visitors, pages and sources show up within seconds.", "/wp-admin/analytics?range=today"},
		}},
		forumSlug: {Intro: "Open your community forum in five steps.", Steps: []setupGuideStep{
			{"Review the forum settings", "Choose the forum's address, who may register and post, and whether new topics need approval.", "/wp-admin/forum?tab=settings"},
			{"Create categories and forums", "Add a few categories and the forums inside them where members start topics.", "/wp-admin/forum?tab=forums"},
			{"Allow Google sign-in (optional)", "Connect a Google client so members can join with one click.", "/wp-admin/forum?tab=google"},
			{"Set up badges", "Reward active members with badges for posts, replies and helpful answers.", "/wp-admin/forum?tab=badges"},
			{"Moderate", "Approve, close, pin or remove topics and replies from the moderation queue.", "/wp-admin/forum?tab=moderation"},
		}},
		gooSlug: {Intro: "Open your online store in six steps.", Steps: []setupGuideStep{
			{"Enter the store details", "Set the store address, the currency and where you sell to.", "/wp-admin/goocommerce?page=settings&tab=general"},
			{"Add your first product", "Add a product with a name, price, images and stock.", "/wp-admin/post-new?post_type=product"},
			{"Set up shipping", "Create shipping zones and add the methods and rates for each.", "/wp-admin/goocommerce?page=settings&tab=shipping"},
			{"Set up payments and checkout", "Turn on the payment methods you accept and review the checkout fields.", "/wp-admin/goocommerce?page=settings&tab=checkout"},
			{"Pick a store theme", "Choose a store theme that fits your products.", "/wp-admin/goocommerce?page=themes"},
			{"Switch on the modules you need (optional)", "Subscriptions, memberships, bookings, gift cards and more are ready when you need them.", "/wp-admin/goocommerce?page=extensions"},
		}},
		asSlug: {Intro: "Protect the admin logins in four steps.", Steps: []setupGuideStep{
			{"Turn on two-factor sign-in", "Choose which roles must use emailed codes or an authenticator app when they sign in.", "/wp-admin/admin?page=adminsecgo-settings&tab=twofactor"},
			{"Set up your own second factor", "Add an authenticator app to your own account and save the backup codes somewhere safe.", "/wp-admin/admin?page=adminsecgo-account"},
			{"Protect the login page", "Set a custom login URL, login lockouts and the password policy.", "/wp-admin/admin?page=adminsecgo-settings&tab=login"},
			{"Review the activity log", "Sign-ins, failed attempts and lockouts are logged. Check it from time to time.", "/wp-admin/admin?page=adminsecgo-log"},
		}},
		gocdnSlug: {Intro: "Serve your media from a CDN in four steps.", Steps: []setupGuideStep{
			{"Connect your storage", "Pick the provider (Amazon S3, Cloudflare R2, DigitalOcean Spaces, Backblaze B2…), enter the bucket and the access keys, and test the connection.", "/wp-admin/gocdn"},
			{"Offload the media library", "Copy the existing uploads to the bucket. New uploads go there automatically.", "/wp-admin/gocdn?page=offload"},
			{"Optimize images (optional)", "Resize images and convert them to WebP on the fly.", "/wp-admin/gocdn?page=images"},
			{"Purge the caches", "After changing the CDN settings, purge the page cache and the CDN.", "/wp-admin/gocdn?page=purge"},
		}},
		hgSlug: {Intro: "Turn the site into a headless CMS in five steps.", Steps: []setupGuideStep{
			{"Design your content types", "In the Content-Type Builder, create collection types, single types and components with their fields.", "/wp-admin/headlessgo?page=builder"},
			{"Add content", "In the Content Manager, add entries, then save them as drafts or publish them.", "/wp-admin/headlessgo"},
			{"Create an API token", "Create a token for your front-end application to read (or write) the content.", "/wp-admin/headlessgo?page=tokens"},
			{"Set permissions and CORS", "Choose what the public API may read and which origins may call it.", "/wp-admin/headlessgo?page=settings"},
			{"Add webhooks (optional)", "Let your front end rebuild when content changes.", "/wp-admin/headlessgo?page=webhooks"},
		}},
		bmgrSlug: {Intro: "Keep the site safe with backups in three steps.", Steps: []setupGuideStep{
			{"Take your first backup", "Choose what to include (database, media, themes, plugins, users) and create a backup.", "/wp-admin/admin?page=backup-manager&tab=create"},
			{"Schedule automatic backups", "Back up daily or weekly and choose how many backups to keep.", "/wp-admin/admin?page=backup-manager&tab=schedule"},
			{"Download a copy", "Download a backup and keep it off the server. To move the site, import that file on the new one.", "/wp-admin/admin?page=backup-manager&tab=backups"},
		}},
		flipbookSlug: {Intro: "Turn a PDF into a flipbook in three steps.", Steps: []setupGuideStep{
			{"Add a PDF", "Click Add New and pick a PDF from the Media Library, or upload a new one.", "/wp-admin/admin?page=pursae-flipbook&action=new"},
			{"Adjust the look", "Set the height in posts, the background and the toolbar buttons, and check the preview.", "/wp-admin/admin?page=pursae-flipbook"},
			{"Place it on a page", "Copy the shortcode into a post or a page, or share the full-screen link.", "/wp-admin/edit?post_type=page"},
		}},
		maintSlug: {Intro: "Show a maintenance or coming soon page in four steps.", Steps: []setupGuideStep{
			{"Choose the mode and the texts", "Pick maintenance or coming soon, write the title and the message, and set a countdown if you like.", "/wp-admin/admin?page=maintenance&tab=general"},
			{"Pick a design", "Choose a theme for the page and add your logo and social links.", "/wp-admin/admin?page=maintenance&tab=themes"},
			{"Decide who still sees the site", "Logged-in users always do. Add whitelisted IP addresses or share the secret access link.", "/wp-admin/admin?page=maintenance&tab=access"},
			{"Switch it on", "Turn the mode on in the general settings and open the site in a private window to check.", "/wp-admin/admin?page=maintenance&tab=general"},
		}},
	}
	return g
}

// setupGuideFor returns the setup guide of a plugin (stored slug), built-in or installed.
func (s *Server) setupGuideFor(ctx context.Context, slug string) (setupGuide, bool) {
	if _, ok := nativeSetupGuides()[slug]; ok {
		for _, r := range s.nativePluginRows() {
			if r.Slug == slug {
				return s.setupGuideForRow(ctx, r)
			}
		}
	}
	return s.setupGuideForRow(ctx, pluginRow{Slug: slug})
}

// setupGuideForRow is setupGuideFor for a Plugins screen row (name and settings link known).
func (s *Server) setupGuideForRow(ctx context.Context, r pluginRow) (setupGuide, bool) {
	slug := r.Slug
	if g, ok := nativeSetupGuides()[slug]; ok {
		g.Slug, g.Native, g.Name, g.SettingsURL = slug, true, r.Name, r.SettingsURL
		switch slug {
		case msSlug:
			g.WizardURL = "/wp-admin/admin?page=wp-mail-smtp-setup-wizard"
			g.SettingsURL = "/wp-admin/admin?page=" + msSlug
		case seogoSlug:
			g.WizardURL = "/wp-admin/seogo?page=wizard"
		case "wpgo-cache":
			g.SettingsURL = "/wp-admin/admin?page=wpgo-cache"
		case webbuilder.PluginSlug:
			g.SettingsURL = wbSettingsURL
		}
		return g, true
	}
	if !plugins.ValidSlug(slug) || s.plugins == nil {
		return setupGuide{}, false
	}
	pkg, err := plugins.LoadPackageFile(filepath.Join(s.plugins.dir, slug+".zip"), s.plugins.trusted(), false)
	if err != nil || len(pkg.Manifest.Setup) == 0 {
		return setupGuide{}, false
	}
	m := pkg.Manifest
	g := setupGuide{Slug: slug, Name: m.Name}
	for _, st := range m.Setup {
		u := st.URL
		if u != "" && !plugins.ValidSetupURL(u) { // validated when installed; checked again before it becomes a link
			u = ""
		}
		g.Steps = append(g.Steps, setupGuideStep{Title: st.Title, Body: st.Body, URL: u})
		if g.SettingsURL == "" && u != "" {
			g.SettingsURL = u
		}
	}
	return g, true
}

func pluginSetupKey(slug string) string { return "plugin_setup_" + slug }

func (s *Server) pluginSetupLoad(slug string) pluginSetupState {
	var st pluginSetupState
	m, _ := s.settings.Get(pluginSetupKey(slug)).(map[string]any)
	st.Asked, _ = m["asked"].(bool)
	st.Finished, _ = m["finished"].(bool)
	list, _ := m["done"].([]any)
	for _, v := range list {
		if f, ok := v.(float64); ok {
			st.Done = append(st.Done, int(f))
		}
	}
	return st
}

func (s *Server) pluginSetupSave(ctx context.Context, slug string, st pluginSetupState) error {
	done := st.Done
	if done == nil {
		done = []int{}
	}
	return s.settings.Set(ctx, map[string]any{pluginSetupKey(slug): map[string]any{"asked": st.Asked, "done": done, "finished": st.Finished}})
}

// pluginSetupURL is the guide's address for a stored slug; welcome asks the first-activation question.
func pluginSetupURL(slug string, welcome bool) string {
	u := pluginSetupPath + "?plugin=" + url.QueryEscape(pluginURLSlug(slug))
	if welcome {
		u += "&welcome=1"
	}
	return u
}

// guideEntryURL is where "Setup guide" leads: the plugin's own wizard, or the step list.
func (g setupGuide) entryURL() string {
	if g.WizardURL != "" {
		return g.WizardURL
	}
	return pluginSetupURL(g.Slug, false)
}

// setupWelcomeWriter turns the redirect that ends a successful activation into the
// first-activation question. A failed activation (…?error=…) is left alone.
type setupWelcomeWriter struct {
	gin.ResponseWriter
	to string
}

func (w *setupWelcomeWriter) WriteHeader(code int) {
	if code == http.StatusFound {
		if loc := w.Header().Get("Location"); loc != "" && !containsQueryKey(loc, "error") {
			w.Header().Set("Location", w.to)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func containsQueryKey(loc, key string) bool {
	u, err := url.Parse(loc)
	return err == nil && u.Query().Has(key)
}

// pluginSetupOnActivate arms the first-activation question for slug, when it has a guide that was
// never offered. It is called before the activation runs.
func (s *Server) pluginSetupOnActivate(c *gin.Context, slug string) {
	g, ok := s.setupGuideFor(c.Request.Context(), slug)
	if !ok || s.pluginSetupLoad(slug).Asked {
		return
	}
	c.Writer = &setupWelcomeWriter{ResponseWriter: c.Writer, to: pluginSetupURL(g.Slug, true)}
}

// pluginSetupPage data.
type pluginSetupData struct {
	Guide            setupGuide
	Welcome          bool
	Nonce            string
	Steps            []pluginSetupStepView
	Current          int // 0-based
	Step             pluginSetupStepView
	Count, DoneCount int
	Percent          int
	Prev, Next       string // step links
	Last             bool
	Finished         bool
}

type pluginSetupStepView struct {
	Index        int
	Num          int
	Title, Body  string
	URL, Link    string // the screen, the guide's link to this step
	Done, Active bool
}

// pluginSetupPage serves /wp-admin/plugin-setup: the first-activation question and the guide.
func (s *Server) pluginSetupPage(c *gin.Context) {
	if !currentUser(c).Can("activate_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage plugins for this site.")
		return
	}
	slug := pluginSlugFromURL(c.Query("plugin"))
	if c.Request.Method == http.MethodPost {
		slug = pluginSlugFromURL(c.PostForm("plugin"))
	}
	ctx := c.Request.Context()
	g, ok := s.setupGuideFor(ctx, slug)
	if !ok {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("That plugin has no setup guide."))
		return
	}
	if !s.pluginActive(ctx, slug) {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("Activate the plugin before starting its setup guide."))
		return
	}
	nonceAction := "plugin-setup_" + slug
	st := s.pluginSetupLoad(slug)
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, nonceAction) {
			s.denyNonce(c)
			return
		}
		step, _ := strconv.Atoi(c.PostForm("step"))
		if step < 0 || step >= len(g.Steps) {
			step = 0
		}
		to := pluginSetupURL(slug, false)
		switch c.PostForm("do") {
		case "start": // "Start the setup guide"
			st.Asked = true
			to = g.entryURL()
		case "settings": // "Go straight to the settings"
			st.Asked = true
			to = g.SettingsURL
			if to == "" {
				to = "/wp-admin/plugins?activate=true"
			}
		case "done": // "Mark as done and continue"
			st.Asked = true
			if !st.done(step) && len(g.Steps) > 0 {
				st.Done = append(st.Done, step)
			}
			if step+1 < len(g.Steps) {
				to += "&step=" + strconv.Itoa(step+2)
			} else {
				st.Finished = true
				to += "&finished=1"
			}
		case "undo":
			kept := st.Done[:0]
			for _, d := range st.Done {
				if d != step {
					kept = append(kept, d)
				}
			}
			st.Done, st.Finished = kept, false
			to += "&step=" + strconv.Itoa(step+1)
		case "restart":
			st = pluginSetupState{Asked: true}
		}
		if err := s.pluginSetupSave(ctx, slug, st); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("The setup guide could not be saved: "+err.Error()))
			return
		}
		c.Redirect(http.StatusFound, to)
		return
	}

	d := pluginSetupData{Guide: g, Welcome: c.Query("welcome") == "1", Nonce: s.nonce(c, nonceAction), Count: len(g.Steps), Finished: st.Finished || c.Query("finished") == "1"}
	if !d.Welcome && g.WizardURL != "" { // the plugin's own wizard is its guide
		c.Redirect(http.StatusFound, g.WizardURL)
		return
	}
	cur := -1
	if n, err := strconv.Atoi(c.Query("step")); err == nil && n >= 1 && n <= len(g.Steps) {
		cur = n - 1
	}
	for i, step := range g.Steps {
		v := pluginSetupStepView{Index: i, Num: i + 1, Title: step.Title, Body: step.Body, URL: step.URL,
			Link: pluginSetupURL(slug, false) + "&step=" + strconv.Itoa(i+1), Done: st.done(i)}
		if v.Done {
			d.DoneCount++
		} else if cur < 0 {
			cur = i // the first unfinished step
		}
		d.Steps = append(d.Steps, v)
	}
	if cur < 0 {
		cur = 0
	}
	if len(d.Steps) > 0 {
		d.Current = cur
		d.Steps[cur].Active = true
		d.Step = d.Steps[cur]
		d.Last = cur == len(d.Steps)-1
		d.Percent = d.DoneCount * 100 / len(d.Steps)
		if cur > 0 {
			d.Prev = d.Steps[cur-1].Link
		}
		if cur+1 < len(d.Steps) {
			d.Next = d.Steps[cur+1].Link
		}
	}
	title := "Setup Guide"
	if d.Welcome {
		title = "Get Started"
	}
	s.adminPage(c, "plugins", title+" ‹ "+g.Name, "plugins-php pursae-plugin-setup", "admin/plugin-setup", d)
}

// pluginActive reports whether the plugin with the stored slug is active.
func (s *Server) pluginActive(ctx context.Context, slug string) bool {
	for _, r := range s.pluginInventory(ctx) {
		if r.Slug == slug {
			return r.Active
		}
	}
	return false
}
