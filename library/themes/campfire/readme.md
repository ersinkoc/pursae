# Campfire

A warm community board for makers and hobbyists, built for **Pursae Forum**. Ember and moss on
warm paper, a friendly serif for headings and a clean sans for reading. Everything works without
JavaScript, on phones as well as desktops.

## Features

- Declares `supports: {"pursae-forum": true}`: forum pages print their own titles and use the
  theme's `templates/forum.html` wrapper.
- Overrides the forum home (`templates/forum/index.html`) with **category cards** (each shows its
  forums, topic and reply counts and the latest topic) followed by the latest topics.
- Gives every Pursae Forum view its look through the `--pf-*` tokens, with light `.pf-*`
  refinements (serif names, pill buttons, warm code blocks); the layout stays Pursae Forum's.
- Header account menu (`forum_user_menu`): Log in and Join for visitors, avatar and name for
  members.
- Front page with the latest topics (`forum_recent_topics`) and member counts (`forum_stats`).
- Optional community sidebar on forum pages.
- Self-hosted Fraunces and Albert Sans (SIL Open Font License).

## Customizer

**Campfire**: accent color (darkened automatically where text needs contrast) and forum layout
(full width or with the community sidebar).

## Demo content

Home, Guidelines and About pages, primary and footer menus, and a forum section: six members,
three categories with eight forums, and nine topics with replies about woodworking, welding,
radio repair, ceramics and leathercraft. Importing it switches Pursae Forum on. All text is
original.

## License

GPL-2.0-or-later. Fonts: SIL Open Font License 1.1.
