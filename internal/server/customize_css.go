package server

// Code generated from the original themes' inc/customizer.php (Twenty Sixteen) and
// inc/color-patterns.php (Twenty Seventeen); {{name}} placeholders are filled by customize_colors.go.

const tt16SchemeCSS = `	/* Color Scheme */

	/* Background Color */
	body {
		background-color: {{background_color}};
	}

	/* Page Background Color */
	.site {
		background-color: {{page_background_color}};
	}

	mark,
	ins,
	button,
	button[disabled]:hover,
	button[disabled]:focus,
	input[type="button"],
	input[type="button"][disabled]:hover,
	input[type="button"][disabled]:focus,
	input[type="reset"],
	input[type="reset"][disabled]:hover,
	input[type="reset"][disabled]:focus,
	input[type="submit"],
	input[type="submit"][disabled]:hover,
	input[type="submit"][disabled]:focus,
	.menu-toggle.toggled-on,
	.menu-toggle.toggled-on:hover,
	.menu-toggle.toggled-on:focus,
	.pagination .prev,
	.pagination .next,
	.pagination .prev:hover,
	.pagination .prev:focus,
	.pagination .next:hover,
	.pagination .next:focus,
	.pagination .nav-links:before,
	.pagination .nav-links:after,
	.widget_calendar tbody a,
	.widget_calendar tbody a:hover,
	.widget_calendar tbody a:focus,
	.page-links a,
	.page-links a:hover,
	.page-links a:focus {
		color: {{page_background_color}};
	}

	/* Link Color */
	.menu-toggle:hover,
	.menu-toggle:focus,
	a,
	.main-navigation a:hover,
	.main-navigation a:focus,
	.dropdown-toggle:hover,
	.dropdown-toggle:focus,
	.social-navigation a:hover:before,
	.social-navigation a:focus:before,
	.post-navigation a:hover .post-title,
	.post-navigation a:focus .post-title,
	.tagcloud a:hover,
	.tagcloud a:focus,
	.site-branding .site-title a:hover,
	.site-branding .site-title a:focus,
	.entry-title a:hover,
	.entry-title a:focus,
	.entry-footer a:hover,
	.entry-footer a:focus,
	.comment-metadata a:hover,
	.comment-metadata a:focus,
	.pingback .comment-edit-link:hover,
	.pingback .comment-edit-link:focus,
	.comment-reply-link,
	.comment-reply-link:hover,
	.comment-reply-link:focus,
	.required,
	.site-info a:hover,
	.site-info a:focus {
		color: {{link_color}};
	}

	mark,
	ins,
	button:hover,
	button:focus,
	input[type="button"]:hover,
	input[type="button"]:focus,
	input[type="reset"]:hover,
	input[type="reset"]:focus,
	input[type="submit"]:hover,
	input[type="submit"]:focus,
	.pagination .prev:hover,
	.pagination .prev:focus,
	.pagination .next:hover,
	.pagination .next:focus,
	.widget_calendar tbody a,
	.page-links a:hover,
	.page-links a:focus {
		background-color: {{link_color}};
	}

	input[type="date"]:focus,
	input[type="time"]:focus,
	input[type="datetime-local"]:focus,
	input[type="week"]:focus,
	input[type="month"]:focus,
	input[type="text"]:focus,
	input[type="email"]:focus,
	input[type="url"]:focus,
	input[type="password"]:focus,
	input[type="search"]:focus,
	input[type="tel"]:focus,
	input[type="number"]:focus,
	textarea:focus,
	.tagcloud a:hover,
	.tagcloud a:focus,
	.menu-toggle:hover,
	.menu-toggle:focus {
		border-color: {{link_color}};
	}

	/* Main Text Color */
	body,
	blockquote cite,
	blockquote small,
	.main-navigation a,
	.menu-toggle,
	.dropdown-toggle,
	.social-navigation a,
	.post-navigation a,
	.pagination a:hover,
	.pagination a:focus,
	.widget-title a,
	.site-branding .site-title a,
	.entry-title a,
	.page-links > .page-links-title,
	.comment-author,
	.comment-reply-title small a:hover,
	.comment-reply-title small a:focus {
		color: {{main_text_color}};
	}

	blockquote,
	.menu-toggle.toggled-on,
	.menu-toggle.toggled-on:hover,
	.menu-toggle.toggled-on:focus,
	.post-navigation,
	.post-navigation div + div,
	.pagination,
	.widget,
	.page-header,
	.page-links a,
	.comments-title,
	.comment-reply-title {
		border-color: {{main_text_color}};
	}

	button,
	button[disabled]:hover,
	button[disabled]:focus,
	input[type="button"],
	input[type="button"][disabled]:hover,
	input[type="button"][disabled]:focus,
	input[type="reset"],
	input[type="reset"][disabled]:hover,
	input[type="reset"][disabled]:focus,
	input[type="submit"],
	input[type="submit"][disabled]:hover,
	input[type="submit"][disabled]:focus,
	.menu-toggle.toggled-on,
	.menu-toggle.toggled-on:hover,
	.menu-toggle.toggled-on:focus,
	.pagination:before,
	.pagination:after,
	.pagination .prev,
	.pagination .next,
	.page-links a {
		background-color: {{main_text_color}};
	}

	/* Secondary Text Color */

	/**
	 * IE8 and earlier will drop any block with CSS3 selectors.
	 * Do not combine these styles with the next block.
	 */
	body:not(.search-results) .entry-summary {
		color: {{secondary_text_color}};
	}

	blockquote,
	.post-password-form label,
	a:hover,
	a:focus,
	a:active,
	.post-navigation .meta-nav,
	.image-navigation,
	.comment-navigation,
	.widget_recent_entries .post-date,
	.widget_rss .rss-date,
	.widget_rss cite,
	.site-description,
	.author-bio,
	.entry-footer,
	.entry-footer a,
	.sticky-post,
	.taxonomy-description,
	.entry-caption,
	.comment-metadata,
	.pingback .edit-link,
	.comment-metadata a,
	.pingback .comment-edit-link,
	.comment-form label,
	.comment-notes,
	.comment-awaiting-moderation,
	.logged-in-as,
	.form-allowed-tags,
	.site-info,
	.site-info a,
	.wp-caption .wp-caption-text,
	.gallery-caption,
	.widecolumn label,
	.widecolumn .mu_register label {
		color: {{secondary_text_color}};
	}

	.widget_calendar tbody a:hover,
	.widget_calendar tbody a:focus {
		background-color: {{secondary_text_color}};
	}

	/* Border Color */
	fieldset,
	pre,
	abbr,
	acronym,
	table,
	th,
	td,
	input[type="date"],
	input[type="time"],
	input[type="datetime-local"],
	input[type="week"],
	input[type="month"],
	input[type="text"],
	input[type="email"],
	input[type="url"],
	input[type="password"],
	input[type="search"],
	input[type="tel"],
	input[type="number"],
	textarea,
	.main-navigation li,
	.main-navigation .primary-menu,
	.menu-toggle,
	.dropdown-toggle:after,
	.social-navigation a,
	.image-navigation,
	.comment-navigation,
	.tagcloud a,
	.entry-content,
	.entry-summary,
	.page-links a,
	.page-links > span,
	.comment-list article,
	.comment-list .pingback,
	.comment-list .trackback,
	.comment-reply-link,
	.no-comments,
	.widecolumn .mu_register .mu_alert {
		border-color: {{border_color}};
	}

	hr,
	code {
		background-color: {{border_color}};
	}

	@media screen and (min-width: 56.875em) {
		.main-navigation li:hover > a,
		.main-navigation li.focus > a {
			color: {{link_color}};
		}

		.main-navigation ul ul,
		.main-navigation ul ul li {
			border-color: {{border_color}};
		}

		.main-navigation ul ul:before {
			border-top-color: {{border_color}};
			border-bottom-color: {{border_color}};
		}

		.main-navigation ul ul li {
			background-color: {{page_background_color}};
		}

		.main-navigation ul ul:after {
			border-top-color: {{page_background_color}};
			border-bottom-color: {{page_background_color}};
		}
	}
`

