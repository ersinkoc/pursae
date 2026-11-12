# Devboard

A dense developer Q&A board built for **Pursae Forum**. Compact topic rows with monospace counts,
code blocks that read well in posts, a dark header with forum search and the account menu, and a
stats sidebar beside every forum page. Works without JavaScript, on phones as well as desktops.

## Features

- Declares `supports: {"pursae-forum": true}`: forum pages print their own titles and use the
  theme's `templates/forum.html` wrapper, with the sidebar on by default.
- Sidebar with board stats (`forum_stats`), recently active topics (`forum_recent_topics`) and
  short asking tips.
- Dense, bordered topic and forum lists; badges and counts in JetBrains Mono.
- Code styling for posts: dark blocks with a language label for `language-go`, `language-sql`,
  `language-rust`, `language-css`, `language-yaml`, `language-js`, `language-ts`,
  `language-bash` and `language-python` classes, inline code and keyboard keys.
- Header forum search and account menu (`forum_user_menu`).
- Front page with the latest questions and the same sidebar.
- Self-hosted Inter and JetBrains Mono (SIL Open Font License).

## Customizer

**Devboard**: accent color and forum layout (with the sidebar, or full width).

## Demo content

Home, Posting Guidelines and About pages, primary and footer menus, and a forum section: six
members, three categories with nine forums, and nine questions with answers about Go, PostgreSQL,
CSS, CI caching, auth tokens, Rust lifetimes and porting from Python. Importing it switches
Pursae Forum on. All text is original.

## License

GPL-2.0-or-later. Fonts: SIL Open Font License 1.1.
