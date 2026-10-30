#!/usr/bin/env python3
"""Builds the Golementor store themes of the theme library (web/theme-library/<slug>/).

Each theme is a package (docs/theme-package.md) whose theme.json names a Golementor design
(golementor/design.json: the kit's global colors and fonts plus Theme Builder parts: header, footer,
single product, products archive, cart, checkout and my account) and a demo (demo/demo.json: a
store, pages built with Golementor, menus). The images come from art.py.

    python3 tools/goo-golementor-themes/build.py            # writes the three packages
    python3 tools/goo-golementor-themes/art.py              # draws their demo images and screenshots

The data below is the source of truth; edit it and run the script again.
"""

import hashlib
import json
import os
import shutil
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
LIB = os.path.join(ROOT, "web", "theme-library")
sys.path.insert(0, os.path.dirname(__file__))
from themes import THEMES  # noqa: E402


# ---------------------------------------------------------------------------------------------------
# Element helpers

class IDs:
    def __init__(self, seed):
        self.seed, self.n = seed, 0

    def __call__(self):
        self.n += 1
        return hashlib.sha1(f"{self.seed}:{self.n}".encode()).hexdigest()[:7]


def px(n, unit="px"):
    return {"unit": unit, "size": n, "sizes": []}


def box(t, r=None, b=None, l=None, unit="px"):
    r = t if r is None else r
    b = t if b is None else b
    l = r if l is None else l
    return {"unit": unit, "top": str(t), "right": str(r), "bottom": str(b), "left": str(l), "isLinked": t == r == b == l}


def gap(n):
    return {"unit": "px", "size": n, "column": str(n), "row": str(n), "isLinked": True}


def link(url):
    return {"url": url, "is_external": "", "nofollow": "", "custom_attributes": ""}


def col(c):
    return f"globals/colors?id={c}"


def typo(t):
    return f"globals/typography?id={t}"


class Tree:
    """Builds an element tree with stable ids."""

    def __init__(self, seed):
        self.id = IDs(seed)

    def con(self, settings, *kids, inner=None):
        s = {"content_width": "full", "flex_direction": "column"}
        s.update(settings)
        return {"id": self.id(), "elType": "container", "isInner": bool(inner), "settings": s, "elements": list(kids)}

    def section(self, settings, *kids, width=1240):
        s = {"content_width": "boxed", "boxed_width": px(width), "flex_direction": "column",
             "padding": box(72, 24), "padding_mobile": box(48, 18)}
        s.update(settings)
        return {"id": self.id(), "elType": "container", "isInner": False, "settings": s, "elements": list(kids)}

    def row(self, settings, *kids):
        s = {"content_width": "full", "flex_direction": "row", "flex_gap": gap(24), "flex_align_items": "center",
             "flex_direction_mobile": "column"}
        s.update(settings)
        return {"id": self.id(), "elType": "container", "isInner": True, "settings": s, "elements": list(kids)}

    def stack(self, settings, *kids):
        s = {"content_width": "full", "flex_direction": "column", "flex_gap": gap(16)}
        s.update(settings)
        return {"id": self.id(), "elType": "container", "isInner": True, "settings": s, "elements": list(kids)}

    def grid(self, cols, settings, *kids, tablet=None, mobile=1):
        s = {"container_type": "grid", "content_width": "full", "grid_gaps": {"column": "24", "row": "24", "isLinked": True, "unit": "px"},
             "grid_columns_grid": {"unit": "fr", "size": cols, "sizes": []},
             "grid_columns_grid_tablet": {"unit": "fr", "size": tablet or min(cols, 2), "sizes": []},
             "grid_columns_grid_mobile": {"unit": "fr", "size": mobile, "sizes": []},
             "grid_rows_grid": {"unit": "fr", "size": 1, "sizes": []}, "grid_auto_flow": "row"}
        s.update(settings)
        return {"id": self.id(), "elType": "container", "isInner": True, "settings": s, "elements": list(kids)}

    def w(self, typ, settings):
        return {"id": self.id(), "elType": "widget", "widgetType": typ, "settings": settings, "elements": []}

    # Common widgets ------------------------------------------------------------------------------
    def heading(self, text, tag="h2", t="primary", c="primary", align="", **kw):
        s = {"title": text, "header_size": tag, "__globals__": {"title_color": col(c), "typography_typography": typo(t)}}
        if align:
            s["align"] = align
        s.update(kw)
        return self.w("heading", s)

    def text(self, html, t="text", c="text", align="", **kw):
        s = {"editor": html, "__globals__": {"text_color": col(c), "typography_typography": typo(t)}}
        if align:
            s["align"] = align
        s.update(kw)
        return self.w("text-editor", s)

    def button(self, label, url, bg="accent", fg="paper", hover="primary", align="", **kw):
        s = {"text": label, "link": link(url), "size": "md",
             "__globals__": {"typography_typography": typo("accent"), "background_color": col(bg), "button_text_color": col(fg),
                             "button_background_hover_color": col(hover), "hover_color": col(fg)}}
        if align:
            s["align"] = align
        s.update(kw)
        return self.w("button", s)

    def image(self, media, alt="", radius=0, **kw):
        s = {"image": {"url": "{{media:" + media + "}}", "id": "{{media_id:" + media + "}}", "size": "", "alt": alt, "source": "library"},
             "image_size": "large"}
        if radius:
            s["image_border_radius"] = box(radius)
        s.update(kw)
        return self.w("image", s)

    def icon_box(self, icon, title, desc, c="accent", fg="primary", muted="text", **kw):
        s = {"selected_icon": {"value": icon, "library": "fa-solid"}, "title_text": title, "description_text": desc,
             "position": "left", "title_size": "h3", "icon_size": px(24), "icon_space": px(14),
             "__globals__": {"primary_color": col(c), "title_color": col(fg), "title_typography_typography": typo("h4"),
                             "description_color": col(muted), "description_typography_typography": typo("small")}}
        s.update(kw)
        return self.w("icon-box", s)

    def spacer(self, h):
        return self.w("spacer", {"space": px(h)})


# ---------------------------------------------------------------------------------------------------
# Design: kit and Theme Builder parts