const tt16PageBackgroundCSS = `
		/* Custom Page Background Color */
		.site {
			background-color: {{1}};
		}

		mark,
		ins,
		button,
		button[disabled]:hover,
		button[disabled]:focus,
		input[type="button"],
		input[type="button"][disabled]:hover,
		input[type="button"][disabled]:focus,
		input[type="reset"],
		input[type="reset"][disabled]:hover,
		input[type="reset"][disabled]:focus,
		input[type="submit"],
		input[type="submit"][disabled]:hover,
		input[type="submit"][disabled]:focus,
		.menu-toggle.toggled-on,
		.menu-toggle.toggled-on:hover,
		.menu-toggle.toggled-on:focus,
		.pagination .prev,
		.pagination .next,
		.pagination .prev:hover,
		.pagination .prev:focus,
		.pagination .next:hover,
		.pagination .next:focus,
		.pagination .nav-links:before,
		.pagination .nav-links:after,
		.widget_calendar tbody a,
		.widget_calendar tbody a:hover,
		.widget_calendar tbody a:focus,
		.page-links a,
		.page-links a:hover,
		.page-links a:focus {
			color: {{1}};
		}

		@media screen and (min-width: 56.875em) {
			.main-navigation ul ul li {
				background-color: {{1}};
			}

			.main-navigation ul ul:after {
				border-top-color: {{1}};
				border-bottom-color: {{1}};
			}
		}
	`

const tt16LinkColorCSS = `
		/* Custom Link Color */
		.menu-toggle:hover,
		.menu-toggle:focus,
		a,
		.main-navigation a:hover,
		.main-navigation a:focus,
		.dropdown-toggle:hover,
		.dropdown-toggle:focus,
		.social-navigation a:hover:before,
		.social-navigation a:focus:before,
		.post-navigation a:hover .post-title,
		.post-navigation a:focus .post-title,
		.tagcloud a:hover,
		.tagcloud a:focus,
		.site-branding .site-title a:hover,
		.site-branding .site-title a:focus,
		.entry-title a:hover,
		.entry-title a:focus,
		.entry-footer a:hover,
		.entry-footer a:focus,
		.comment-metadata a:hover,
		.comment-metadata a:focus,
		.pingback .comment-edit-link:hover,
		.pingback .comment-edit-link:focus,
		.comment-reply-link,
		.comment-reply-link:hover,
		.comment-reply-link:focus,
		.required,
		.site-info a:hover,
		.site-info a:focus {
			color: {{1}};
		}

		mark,
		ins,
		button:hover,
		button:focus,
		input[type="button"]:hover,
		input[type="button"]:focus,
		input[type="reset"]:hover,
		input[type="reset"]:focus,
		input[type="submit"]:hover,
		input[type="submit"]:focus,
		.pagination .prev:hover,
		.pagination .prev:focus,
		.pagination .next:hover,
		.pagination .next:focus,
		.widget_calendar tbody a,
		.page-links a:hover,
		.page-links a:focus {
			background-color: {{1}};
		}

		input[type="date"]:focus,
		input[type="time"]:focus,
		input[type="datetime-local"]:focus,
		input[type="week"]:focus,
		input[type="month"]:focus,
		input[type="text"]:focus,
		input[type="email"]:focus,
		input[type="url"]:focus,
		input[type="password"]:focus,
		input[type="search"]:focus,
		input[type="tel"]:focus,
		input[type="number"]:focus,
		textarea:focus,
		.tagcloud a:hover,
		.tagcloud a:focus,
		.menu-toggle:hover,
		.menu-toggle:focus {
			border-color: {{1}};
		}

		@media screen and (min-width: 56.875em) {
			.main-navigation li:hover > a,
			.main-navigation li.focus > a {
				color: {{1}};
			}
		}
	`

