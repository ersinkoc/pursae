# Playbook — company / service site

Same steps as the server prompt `build_site kind=business` (`pursae_mcp.py prompt build_site kind=business brief="…"`).

1. **Plan** — `site_overview`. Ask once for anything missing: name, what they offer, city/area,
   audience, language, brand color, logo, real photos, contact details. Propose: Home, About,
   Services (or one page per service), Contact, optional Pricing / FAQ / Team. Get a yes.
2. **Plugins** — ask to turn on Pursae Web Builder (`golementor`) and Pursae SEO if they are off.
3. **Frame** — header template (nav section: brand, menu links, a CTA button) and footer template
   (footer section: address, phone, e-mail, links, © line); new header/footer templates apply to the whole site (`include/general`). Or import a theme kit
   that matches and rewrite it.
4. **Pages** (one `site` look everywhere):
   - Home: hero (clear promise + CTA) → features (3–6 services) → media (why us, with photo) →
     steps or stats → quote (a real testimonial if given; otherwise leave it out) → faq → cta.
   - About: text/media (story) → features (values) → team (layout cells or features) → cta.
   - Services: features per service group, or one page per service with hero + media + faq + cta.
   - Contact: form section (labels in the site language) + media/text with address, hours, map link.
5. **Wire** — `front_page: true` on Home, `menu_build` (primary + footer), `update_settings` title and
   tagline, logo/icon if given, `seo_update_meta` per page, `cache_purge {all: true}`.
6. **Report** — URLs + edit links; list what the owner should still supply (photos, legal pages).