def kit_settings(th):
    colors = th["colors"]
    sys_ids = ["primary", "secondary", "text", "accent"]
    sysc = [{"_id": i, "title": colors[i][0], "color": colors[i][1]} for i in sys_ids]
    cust = [{"_id": k, "title": v[0], "color": v[1]} for k, v in colors.items() if k not in sys_ids]
    fonts = th["fonts"]

    def ty(i, title, fam, weight, size, lh, tablet=None, mobile=None, **kw):
        d = {"_id": i, "title": title, "typography_typography": "custom", "typography_font_family": fam, "typography_font_weight": str(weight),
             "typography_font_size": px(size), "typography_line_height": px(lh, "em")}
        if tablet:
            d["typography_font_size_tablet"] = px(tablet)
        if mobile:
            d["typography_font_size_mobile"] = px(mobile)
        for k, v in kw.items():
            d["typography_" + k] = v
        return d

    h, b = fonts["heading"], fonts["body"]
    hw = th.get("heading_weight", 500)
    sys_t = [ty("primary", "Headings", h, hw, 40, 1.15, 34, 28, letter_spacing=px(th.get("heading_tracking", -0.5))),
             ty("secondary", "Eyebrow", b, 700, 12, 1.4, letter_spacing=px(2.4), text_transform="uppercase"),
             ty("text", "Body", b, 400, 16, 1.7, 16, 15),
             ty("accent", "Buttons and menu", b, 600, 15, 1.2)]
    cust_t = [ty("display", "Display", h, hw, 64, 1.04, 50, 38, letter_spacing=px(th.get("heading_tracking", -0.5) * 2)),
              ty("h4", "Card title", h, hw, 20, 1.3),
              ty("small", "Small", b, 400, 14, 1.6),
              ty("lead", "Lead", b, 400, 19, 1.65, 18, 17)]
    if fonts.get("hand"):
        cust_t.append(ty("hand", "Handwritten", fonts["hand"], 600, 26, 1.2))
    radius = th.get("radius", 999)
    settings = {
        "system_colors": sysc, "custom_colors": cust, "system_typography": sys_t, "custom_typography": cust_t,
        "container_width": px(1240), "container_padding": box(0), "space_between_widgets": {"column": "20", "row": "20", "isLinked": True, "unit": "px"},
        "body_background_background": "classic",
        "button_border_radius": box(radius), "button_padding": box(15, 30, 15, 30),
        "form_field_border_border": "solid", "form_field_border_width": box(1), "form_field_border_radius": box(min(radius, 10)),
        "form_field_padding": box(12, 16, 12, 16),
        "site_name": th["name"], "site_description": th["tagline"],
        "__globals__": {
            "body_color": col("text"), "body_typography_typography": typo("text"), "body_background_color": col("paper"),
            "link_normal_color": col("accent"), "link_hover_color": col("primary"),
            "h1_color": col("primary"), "h1_typography_typography": typo("display"),
            "h2_color": col("primary"), "h2_typography_typography": typo("primary"),
            "h3_color": col("primary"), "h3_typography_typography": typo("h4"),
            "h4_color": col("primary"), "h5_color": col("primary"), "h6_color": col("primary"),
            "button_typography_typography": typo("accent"), "button_text_color": col("paper"), "button_background_color": col("accent"),
            "button_hover_text_color": col("paper"), "button_hover_background_color": col("primary"),
            "form_label_color": col("primary"), "form_field_text_color": col("primary"), "form_field_background_color": col("paper"),
            "form_field_border_color": col("line"), "form_field_focus_border_color": col("accent"),
        },
    }
    return settings


def header_part(T, th):
    hd = th["header"]
    bar = T.section({"padding": box(9, 24), "padding_mobile": box(8, 16), "background_background": "classic",
                     "__globals__": {"background_color": col(hd["bar_bg"])}},
                    T.text(f"<p>{th['announcement']}</p>", t="small", c=hd["bar_fg"], align="center"))
    logo = T.w("theme-site-title", {"header_size": "p", "link_to": "yes", "align": "center" if hd["layout"] == "centered" else "left",
                                    "__globals__": {"title_color": col(hd["logo"]), "typography_typography": typo("logo")}})
    nav = T.w("nav-menu", {"menu_location": "primary", "layout": "horizontal", "pointer": "underline", "animation_line": "fade",
                           "dropdown": "tablet", "toggle": "burger", "toggle_align": "left", "full_width": "stretch",
                           "align_items": "center" if hd["layout"] == "centered" else "start",
                           "padding_horizontal_menu_item": px(12), "padding_vertical_menu_item": px(8),
                           "submenu_icon": {"value": "", "library": ""},
                           "__globals__": {"color_menu_item": col(hd["fg"]), "color_menu_item_hover": col(hd["hover"]),
                                           "color_menu_item_active": col(hd["hover"]), "pointer_color_menu_item_hover": col(hd["hover"]),
                                           "pointer_color_menu_item_active": col(hd["hover"]), "menu_typography_typography": typo("accent"),
                                           "color_dropdown_item": col("primary"), "background_color_dropdown_item": col("paper")}})
    tools = T.row({"flex_justify_content": "flex-end", "flex_gap": gap(18), "flex_direction_mobile": "row", "flex_wrap": "nowrap"},
                  T.w("golylang-language-switcher", {"layout": "horizontal", "show_flags": "", "display": "code",
                                                     "__globals__": {"text_color": col(hd["fg"]), "active_color": col(hd["hover"])}}),
                  T.w("woocommerce-menu-cart", {"icon": hd.get("cart_icon", "bag"), "show_subtotal": "",
                                                "__globals__": {"toggle_icon_color": col(hd["fg"]),
                                                                "items_indicator_background_color": col(hd["badge"])}}))
    if hd["layout"] == "centered":
        top = T.con({"container_type": "grid", "grid_columns_grid": {"unit": "custom", "size": "1fr auto 1fr", "sizes": []},
                     "grid_columns_grid_mobile": {"unit": "custom", "size": "48px 1fr auto", "sizes": []},
                     "grid_rows_grid": {"unit": "custom", "size": "auto", "sizes": []},
                     "grid_gaps": {"column": "20", "row": "0", "isLinked": False, "unit": "px"}, "grid_align_items": "center"},
                    T.w("search-form", {"placeholder": hd.get("search", "Search products…"), "button_type": "icon",
                                        "_element_width": "initial", "_element_custom_width": px(220), "hide_mobile": "hidden-mobile"}),
                    logo, tools, inner=True)
        main = T.section({"padding": box(22, 24, 10, 24), "padding_mobile": box(14, 16), "background_background": "classic", "html_tag": "header",
                          "__globals__": {"background_color": col(hd["bg"])}},
                         top, nav)
    else:
        grid = T.con({"container_type": "grid", "grid_columns_grid": {"unit": "custom", "size": "auto 1fr auto", "sizes": []},
                      "grid_columns_grid_tablet": {"unit": "custom", "size": "auto 1fr auto", "sizes": []},
                      "grid_columns_grid_mobile": {"unit": "custom", "size": "auto 1fr auto", "sizes": []},
                      "grid_rows_grid": {"unit": "custom", "size": "auto", "sizes": []},
                      "grid_gaps": {"column": "28", "row": "0", "isLinked": False, "unit": "px"}, "grid_align_items": "center"},
                     logo, nav, tools, inner=True)
        main = T.section({"padding": box(16, 24), "padding_mobile": box(12, 16), "background_background": "classic", "html_tag": "header",
                          "border_border": "solid", "border_width": box(0, 0, 1, 0),
                          "__globals__": {"background_color": col(hd["bg"]), "border_color": col("line")}},
                         grid)
    return [bar, main]