const tt16MainTextCSS = `
		/* Custom Main Text Color */
		body,
		blockquote cite,
		blockquote small,
		.main-navigation a,
		.menu-toggle,
		.dropdown-toggle,
		.social-navigation a,
		.post-navigation a,
		.pagination a:hover,
		.pagination a:focus,
		.widget-title a,
		.site-branding .site-title a,
		.entry-title a,
		.page-links > .page-links-title,
		.comment-author,
		.comment-reply-title small a:hover,
		.comment-reply-title small a:focus {
			color: {{1}};
		}

		blockquote,
		.menu-toggle.toggled-on,
		.menu-toggle.toggled-on:hover,
		.menu-toggle.toggled-on:focus,
		.post-navigation,
		.post-navigation div + div,
		.pagination,
		.widget,
		.page-header,
		.page-links a,
		.comments-title,
		.comment-reply-title {
			border-color: {{1}};
		}

		button,
		button[disabled]:hover,
		button[disabled]:focus,
		input[type="button"],
		input[type="button"][disabled]:hover,
		input[type="button"][disabled]:focus,
		input[type="reset"],
		input[type="reset"][disabled]:hover,
		input[type="reset"][disabled]:focus,
		input[type="submit"],
		input[type="submit"][disabled]:hover,
		input[type="submit"][disabled]:focus,
		.menu-toggle.toggled-on,
		.menu-toggle.toggled-on:hover,
		.menu-toggle.toggled-on:focus,
		.pagination:before,
		.pagination:after,
		.pagination .prev,
		.pagination .next,
		.page-links a {
			background-color: {{1}};
		}

		/* Border Color */
		fieldset,
		pre,
		abbr,
		acronym,
		table,
		th,
		td,
		input[type="date"],
		input[type="time"],
		input[type="datetime-local"],
		input[type="week"],
		input[type="month"],
		input[type="text"],
		input[type="email"],
		input[type="url"],
		input[type="password"],
		input[type="search"],
		input[type="tel"],
		input[type="number"],
		textarea,
		.main-navigation li,
		.main-navigation .primary-menu,
		.menu-toggle,
		.dropdown-toggle:after,
		.social-navigation a,
		.image-navigation,
		.comment-navigation,
		.tagcloud a,
		.entry-content,
		.entry-summary,
		.page-links a,
		.page-links > span,
		.comment-list article,
		.comment-list .pingback,
		.comment-list .trackback,
		.comment-reply-link,
		.no-comments,
		.widecolumn .mu_register .mu_alert {
			border-color: {{2}};
		}

		hr,
		code {
			background-color: {{2}};
		}

		@media screen and (min-width: 56.875em) {
			.main-navigation ul ul,
			.main-navigation ul ul li {
				border-color: {{2}};
			}

			.main-navigation ul ul:before {
				border-top-color: {{2}};
				border-bottom-color: {{2}};
			}
		}
	`

const tt16SecondaryTextCSS = `
		/* Custom Secondary Text Color */

		/**
		 * IE8 and earlier will drop any block with CSS3 selectors.
		 * Do not combine these styles with the next block.
		 */
		body:not(.search-results) .entry-summary {
			color: {{1}};
		}

		blockquote,
		.post-password-form label,
		a:hover,
		a:focus,
		a:active,
		.post-navigation .meta-nav,
		.image-navigation,
		.comment-navigation,
		.widget_recent_entries .post-date,
		.widget_rss .rss-date,
		.widget_rss cite,
		.site-description,
		.author-bio,
		.entry-footer,
		.entry-footer a,
		.sticky-post,
		.taxonomy-description,
		.entry-caption,
		.comment-metadata,
		.pingback .edit-link,
		.comment-metadata a,
		.pingback .comment-edit-link,
		.comment-form label,
		.comment-notes,
		.comment-awaiting-moderation,
		.logged-in-as,
		.form-allowed-tags,
		.site-info,
		.site-info a,
		.wp-caption .wp-caption-text,
		.gallery-caption,
		.widecolumn label,
		.widecolumn .mu_register label {
			color: {{1}};
		}

		.widget_calendar tbody a:hover,
		.widget_calendar tbody a:focus {
			background-color: {{1}};
		}
	`

