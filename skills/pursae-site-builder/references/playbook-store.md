# Playbook — online store (Pursae Commerce)

Same steps as the server prompt `build_site kind=store`.

1. **Plan** — `site_overview`. Ask: what they sell, categories, a first set of products with prices
   (never invent prices without saying so), currency, country, shipping (zones, flat/free), payment
   methods they use, return policy, brand color, logo, photos. Get a yes on the plan.
2. **Plugins** — ask to turn on Pursae Commerce, Pursae Web Builder, Pursae SEO.
3. **Store setup**
   - `commerce_get_settings {group: "general"}` → `commerce_update_settings {group: "general", update: [{id: "woocommerce_currency", value: "EUR"}, {id: "woocommerce_store_address", value: "…"}]}`.
   - `commerce_list_payment_gateways` → enable at least one (`bacs`, `cod`, `cheque`, or a card
     gateway whose keys **the owner** enters in the admin).
   - `commerce_create_shipping_zone {name}` + `rest_request POST /wc/v3/shipping/zones/{id}/methods {method_id: "flat_rate"}`.
4. **Catalog** — `commerce_create_category` per category (image optional); `upload_media
   {filename, source_url}` per photo; `commerce_create_product {name, type: "simple", regular_price,
   description, short_description, categories: [{id}], images: [{id}]}`. Variable products: see
   `rest_request` on `/wc/v3/products/{id}/variations`.
5. **Pages** — Home: hero (offer + "Shop now" → /shop/) → features (categories as cards with links) →
   media (brand story) → quote → faq (shipping/returns) → cta (newsletter or discount with a
   `commerce_create_coupon`). About, Contact (form), Shipping & Returns (text), Terms/Privacy drafts
   marked for review. Shop, cart, checkout, my-account come from Commerce: link them, do not rebuild.
6. **Wire** — front page, menus (Shop, categories as children, About, Contact; footer: policies),
   SEO, `cache_purge`. Place a test order only if the user asks.