def footer_part(T, th):
    ft = th["footer"]
    c_head = lambda txt: T.heading(txt, tag="p", t="secondary", c=ft["muted"])
    brand = T.stack({"flex_gap": gap(14)},
                    T.w("theme-site-title", {"header_size": "p", "link_to": "yes", "__globals__": {"title_color": col(ft["fg"]), "typography_typography": typo("logo")}}),
                    T.text(f"<p>{th['footer_blurb']}</p>", t="small", c=ft["muted"]),
                    T.w("golylang-language-switcher", {"layout": "horizontal", "show_flags": "yes", "display": "name",
                                                       "__globals__": {"text_color": col(ft["muted"]), "active_color": col(ft["fg"])}}))
    menu = T.stack({"flex_gap": gap(10)}, c_head("Explore"),
                   T.w("nav-menu", {"menu_location": "footer", "layout": "vertical", "pointer": "none", "dropdown": "none",
                                    "padding_horizontal_menu_item": px(0), "padding_vertical_menu_item": px(5),
                                    "__globals__": {"color_menu_item": col(ft["muted"]), "color_menu_item_hover": col(ft["fg"]),
                                                    "menu_typography_typography": typo("small")}}))
    shop_links = [{"text": t, "selected_icon": {"value": "fas fa-chevron-right", "library": "fa-solid"}, "link": link(u), "_id": T.id()}
                  for t, u in [("Shop all", "/shop/"), ("Your cart", "/cart/"), ("My account", "/my-account/"), ("Checkout", "/checkout/")]]
    shop = T.stack({"flex_gap": gap(10)}, c_head("Store"),
                   T.w("icon-list", {"icon_list": shop_links, "space_between": px(6), "icon_size": px(9), "text_indent": px(8),
                                     "__globals__": {"icon_color": col(ft["muted"]), "text_color": col(ft["muted"]),
                                                     "text_color_hover": col(ft["fg"]), "icon_typography_typography": typo("small")}}))
    contact = T.stack({"flex_gap": gap(10)}, c_head("Visit"), T.text(th["footer_contact"], t="small", c=ft["muted"]))
    cols = T.grid(4, {"grid_columns_grid": {"unit": "custom", "size": "1.4fr 1fr 1fr 1.2fr", "sizes": []},
                      "grid_columns_grid_tablet": {"unit": "fr", "size": 2, "sizes": []},
                      "grid_gaps": {"column": "40", "row": "32", "isLinked": False, "unit": "px"}},
                  brand, menu, shop, contact)
    bottom = T.row({"flex_justify_content": "space-between", "padding": box(22, 0, 0, 0), "border_border": "solid", "border_width": box(1, 0, 0, 0),
                    "__globals__": {"border_color": col(ft["line"])}},
                   T.w("theme-site-copyright", {"text": "© [year] [site_title]. All rights reserved.",
                                                "__globals__": {"text_color": col(ft["muted"]), "typography_typography": typo("small")}}),
                   T.text("<p>Prices include VAT where it applies &middot; Secure checkout</p>", t="small", c=ft["muted"]))
    return [T.section({"padding": box(64, 24, 28, 24), "flex_gap": gap(40), "background_background": "classic", "html_tag": "footer",
                       "__globals__": {"background_color": col(ft["bg"])}}, cols, bottom)]


def single_product_part(T, th):
    sp = th["single"]
    left = T.stack({"width": px(54, "%"), "width_mobile": px(100, "%")},
                   T.w("woocommerce-product-images", {"image_radius": px(th.get("image_radius", 18))}))
    usps = [{"text": t, "selected_icon": {"value": i, "library": "fa-solid"}, "_id": T.id()} for i, t in th["product_usps"]]
    right = T.stack({"width": px(46, "%"), "width_mobile": px(100, "%"), "flex_gap": gap(18), "flex_align_items": "stretch"},
                    T.w("woocommerce-breadcrumb", {"__globals__": {"text_color": col("muted"), "link_color": col("muted"), "text_typography_typography": typo("small")}}),
                    T.w("woocommerce-product-title", {"header_size": "h1", "__globals__": {"title_color": col("primary"), "typography_typography": typo("primary")}}),
                    T.w("woocommerce-product-rating", {"__globals__": {"star_color": col("accent")}}),
                    T.w("woocommerce-product-price", {"__globals__": {"price_color": col(sp["price"]), "price_typography_typography": typo("h4"),
                                                                      "sale_price_color": col("accent")}}),
                    T.w("woocommerce-product-short-description", {"__globals__": {"text_color": col("text"), "text_typography_typography": typo("text")}}),
                    T.w("woocommerce-product-add-to-cart", {"button_radius": px(th.get("radius", 999)),
                                                            "__globals__": {"button_bg_color": col("accent"), "button_text_color": col("paper"),
                                                                            "button_hover_bg_color": col("primary")}}),
                    T.w("divider", {"weight": px(1), "__globals__": {"color": col("line")}}),
                    T.w("icon-list", {"icon_list": usps, "space_between": px(10), "icon_size": px(15), "text_indent": px(10),
                                      "__globals__": {"icon_color": col("accent"), "text_color": col("text"), "icon_typography_typography": typo("small")}}),
                    T.w("woocommerce-product-meta", {"__globals__": {"text_color": col("muted"), "link_color": col("accent"), "text_typography_typography": typo("small")}}))
    top = T.section({"padding": box(40, 24, 56, 24)},
                    T.row({"flex_gap": gap(56), "flex_align_items": "flex-start"}, left, right))
    tabs = T.section({"padding": box(48, 24), "background_background": "classic", "__globals__": {"background_color": col(sp["band"])}},
                     T.w("woocommerce-product-data-tabs", {"__globals__": {"tab_active_text_color": col("accent"), "tab_text_color": col("text")}}))
    related = T.section({"padding": box(64, 24)},
                        T.w("woocommerce-product-related", {"columns": "4", "posts_per_page": 4, "image_radius": px(th.get("image_radius", 18)),
                                                            "__globals__": {"heading_color": col("primary"), "heading_typography_typography": typo("primary"),
                                                                            "title_color": col("primary"), "title_typography_typography": typo("h4"),
                                                                            "button_bg_color": col("primary"), "button_text_color": col("paper")}}))
    return [top, tabs, related]


