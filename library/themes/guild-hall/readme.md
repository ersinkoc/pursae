# Guild Hall

A dark community hub for gaming clans and player communities, built for **Pursae Forum**: a sticky
header with the forum's Log in and Join links, a neon hero, channel tiles, a live panel of recently
active topics and forum stats, and forum views in lime and magenta on deep violet.

**Two ways to use it.**

- *As a theme.* Activate Guild Hall: the header, footer, pages and the forum wrapper
  (`templates/forum.html`, with a sidebar of forum stats and recent topics) come from the theme's
  own templates. The theme declares `supports: {"pursae-forum": true}`, so forum views print their own
  title, breadcrumbs and actions. Customizer › Forum sets the accent colour and the forum layout
  (with a sidebar or full width).
- *With Pursae Web Builder.* Web Builder › Theme Library › Guild Hall imports the header (Log in and
  Join links), footer, front page (hero and channel tiles linking the demo forums), post, page,
  archive, search and 404 templates into Hello Pursae, plus the Guild rules and About pages. The
  header part carries the forum's colour tokens, so forum views follow the theme.

**Demo.** Importing the demo switches Pursae Forum on and adds six members (two moderators), three
categories with six forums (Announcements, Introductions, Looking for Group, Guides and Builds, Clips and
Highlights, Off-Topic Tavern), ten topics with replies, the Home, Guild rules and About pages and the
menus. Remove Demo Content deletes all of it again.

No JavaScript on the front end. Fonts: Chakra Petch and Inter (SIL Open Font License), self-hosted.
Artwork: original illustrations made for this theme, dedicated to the public domain (CC0).