const tt17CustomColorsCSS = `
/**
 * Twenty Seventeen: Color Patterns
 *
 * Colors are ordered from dark to light.
 */

.colors-custom a:hover,
.colors-custom a:active,
.colors-custom .entry-content a:focus,
.colors-custom .entry-content a:hover,
.colors-custom .entry-summary a:focus,
.colors-custom .entry-summary a:hover,
.colors-custom .comment-content a:focus,
.colors-custom .comment-content a:hover,
.colors-custom .widget a:focus,
.colors-custom .widget a:hover,
.colors-custom .site-footer .widget-area a:focus,
.colors-custom .site-footer .widget-area a:hover,
.colors-custom .posts-navigation a:focus,
.colors-custom .posts-navigation a:hover,
.colors-custom .comment-metadata a:focus,
.colors-custom .comment-metadata a:hover,
.colors-custom .comment-metadata a.comment-edit-link:focus,
.colors-custom .comment-metadata a.comment-edit-link:hover,
.colors-custom .comment-reply-link:focus,
.colors-custom .comment-reply-link:hover,
.colors-custom .widget_authors a:focus strong,
.colors-custom .widget_authors a:hover strong,
.colors-custom .entry-title a:focus,
.colors-custom .entry-title a:hover,
.colors-custom .entry-meta a:focus,
.colors-custom .entry-meta a:hover,
.colors-custom.blog .entry-meta a.post-edit-link:focus,
.colors-custom.blog .entry-meta a.post-edit-link:hover,
.colors-custom.archive .entry-meta a.post-edit-link:focus,
.colors-custom.archive .entry-meta a.post-edit-link:hover,
.colors-custom.search .entry-meta a.post-edit-link:focus,
.colors-custom.search .entry-meta a.post-edit-link:hover,
.colors-custom .page-links a:focus .page-number,
.colors-custom .page-links a:hover .page-number,
.colors-custom .entry-footer a:focus,
.colors-custom .entry-footer a:hover,
.colors-custom .entry-footer .cat-links a:focus,
.colors-custom .entry-footer .cat-links a:hover,
.colors-custom .entry-footer .tags-links a:focus,
.colors-custom .entry-footer .tags-links a:hover,
.colors-custom .post-navigation a:focus,
.colors-custom .post-navigation a:hover,
.colors-custom .pagination a:not(.prev):not(.next):focus,
.colors-custom .pagination a:not(.prev):not(.next):hover,
.colors-custom .comments-pagination a:not(.prev):not(.next):focus,
.colors-custom .comments-pagination a:not(.prev):not(.next):hover,
.colors-custom .logged-in-as a:focus,
.colors-custom .logged-in-as a:hover,
.colors-custom a:focus .nav-title,
.colors-custom a:hover .nav-title,
.colors-custom .edit-link a:focus,
.colors-custom .edit-link a:hover,
.colors-custom .site-info a:focus,
.colors-custom .site-info a:hover,
.colors-custom .widget .widget-title a:focus,
.colors-custom .widget .widget-title a:hover,
.colors-custom .widget ul li a:focus,
.colors-custom .widget ul li a:hover {
	color: hsl( {{hue}}, {{sat}}, 0% ); /* base: #000; */
}

.colors-custom .entry-content a,
.colors-custom .entry-summary a,
.colors-custom .comment-content a,
.colors-custom .widget a,
.colors-custom .site-footer .widget-area a,
.colors-custom .posts-navigation a,
.colors-custom .widget_authors a strong {
	-webkit-box-shadow: inset 0 -1px 0 hsl( {{hue}}, {{sat}}, 6% ); /* base: rgba(15, 15, 15, 1); */
	box-shadow: inset 0 -1px 0 hsl( {{hue}}, {{sat}}, 6% ); /* base: rgba(15, 15, 15, 1); */
}

.colors-custom button,
.colors-custom input[type="button"],
.colors-custom input[type="submit"],
.colors-custom .entry-footer .edit-link a.post-edit-link {
	background-color: hsl( {{hue}}, {{sat}}, 13% ); /* base: #222; */
}

.colors-custom input[type="text"]:focus,
.colors-custom input[type="email"]:focus,
.colors-custom input[type="url"]:focus,
.colors-custom input[type="password"]:focus,
.colors-custom input[type="search"]:focus,
.colors-custom input[type="number"]:focus,
.colors-custom input[type="tel"]:focus,
.colors-custom input[type="range"]:focus,
.colors-custom input[type="date"]:focus,
.colors-custom input[type="month"]:focus,
.colors-custom input[type="week"]:focus,
.colors-custom input[type="time"]:focus,
.colors-custom input[type="datetime"]:focus,
.colors-custom .colors-custom input[type="datetime-local"]:focus,
.colors-custom input[type="color"]:focus,
.colors-custom textarea:focus,
.colors-custom button.secondary,
.colors-custom input[type="reset"],
.colors-custom input[type="button"].secondary,
.colors-custom input[type="reset"].secondary,
.colors-custom input[type="submit"].secondary,
.colors-custom a,
.colors-custom .site-title,
.colors-custom .site-title a,
.colors-custom .navigation-top a,
.colors-custom .dropdown-toggle,
.colors-custom .menu-toggle,
.colors-custom .page .panel-content .entry-title,
.colors-custom .page-title,
.colors-custom.page:not(.twentyseventeen-front-page) .entry-title,
.colors-custom .page-links a .page-number,
.colors-custom .comment-metadata a.comment-edit-link,
.colors-custom .comment-reply-link .icon,
.colors-custom h2.widget-title,
.colors-custom mark,
.colors-custom .post-navigation a:focus .icon,
.colors-custom .post-navigation a:hover .icon,
.colors-custom .site-content .site-content-light,
.colors-custom .twentyseventeen-panel .recent-posts .entry-header .edit-link {
	color: hsl( {{hue}}, {{sat}}, 13% ); /* base: #222; */
}

.colors-custom .entry-content a:focus,
.colors-custom .entry-content a:hover,
.colors-custom .entry-summary a:focus,
.colors-custom .entry-summary a:hover,
.colors-custom .comment-content a:focus,
.colors-custom .comment-content a:hover,
.colors-custom .widget a:focus,
.colors-custom .widget a:hover,
.colors-custom .site-footer .widget-area a:focus,
.colors-custom .site-footer .widget-area a:hover,
.colors-custom .posts-navigation a:focus,
.colors-custom .posts-navigation a:hover,
.colors-custom .comment-metadata a:focus,
.colors-custom .comment-metadata a:hover,
.colors-custom .comment-metadata a.comment-edit-link:focus,
.colors-custom .comment-metadata a.comment-edit-link:hover,
.colors-custom .comment-reply-link:focus,
.colors-custom .comment-reply-link:hover,
.colors-custom .widget_authors a:focus strong,
.colors-custom .widget_authors a:hover strong,
.colors-custom .entry-title a:focus,
.colors-custom .entry-title a:hover,
.colors-custom .entry-meta a:focus,
.colors-custom .entry-meta a:hover,
.colors-custom.blog .entry-meta a.post-edit-link:focus,
.colors-custom.blog .entry-meta a.post-edit-link:hover,
.colors-custom.archive .entry-meta a.post-edit-link:focus,
.colors-custom.archive .entry-meta a.post-edit-link:hover,
.colors-custom.search .entry-meta a.post-edit-link:focus,
.colors-custom.search .entry-meta a.post-edit-link:hover,
.colors-custom .page-links a:focus .page-number,
.colors-custom .page-links a:hover .page-number,
.colors-custom .entry-footer .cat-links a:focus,
.colors-custom .entry-footer .cat-links a:hover,
.colors-custom .entry-footer .tags-links a:focus,
.colors-custom .entry-footer .tags-links a:hover,
.colors-custom .post-navigation a:focus,
.colors-custom .post-navigation a:hover,
.colors-custom .pagination a:not(.prev):not(.next):focus,
.colors-custom .pagination a:not(.prev):not(.next):hover,
.colors-custom .comments-pagination a:not(.prev):not(.next):focus,
.colors-custom .comments-pagination a:not(.prev):not(.next):hover,
.colors-custom .logged-in-as a:focus,
.colors-custom .logged-in-as a:hover,
.colors-custom a:focus .nav-title,
.colors-custom a:hover .nav-title,
.colors-custom .edit-link a:focus,
.colors-custom .edit-link a:hover,
.colors-custom .site-info a:focus,
.colors-custom .site-info a:hover,
.colors-custom .widget .widget-title a:focus,
.colors-custom .widget .widget-title a:hover,
.colors-custom .widget ul li a:focus,
.colors-custom .widget ul li a:hover {
	-webkit-box-shadow: inset 0 0 0 hsl( {{hue}}, {{sat}}, 13% ), 0 3px 0 hsl( {{hue}}, {{sat}}, 13% );
	box-shadow: inset 0 0 0 hsl( {{hue}}, {{sat}} , 13% ), 0 3px 0 hsl( {{hue}}, {{sat}}, 13% );
}

body.colors-custom,
.colors-custom button,
.colors-custom input,
.colors-custom select,
.colors-custom textarea,
.colors-custom h3,
.colors-custom h4,
.colors-custom h6,
.colors-custom label,
.colors-custom .entry-title a,
.colors-custom.twentyseventeen-front-page .panel-content .recent-posts article,
.colors-custom .entry-footer .cat-links a,
.colors-custom .entry-footer .tags-links a,
.colors-custom .format-quote blockquote,
.colors-custom .nav-title,
.colors-custom .comment-body,
.colors-custom .site-content .wp-playlist-light .wp-playlist-current-item .wp-playlist-item-album {
	color: hsl( {{hue}}, {{rsat}}, 20% ); /* base: #333; */
}

.colors-custom .social-navigation a:hover,
.colors-custom .social-navigation a:focus {
	background: hsl( {{hue}}, {{rsat}}, 20% ); /* base: #333; */
}

.colors-custom input[type="text"]:focus,
.colors-custom input[type="email"]:focus,
.colors-custom input[type="url"]:focus,
.colors-custom input[type="password"]:focus,
.colors-custom input[type="search"]:focus,
.colors-custom input[type="number"]:focus,
.colors-custom input[type="tel"]:focus,
.colors-custom input[type="range"]:focus,
.colors-custom input[type="date"]:focus,
.colors-custom input[type="month"]:focus,
.colors-custom input[type="week"]:focus,
.colors-custom input[type="time"]:focus,
.colors-custom input[type="datetime"]:focus,
.colors-custom input[type="datetime-local"]:focus,
.colors-custom input[type="color"]:focus,
.colors-custom textarea:focus,
.bypostauthor > .comment-body > .comment-meta > .comment-author .avatar {
	border-color: hsl( {{hue}}, {{rsat}}, 20% ); /* base: #333; */
}

.colors-custom h2,
.colors-custom blockquote,
.colors-custom input[type="text"],
.colors-custom input[type="email"],
.colors-custom input[type="url"],
.colors-custom input[type="password"],
.colors-custom input[type="search"],
.colors-custom input[type="number"],
.colors-custom input[type="tel"],
.colors-custom input[type="range"],
.colors-custom input[type="date"],
.colors-custom input[type="month"],
.colors-custom input[type="week"],
.colors-custom input[type="time"],
.colors-custom input[type="datetime"],
.colors-custom input[type="datetime-local"],
.colors-custom input[type="color"],
.colors-custom textarea,
.colors-custom .site-description,
.colors-custom .entry-content blockquote.alignleft,
.colors-custom .entry-content blockquote.alignright,
.colors-custom .colors-custom .taxonomy-description,
.colors-custom .site-info a,
.colors-custom .wp-caption,
.colors-custom .gallery-caption {
	color: hsl( {{hue}}, {{sat}}, 40% ); /* base: #666; */
}

.colors-custom abbr,
.colors-custom acronym {
	border-bottom-color: hsl( {{hue}}, {{sat}}, 40% ); /* base: #666; */
}

.colors-custom h5,
.colors-custom .entry-meta,
.colors-custom .entry-meta a,
.colors-custom.blog .entry-meta a.post-edit-link,
.colors-custom.archive .entry-meta a.post-edit-link,
.colors-custom.search .entry-meta a.post-edit-link,
.colors-custom .nav-subtitle,
.colors-custom .comment-metadata,
.colors-custom .comment-metadata a,
.colors-custom .no-comments,
.colors-custom .comment-awaiting-moderation,
.colors-custom .page-numbers.current,
.colors-custom .page-links .page-number,
.colors-custom .navigation-top .current-menu-item > a,
.colors-custom .navigation-top .current_page_item > a,
.colors-custom .main-navigation a:hover,
.colors-custom .site-content .wp-playlist-light .wp-playlist-current-item .wp-playlist-item-artist {
	color: hsl( {{hue}}, {{sat}}, 46% ); /* base: #767676; */
}

.colors-custom :not( .mejs-button ) > button:hover,
.colors-custom :not( .mejs-button ) > button:focus,
.colors-custom input[type="button"]:hover,
.colors-custom input[type="button"]:focus,
.colors-custom input[type="submit"]:hover,
.colors-custom input[type="submit"]:focus,
.colors-custom .entry-footer .edit-link a.post-edit-link:hover,
.colors-custom .entry-footer .edit-link a.post-edit-link:focus,
.colors-custom .social-navigation a,
.colors-custom .prev.page-numbers:focus,
.colors-custom .prev.page-numbers:hover,
.colors-custom .next.page-numbers:focus,
.colors-custom .next.page-numbers:hover,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:hover,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:focus {
	background: hsl( {{hue}}, {{sat}}, 46% ); /* base: #767676; */
}

.colors-custom button.secondary:hover,
.colors-custom button.secondary:focus,
.colors-custom input[type="reset"]:hover,
.colors-custom input[type="reset"]:focus,
.colors-custom input[type="button"].secondary:hover,
.colors-custom input[type="button"].secondary:focus,
.colors-custom input[type="reset"].secondary:hover,
.colors-custom input[type="reset"].secondary:focus,
.colors-custom input[type="submit"].secondary:hover,
.colors-custom input[type="submit"].secondary:focus,
.colors-custom hr {
	background: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
}

.colors-custom input[type="text"],
.colors-custom input[type="email"],
.colors-custom input[type="url"],
.colors-custom input[type="password"],
.colors-custom input[type="search"],
.colors-custom input[type="number"],
.colors-custom input[type="tel"],
.colors-custom input[type="range"],
.colors-custom input[type="date"],
.colors-custom input[type="month"],
.colors-custom input[type="week"],
.colors-custom input[type="time"],
.colors-custom input[type="datetime"],
.colors-custom input[type="datetime-local"],
.colors-custom input[type="color"],
.colors-custom textarea,
.colors-custom select,
.colors-custom fieldset,
.colors-custom .widget .tagcloud a:hover,
.colors-custom .widget .tagcloud a:focus,
.colors-custom .widget.widget_tag_cloud a:hover,
.colors-custom .widget.widget_tag_cloud a:focus,
.colors-custom .wp_widget_tag_cloud a:hover,
.colors-custom .wp_widget_tag_cloud a:focus {
	border-color: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
}

.colors-custom thead th {
	border-bottom-color: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
}

.colors-custom .entry-footer .cat-links .icon,
.colors-custom .entry-footer .tags-links .icon {
	color: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
}

.colors-custom button.secondary,
.colors-custom input[type="reset"],
.colors-custom input[type="button"].secondary,
.colors-custom input[type="reset"].secondary,
.colors-custom input[type="submit"].secondary,
.colors-custom .prev.page-numbers,
.colors-custom .next.page-numbers {
	background-color: hsl( {{hue}}, {{sat}}, 87% ); /* base: #ddd; */
}

.colors-custom .widget .tagcloud a,
.colors-custom .widget.widget_tag_cloud a,
.colors-custom .wp_widget_tag_cloud a {
	border-color: hsl( {{hue}}, {{sat}}, 87% ); /* base: #ddd; */
}

.colors-custom.twentyseventeen-front-page article:not(.has-post-thumbnail):not(:first-child),
.colors-custom .widget ul li {
	border-top-color: hsl( {{hue}}, {{sat}}, 87% ); /* base: #ddd; */
}

.colors-custom .widget ul li {
	border-bottom-color: hsl( {{hue}}, {{sat}}, 87% ); /* base: #ddd; */
}

.colors-custom pre,
.colors-custom mark,
.colors-custom ins {
	background: hsl( {{hue}}, {{sat}}, 93% ); /* base: #eee; */
}

.colors-custom .navigation-top,
.colors-custom .main-navigation > div > ul,
.colors-custom .pagination,
.colors-custom .comments-pagination,
.colors-custom .entry-footer,
.colors-custom .site-footer {
	border-top-color: hsl( {{hue}}, {{sat}}, 93% ); /* base: #eee; */
}

.colors-custom .navigation-top,
.colors-custom .main-navigation li,
.colors-custom .entry-footer,
.colors-custom .single-featured-image-header,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item,
.colors-custom tr {
	border-bottom-color: hsl( {{hue}}, {{sat}}, 93% ); /* base: #eee; */
}

.colors-custom .site-content .wp-playlist-light {
	border-color: hsl( {{hue}}, {{sat}}, 93% ); /* base: #eee; */
}

.colors-custom .site-header,
.colors-custom .single-featured-image-header {
	background-color: hsl( {{hue}}, {{sat}}, 98% ); /* base: #fafafa; */
}

.colors-custom button,
.colors-custom input[type="button"],
.colors-custom input[type="submit"],
.colors-custom .entry-footer .edit-link a.post-edit-link,
.colors-custom .social-navigation a,
.colors-custom .site-content .wp-playlist-light a.wp-playlist-caption:hover,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:hover a,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:focus a,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:hover,
.colors-custom .site-content .wp-playlist-light .wp-playlist-item:focus,
.colors-custom .prev.page-numbers:focus,
.colors-custom .prev.page-numbers:hover,
.colors-custom .next.page-numbers:focus,
.colors-custom .next.page-numbers:hover,
.colors-custom.has-header-image .site-title,
.colors-custom.has-header-video .site-title,
.colors-custom.has-header-image .site-title a,
.colors-custom.has-header-video .site-title a,
.colors-custom.has-header-image .site-description,
.colors-custom.has-header-video .site-description {
	color: hsl( {{hue}}, {{sat}}, 100% ); /* base: #fff; */
}

body.colors-custom,
.colors-custom .navigation-top,
.colors-custom .main-navigation ul {
	background: hsl( {{hue}}, {{sat}}, 100% ); /* base: #fff; */
}

.colors-custom .widget ul li a,
.colors-custom .site-footer .widget-area ul li a {
	-webkit-box-shadow: inset 0 -1px 0 hsl( {{hue}}, {{sat}}, 100% ); /* base: rgba(255, 255, 255, 1); */
	box-shadow: inset 0 -1px 0 hsl( {{hue}}, {{sat}}, 100% );  /* base: rgba(255, 255, 255, 1); */
}

.colors-custom .menu-toggle,
.colors-custom .menu-toggle:hover,
.colors-custom .menu-toggle:focus,
.colors-custom .menu .dropdown-toggle,
.colors-custom .menu-scroll-down,
.colors-custom .menu-scroll-down:hover,
.colors-custom .menu-scroll-down:focus {
	background-color: transparent;
}

.colors-custom .widget .tagcloud a,
.colors-custom .widget .tagcloud a:focus,
.colors-custom .widget .tagcloud a:hover,
.colors-custom .widget.widget_tag_cloud a,
.colors-custom .widget.widget_tag_cloud a:focus,
.colors-custom .widget.widget_tag_cloud a:hover,
.colors-custom .wp_widget_tag_cloud a,
.colors-custom .wp_widget_tag_cloud a:focus,
.colors-custom .wp_widget_tag_cloud a:hover,
.colors-custom .entry-footer .edit-link a.post-edit-link:focus,
.colors-custom .entry-footer .edit-link a.post-edit-link:hover {
	-webkit-box-shadow: none !important;
	box-shadow: none !important;
}

/* Reset non-customizable hover styling for links */
.colors-custom .entry-content a:hover,
.colors-custom .entry-content a:focus,
.colors-custom .entry-summary a:hover,
.colors-custom .entry-summary a:focus,
.colors-custom .comment-content a:focus,
.colors-custom .comment-content a:hover,
.colors-custom .widget a:hover,
.colors-custom .widget a:focus,
.colors-custom .site-footer .widget-area a:hover,
.colors-custom .site-footer .widget-area a:focus,
.colors-custom .posts-navigation a:hover,
.colors-custom .posts-navigation a:focus,
.colors-custom .widget_authors a:hover strong,
.colors-custom .widget_authors a:focus strong {
	-webkit-box-shadow: inset 0 0 0 rgba(0, 0, 0, 0), 0 3px 0 rgba(0, 0, 0, 1);
	box-shadow: inset 0 0 0 rgba(0, 0, 0, 0), 0 3px 0 rgba(0, 0, 0, 1);
}

.colors-custom .gallery-item a,
.colors-custom .gallery-item a:hover,
.colors-custom .gallery-item a:focus {
	-webkit-box-shadow: none;
	box-shadow: none;
}

@media screen and (min-width: 48em) {

	.colors-custom .nav-links .nav-previous .nav-title .icon,
	.colors-custom .nav-links .nav-next .nav-title .icon {
		color: hsl( {{hue}}, {{sat}}, 20% ); /* base: #222; */
	}

	.colors-custom .main-navigation li li:hover,
	.colors-custom .main-navigation li li.focus {
		background: hsl( {{hue}}, {{sat}}, 46% ); /* base: #767676; */
	}

	.colors-custom .navigation-top .menu-scroll-down {
		color: hsl( {{hue}}, {{sat}}, 46% ); /* base: #767676; */;
	}

	.colors-custom abbr[title] {
		border-bottom-color: hsl( {{hue}}, {{sat}}, 46% ); /* base: #767676; */;
	}

	.colors-custom .main-navigation ul ul {
		border-color: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
		background: hsl( {{hue}}, {{sat}}, 100% ); /* base: #fff; */
	}

	.colors-custom .main-navigation ul li.menu-item-has-children:before,
	.colors-custom .main-navigation ul li.page_item_has_children:before {
		border-bottom-color: hsl( {{hue}}, {{sat}}, 73% ); /* base: #bbb; */
	}

	.colors-custom .main-navigation ul li.menu-item-has-children:after,
	.colors-custom .main-navigation ul li.page_item_has_children:after {
		border-bottom-color: hsl( {{hue}}, {{sat}}, 100% ); /* base: #fff; */
	}

	.colors-custom .main-navigation li li.focus > a,
	.colors-custom .main-navigation li li:focus > a,
	.colors-custom .main-navigation li li:hover > a,
	.colors-custom .main-navigation li li a:hover,
	.colors-custom .main-navigation li li a:focus,
	.colors-custom .main-navigation li li.current_page_item a:hover,
	.colors-custom .main-navigation li li.current-menu-item a:hover,
	.colors-custom .main-navigation li li.current_page_item a:focus,
	.colors-custom .main-navigation li li.current-menu-item a:focus {
		color: hsl( {{hue}}, {{sat}}, 100% ); /* base: #fff; */
	}
}`