def archive_part(T, th):
    ar = th["archive"]
    band = T.section({"padding": box(56, 24, 44, 24), "flex_gap": gap(10), "flex_align_items": "center", "background_background": "classic",
                      "__globals__": {"background_color": col(ar["band"])}},
                     T.w("woocommerce-breadcrumb", {"align": "center", "__globals__": {"text_color": col("muted"), "link_color": col("muted"),
                                                                                       "text_typography_typography": typo("small")}}),
                     T.w("theme-post-title", {"header_size": "h1", "align": "center", "__globals__": {"title_color": col("primary"), "typography_typography": typo("display")}}),
                     T.text(f"<p>{ar['intro']}</p>", t="lead", c="text", align="center"))
    grid = T.section({"padding": box(48, 24, 72, 24)},
                     T.w("wc-archive-products", {"image_radius": px(th.get("image_radius", 18)),
                                                 "__globals__": {"title_color": col("primary"), "title_typography_typography": typo("h4"),
                                                                 "price_color": col("text"), "button_bg_color": col(ar["button"]),
                                                                 "button_text_color": col("paper"), "onsale_bg_color": col("accent")}}))
    return [band, grid]


def page_part(T, th, widget, intro):
    return [T.section({"padding": box(48, 24, 72, 24), "flex_gap": gap(18)},
                      T.w("theme-post-title", {"header_size": "h1", "__globals__": {"title_color": col("primary"), "typography_typography": typo("primary")}}),
                      T.text(f"<p>{intro}</p>", t="text", c="muted"),
                      T.w(widget, {"__globals__": {"headings_color": col("primary"), "buttons_bg_color": col("accent"),
                                                   "buttons_text_color": col("paper")}}))]


def design(th):
    T = Tree(th["slug"] + ":parts")
    kit = kit_settings(th)
    # the logo typography (site title)
    kit["custom_typography"].append({"_id": "logo", "title": "Logo", "typography_typography": "custom",
                                     "typography_font_family": th["fonts"]["heading"], "typography_font_weight": str(th.get("logo_weight", 600)),
                                     "typography_font_size": px(th.get("logo_size", 28)), "typography_line_height": px(1, "em"),
                                     "typography_letter_spacing": px(th.get("logo_tracking", -0.5))})
    parts = [
        {"type": "header", "title": th["name"] + " Header", "conditions": ["include/general"], "elements": header_part(T, th)},
        {"type": "footer", "title": th["name"] + " Footer", "conditions": ["include/general"], "elements": footer_part(T, th)},
        {"type": "single-product", "title": "Product", "conditions": ["include/product"], "elements": single_product_part(T, th)},
        {"type": "product-archive", "title": "Shop & Categories", "conditions": ["include/product_archive"], "elements": archive_part(T, th)},
        {"type": "single-page", "title": "Cart", "conditions": ["include/goocommerce/cart"],
         "elements": page_part(T, th, "woocommerce-cart", th["cart_intro"])},
        {"type": "single-page", "title": "Checkout", "conditions": ["include/goocommerce/checkout"],
         "elements": page_part(T, th, "woocommerce-checkout-page", "Almost there. Delivery details first, then payment.")},
        {"type": "single-page", "title": "My Account", "conditions": ["include/goocommerce/myaccount"],
         "elements": page_part(T, th, "woocommerce-my-account", "Orders, addresses and account details in one place.")},
    ]
    for p in parts:
        p["status"] = "publish"
        p["settings"] = {}
    return {"format": "gowebeditor-theme", "version": 1, "name": th["name"], "kit": kit, "custom_css": "", "parts": parts}


# ---------------------------------------------------------------------------------------------------
# Demo pages

