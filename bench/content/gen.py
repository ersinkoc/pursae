#!/usr/bin/env python3
"""Deterministic benchmark content: one WXR file (posts, pages, categories, tags) and one JSON
file of WooCommerce REST product payloads. Both sides import exactly these files.

    python3 bench/content/gen.py bench/content/out

Output: site.wxr (1000 posts, 50 pages, 10 categories, 60 tags) and products.json (200 simple
products in 5 product categories). Images are not in the WXR: run.py uploads the same 12
Twenty Twenty-Five images through /wp/v2/media on each side and sets them as featured images.
"""
import json
import os
import random
import sys
from datetime import datetime, timedelta
from xml.sax.saxutils import escape

SEED = 20261109
POSTS, PAGES, PRODUCTS = 1000, 50, 200

WORDS = ("garden flower botany leaf stem root petal bloom season soil water light shade green "
         "meadow forest river stone path morning evening summer winter autumn spring seed grow "
         "plant tree branch field valley hill cloud rain sun wind quiet simple gentle bright "
         "story journey craft studio design color texture pattern detail moment practice notes "
         "collection archive editorial gallery review guide method habit routine weekend city").split()
CATEGORIES = ["Botany", "Travel", "Design", "Studio", "Notes", "Reviews", "Guides", "Seasons", "Craft", "Journal"]
PRODUCT_CATS = ["Seeds", "Tools", "Pots", "Prints", "Books"]


def words(r, n):
    return " ".join(r.choice(WORDS) for _ in range(n))


def sentence(r):
    s = words(r, r.randint(8, 18))
    return s[0].upper() + s[1:] + "."


def paragraph(r):
    return " ".join(sentence(r) for _ in range(r.randint(3, 6)))


def blocks(r, paras):
    out = []
    for i in range(paras):
        if i and i % 3 == 0:
            out.append(f"<!-- wp:heading -->\n<h2 class=\"wp-block-heading\">{words(r, 4).title()}</h2>\n<!-- /wp:heading -->")
        out.append(f"<!-- wp:paragraph -->\n<p>{paragraph(r)}</p>\n<!-- /wp:paragraph -->")
    items = "".join(f"<!-- wp:list-item -->\n<li>{words(r, 5)}</li>\n<!-- /wp:list-item -->" for _ in range(4))
    out.append(f"<!-- wp:list -->\n<ul class=\"wp-block-list\">{items}</ul>\n<!-- /wp:list -->")
    return "\n\n".join(out)


def slugify(s):
    return "-".join(s.lower().split())


def item(pid, typ, title, slug, content, excerpt, date, cats, tags, parent=0, order=0):
    terms = "".join(f'<category domain="category" nicename="{slugify(c)}"><![CDATA[{c}]]></category>' for c in cats)
    terms += "".join(f'<category domain="post_tag" nicename="{slugify(t)}"><![CDATA[{t}]]></category>' for t in tags)
    d = date.strftime("%Y-%m-%d %H:%M:%S")
    return f"""
<item>
<title>{escape(title)}</title>
<link>/?p={pid}</link>
<pubDate>{date.strftime('%a, %d %b %Y %H:%M:%S +0000')}</pubDate>
<dc:creator><![CDATA[admin]]></dc:creator>
<guid isPermaLink="false">/?p={pid}</guid>
<description></description>
<content:encoded><![CDATA[{content}]]></content:encoded>
<excerpt:encoded><![CDATA[{excerpt}]]></excerpt:encoded>
<wp:post_id>{pid}</wp:post_id>
<wp:post_date><![CDATA[{d}]]></wp:post_date>
<wp:post_date_gmt><![CDATA[{d}]]></wp:post_date_gmt>
<wp:post_modified><![CDATA[{d}]]></wp:post_modified>
<wp:post_modified_gmt><![CDATA[{d}]]></wp:post_modified_gmt>
<wp:comment_status><![CDATA[open]]></wp:comment_status>
<wp:ping_status><![CDATA[closed]]></wp:ping_status>
<wp:post_name><![CDATA[{slug}]]></wp:post_name>
<wp:status><![CDATA[publish]]></wp:status>
<wp:post_parent>{parent}</wp:post_parent>
<wp:menu_order>{order}</wp:menu_order>
<wp:post_type><![CDATA[{typ}]]></wp:post_type>
<wp:post_password><![CDATA[]]></wp:post_password>
<wp:is_sticky>0</wp:is_sticky>
{terms}
</item>"""


