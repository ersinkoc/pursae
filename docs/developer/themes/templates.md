---
title: Templates
description: The template hierarchy, the data every template receives and the functions it can call.
weight: 40
section: themes
---

Templates are Go [`html/template`](https://pkg.go.dev/html/template) files. Output is escaped
according to context automatically; values the core has already rendered as HTML (`.Content`,
`.Head`, menus, widget areas) are `template.HTML` and print as-is.

## Hierarchy

The core picks a page kind from the request and resolves it along the WordPress hierarchy to the
first template the package has. Only `index.html` is required.

| Request | Templates tried, in order |
|---------|--------------------------|
| front page (static page or latest posts) | `front-page.html`, then `home.html` (latest posts) or `page.html` (a static page), `index.html` |
| blog index (posts page) | `home.html`, `index.html` |
| a post | `single.html`, `index.html` |
| a page | `page.html`, `index.html` |
| category archive | `category.html`, `archive.html`, `index.html` |
| tag archive | `tag.html`, `archive.html`, `index.html` |
| author archive | `author.html`, `archive.html`, `index.html` |
| date archive | `date.html`, `archive.html`, `index.html` |
| search results | `search.html`, `index.html` |
| not found | `404.html`, `index.html` |

Parts — `header.html`, `footer.html`, `sidebar.html`, `comments.html`, `content*.html` — are
included with `{{template "header" .}}`. Every file is parsed into one template set, so a
`{{define "name"}}` in any file is callable from any other. The bundled themes use the same
mechanism with the names `theme/index`, `theme/single`, `theme/page`, `theme/archive`,
`theme/search` and `theme/404`.

## The view

Every template receives the same **site view** (`.`):

### Page

| Field | Type | Meaning |
|-------|------|---------|
| `.Template` | string | `index`, `single`, `page`, `archive`, `search`, `404` |
| `.Q` | query | the main query: `.Q.Kind` (`home`, `front-page`, `single`, `page`, `category`, `tag`, `author`, `date`, `search`, `404`), `.Q.Post`, `.Q.Term`, `.Q.Author`, `.Q.Year/.Month/.Day`, `.Q.Search`, `.Q.Paged`, `.Q.Preview` |
| `.DocumentTitle` | HTML | `wp_get_document_title()` with separator and site name |
| `.Head` | HTML | `wp_head()`: feeds, styles, scripts, Customizer CSS, plugin assets. Print it before `</head>` |
| `.Footer` | HTML | `wp_footer()`: scripts. Print it before `</body>` |
| `.BodyOpen` | HTML | `wp_body_open()` |
| `.BodyClass` | string | `body_class()`: `home`, `blog`, `single`, `single-post`, `postid-12`, `page-template-default`, `logged-in`, `admin-bar`, your Customizer `body_class` values, … |
| `.AdminBar` | HTML | the toolbar for logged-in users (print it last in `<body>`) |
| `.SiteName`, `.Description`, `.HomeURL` | string | `bloginfo()` |
| `.IsFront`, `.TitleIsH1`, `.IsHomeHeader`, `.PostsPageTitle` | | front-page checks for the header markup |
| `.CustomLogo`, `.SiteIcon` | HTML | from Site Identity |
| `.ThemeMods` | map | the theme's Customizer values (`{{index .ThemeMods "accent"}}`) |
| `.CustomCSS` | string | Additional CSS (already printed in `.Head`) |
| `.Theme`, `.ThemeURI` | | the theme (`.Theme.Name`, `.Theme.Version`) and `/wp-content/themes/<slug>` |
| `.Data` | map | free-form data a bundled theme's Go code adds (`.Data.Classic`); empty for packages |

### Content

| Field | Type | Meaning |
|-------|------|---------|
| `.Posts` | []post | the loop: posts of the current page of the query |
| `.Post` | post | the single post or page (nil elsewhere) |
| `.TotalPosts`, `.TotalPages` | int | `found_posts` / `max_num_pages` |
| `.Pagination` | HTML | `the_posts_pagination()` |
| `.PostNav` | HTML | previous/next links on singular views |
| `.ArchiveTitle`, `.ArchiveDesc` | HTML | `get_the_archive_title()` / description |
| `.SearchQuery` | string | `get_search_query()` |
| `.Comments` | comments | nil when comments are not shown; else `.Title`, `.List` (HTML), `.Count`, `.Open`, `.ClosedNotice`, `.Form` (HTML) |
| `.PrivacyLink` | HTML | `the_privacy_policy_link()` |
| `.CanPublish` | bool | the viewer may publish posts |

### Navigation and widgets

| Field | Type | Meaning |
|-------|------|---------|
| `.Menus` | map[location][]item | menu items per `menu_locations` id, nested. Item: `.ID`, `.Label`, `.URL`, `.Target`, `.Classes`, `.Children`, `.Current`, `.CurrentAncestor`, `.Kind`/`.Object`/`.TargetID` (what it links to) |
| `.HasMenus` | bool | any location has a menu |
| `.PrimaryMenu` (= `.FooterPrimary`), `.SocialMenu` (= `.FooterSocial`) | HTML | the core's rendering of the `primary` and `social` locations as `<ul class="menu">` lists (`wp_nav_menu()`), when the theme declares those ids; other locations are rendered from `.Menus` |
| `.Areas` | map[id]HTML | rendered widget areas (`dynamic_sidebar()`), keyed by `widget_areas` id |
| `.Sidebar1`, `.ContentBottom` | HTML | shortcuts for the `sidebar-1` and `content-bottom` areas |

### A post (`.Post` and each of `.Posts`)

| Field | Type | Meaning |
|-------|------|---------|
| `.ID`, `.Type`, `.Format`, `.Sticky` | | |
| `.Title` | HTML | `the_title()` with "Protected:" / "Private:" prefixes |
| `.Permalink`, `.CommentsURL` | string | |
| `.Content` | HTML | `the_content()`: blocks rendered, autop, shortcodes, `the_content` filters; the more-link in archives |
| `.Excerpt`, `.ShowExcerpt` | HTML, bool | `the_excerpt()`, and whether the theme should show it instead of the content (search, or a manual excerpt) |
| `.Class` | string | `post_class()`: `post-12 post type-post status-publish format-standard hentry category-news tag-go …` |
| `.EntryMeta` | HTML | the theme's default byline (date and author links) |
| `.EditLink` | HTML | `edit_post_link()` for users who may edit |
| `.Date`, `.Modified` | time | use `{{date .Date ""}}` |
| `.DateText`, `.ModifiedText`, `.DateISO`, `.ModifiedISO` | string | formatted with the site's date format / ISO 8601 |
| `.AuthorName`, `.AuthorURL`, `.AuthorBio`, `.Biography` | | the author and the author box |
| `.Categories`, `.Tags` | []term | `.ID`, `.Name`, `.Slug`, `.URL` |
| `.CommentCount`, `.CommentsOpen`, `.ShowComments`, `.HasPassword` | | |
| `.Thumbnail`, `.ThumbnailURL`, `.ShowFeatured` | HTML, string, bool | the featured image (`post-thumbnails` support) |
| `.Post` | store post | the raw record: `.Post.Slug`, `.Post.Status`, `.Post.Meta`, `.Post.PublishedAt`, … |

## Functions

| Function | Does |
|----------|------|
| `safe s`, `safeURL s`, `safeAttr s` | mark a string as trusted HTML / URL / attribute |
| `date t format` | format a `time.Time` in the site's time zone with a WordPress format string (`"F j, Y"`); `""` uses the site's date format |
| `dateFormat`, `timeFormat` | the site's formats |
| `setting key`, `settingStr key`, `settingBool key`, `settingInt key`, `settingID key` | read an option (`settingStr "blogname"`, `settingBool "show_avatars"`) |
| `plural n one many` | `"%s post"` / `"%s posts"` with the number inserted |
| `num n` | number with thousands separators |
| `add a b`, `sub a b`, `seq from to` | arithmetic and ranges (`{{range seq 1 .TotalPages}}`) |
| `dict k v …` | build a map to pass several values to a part |
| `list s …`, `split s sep`, `contains s sub`, `lower s`, `ucfirst s`, `repeat s n`, `trimSlash s` | string helpers |
| `texturize s` | escape and apply `wptexturize()` to a plain string |
| `query s` | URL-encode a query value |
| `json v` | encode as JSON inside a `<script>` |
| `version` | the WordPress Go version |

The theme package spec also lists WordPress-named helpers (`wp_head`, `wp_footer`, `body_class`,
`post_class`, `the_content`, `wp_nav_menu`, `dynamic_sidebar`, `get_template_part`) as aliases of
the fields and templates above; prefer the fields, which work in every theme.

## Patterns

A loop with a part per post:

```html
{{define "content"}}
<article id="post-{{.ID}}" class="{{.Class}}">
  <h2 class="entry-title"><a href="{{.Permalink}}" rel="bookmark">{{.Title}}</a></h2>
  {{with .Thumbnail}}<figure class="post-thumbnail">{{.}}</figure>{{end}}
  <div class="entry-meta"><time datetime="{{.DateISO}}">{{.DateText}}</time> by <a href="{{.AuthorURL}}">{{.AuthorName}}</a></div>
  <div class="entry-content">{{.Content}}</div>
  <footer class="entry-footer">{{range .Categories}}<a href="{{.URL}}" rel="category tag">{{.Name}}</a> {{end}}</footer>
</article>
{{end}}
```

```html
{{template "header" .}}
{{if .Posts}}{{range .Posts}}{{template "content" .}}{{end}}{{.Pagination}}{{else}}<p>Nothing found.</p>{{end}}
{{template "footer" .}}
```

A nested menu:

```html
{{define "menu"}}<ul class="menu">{{range .}}<li class="menu-item {{.Classes}}{{if .Current}} current-menu-item{{end}}{{if .Children}} menu-item-has-children{{end}}">
  <a href="{{.URL}}"{{with .Target}} target="{{.}}"{{end}}>{{.Label}}</a>{{with .Children}}<ul class="sub-menu">{{range .}}{{template "menu-item" .}}{{end}}</ul>{{end}}
</li>{{end}}</ul>{{end}}
```

Comments on a single post:

```html
{{with .Comments}}<section id="comments" class="comments-area">
  <h2 class="comments-title">{{.Title}}</h2>
  {{.List}}
  {{if .Open}}{{.Form}}{{else if .ClosedNotice}}<p class="no-comments">Comments are closed.</p>{{end}}
</section>{{end}}
```

The `.Form` and `.List` markup is the WordPress markup (`comment_form()`, `wp_list_comments()`),
so the core's comment styles and `comment-reply.js` apply.