def home_page(th):
    T = Tree(th["slug"] + ":home")
    h = th["home"]
    hero_text = T.stack({"flex_gap": gap(20), "width": px(h.get("hero_width", 560)), "width_mobile": px(100, "%"),
                         "flex_align_items": "flex-start"},
                        T.heading(h["eyebrow"], tag="p", t="secondary", c=h["hero_eyebrow"]),
                        T.heading(h["title"], tag="h1", t="display", c=h["hero_fg"]),
                        T.text(f"<p>{h['lead']}</p>", t="lead", c=h["hero_fg"]),
                        T.row({"flex_gap": gap(12), "flex_direction_mobile": "row", "flex_wrap": "wrap"},
                              T.button(h["cta"], "/shop/", bg=h["cta_bg"], fg=h["cta_fg"], hover="primary"),
                              T.button(h["cta2"], "{{page:about}}", bg=h["cta2_bg"], fg=h["cta2_fg"], hover=h["cta2_bg"],
                                       border_border="solid", border_width=box(1),
                                       __globals__={"typography_typography": typo("accent"), "background_color": col(h["cta2_bg"]),
                                                    "button_text_color": col(h["cta2_fg"]), "border_color": col(h["cta2_fg"]),
                                                    "button_background_hover_color": col(h["cta2_fg"]), "hover_color": col(h["cta2_bg"])})))
    hero = T.section({"content_width": "boxed", "min_height": px(h.get("hero_height", 640)), "min_height_mobile": px(520),
                      "flex_justify_content": "center", "padding": box(96, 24), "background_background": "classic",
                      "background_image": {"url": "{{media:hero}}", "id": "{{media_id:hero}}", "size": "", "alt": "", "source": "library"},
                      "background_position": h.get("hero_position", "center right"), "background_size": "cover", "background_repeat": "no-repeat",
                      "background_overlay_background": "gradient", "background_overlay_color": h["overlay"][0],
                      "background_overlay_color_b": h["overlay"][1], "background_overlay_gradient_angle": px(90, "deg"),
                      "background_overlay_color_stop": px(h.get("overlay_from", 12), "%"), "background_overlay_color_b_stop": px(72, "%"),
                      "background_overlay_opacity": px(0.92)}, hero_text)
    usps = T.section({"padding": box(26, 24), "background_background": "classic", "__globals__": {"background_color": col(h["usp_bg"])}},
                     T.grid(4, {"grid_columns_grid_tablet": {"unit": "fr", "size": 2, "sizes": []}},
                            *[T.icon_box(i, t, d, c=h["usp_icon"], fg=h.get("usp_fg", "primary"), muted=h.get("usp_muted", "text"))
                              for i, t, d in th["usps"]]))
    cats = T.section({"padding": box(80, 24, 40, 24), "flex_gap": gap(28)},
                     T.row({"flex_justify_content": "space-between", "flex_align_items": "flex-end"},
                           T.stack({"flex_gap": gap(8)}, T.heading(h["cats_eyebrow"], tag="p", t="secondary", c="accent"),
                                   T.heading(h["cats_title"], tag="h2")),
                           T.button("Browse the shop", "/shop/", bg="paper", fg="primary", hover="sand", border_border="solid",
                                    border_width=box(1), hide_mobile="hidden-mobile")),
                     T.w("wc-categories", {"columns": "4", "number": 4, "hide_empty": "yes", "image_radius": px(th.get("image_radius", 18)),
                                           "__globals__": {"title_color": col("primary"), "title_typography_typography": typo("h4")}}))
    featured = T.section({"padding": box(40, 24, 72, 24), "flex_gap": gap(28)},
                         T.stack({"flex_gap": gap(8), "flex_align_items": "center"}, T.heading(h["feat_eyebrow"], tag="p", t="secondary", c="accent", align="center"),
                                 T.heading(h["feat_title"], tag="h2", align="center")),
                         T.w("woocommerce-products", {"columns": "4", "rows": 1, "query_post_type": "featured", "orderby": "menu_order", "order": "asc",
                                                      "image_radius": px(th.get("image_radius", 18)),
                                                      "__globals__": {"title_color": col("primary"), "title_typography_typography": typo("h4"),
                                                                      "button_bg_color": col("primary"), "button_text_color": col("paper"),
                                                                      "onsale_bg_color": col("accent")}}))
    story = T.section({"padding": box(88, 24), "background_background": "classic", "__globals__": {"background_color": col(h["story_bg"])}},
                      T.row({"flex_gap": gap(64)},
                            T.stack({"width": px(50, "%"), "width_mobile": px(100, "%")}, T.image("about", th["about_alt"], radius=th.get("image_radius", 18))),
                            T.stack({"width": px(50, "%"), "width_mobile": px(100, "%"), "flex_gap": gap(18), "flex_align_items": "flex-start"},
                                    T.heading(h["story_eyebrow"], tag="p", t="secondary", c=h["story_eyebrow_c"]),
                                    T.heading(h["story_title"], tag="h2", c=h["story_fg"]),
                                    T.text(h["story_text"], t="text", c=h["story_text_c"]),
                                    T.button("Our story", "{{page:about}}", bg=h["story_btn"], fg=h["story_btn_fg"], hover="accent"))))
    sale = T.section({"padding": box(80, 24, 40, 24), "flex_gap": gap(28)},
                     T.stack({"flex_gap": gap(8), "flex_align_items": "center"}, T.heading("Good to go", tag="p", t="secondary", c="accent", align="center"),
                             T.heading(h["sale_title"], tag="h2", align="center")),
                     T.w("woocommerce-products", {"columns": "4", "rows": 2, "query_post_type": "recent", "orderby": "date", "order": "desc",
                                                  "image_radius": px(th.get("image_radius", 18)),
                                                  "__globals__": {"title_color": col("primary"), "title_typography_typography": typo("h4"),
                                                                  "button_bg_color": col("primary"), "button_text_color": col("paper"),
                                                                  "onsale_bg_color": col("accent")}}),
                     T.button("See everything", "/shop/", align="center", bg="accent", fg="paper"))
    quote = T.section({"padding": box(72, 24, 88, 24), "flex_align_items": "center", "flex_gap": gap(18)},
                      T.heading("&ldquo;" + h["quote"] + "&rdquo;", tag="p", t="primary", c="primary", align="center",
                                _element_width="initial", _element_custom_width=px(820)),
                      T.text(f"<p>{h['quote_by']}</p>", t="secondary", c="muted", align="center"))
    news = T.section({"padding": box(64, 24), "background_background": "classic", "__globals__": {"background_color": col(h["news_bg"])}},
                     T.row({"flex_justify_content": "space-between"},
                           T.stack({"flex_gap": gap(8), "width": px(60, "%"), "width_mobile": px(100, "%")},
                                   T.heading(h["news_title"], tag="h2", c=h["news_fg"]),
                                   T.text(f"<p>{h['news_text']}</p>", t="text", c=h["news_fg"])),
                           T.button("Get 10% off: WELCOME10", "/shop/", bg=h["news_btn"], fg=h["news_btn_fg"], hover="paper")))
    return [hero, usps, cats, featured, story, sale, quote, news]


def about_page(th):
    T = Tree(th["slug"] + ":about")
    a = th["about"]
    intro = T.section({"padding": box(88, 24, 48, 24), "flex_align_items": "center", "flex_gap": gap(16)},
                      T.heading(a["eyebrow"], tag="p", t="secondary", c="accent", align="center"),
                      T.heading(a["title"], tag="h1", t="display", align="center", _element_width="initial", _element_custom_width=px(860)),
                      T.text(f"<p>{a['lead']}</p>", t="lead", align="center", _element_width="initial", _element_custom_width=px(720)))
    body = T.section({"padding": box(32, 24, 80, 24)},
                     T.row({"flex_gap": gap(64), "flex_align_items": "flex-start"},
                           T.stack({"width": px(55, "%"), "width_mobile": px(100, "%")}, T.image("about", th["about_alt"], radius=th.get("image_radius", 18))),
                           T.stack({"width": px(45, "%"), "width_mobile": px(100, "%"), "flex_gap": gap(16)},
                                   T.heading(a["h2"], tag="h2"), T.text(a["text"]))))
    values = T.section({"padding": box(72, 24), "background_background": "classic", "__globals__": {"background_color": col("sand")}},
                       T.grid(3, {"grid_gaps": {"column": "40", "row": "32", "isLinked": False, "unit": "px"}},
                              *[T.icon_box(i, t, d, position="top") for i, t, d in a["values"]]))
    cta = T.section({"padding": box(80, 24), "flex_align_items": "center", "flex_gap": gap(18)},
                    T.heading(a["cta_title"], tag="h2", align="center"), T.button("Visit the shop", "/shop/", align="center"))
    return [intro, body, values, cta]