def main(out):
    os.makedirs(out, exist_ok=True)
    r = random.Random(SEED)
    tags = sorted({words(r, 1).title() + " " + words(r, 1).title() for _ in range(200)})[:60]
    start = datetime(2024, 1, 1, 9, 0, 0)
    parts = []
    for i, c in enumerate(CATEGORIES, 1):
        parts.append(f"<wp:category><wp:term_id>{i}</wp:term_id><wp:category_nicename><![CDATA[{slugify(c)}]]></wp:category_nicename>"
                     f"<wp:category_parent><![CDATA[]]></wp:category_parent><wp:cat_name><![CDATA[{c}]]></wp:cat_name></wp:category>")
    for i, t in enumerate(tags, 100):
        parts.append(f"<wp:tag><wp:term_id>{i}</wp:term_id><wp:tag_slug><![CDATA[{slugify(t)}]]></wp:tag_slug>"
                     f"<wp:tag_name><![CDATA[{t}]]></wp:tag_name></wp:tag>")
    pid = 1000
    for n in range(PAGES):
        pid += 1
        title = words(r, 3).title()
        parts.append(item(pid, "page", title, f"page-{n + 1}", blocks(r, 6), "", start + timedelta(hours=n), [], [], order=n))
    for n in range(POSTS):
        pid += 1
        title = words(r, r.randint(3, 7)).title()
        date = start + timedelta(hours=16 * n)
        parts.append(item(pid, "post", title, f"post-{n + 1}", blocks(r, r.randint(4, 9)), sentence(r), date,
                          [CATEGORIES[n % len(CATEGORIES)]], r.sample(tags, 3)))
    wxr = f"""<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/"
 xmlns:wfw="http://wellformedweb.org/CommentAPI/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>Bench</title>
<link>http://bench.invalid</link>
<description>Benchmark content</description>
<language>en-US</language>
<wp:wxr_version>1.2</wp:wxr_version>
<wp:base_site_url>http://bench.invalid</wp:base_site_url>
<wp:base_blog_url>http://bench.invalid</wp:base_blog_url>
<wp:author><wp:author_id>1</wp:author_id><wp:author_login><![CDATA[admin]]></wp:author_login><wp:author_email><![CDATA[admin@bench.invalid]]></wp:author_email><wp:author_display_name><![CDATA[admin]]></wp:author_display_name></wp:author>
{''.join(parts)}
</channel>
</rss>
"""
    with open(os.path.join(out, "site.wxr"), "w") as f:
        f.write(wxr)
    products = []
    for n in range(PRODUCTS):
        name = words(r, 3).title()
        products.append({
            "name": name, "slug": f"product-{n + 1}", "type": "simple", "status": "publish",
            "regular_price": f"{r.randint(5, 200)}.{r.choice(['00', '50', '99'])}",
            "description": f"<p>{paragraph(r)}</p><p>{paragraph(r)}</p>",
            "short_description": f"<p>{sentence(r)}</p>",
            "sku": f"BENCH-{n + 1:04d}", "manage_stock": True, "stock_quantity": r.randint(1, 100),
            "category": PRODUCT_CATS[n % len(PRODUCT_CATS)],
        })
    with open(os.path.join(out, "products.json"), "w") as f:
        json.dump({"categories": PRODUCT_CATS, "products": products}, f, indent=1)
    print(f"wrote {out}/site.wxr ({POSTS} posts, {PAGES} pages) and products.json ({PRODUCTS} products)")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "out"))
