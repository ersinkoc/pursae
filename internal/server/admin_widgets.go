package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
)

// widgetsScreen is wp-admin/widgets.php with the block-based widgets editor
// (widgets-form-blocks.php): WordPress' own @wordpress/edit-widgets package working against
// the sidebars, widgets and widget-types REST endpoints.
func (s *Server) widgetsScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_theme_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit theme options on this site.")
		return
	}
	if _, _, err := s.widgetAreas(c); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	preload := s.restPreload(c, []preloadPath{
		{"/wp/v2/media", http.MethodOptions},
		{"/wp/v2/widget-types?context=edit&per_page=-1", http.MethodGet},
		{"/wp/v2/sidebars?context=edit&per_page=-1", http.MethodGet},
		{"/wp/v2/widgets?context=edit&per_page=-1&_embed=about", http.MethodGet},
	})
	// core-data asks OPTIONS /batch/v1 for requests.maxItems before its first batch save; the
	// generic OPTIONS handler has no per-route args, so the answer is preloaded here.
	if opts, ok := preload["OPTIONS"].(map[string]any); ok {
		opts["/batch/v1"] = map[string]any{"body": batchOptions(), "headers": map[string]string{"Allow": "POST"}}
	}
	preloadJSON, _ := json.Marshal(preload)

	categories := []map[string]any{
		{"slug": "text", "title": "Text", "icon": nil}, {"slug": "media", "title": "Media", "icon": nil},
		{"slug": "design", "title": "Design", "icon": nil}, {"slug": "widgets", "title": "Widgets", "icon": nil},
		{"slug": "theme", "title": "Theme", "icon": nil}, {"slug": "embed", "title": "Embeds", "icon": nil},
		{"slug": "reusable", "title": "Patterns", "icon": nil},
	}
	catJSON, _ := json.Marshal(categories)
	settingsJSON, _ := json.Marshal(s.widgetsEditorSettings(c, categories))

	a := s.pageAssets(c)
	// common goes first: admin-header.php has printed it before admin-widgets (which uses wp.deprecateL10nObject) in WordPress.
	a.Script("common", "wp-edit-widgets", "wp-format-library", "admin-widgets").Style("wp-edit-widgets", "wp-format-library", "wp-block-library-theme")
	a.AddInline("wp-api-fetch", "wp.apiFetch.use( wp.apiFetch.createPreloadingMiddleware( "+string(preloadJSON)+" ) );")
	a.AddInline("wp-blocks", "wp.blocks.setCategories( "+string(catJSON)+" );")
	a.AddInline("wp-edit-widgets", `wp.domReady( function() {
			wp.editWidgets.initialize( "widgets-editor", `+string(settingsJSON)+` );
		} );`)
	s.enqueueMedia(c, 0)

	s.adminPage(c, "widgets", "Widgets", "widgets-php block-editor-page wp-embed-responsive", "admin/widgets", nil)
}