def contact_page(th):
    T = Tree(th["slug"] + ":contact")
    c = th["contact"]
    items = [{"text": t, "selected_icon": {"value": i, "library": "fa-solid"}, "_id": T.id()} for i, t in c["lines"]]
    return [T.section({"padding": box(88, 24), "flex_gap": gap(18)},
                      T.row({"flex_gap": gap(64), "flex_align_items": "flex-start"},
                            T.stack({"width": px(50, "%"), "width_mobile": px(100, "%"), "flex_gap": gap(18)},
                                    T.heading("Say hello", tag="p", t="secondary", c="accent"),
                                    T.heading(c["title"], tag="h1", t="primary"),
                                    T.text(f"<p>{c['text']}</p>", t="lead"),
                                    T.w("icon-list", {"icon_list": items, "space_between": px(14), "icon_size": px(16), "text_indent": px(12),
                                                      "__globals__": {"icon_color": col("accent"), "text_color": col("primary"),
                                                                      "icon_typography_typography": typo("text")}})),
                            T.stack({"width": px(50, "%"), "width_mobile": px(100, "%"), "padding": box(32), "border_radius": box(th.get("image_radius", 18)),
                                     "background_background": "classic", "__globals__": {"background_color": col("sand")}, "flex_gap": gap(12)},
                                    T.heading("Opening hours", tag="h3", t="h4"),
                                    T.text(c["hours"]),
                                    T.heading("Delivery", tag="h3", t="h4"),
                                    T.text(f"<p>{c['delivery']}</p>"))))]


def demo(th):
    store = th["store"]
    media = [{"id": "hero", "file": f"demo/images/{th['slug']}-hero.jpg", "alt": th["hero_alt"]},
             {"id": "about", "file": f"demo/images/{th['slug']}-about.jpg", "alt": th["about_alt"]}]
    for c in store["categories"]:
        media.append({"id": "cat-" + c["slug"], "file": f"demo/images/{th['slug']}-cat-{c['slug']}.jpg", "alt": c["name"]})
        c["image"] = "cat-" + c["slug"]
    for p in store["products"]:
        media.append({"id": "p-" + p["slug"], "file": f"demo/images/{th['slug']}-p-{p['slug']}.jpg", "alt": p["name"]})
        p["image"] = "p-" + p["slug"]
    pages = [
        {"slug": "home", "title": "Home", "front_page": True, "content": "", "golementor": home_page(th),
         "golementor_settings": {"hide_title": "yes"}},
        {"slug": "about", "title": th["about"]["menu"], "content": "", "golementor": about_page(th), "golementor_settings": {"hide_title": "yes"}},
        {"slug": "contact", "title": "Contact", "content": "", "golementor": contact_page(th), "golementor_settings": {"hide_title": "yes"}},
    ]
    cats = [{"title": c["name"], "url": f"/product-category/{c['slug']}/"} for c in store["categories"]]
    menus = [
        {"name": th["name"] + " menu", "location": "primary",
         "items": [{"title": "Shop", "url": "/shop/", "children": cats}] + cats[:3] +
                  [{"title": th["about"]["menu"], "page": "about"}, {"title": "Contact", "page": "contact"}]},
        {"name": "Footer menu", "location": "footer",
         "items": [{"title": "Shop", "url": "/shop/"}, {"title": th["about"]["menu"], "page": "about"}, {"title": "Contact", "page": "contact"},
                   {"title": "My account", "url": "/my-account/"}]},
    ]
    goo = {k: v for k, v in store.items()}
    return {"version": 1, "options": {"blogname": th["name"], "blogdescription": th["tagline"]}, "media": media,
            "pages": pages, "menus": menus, "goocommerce": goo}


# ---------------------------------------------------------------------------------------------------
# Package files

FALLBACK_HEADER = """<!doctype html>
<html lang="en-US">
<head>
	<meta charset="UTF-8" />
	<meta name="viewport" content="width=device-width, initial-scale=1" />
	<title>{{.DocumentTitle}}</title>
{{.Head}}</head>
<body class="{{.BodyClass}}">
{{.BodyOpen}}<div id="page" class="site gs-fallback">
	<a class="skip-link screen-reader-text" href="#main">Skip to content</a>
	<header class="site-header">
		<p class="site-title"><a href="{{.HomeURL}}" rel="home">{{.SiteName}}</a></p>
		{{if has_nav_menu . "primary"}}<nav class="main-navigation" aria-label="Primary menu">{{nav_menu . "primary"}}</nav>{{end}}
		{{goo_language_switcher .}}
		{{if goo_on}}<a class="gs-cart" href="{{goo_url . "cart"}}">Cart (<span data-goo-cart-count>{{goo_cart_count .}}</span>)</a>{{end}}
	</header>
	<main id="main" class="site-main" tabindex="-1">
"""
FALLBACK_FOOTER = """	</main>
	<footer class="site-footer">
		{{if has_nav_menu . "footer"}}<nav class="footer-navigation" aria-label="Footer menu">{{nav_menu . "footer"}}</nav>{{end}}
		<p class="site-info">&copy; {{.SiteName}} &middot; Built with Golementor and Goocommerce</p>
	</footer>
</div>
{{.Footer}}{{.AdminBar}}
</body>
</html>
"""
FALLBACK = {
    "header.html": FALLBACK_HEADER,
    "footer.html": FALLBACK_FOOTER,
    "index.html": """{{template "header" .}}
<div class="gs-wrap">
{{if .IsHomeHeader}}<h1 class="page-title">{{.PostsPageTitle}}</h1>{{end}}
{{range .Posts}}<article id="post-{{.ID}}" class="{{.Class}}"><h2 class="entry-title"><a href="{{.Permalink}}">{{.Title}}</a></h2>{{if .ShowExcerpt}}<div class="entry-summary">{{.Excerpt}}</div>{{end}}</article>
{{else}}{{template "content-none" .}}{{end}}
{{.Pagination}}
</div>
{{template "footer" .}}
""",
    "page.html": """{{template "header" .}}
{{with .Post}}<article id="post-{{.ID}}" class="{{.Class}} gs-wrap">
	<h1 class="entry-title">{{.Title}}</h1>
	<div class="entry-content">{{.Content}}</div>
</article>{{end}}
{{template "footer" .}}
""",
    "single.html": """{{template "header" .}}
{{with .Post}}<article id="post-{{.ID}}" class="{{.Class}} gs-wrap">
	<h1 class="entry-title">{{.Title}}</h1>
	<div class="entry-content">{{.Content}}</div>
</article>{{end}}
{{template "footer" .}}
""",
    "404.html": """{{template "header" .}}
<section class="gs-wrap error-404 not-found">
	<h1 class="page-title">Page not found</h1>
	<p>The page you were looking for has moved or never existed.</p>
	{{if goo_on}}{{goo_product_search .}}{{else}}{{template "searchform" .}}{{end}}
</section>
{{template "footer" .}}
""",
    "searchform.html": """<form role="search" method="get" class="search-form" action="{{.HomeURL}}">
	<label><span class="screen-reader-text">Search for:</span>
	<input type="search" class="search-field" placeholder="Search&hellip;" value="{{.SearchQuery}}" name="s" /></label>
	<button type="submit" class="search-submit">Search</button>
</form>
""",
    "content-none.html": """<section class="no-results not-found">
	<h2 class="page-title">Nothing found</h2>
	{{template "searchform" .}}
</section>
""",
}