// tt20Elements is twentytwenty_get_elements_array(), generated from the theme's functions.php.
var tt20Elements = []tt20Element{
	{"content", "accent", "color", ".color-accent,.color-accent-hover:hover,.color-accent-hover:focus,:root .has-accent-color,.has-drop-cap:not(:focus):first-letter,.wp-block-button.is-style-outline,a"},
	{"content", "accent", "border-color", "blockquote,.border-color-accent,.border-color-accent-hover:hover,.border-color-accent-hover:focus"},
	{"content", "accent", "background-color", "button,.button,.faux-button,.wp-block-button__link,.wp-block-file .wp-block-file__button,input[type=\"button\"],input[type=\"reset\"],input[type=\"submit\"],.bg-accent,.bg-accent-hover:hover,.bg-accent-hover:focus,:root .has-accent-background-color,.comment-reply-link"},
	{"content", "accent", "fill", ".fill-children-accent,.fill-children-accent *"},
	{"content", "background", "color", ":root .has-background-color,button,.button,.faux-button,.wp-block-button__link,.wp-block-file__button,input[type=\"button\"],input[type=\"reset\"],input[type=\"submit\"],.wp-block-button,.comment-reply-link,.has-background.has-primary-background-color:not(.has-text-color),.has-background.has-primary-background-color *:not(.has-text-color),.has-background.has-accent-background-color:not(.has-text-color),.has-background.has-accent-background-color *:not(.has-text-color)"},
	{"content", "background", "background-color", ":root .has-background-background-color"},
	{"content", "text", "color", "body,.entry-title a,:root .has-primary-color"},
	{"content", "text", "background-color", ":root .has-primary-background-color"},
	{"content", "secondary", "color", "cite,figcaption,.wp-caption-text,.post-meta,.entry-content .wp-block-archives li,.entry-content .wp-block-categories li,.entry-content .wp-block-latest-posts li,.wp-block-latest-comments__comment-date,.wp-block-latest-posts__post-date,.wp-block-embed figcaption,.wp-block-image figcaption,.wp-block-pullquote cite,.comment-metadata,.comment-respond .comment-notes,.comment-respond .logged-in-as,.pagination .dots,.entry-content hr:not(.has-background),hr.styled-separator,:root .has-secondary-color"},
	{"content", "secondary", "background-color", ":root .has-secondary-background-color"},
	{"content", "borders", "border-color", "pre,fieldset,input,textarea,table,table *,hr"},
	{"content", "borders", "background-color", "caption,code,code,kbd,samp,.wp-block-table.is-style-stripes tbody tr:nth-child(odd),:root .has-subtle-background-background-color"},
	{"content", "borders", "border-bottom-color", ".wp-block-table.is-style-stripes"},
	{"content", "borders", "border-top-color", ".wp-block-latest-posts.is-grid li"},
	{"content", "borders", "color", ":root .has-subtle-background-color"},
	{"header-footer", "accent", "color", "body:not(.overlay-header) .primary-menu > li > a,body:not(.overlay-header) .primary-menu > li > .icon,.modal-menu a,.footer-menu a, .footer-widgets a:where(:not(.wp-block-button__link)),#site-footer .wp-block-button.is-style-outline,.wp-block-pullquote:before,.singular:not(.overlay-header) .entry-header a,.archive-header a,.header-footer-group .color-accent,.header-footer-group .color-accent-hover:hover"},
	{"header-footer", "accent", "background-color", ".social-icons a,#site-footer button:not(.toggle),#site-footer .button,#site-footer .faux-button,#site-footer .wp-block-button__link,#site-footer .wp-block-file__button,#site-footer input[type=\"button\"],#site-footer input[type=\"reset\"],#site-footer input[type=\"submit\"]"},
	{"header-footer", "background", "color", ".social-icons a,body:not(.overlay-header) .primary-menu ul,.header-footer-group button,.header-footer-group .button,.header-footer-group .faux-button,.header-footer-group .wp-block-button:not(.is-style-outline) .wp-block-button__link,.header-footer-group .wp-block-file__button,.header-footer-group input[type=\"button\"],.header-footer-group input[type=\"reset\"],.header-footer-group input[type=\"submit\"]"},
	{"header-footer", "background", "background-color", "#site-header,.footer-nav-widgets-wrapper,#site-footer,.menu-modal,.menu-modal-inner,.search-modal-inner,.archive-header,.singular .entry-header,.singular .featured-media:before,.wp-block-pullquote:before"},
	{"header-footer", "text", "color", ".header-footer-group,body:not(.overlay-header) #site-header .toggle,.menu-modal .toggle"},
	{"header-footer", "text", "background-color", "body:not(.overlay-header) .primary-menu ul"},
	{"header-footer", "text", "border-bottom-color", "body:not(.overlay-header) .primary-menu > li > ul:after"},
	{"header-footer", "text", "border-left-color", "body:not(.overlay-header) .primary-menu ul ul:after"},
	{"header-footer", "secondary", "color", ".site-description,body:not(.overlay-header) .toggle-inner .toggle-text,.widget .post-date,.widget .rss-date,.widget_archive li,.widget_categories li,.widget cite,.widget_pages li,.widget_meta li,.widget_nav_menu li,.powered-by-wordpress,.footer-credits .privacy-policy,.to-the-top,.singular .entry-header .post-meta,.singular:not(.overlay-header) .entry-header .post-meta a"},
	{"header-footer", "borders", "border-color", ".header-footer-group pre,.header-footer-group fieldset,.header-footer-group input,.header-footer-group textarea,.header-footer-group table,.header-footer-group table *,.footer-nav-widgets-wrapper,#site-footer,.menu-modal nav *,.footer-widgets-outer-wrapper,.footer-top"},
	{"header-footer", "borders", "background-color", ".header-footer-group table caption,body:not(.overlay-header) .header-inner .toggle-wrapper::before"},
}