// widgetsEditorSettings is get_block_editor_settings() for the core/edit-widgets context, merged
// with get_legacy_widget_block_editor_settings() and the theme's editor styles.
func (s *Server) widgetsEditorSettings(c *gin.Context, categories []map[string]any) map[string]any {
	lb := s.links(c)
	theme := s.activeTheme()
	themeURI := lb.HomeURL(theme.URI() + "/")
	var editorStyle string
	for _, f := range theme.EditorStyles {
		if strings.HasSuffix(f, ".css") && !strings.HasPrefix(f, "fonts/") {
			editorStyle += s.webFile("themes/"+theme.Slug+"/assets/"+f) + "\n"
		}
	}
	resolved := ""
	for _, href := range []string{
		"/wp-includes/css/dist/block-library/style.min.css", "/wp-includes/css/dist/block-library/theme.min.css",
		"/wp-includes/css/dist/block-library/editor.min.css", "/wp-includes/css/dist/block-library/editor-elements.min.css",
		"/wp-includes/css/dist/block-editor/content.min.css", "/wp-includes/css/dist/block-library/common.min.css",
		"/wp-includes/css/dist/widgets/style.min.css",
	} {
		resolved += `<link rel="stylesheet" href="` + lb.HomeURL(href) + `" media="all" />` + "\n"
	}
	patterns := []map[string]any{}
	for _, pt := range corePatterns() {
		patterns = append(patterns, map[string]any{"name": pt["name"], "title": pt["title"], "content": pt["content"],
			"categories": pt["categories"], "description": pt["description"], "viewportWidth": pt["viewport_width"],
			"blockTypes": pt["block_types"], "postTypes": pt["post_types"], "inserter": pt["inserter"], "keywords": pt["keywords"]})
	}
	allowedMimes := map[string]string{}
	for k, v := range media.MimeTypes {
		allowedMimes[k] = v
	}
	return map[string]any{
		"widgetTypesToHideFromLegacyWidgetBlock": []string{"pages", "calendar", "archives", "media_audio", "media_image",
			"media_gallery", "media_video", "search", "text", "categories", "recent-posts", "recent-comments", "rss",
			"tag_cloud", "custom_html", "block"},
		"alignWide": false, "allowedBlockTypes": true, "allowedMimeTypes": allowedMimes,
		"defaultEditorStyles": []map[string]any{{"css": s.webFile("wp-includes/css/dist/block-editor/default-editor-styles.min.css")}},
		"blockCategories":     categories, "isRTL": false, "imageDefaultSize": "large", "imageEditing": true,
		"imageDimensions": map[string]any{
			"thumbnail": map[string]any{"width": s.settings.Int("thumbnail_size_w"), "height": s.settings.Int("thumbnail_size_h"), "crop": s.settings.Bool("thumbnail_crop")},
			"medium":    map[string]any{"width": s.settings.Int("medium_size_w"), "height": s.settings.Int("medium_size_h"), "crop": false},
			"large":     map[string]any{"width": s.settings.Int("large_size_w"), "height": s.settings.Int("large_size_h"), "crop": false},
		},
		"imageSizes": []map[string]string{{"slug": "thumbnail", "name": "Thumbnail"}, {"slug": "medium", "name": "Medium"},
			{"slug": "large", "name": "Large"}, {"slug": "full", "name": "Full Size"}},
		"maxUploadFileSize":                maxUploadBytes,
		"__unstableGalleryWithImageBlocks": true,
		"__experimentalFeatures":           editorFeatures(theme),
		"colors":                           theme.Palette,
		"disableCustomColors":              false, "disableCustomFontSizes": false, "disableCustomGradients": false,
		"disableLayoutStyles": false, "enableCustomLineHeight": true, "enableCustomSpacing": false, "enableCustomUnits": true,
		"styles": []map[string]any{
			{"css": s.webFile("wp-includes/css/global-styles-classic.css"), "__unstableType": "presets", "isGlobalStyles": true},
			{"css": ".is-layout-flow > * + *{margin-block-start:1.2rem}body .is-layout-flex{display:flex}.is-layout-flex{flex-wrap:wrap;align-items:center}.is-layout-flex > :is(*, div){margin:0}", "__unstableType": "base-layout", "isGlobalStyles": true},
			{"css": editorStyle, "baseURL": themeURI + "css/", "__unstableType": "theme", "isGlobalStyles": false},
		},
		"__unstableResolvedAssets":                       map[string]string{"styles": resolved, "scripts": ""},
		"__unstableIsBlockBasedTheme":                    false,
		"__experimentalDashboardLink":                    "/wp-admin/",
		"__experimentalAdditionalBlockPatterns":          patterns,
		"__experimentalAdditionalBlockPatternCategories": corePatternCategories(),
		"__experimentalDiscussionSettings": map[string]any{
			"commentOrder": s.settings.String("comment_order"), "commentsPerPage": s.settings.Int("comments_per_page"),
			"defaultCommentsPage": s.settings.String("default_comments_page"), "pageComments": s.settings.Bool("page_comments"),
			"threadComments": s.settings.Bool("thread_comments"), "threadCommentsDepth": s.settings.Int("thread_comments_depth"),
			"defaultCommentStatus": s.settings.String("default_comment_status"), "avatarURL": defaultAvatar,
		},
		"richEditingEnabled":     true,
		"codeEditingEnabled":     true,
		"supportsLayout":         false,
		"supportsTemplateMode":   false,
		"enableCustomFields":     false,
		"canUpdateBlockBindings": currentUser(c).Can("manage_options", nil),
	}
}