def theme_json(th):
    return {
        "slug": th["slug"], "name": th["name"], "version": "1.0.0", "description": th["description"],
        "author": "WordPress Go", "author_url": "https://github.com/boracomet/wordpress-go",
        "theme_url": f"https://github.com/boracomet/wordpress-go/tree/main/web/theme-library/{th['slug']}",
        "tags": ["E-Commerce", "Golementor", "Theme Builder", th["tag"], "Custom Colors", "Custom Menu", "Translation Ready", "Full Width Template"],
        "requires_core": "0.1.0", "screenshot": "assets/screenshot.png", "category": th["category"], "style": th["style"],
        "demo": "demo/demo.json", "golementor": "golementor/design.json",
        "menu_locations": [{"id": "primary", "name": "Primary Menu"}, {"id": "footer", "name": "Footer Menu"}],
        "styles": [{"handle": th["slug"] + "-style", "src": "assets/style.css", "deps": [], "media": "all"}],
        "preload_fonts": ["assets/fonts/" + th["font_files"][0][1]],
        "palette": [{"name": v[0], "slug": k, "color": v[1]} for k, v in th["colors"].items()],
        "supports": {"post-thumbnails": True, "title-tag": True, "align-wide": True, "responsive-embeds": True, "woocommerce": True,
                     "html5": ["search-form", "comment-form", "comment-list", "gallery", "caption", "script", "style"]},
    }


def style_css(th):
    faces = "\n".join(f'@font-face {{ font-family: "{fam}"; src: url("fonts/{file}") format("woff2"); font-weight: {w}; font-style: normal; font-display: swap; }}'
                      for fam, file, w in th["font_files"])
    r = th.get("image_radius", 18)
    br = th.get("radius", 999)
    return f"""/* {th['name']}: a Golementor store theme. Layout, colours and type live in the Golementor design
   (Theme Builder parts and Site Settings); this file carries the fonts and the store's finishing touches. */
{faces}

body {{ -webkit-font-smoothing: antialiased; }}
.gs-fallback .site-header {{ display: flex; flex-wrap: wrap; gap: 16px 28px; align-items: center; padding: 18px 24px; }}
.gs-fallback .gs-wrap, .gs-fallback .site-footer {{ max-width: 1240px; margin: 0 auto; padding: 24px; }}

/* Product cards */
.woocommerce ul.products li.product {{ text-align: left; }}
.woocommerce ul.products li.product a img {{ border-radius: {r}px; aspect-ratio: 1; object-fit: cover; background: var(--e-global-color-sand); transition: transform .35s ease; }}
.woocommerce ul.products li.product:hover a img {{ transform: translateY(-3px); }}
.woocommerce ul.products li.product .woocommerce-loop-product__title {{ padding: 4px 0 2px; font-size: 17px; }}
.woocommerce ul.products li.product .price {{ display: block; margin: 2px 0 12px; color: var(--e-global-color-text); font-weight: 600; }}
.woocommerce ul.products li.product .price del {{ opacity: .5; font-weight: 400; }}
.woocommerce ul.products li.product .price ins {{ text-decoration: none; color: var(--e-global-color-accent); }}
.woocommerce ul.products li.product .button {{ border-radius: {br}px; padding: 10px 18px; font-size: 14px; font-weight: 600;
	background: var(--e-global-color-primary); color: var(--e-global-color-paper); transition: background-color .2s; }}
.woocommerce ul.products li.product .button:hover {{ background: var(--e-global-color-accent); color: var(--e-global-color-paper); }}
.woocommerce span.onsale {{ min-height: 0; min-width: 0; padding: 5px 12px; line-height: 1.2; border-radius: 999px; font-size: 12px; font-weight: 700;
	letter-spacing: .04em; text-transform: uppercase; background: var(--e-global-color-accent); color: var(--e-global-color-paper); top: 12px; left: 12px; right: auto; margin: 0; }}
.woocommerce ul.products li.product-category a img {{ border-radius: {r}px; aspect-ratio: 1; object-fit: cover; }}
.woocommerce ul.products li.product-category .woocommerce-loop-category__title {{ text-align: center; font-size: 18px; padding-top: 6px; }}
.woocommerce ul.products li.product-category mark.count {{ background: none; color: var(--e-global-color-muted); font-size: .8em; }}

/* Single product */
.woocommerce div.product .woocommerce-product-gallery__image img {{ border-radius: {r}px; }}
.woocommerce div.product .flex-control-thumbs img {{ border-radius: {max(r // 2, 4)}px; opacity: .7; }}
.woocommerce div.product .flex-control-thumbs img.flex-active, .woocommerce div.product .flex-control-thumbs img:hover {{ opacity: 1; }}
.woocommerce div.product .woocommerce-tabs ul.tabs {{ padding: 0; margin: 0 0 24px; }}
.woocommerce div.product .woocommerce-tabs ul.tabs li {{ border: 0; background: transparent; border-radius: 0; margin: 0 18px 0 0; padding: 0; }}
.woocommerce div.product .woocommerce-tabs ul.tabs li::before, .woocommerce div.product .woocommerce-tabs ul.tabs li::after, .woocommerce div.product .woocommerce-tabs ul.tabs::before {{ display: none; }}
.woocommerce div.product .woocommerce-tabs ul.tabs li a {{ padding: 8px 0; font-weight: 600; border-bottom: 2px solid transparent; }}
.woocommerce div.product .woocommerce-tabs ul.tabs li.active a {{ border-bottom-color: currentColor; }}
.woocommerce div.product form.cart .button, .woocommerce .cart .button, .woocommerce button.button.alt, .woocommerce a.button.alt,
.woocommerce #respond input#submit, .woocommerce a.checkout-button {{ border-radius: {br}px; background: var(--e-global-color-accent); color: var(--e-global-color-paper); }}
.woocommerce div.product form.cart .button:hover, .woocommerce button.button.alt:hover, .woocommerce a.button.alt:hover, .woocommerce a.checkout-button:hover {{ background: var(--e-global-color-primary); color: var(--e-global-color-paper); }}
.woocommerce .quantity .qty {{ border-radius: {min(br, 12)}px; border: 1px solid var(--e-global-color-line); }}
.woocommerce .star-rating {{ color: var(--e-global-color-accent); }}

/* Cart, checkout, notices */
.woocommerce table.shop_table {{ border-radius: {min(r, 14)}px; border-color: var(--e-global-color-line); }}
.woocommerce-cart .cart-collaterals .cart_totals, .woocommerce-checkout #order_review {{ background: var(--e-global-color-sand); padding: 24px; border-radius: {min(r, 18)}px; }}
.woocommerce-checkout #payment {{ background: var(--e-global-color-paper); border-radius: {min(r, 14)}px; }}
.woocommerce-checkout #payment div.payment_box {{ background: var(--e-global-color-cream); }}
.woocommerce-checkout #payment div.payment_box::before {{ border-bottom-color: var(--e-global-color-cream); }}
.woocommerce .coupon #coupon_code {{ width: 170px; }}
.woocommerce table.cart img {{ width: 64px; border-radius: {max(r // 3, 4)}px; }}
.woocommerce-message, .woocommerce-info {{ border-top-color: var(--e-global-color-accent); }}
.woocommerce-message::before, .woocommerce-info::before {{ color: var(--e-global-color-accent); }}
.woocommerce form .form-row input.input-text, .woocommerce form .form-row textarea, .woocommerce form .form-row select {{
	border: 1px solid var(--e-global-color-line); border-radius: {min(br, 10)}px; padding: 11px 14px; background: var(--e-global-color-paper); }}
.woocommerce nav.woocommerce-pagination ul {{ border: 0; }}
.woocommerce nav.woocommerce-pagination ul li {{ border: 0; margin: 0 4px; }}
.woocommerce nav.woocommerce-pagination ul li a, .woocommerce nav.woocommerce-pagination ul li span {{ border-radius: 999px; min-width: 38px; padding: 10px; }}
.woocommerce nav.woocommerce-pagination ul li span.current {{ background: var(--e-global-color-primary); color: var(--e-global-color-paper); }}

/* Header details */
.elementor-widget-woocommerce-menu-cart .goo-menu-cart__count {{ background: var(--e-global-color-accent); }}
.elementor-location-header .goo-lang-switcher a {{ font-size: 12px; }}
{th.get('extra_css', '')}
"""


def readme(th):
    return f"""# {th['name']}

{th['description']}

**Built with Golementor.** Activating the theme loads its design into the Theme Builder: header, footer,
single product, shop and category archive, cart, checkout and my account parts, plus the Site Settings
(global colours and fonts). Edit any of them visually in Appearance › Theme Builder; Save as theme keeps
a copy. The store widgets (products, categories, menu cart, product images, price, add to cart, tabs,
related products, cart, checkout, my account, breadcrumbs) are in the editor's Store panel.

**Golylang ready.** The header and footer carry the language switcher; translate pages, products and
categories with Golylang, and theme parts from the Theme Builder's language links.

The demo imports a complete store: twelve products (with variations and a bundle), four categories,
reviews, coupons (WELCOME10) and shipping, plus a home page, {th['about']['menu']} and Contact built with Golementor.

Fonts: {th['fonts_credit']} (SIL Open Font License), self-hosted. Artwork: original illustrations made for
this theme, dedicated to the public domain (CC0).
"""


def build(th):
    d = os.path.join(LIB, th["slug"])
    os.makedirs(os.path.join(d, "templates"), exist_ok=True)
    os.makedirs(os.path.join(d, "assets", "fonts"), exist_ok=True)
    os.makedirs(os.path.join(d, "golementor"), exist_ok=True)
    os.makedirs(os.path.join(d, "demo", "images"), exist_ok=True)
    for fam, file, _ in th["font_files"]:
        src = None
        for root, _, files in os.walk(LIB):
            if file in files and not root.startswith(d):
                src = os.path.join(root, file)
                break
        if src is None:
            sys.exit(f"font {file} not found in the library")
        shutil.copyfile(src, os.path.join(d, "assets", "fonts", file))
    with open(os.path.join(d, "theme.json"), "w") as f:
        json.dump(theme_json(th), f, indent=2, ensure_ascii=False)
        f.write("\n")
    with open(os.path.join(d, "golementor", "design.json"), "w") as f:
        json.dump(design(th), f, indent=1, ensure_ascii=False)
        f.write("\n")
    with open(os.path.join(d, "demo", "demo.json"), "w") as f:
        json.dump(demo(json.loads(json.dumps(th))), f, indent=1, ensure_ascii=False)
        f.write("\n")
    with open(os.path.join(d, "assets", "style.css"), "w") as f:
        f.write(style_css(th))
    with open(os.path.join(d, "readme.md"), "w") as f:
        f.write(readme(th))
    for name, body in FALLBACK.items():
        with open(os.path.join(d, "templates", name), "w") as f:
            f.write(body)
    print("built", th["slug"])


if __name__ == "__main__":
    want = sys.argv[1:]
    for th in THEMES:
        if not want or th["slug"] in want:
            build(th)
