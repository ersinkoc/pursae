#!/usr/bin/env python3
"""Draws the demo images of the Pursae Web Builder store themes: flat, soft-gradient illustrations with a
subtle grain, a floor line and soft shadows, in each theme's palette. Original artwork, dedicated to
the public domain (CC0). Needs Pillow.

    python3 tools/goo-golementor-themes/art.py [slug ...]
"""

import math
import os
import random
import sys

from PIL import Image, ImageChops, ImageDraw, ImageFilter

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
LIB = os.path.join(ROOT, "library", "themes")
SS = 2  # supersampling


def hexc(h, a=255):
    h = h.lstrip("#")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16), a)


def mix(c1, c2, t):
    return tuple(int(round(c1[i] + (c2[i] - c1[i]) * t)) for i in range(4))


def shade(c, f):
    """f < 1 darkens, f > 1 lightens."""
    if f < 1:
        return mix(c, (0, 0, 0, c[3]), 1 - f)
    return mix(c, (255, 255, 255, c[3]), f - 1)


class Canvas:
    def __init__(self, w, h, top, bottom, floor=0.78, seed=1, transparent=False):
        self.W, self.H = w * SS, h * SS
        self.w, self.h = w, h
        self.img = Image.new("RGBA", (self.W, self.H))
        self.rand = random.Random(seed)
        if transparent:  # a layer for scenes: no background, same floor
            self.floor = int(self.H * floor)
            return
        grad = Image.linear_gradient("L").resize((self.W, self.H))
        self.img = Image.composite(Image.new("RGBA", (self.W, self.H), bottom), Image.new("RGBA", (self.W, self.H), top), grad)
        self.floor = int(self.H * floor) if floor else None
        if floor:
            band = Image.new("RGBA", (self.W, self.H - self.floor), shade(bottom, 0.94))
            self.img.alpha_composite(band, (0, self.floor))
            d = ImageDraw.Draw(self.img)
            d.line([(0, self.floor), (self.W, self.floor)], fill=shade(bottom, 0.86), width=3 * SS)
        # soft light from the top left
        glow = Image.new("L", (self.W, self.H), 0)
        ImageDraw.Draw(glow).ellipse([-self.W * 0.3, -self.H * 0.5, self.W * 0.7, self.H * 0.6], fill=60)
        glow = glow.filter(ImageFilter.GaussianBlur(self.W // 8))
        self.img = Image.composite(Image.new("RGBA", (self.W, self.H), (255, 255, 255, 255)), self.img, glow.point(lambda v: v // 3))

    def s(self, v):
        return v * SS

    def P(self, pts):
        return [(x * SS, y * SS) for x, y in pts]

    def _layer(self, draw_fn):
        m = Image.new("L", (self.W, self.H), 0)
        draw_fn(ImageDraw.Draw(m))
        return m

    def fill(self, mask, c1, c2=None, angle=90, light=True):
        """Fills mask with a gradient c1 → c2 (angle 90 = top to bottom, 0 = left to right)."""
        c2 = c2 or shade(c1, 0.82)
        box = mask.getbbox()
        if not box:
            return
        x0, y0, x1, y1 = box
        bw, bh = max(x1 - x0, 1), max(y1 - y0, 1)
        g = Image.linear_gradient("L").resize((bw, bh)) if angle == 90 else Image.linear_gradient("L").rotate(90, expand=True).transpose(Image.FLIP_LEFT_RIGHT).resize((bw, bh))
        tile = Image.composite(Image.new("RGBA", (bw, bh), c2), Image.new("RGBA", (bw, bh), c1), g)
        layer = Image.new("RGBA", (self.W, self.H), (0, 0, 0, 0))
        layer.paste(tile, (x0, y0))
        alpha = ImageChops.multiply(mask, Image.new("L", (self.W, self.H), c1[3]))
        layer.putalpha(alpha)
        self.img.alpha_composite(layer)

    def poly(self, pts, c1, c2=None, angle=90):
        self.fill(self._layer(lambda d: d.polygon(self.P(pts), fill=255)), c1, c2, angle)

    def ellipse(self, x0, y0, x1, y1, c1, c2=None, angle=90):
        self.fill(self._layer(lambda d: d.ellipse([x0 * SS, y0 * SS, x1 * SS, y1 * SS], fill=255)), c1, c2, angle)

    def rrect(self, x0, y0, x1, y1, r, c1, c2=None, angle=90):
        self.fill(self._layer(lambda d: d.rounded_rectangle([x0 * SS, y0 * SS, x1 * SS, y1 * SS], radius=r * SS, fill=255)), c1, c2, angle)

    def pie(self, x0, y0, x1, y1, a0, a1, c1, c2=None):
        self.fill(self._layer(lambda d: d.pieslice([x0 * SS, y0 * SS, x1 * SS, y1 * SS], a0, a1, fill=255)), c1, c2)

    def line(self, pts, color, width, curve=False):
        d = ImageDraw.Draw(self.img)
        d.line(self.P(pts), fill=color, width=int(width * SS), joint="curve")
        if curve:
            for x, y in pts:
                r = width * SS / 2
                d.ellipse([x * SS - r, y * SS - r, x * SS + r, y * SS + r], fill=color)

    def arc(self, x0, y0, x1, y1, a0, a1, color, width):
        ImageDraw.Draw(self.img).arc([x0 * SS, y0 * SS, x1 * SS, y1 * SS], a0, a1, fill=color, width=int(width * SS))

    def shadow(self, cx, cy, w, h, alpha=70):
        m = Image.new("L", (self.W, self.H), 0)
        ImageDraw.Draw(m).ellipse([(cx - w / 2) * SS, (cy - h / 2) * SS, (cx + w / 2) * SS, (cy + h / 2) * SS], fill=alpha)
        m = m.filter(ImageFilter.GaussianBlur(h * SS / 2.2))
        layer = Image.new("RGBA", (self.W, self.H), (20, 20, 30, 255))
        layer.putalpha(m)
        self.img.alpha_composite(layer)

    def bezier(self, p0, p1, p2, n=40):
        return [((1 - t) ** 2 * p0[0] + 2 * (1 - t) * t * p1[0] + t * t * p2[0], (1 - t) ** 2 * p0[1] + 2 * (1 - t) * t * p1[1] + t * t * p2[1])
                for t in (i / n for i in range(n + 1))]

    def save(self, path, quality=86):
        img = self.img.resize((self.w, self.h), Image.LANCZOS).convert("RGB")
        noise = Image.effect_noise((self.w, self.h), 18).convert("RGB")
        img = Image.blend(img, ImageChops.overlay(img, noise), 0.12)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        q = quality
        while True:
            img.save(path, "JPEG", quality=q, optimize=True, progressive=True)
            if os.path.getsize(path) <= 1 << 20 or q < 50:
                break
            q -= 6
        return path


# ---------------------------------------------------------------------------------------------------
# Motifs (coordinates are in output pixels; most take a centre x, a floor y and a scale)

def leaf(cv, cx, cy, length, width, angle, c, vein=True):
    a = math.radians(angle)
    ux, uy = math.cos(a), math.sin(a)
    nx, ny = -uy, ux
    pts = []
    for i in range(0, 41):
        t = i / 40
        r = math.sin(math.pi * t) ** 0.8 * width / 2
        pts.append((cx + ux * length * t + nx * r, cy + uy * length * t + ny * r))
    for i in range(40, -1, -1):
        t = i / 40
        r = math.sin(math.pi * t) ** 0.8 * width / 2
        pts.append((cx + ux * length * t - nx * r, cy + uy * length * t - ny * r))
    cv.poly(pts, shade(c, 1.08), shade(c, 0.86))
    if vein:
        cv.line([(cx, cy), (cx + ux * length * 0.92, cy + uy * length * 0.92)], shade(c, 0.78), max(2, width / 30))


def monstera_leaf(cv, cx, cy, size, angle, c, bg):
    leaf(cv, cx, cy, size, size * 0.9, angle, c, vein=True)
    a = math.radians(angle)
    ux, uy = math.cos(a), math.sin(a)
    nx, ny = -uy, ux
    for k, t in enumerate([0.3, 0.5, 0.7]):
        for side in (1, -1):
            px0 = cx + ux * size * t + nx * side * size * 0.18
            py0 = cy + uy * size * t + ny * side * size * 0.18
            px1 = cx + ux * size * (t + 0.08) + nx * side * size * 0.5
            py1 = cy + uy * size * (t + 0.08) + ny * side * size * 0.5
            cv.line([(px0, py0), (px1, py1)], bg, size * 0.035)


def pot(cv, cx, floor, w, h, c, rim=True, band=None):
    cv.shadow(cx, floor, w * 1.15, h * 0.16)
    top = floor - h
    cv.poly([(cx - w / 2, top + h * 0.12), (cx + w / 2, top + h * 0.12), (cx + w * 0.38, floor), (cx - w * 0.38, floor)], shade(c, 1.06), shade(c, 0.85), angle=0)
    if rim:
        cv.rrect(cx - w * 0.54, top, cx + w * 0.54, top + h * 0.2, h * 0.04, shade(c, 1.12), shade(c, 0.9), angle=0)
    if band:
        cv.poly([(cx - w * 0.47, top + h * 0.45), (cx + w * 0.47, top + h * 0.45), (cx + w * 0.455, top + h * 0.55), (cx - w * 0.455, top + h * 0.55)], band)
    return top


def soil(cv, cx, top, w, c=hexc("#4A3426")):
    cv.ellipse(cx - w * 0.46, top - w * 0.03, cx + w * 0.46, top + w * 0.06, c)


def sparkle(cv, x, y, r, c):
    cv.poly([(x, y - r), (x + r * 0.25, y - r * 0.25), (x + r, y), (x + r * 0.25, y + r * 0.25), (x, y + r), (x - r * 0.25, y + r * 0.25),
             (x - r, y), (x - r * 0.25, y - r * 0.25)], c)


def star(cv, cx, cy, r, c, c2=None, points=5, inner=0.48, rot=-90):
    pts = []
    for i in range(points * 2):
        rr = r if i % 2 == 0 else r * inner
        a = math.radians(rot + i * 180 / points)
        pts.append((cx + rr * math.cos(a), cy + rr * math.sin(a)))
    cv.poly(pts, c, c2)


def box3d(cv, x, y, w, h, d, c):
    """An isometric-ish box: front face at (x, y, w, h), depth d up-right."""
    cv.poly([(x, y), (x + d, y - d * 0.6), (x + w + d, y - d * 0.6), (x + w, y)], shade(c, 1.12))
    cv.poly([(x + w, y), (x + w + d, y - d * 0.6), (x + w + d, y + h - d * 0.6), (x + w, y + h)], shade(c, 0.82))
    cv.rrect(x, y, x + w, y + h, 2, c, shade(c, 0.92))


# ---------------------------------------------------------------------------------------------------
# Products

def draw_product(th, key, cv, S):
    C = {k: hexc(v[1]) for k, v in th["colors"].items()}
    W, F = cv.w, cv.floor // SS
    cx = W / 2
    g1, g2, terra = hexc("#3F7A4E"), hexc("#2E5E3C"), hexc("#C2613F")
    # ---- Fernwood
    if key == "monstera-deliciosa":
        top = pot(cv, cx, F, 360, 300, terra)
        for ang, ln, x in [(-120, 360, -10), (-70, 380, 20), (-155, 300, -30), (-30, 300, 30), (-95, 420, 0)]:
            cv.line([(cx + x * 0.3, top + 10), (cx + x + ln * 0.55 * math.cos(math.radians(ang)), top + ln * 0.55 * math.sin(math.radians(ang)))], g2, 9)
            monstera_leaf(cv, cx + x + ln * 0.5 * math.cos(math.radians(ang)), top + ln * 0.5 * math.sin(math.radians(ang)), 300, ang, g1, cv.img.getpixel((10, 10)))
        soil(cv, cx, top + 30, 380)
    elif key == "fiddle-leaf-fig":
        top = pot(cv, cx, F, 330, 290, hexc("#E8E1D4"), band=hexc("#C9BFAE"))
        cv.line([(cx, top + 20), (cx, top - 520)], hexc("#7A5A3C"), 16)
        for i, y in enumerate(range(0, 520, 70)):
            side = -1 if i % 2 else 1
            leaf(cv, cx, top - 90 - y, 210 - y * 0.12, 150 - y * 0.08, -90 + side * (55 - y * 0.03), g1 if i % 3 else shade(g1, 1.1))
    elif key == "snake-plant":
        top = pot(cv, cx, F, 330, 280, hexc("#F1ECE2"), band=terra)
        for x, h, ang in [(-70, 520, -100), (-25, 640, -93), (25, 600, -86), (70, 480, -78), (0, 420, -90)]:
            leaf(cv, cx + x, top + 20, h, 90, ang, hexc("#4D7A3E"))
            a = math.radians(ang)
            for t in [0.25, 0.45, 0.65]:
                px0, py0 = cx + x + math.cos(a) * h * t, top + 20 + math.sin(a) * h * t
                cv.line([(px0 - 25, py0 + 6), (px0 + 25, py0 - 6)], hexc("#B8C76A"), 6)
    elif key == "golden-pothos":
        top = pot(cv, cx, F - 160, 300, 240, terra)
        cv.shadow(cx, F, 300, 30)
        for side in (-1, 1):
            pts = cv.bezier((cx + side * 120, top + 40), (cx + side * 330, top + 300), (cx + side * 220, F - 20))
            cv.line(pts, g2, 7)
            for i in range(2, len(pts), 6):
                leaf(cv, pts[i][0], pts[i][1], 90, 80, (60 if side > 0 else 120) + (i % 12) * 3, hexc("#5E9A45") if i % 12 else hexc("#C9B84A"))
        for ang in range(-170, -10, 22):
            leaf(cv, cx, top + 10, 150, 120, ang, hexc("#5E9A45") if ang % 3 else hexc("#86A84A"))
    elif key == "calathea-orbifolia":
        top = pot(cv, cx, F, 320, 260, hexc("#2F3B33"))
        for ang, ln in [(-150, 300), (-115, 360), (-80, 380), (-45, 330), (-20, 260), (-165, 240)]:
            a = math.radians(ang)
            ex, ey = cx + math.cos(a) * ln * 0.6, top + math.sin(a) * ln * 0.6
            cv.line([(cx, top + 10), (ex, ey)], g2, 7)
            cv.ellipse(ex - 120, ey - 110, ex + 120, ey + 110, hexc("#6F9A62"), hexc("#557E4C"))
            for k in range(-3, 4):
                cv.line([(ex - 100 + abs(k) * 12, ey + k * 26), (ex + 100 - abs(k) * 12, ey + k * 26)], hexc("#A8C49A"), 5)
    elif key == "terracotta-pot":
        for x, w, h in [(-260, 200, 170), (0, 300, 260), (250, 240, 210)]:
            pot(cv, cx + x, F, w, h, terra if x != 250 else shade(terra, 1.1))
    elif key == "ceramic-planter":
        for x, c in [(-180, hexc("#EFEAE0")), (180, hexc("#AFC2A5"))]:
            cv.shadow(cx + x, F, 330, 34)
            cv.rrect(cx + x - 150, F - 300, cx + x + 150, F, 70, c, shade(c, 0.86), angle=0)
            rnd = random.Random(x)
            for _ in range(60):
                px0, py0 = cx + x + rnd.uniform(-130, 130), F - rnd.uniform(20, 280)
                cv.ellipse(px0, py0, px0 + 5, py0 + 5, shade(c, 0.6))
            leaf(cv, cx + x, F - 290, 180, 110, -60 if x > 0 else -120, g1)
            leaf(cv, cx + x, F - 290, 160, 100, -95, shade(g1, 1.1))
    elif key == "hanging-planter":
        rope = hexc("#E9DCC4")
        cv.arc(cx - 40, 60, cx + 40, 140, 0, 360, hexc("#B48A5A"), 10)
        for dx in (-150, -50, 50, 150):
            cv.line([(cx, 140), (cx + dx * 0.4, 420), (cx + dx, 650)], rope, 8)
        for y in (420, 520):
            for dx in (-110, 0, 110):
                cv.ellipse(cx + dx - 14, y - 14, cx + dx + 14, y + 14, shade(rope, 0.9))
        top = pot(cv, cx, 880, 300, 240, terra)
        for ang in (-160, -120, -60, -20):
            leaf(cv, cx, top + 20, 150, 100, ang, g1)
        for side in (-1, 1):
            pts = cv.bezier((cx + side * 100, top + 60), (cx + side * 230, top + 260), (cx + side * 160, top + 420))
            cv.line(pts, g2, 6)
            for i in range(4, len(pts), 8):
                leaf(cv, pts[i][0], pts[i][1], 70, 60, 90, hexc("#5E9A45"))
        cv.shadow(cx, F, 260, 26, 40)
    elif key == "watering-can":
        brass, dk = hexc("#C9A24A"), hexc("#9C7A2E")
        cv.shadow(cx, F, 560, 40)
        cv.poly([(cx - 230, F - 290), (cx - 510, F - 560), (cx - 478, F - 586), (cx - 210, F - 370)], brass, dk, angle=0)
        cv.rrect(cx - 260, F - 400, cx + 200, F, 60, brass, dk, angle=0)
        cv.arc(cx - 60, F - 620, cx + 300, F - 260, 270, 90, dk, 26)
        cv.ellipse(cx - 536, F - 612, cx - 452, F - 540, dk)
    elif key == "plant-food":
        cv.shadow(cx, F, 300, 34)
        cv.rrect(cx - 130, F - 500, cx + 130, F, 40, hexc("#EAF0E3"), hexc("#C9D7BE"), angle=0)
        cv.rrect(cx - 60, F - 580, cx + 60, F - 490, 14, C["primary"])
        cv.rrect(cx - 105, F - 360, cx + 105, F - 150, 14, C["primary"])
        leaf(cv, cx - 30, F - 210, 110, 70, -50, hexc("#8FB879"))
        leaf(cv, cx + 10, F - 210, 100, 64, -120, hexc("#A9C98F"))
    elif key == "pruning-shears":
        cv.shadow(cx, F, 520, 30)
        steel = hexc("#B9C2C7")
        cv.poly([(cx - 40, F - 260), (cx + 330, F - 520), (cx + 350, F - 490), (cx + 10, F - 220)], steel, shade(steel, 0.8))
        cv.poly([(cx - 40, F - 210), (cx + 340, F - 300), (cx + 330, F - 260), (cx - 10, F - 180)], shade(steel, 1.05), shade(steel, 0.8))
        cv.ellipse(cx - 40, F - 260, cx + 20, F - 200, hexc("#6E7579"))
        wood = hexc("#7A4A2E")
        cv.poly([(cx - 30, F - 240), (cx - 380, F - 120), (cx - 360, F - 80), (cx - 10, F - 210)], wood, shade(wood, 0.8))
        cv.poly([(cx - 20, F - 205), (cx - 330, F - 30), (cx - 300, F - 10), (cx + 10, F - 190)], shade(wood, 1.1), shade(wood, 0.85))
    elif key == "plant-gift-box":
        box3d(cv, cx - 330, F - 300, 520, 300, 120, hexc("#D9C6A5"))
        cv.rrect(cx - 90, F - 300, cx - 30, F, 0, terra)
        for x in (-230, 60):
            top = pot(cv, cx + x, F - 290, 160, 140, terra)
            for ang in (-130, -90, -50):
                leaf(cv, cx + x, top + 10, 140, 70, ang, g1)
        cv.rrect(cx + 150, F - 520, cx + 230, F - 300, 18, hexc("#EAF0E3"))
    # ---- Good Boy Supply
    elif key == "orthopedic-dog-bed":
        cv.shadow(cx, F, 820, 60)
        cv.rrect(cx - 400, F - 280, cx + 400, F, 120, C["secondary"], shade(C["secondary"], 0.8))
        cv.rrect(cx - 330, F - 240, cx + 330, F - 60, 80, C["skyl"], shade(C["skyl"], 0.9))
        cv.rrect(cx - 400, F - 120, cx + 400, F, 50, shade(C["secondary"], 0.9))
        star(cv, cx + 260, F - 330, 30, C["sun"])
    elif key == "leather-leash":
        leather = hexc("#9A5B34")
        pts = cv.bezier((cx - 380, F - 120), (cx - 100, F - 760), (cx + 300, F - 300))
        cv.line(pts, leather, 36, curve=True)
        cv.arc(cx - 470, F - 260, cx - 290, F - 60, 0, 360, leather, 34)
        cv.rrect(cx + 280, F - 330, cx + 340, F - 240, 16, hexc("#D8B04A"))
        cv.arc(cx + 290, F - 260, cx + 380, F - 160, 200, 520, hexc("#D8B04A"), 14)
        cv.shadow(cx, F, 700, 30, 40)
    elif key == "reflective-harness":
        coral = C["accent"]
        cv.shadow(cx, F, 620, 40)
        # the padded vest: a saddle shape with a neck opening, straps looping under the chest
        cv.arc(cx - 330, F - 420, cx - 90, F - 40, 90, 270, C["primary"], 34)
        cv.arc(cx + 90, F - 420, cx + 330, F - 40, 270, 450, C["primary"], 34)
        cv.poly([(cx - 300, F - 470), (cx - 120, F - 600), (cx + 120, F - 600), (cx + 300, F - 470), (cx + 260, F - 200),
                 (cx + 80, F - 140), (cx - 80, F - 140), (cx - 260, F - 200)], coral, shade(coral, 0.8))
        cv.poly([(cx - 240, F - 440), (cx - 100, F - 540), (cx + 100, F - 540), (cx + 240, F - 440), (cx + 205, F - 240),
                 (cx + 60, F - 195), (cx - 60, F - 195), (cx - 205, F - 240)], shade(coral, 1.08), shade(coral, 0.9))
        for y in (F - 470, F - 330):
            cv.line([(cx - 210, y), (cx + 210, y)], hexc("#F4F6FA"), 8)
        cv.arc(cx - 40, F - 660, cx + 40, F - 580, 0, 360, hexc("#C0C6D0"), 14)
        cv.rrect(cx - 60, F - 610, cx + 60, F - 570, 10, C["primary"])
    elif key == "ceramic-bowl":
        for x, c in [(-200, C["skyl"]), (200, C["sand"])]:
            cv.shadow(cx + x, F, 360, 40)
            cv.pie(cx + x - 180, F - 340, cx + x + 180, F + 20, 0, 180, c, shade(c, 0.8))
            cv.ellipse(cx + x - 180, F - 200, cx + x + 180, F - 140, shade(c, 0.88))
            cv.ellipse(cx + x - 150, F - 192, cx + x + 150, F - 150, hexc("#9A6A3E"))
            for i in range(6):
                cv.ellipse(cx + x - 120 + i * 40, F - 190, cx + x - 92 + i * 40, F - 168, hexc("#B07A46"))
    elif key == "cat-tree":
        sisal = hexc("#D7BE8E")
        cv.shadow(cx, F, 620, 40)
        cv.rrect(cx - 300, F - 60, cx + 300, F, 20, C["secondary"])
        for x, y0, y1 in [(-180, F - 60, F - 520), (150, F - 60, F - 380), (-30, F - 520, F - 820)]:
            cv.rrect(cx + x - 30, y1, cx + x + 30, y0, 8, sisal, shade(sisal, 0.85), angle=0)
            for yy in range(int(y1) + 20, int(y0), 26):
                cv.line([(cx + x - 30, yy), (cx + x + 30, yy + 8)], shade(sisal, 0.8), 3)
        cv.rrect(cx - 280, F - 560, cx + 40, F - 510, 18, C["secondary"])
        cv.rrect(cx + 20, F - 440, cx + 300, F - 380, 18, C["secondary"])
        cv.rrect(cx + 60, F - 380, cx + 260, F - 220, 40, shade(C["secondary"], 0.9))
        cv.ellipse(cx + 110, F - 330, cx + 210, F - 240, C["deep"])
        cv.pie(cx - 190, F - 950, cx + 130, F - 760, 0, 180, C["sun"], shade(C["sun"], 0.85))
    elif key == "cat-scratcher":
        card = hexc("#C79E6A")
        cv.shadow(cx, F, 760, 40)
        pts = [(cx - 380 + t * 760, F - 130 - 70 * math.sin(t * math.pi * 1.4)) for t in (i / 60 for i in range(61))]
        cv.poly(pts + [(cx + 380, F), (cx - 380, F)], card, shade(card, 0.8))
        for i in range(0, 760, 22):
            x = cx - 380 + i
            cv.line([(x, F - 10), (x, F - 120 - 70 * math.sin(i / 760 * math.pi * 1.4))], shade(card, 0.85), 3)
        leaf(cv, cx + 200, F - 260, 90, 50, -60, hexc("#7CB36A"))
    elif key == "feather-wand":
        cv.line([(cx - 380, F - 60), (cx + 240, F - 640)], hexc("#C9A86A"), 16)
        for i, c in enumerate([C["accent"], C["sun"], C["secondary"], C["accent"]]):
            leaf(cv, cx + 240, F - 640, 260 - i * 20, 70, -20 - i * 25, c, vein=True)
        cv.ellipse(cx + 210, F - 610, cx + 270, F - 550, hexc("#D8B04A"))
        cv.shadow(cx, F, 600, 26, 40)
    elif key == "rope-tug":
        cv.shadow(cx, F, 760, 40)
        pts = cv.bezier((cx - 360, F - 160), (cx, F - 560), (cx + 360, F - 160))
        for i, (x, y) in enumerate(pts):
            c = C["accent"] if (i // 3) % 2 else C["cream"]
            cv.ellipse(x - 40, y - 40, x + 40, y + 40, c, shade(c, 0.85))
        for x, y in [pts[0], pts[-1]]:
            cv.ellipse(x - 80, y - 70, x + 80, y + 90, C["primary"], shade(C["primary"], 0.8))
    elif key == "squeaky-bone":
        c = C["sun"]
        cv.shadow(cx, F, 700, 40)
        cv.rrect(cx - 260, F - 260, cx + 260, F - 140, 60, c, shade(c, 0.85))
        for x in (-280, 280):
            cv.ellipse(cx + x - 90, F - 330, cx + x + 60, F - 180, c, shade(c, 0.85))
            cv.ellipse(cx + x - 90, F - 220, cx + x + 60, F - 70, c, shade(c, 0.85))
    elif key == "grooming-brush":
        wood = hexc("#B98352")
        cv.shadow(cx, F, 620, 34)
        cv.rrect(cx - 300, F - 230, cx + 120, F - 70, 80, wood, shade(wood, 0.8))
        cv.rrect(cx + 80, F - 190, cx + 380, F - 120, 35, wood, shade(wood, 0.8))
        for i in range(14):
            x = cx - 270 + i * 26
            cv.line([(x, F - 230), (x, F - 290)], hexc("#2D2F3A"), 6)
    elif key == "salmon-treats":
        bag = C["skyl"]
        cv.shadow(cx, F, 420, 40)
        cv.poly([(cx - 190, F - 560), (cx + 190, F - 560), (cx + 220, F), (cx - 220, F)], bag, shade(bag, 0.85), angle=0)
        cv.rrect(cx - 200, F - 600, cx + 200, F - 540, 10, C["primary"])
        cv.ellipse(cx - 110, F - 420, cx + 110, F - 280, C["accent"])
        cv.poly([(cx + 100, F - 350), (cx + 170, F - 400), (cx + 170, F - 300)], C["accent"])
        for i in range(5):
            x = cx - 330 + i * 40 if i < 2 else cx + 200 + (i - 2) * 40
            cv.ellipse(x, F - 50, x + 46, F - 14, hexc("#E7926B"))
    elif key == "puppy-starter-kit":
        box3d(cv, cx - 340, F - 300, 540, 300, 120, C["sun"])
        cv.rrect(cx - 120, F - 300, cx - 60, F, 0, C["accent"])
        cv.arc(cx - 260, F - 520, cx - 40, F - 300, 180, 360, hexc("#9A5B34"), 26)
        for i, (x, y) in enumerate(cv.bezier((cx + 40, F - 300), (cx + 140, F - 520), (cx + 260, F - 330), 14)):
            cv.ellipse(x - 26, y - 26, x + 26, y + 26, C["accent"] if i % 2 else C["cream"])
    # ---- Kite & Crayon
    elif key == "wooden-rainbow-stacker":
        cols = ["#EE5A3C", "#F28C3A", "#FFCB47", "#7CC47F", "#3FA58C", "#4F86C6", "#7C68C4"]
        cv.shadow(cx, F, 860, 50)
        for i, c in enumerate(cols):
            r = 410 - i * 52
            cv.pie(cx - r, F - r, cx + r, F + r, 180, 360, hexc(c), shade(hexc(c), 0.85))
        cv.pie(cx - 46, F - 46, cx + 46, F + 46, 180, 360, cv.img.getpixel((5, F * SS + 20)))
    elif key == "plush-fox":
        fox, cream = C["accent"], hexc("#FFF4E6")
        cv.shadow(cx, F, 520, 50)
        cv.ellipse(cx - 220, F - 420, cx + 220, F, fox, shade(fox, 0.85))
        cv.ellipse(cx - 120, F - 320, cx + 120, F - 20, cream)
        cv.poly([(cx + 180, F - 120), (cx + 420, F - 300), (cx + 380, F - 60)], fox, shade(fox, 0.85))
        cv.poly([(cx + 380, F - 260), (cx + 430, F - 310), (cx + 400, F - 220)], cream)
        cv.ellipse(cx - 200, F - 760, cx + 200, F - 400, fox, shade(fox, 0.9))
        cv.poly([(cx - 190, F - 680), (cx - 150, F - 880), (cx - 60, F - 730)], fox)
        cv.poly([(cx + 190, F - 680), (cx + 150, F - 880), (cx + 60, F - 730)], fox)
        cv.poly([(cx - 200, F - 560), (cx, F - 420), (cx + 200, F - 560), (cx, F - 500)], cream)
        cv.ellipse(cx - 90, F - 620, cx - 54, F - 584, C["primary"])
        cv.ellipse(cx + 54, F - 620, cx + 90, F - 584, C["primary"])
        cv.ellipse(cx - 22, F - 520, cx + 22, F - 490, C["primary"])
    elif key == "building-blocks":
        cols = [C["accent"], C["sun"], C["secondary"], hexc("#4F86C6"), hexc("#E9D7B9")]
        rnd = random.Random(3)
        for row in range(3):
            for i in range(4 - row):
                x = cx - 330 + row * 90 + i * 180
                y = F - 150 - row * 150
                box3d(cv, x, y, 150, 150, 40, cols[(row * 3 + i) % 5])
        cv.poly([(cx - 70, F - 600), (cx + 30, F - 760), (cx + 130, F - 600)], C["accent"], shade(C["accent"], 0.85))
        cv.shadow(cx, F, 760, 30, 40)
    elif key == "pull-along-duck":
        y = C["sun"]
        cv.shadow(cx, F, 560, 40)
        cv.ellipse(cx - 260, F - 380, cx + 220, F - 100, y, shade(y, 0.85))
        cv.ellipse(cx + 80, F - 600, cx + 300, F - 380, y, shade(y, 0.88))
        cv.poly([(cx + 290, F - 500), (cx + 400, F - 480), (cx + 290, F - 450)], C["accent"])
        cv.ellipse(cx + 200, F - 540, cx + 236, F - 504, C["primary"])
        cv.pie(cx - 200, F - 340, cx + 60, F - 140, 180, 360, shade(y, 0.82))
        for x in (-160, 120):
            cv.ellipse(cx + x - 70, F - 140, cx + x + 70, F, C["secondary"], shade(C["secondary"], 0.8))
            cv.ellipse(cx + x - 18, F - 88, cx + x + 18, F - 52, C["cream"])
        cv.line([(cx - 260, F - 150), (cx - 420, F - 60), (cx - 520, F - 40)], C["primary"], 6)
    elif key == "world-puzzle":
        cv.shadow(cx, F, 840, 40)
        cv.rrect(cx - 400, F - 520, cx + 400, F - 40, 18, hexc("#7CC6E0"), hexc("#5AAFCF"))
        land = hexc("#8BC77B")
        for pts in ([(-300, -420), (-160, -460), (-120, -330), (-220, -250), (-310, -300)],
                    [(-200, -230), (-120, -200), (-140, -90), (-210, -120)],
                    [(-20, -440), (120, -460), (150, -380), (60, -340), (-10, -370)],
                    [(10, -320), (110, -300), (90, -140), (20, -170)],
                    [(160, -430), (330, -450), (360, -330), (230, -300), (170, -340)],
                    [(250, -190), (340, -170), (320, -100), (240, -110)]):
            cv.poly([(cx + x, F + y) for x, y in pts], land, shade(land, 0.88))
        for i in range(1, 4):
            cv.line([(cx - 400 + i * 200, F - 520), (cx - 400 + i * 200, F - 40)], hexc("#4B9DBE"), 3)
        cv.line([(cx - 400, F - 280), (cx + 400, F - 280)], hexc("#4B9DBE"), 3)
        cv.rrect(cx + 300, F - 120, cx + 420, F + 0, 10, C["sun"])
    elif key == "memory-game":
        for i, (x, y, a, c) in enumerate([(-250, -260, 0, C["accent"]), (-40, -300, 0, C["secondary"]), (170, -250, 0, C["sun"]),
                                          (-150, -80, 0, hexc("#4F86C6")), (60, -60, 0, C["accent"])]):
            cv.shadow(cx + x + 100, F + y + 230, 220, 30, 40)
            cv.rrect(cx + x, F + y, cx + x + 200, F + y + 220, 20, C["paper"])
            cv.rrect(cx + x + 14, F + y + 14, cx + x + 186, F + y + 206, 14, c, shade(c, 0.85))
            cv.ellipse(cx + x + 60, F + y + 70, cx + x + 140, F + y + 150, C["cream"])
    elif key == "crayon-set":
        cols = ["#EE5A3C", "#F28C3A", "#FFCB47", "#7CC47F", "#3FA58C", "#4F86C6", "#7C68C4", "#26283B"]
        cv.shadow(cx, F, 820, 40)
        for i, c in enumerate(cols):
            x = cx - 380 + i * 98
            cv.rrect(x, F - 420 + (i % 2) * 40, x + 76, F, 12, hexc(c), shade(hexc(c), 0.8), angle=0)
            cv.poly([(x, F - 420 + (i % 2) * 40), (x + 38, F - 520 + (i % 2) * 40), (x + 76, F - 420 + (i % 2) * 40)], shade(hexc(c), 0.9))
            cv.rrect(x, F - 300, x + 76, F - 250, 0, C["cream"])
    elif key == "paint-easel":
        wood = hexc("#D8AE7A")
        cv.shadow(cx, F, 640, 40)
        cv.line([(cx - 260, F), (cx - 60, F - 860)], wood, 26)
        cv.line([(cx + 260, F), (cx + 60, F - 860)], wood, 26)
        cv.rrect(cx - 230, F - 760, cx + 230, F - 360, 12, hexc("#2F3A36"))
        cv.rrect(cx - 250, F - 360, cx + 250, F - 330, 8, wood)
        cv.arc(cx - 160, F - 700, cx + 40, F - 500, 180, 360, C["sun"], 14)
        star(cv, cx + 110, F - 620, 60, C["accent"])
        for i, c in enumerate([C["accent"], C["secondary"], C["sun"]]):
            cv.rrect(cx - 220 + i * 80, F - 420, cx - 170 + i * 80, F - 360, 8, c)
    elif key == "sketchbook":
        for x, c, a in [(-120, C["sun"], 0), (120, C["secondary"], 0)]:
            cv.shadow(cx + x, F, 460, 30, 50)
        cv.rrect(cx - 360, F - 360, cx + 120, F - 20, 16, C["sun"], shade(C["sun"], 0.88))
        cv.rrect(cx - 120, F - 420, cx + 360, F - 80, 16, C["secondary"], shade(C["secondary"], 0.88))
        cv.rrect(cx - 80, F - 380, cx + 320, F - 120, 10, C["paper"])
        cv.arc(cx - 20, F - 340, cx + 140, F - 180, 200, 520, C["accent"], 10)
        cv.line([(cx + 160, F - 300), (cx + 280, F - 200)], hexc("#4F86C6"), 10)
    elif key == "star-night-light":
        for r, a in [(420, 34), (330, 46), (250, 60)]:  # a soft halo of light
            glow = shade(C["sun"], 1.45)
            cv.ellipse(cx - r, F - 330 - r, cx + r, F - 330 + r, (glow[0], glow[1], glow[2], a))
        cv.shadow(cx, F, 420, 36)
        star(cv, cx, F - 330, 300, shade(C["sun"], 1.1), C["sun"], inner=0.5)
        for x, y in [(-360, -700), (320, -760), (380, -420), (-400, -380)]:
            sparkle(cv, cx + x, F + y, 30, C["paper"])
    elif key == "kids-backpack":
        for x, c in [(-190, C["accent"]), (190, C["secondary"])]:
            cv.shadow(cx + x, F, 340, 34)
            cv.arc(cx + x - 90, F - 640, cx + x + 90, F - 470, 180, 360, shade(c, 0.8), 22)
            cv.rrect(cx + x - 160, F - 560, cx + x + 160, F, 70, c, shade(c, 0.85), angle=0)
            cv.rrect(cx + x - 120, F - 260, cx + x + 120, F - 60, 40, shade(c, 0.9))
            cv.rrect(cx + x - 50, F - 470, cx + x + 50, F - 400, 12, C["cream"])
    elif key == "birthday-bundle":
        box3d(cv, cx - 330, F - 320, 520, 320, 120, C["secondary"])
        cv.rrect(cx - 100, F - 320, cx - 40, F, 0, C["sun"])
        cv.poly([(cx - 70, F - 330), (cx - 230, F - 470), (cx - 180, F - 330)], C["sun"])
        cv.poly([(cx - 70, F - 330), (cx + 80, F - 480), (cx + 30, F - 330)], C["sun"])
        cv.ellipse(cx + 140, F - 620, cx + 300, F - 440, C["accent"])
        cv.line([(cx + 220, F - 440), (cx + 200, F - 320)], C["primary"], 4)
        star(cv, cx - 360, F - 600, 50, C["sun"])
    else:
        raise SystemExit(f"no drawing for {key}")


# ---------------------------------------------------------------------------------------------------

def bg_for(th, i):
    C = {k: hexc(v[1]) for k, v in th["colors"].items()}
    sets = th["art_bg"]
    a, b = sets[i % len(sets)]
    return C[a], C[b]


ART = {
    "fernwood": {"art_bg": [("cream", "sand"), ("mint", "cream"), ("sand", "cream"), ("paper", "mint")],
                 "hero_art": ["terracotta-pot", "snake-plant", "monstera-deliciosa", "fiddle-leaf-fig"], "hero_bg": ("mint", "sand"),
                 "hero_layout": [(0.5, 0.5), (0.63, 0.72), (0.78, 0.84), (0.92, 0.7)],
                 "about_art": ["calathea-orbifolia", "golden-pothos", "ceramic-planter", "watering-can"], "about_bg": ("cream", "mint"),
                 "about_layout": [(0.14, 0.72), (0.39, 0.72), (0.63, 0.62), (0.87, 0.5)]},
    "good-boy-supply": {"art_bg": [("cream", "sand"), ("skyl", "cream"), ("sand", "cream"), ("paper", "skyl")],
                        "hero_art": ["squeaky-bone", "orthopedic-dog-bed", "cat-tree", "rope-tug"], "hero_bg": ("sand", "sun"),
                        "hero_layout": [(0.38, 0.36), (0.56, 0.72), (0.84, 0.78), (0.68, 0.4)],
                        "about_art": ["cat-tree", "orthopedic-dog-bed", "ceramic-bowl", "feather-wand"], "about_bg": ("skyl", "cream"),
                        "about_layout": [(0.15, 0.74), (0.44, 0.66), (0.72, 0.55), (0.88, 0.42)]},
    "kite-and-crayon": {"art_bg": [("cream", "sand"), ("minty", "cream"), ("sand", "cream"), ("paper", "minty")],
                        "hero_art": ["pull-along-duck", "wooden-rainbow-stacker", "plush-fox", "building-blocks"], "hero_bg": ("cream", "sand"),
                        "hero_layout": [(0.45, 0.44), (0.61, 0.66), (0.77, 0.66), (0.9, 0.46)],
                        "about_art": ["paint-easel", "building-blocks", "world-puzzle", "crayon-set"], "about_bg": ("minty", "cream"),
                        "about_layout": [(0.17, 0.74), (0.42, 0.55), (0.66, 0.5), (0.87, 0.45)]},
}

CATEGORY_ICON = {
    "houseplants": "monstera-deliciosa", "planters": "terracotta-pot", "care": "watering-can", "gifts": "plant-gift-box",
    "dogs": "orthopedic-dog-bed", "cats": "cat-tree", "toys": "rope-tug",
    "games": "world-puzzle", "art": "crayon-set", "room": "star-night-light",
}
CATEGORY_ICON_BY_THEME = {("good-boy-supply", "care"): "salmon-treats", ("kite-and-crayon", "toys"): "wooden-rainbow-stacker"}


def scene(th, keys, w, h, top, bottom, seed, layout):
    """A wide composition of several products (hero and about images): layout is (centre x as a share of
    the width, scale) per product, standing on one floor."""
    cv = Canvas(w, h, top, bottom, floor=0.86, seed=seed)
    for key, (x, sc) in zip(keys, layout):
        sub = Canvas(1200, 1200, None, None, floor=0.86, seed=seed, transparent=True)
        draw_product(th, key, sub, 1)
        im = sub.img.convert("RGBa").resize((int(sub.W * sc), int(sub.H * sc)), Image.LANCZOS).convert("RGBA")
        layer = Image.new("RGBA", (cv.W, cv.H), (0, 0, 0, 0))
        layer.paste(im, (int(x * cv.W - im.width / 2), int(cv.floor - sub.floor * sc)))
        cv.img.alpha_composite(layer)
    return cv


def portrait(th, key, top, bottom, seed, size):
    """One product on its own: drawn on a layer, then scaled to fill the frame (feet on the floor line)."""
    sub = Canvas(1200, 1200, None, None, seed=seed, transparent=True)
    draw_product(th, key, sub, 1)
    x0, y0, x1, y1 = sub.img.getbbox()
    fx, fy = sub.W / 2, sub.floor
    bw = max(fx - x0, x1 - fx) * 2
    bh = fy - y0
    z = min(0.82 * sub.W / bw, 0.64 * sub.H / bh, 1.5)
    im = sub.img.convert("RGBa").resize((int(sub.W * z), int(sub.H * z)), Image.LANCZOS).convert("RGBA")
    cv = Canvas(1200, 1200, top, bottom, seed=seed)
    layer = Image.new("RGBA", (cv.W, cv.H), (0, 0, 0, 0))
    layer.paste(im, (int(fx - fx * z), int(fy - fy * z)))
    cv.img.alpha_composite(layer)
    if size != 1200:
        cv.img, cv.w, cv.h = cv.img.resize((size * SS, size * SS), Image.LANCZOS), size, size
    return cv


def build(th):
    th = dict(th, **ART[th["slug"]])
    d = os.path.join(LIB, th["slug"], "demo", "images")
    os.makedirs(d, exist_ok=True)
    C = {k: hexc(v[1]) for k, v in th["colors"].items()}
    products = [p["slug"] for p in th["store"]["products"]]
    for i, key in enumerate(products):
        top, bottom = bg_for(th, i)
        portrait(th, key, top, bottom, i, 1200).save(os.path.join(d, f"{th['slug']}-p-{key}.jpg"))
    for i, c in enumerate(th["store"]["categories"]):
        key = CATEGORY_ICON_BY_THEME.get((th["slug"], c["slug"]), CATEGORY_ICON[c["slug"]])
        top, bottom = bg_for(th, i + 1)
        portrait(th, key, top, bottom, 50 + i, 900).save(os.path.join(d, f"{th['slug']}-cat-{c['slug']}.jpg"))
    hero_keys = th["hero_art"]
    hk = [k for k in hero_keys]
    cv = scene(th, hk, 1800, 1000, C[th["hero_bg"][0]], C[th["hero_bg"][1]], 7, th["hero_layout"])
    cv.save(os.path.join(d, f"{th['slug']}-hero.jpg"))
    cv = scene(th, th["about_art"], 1600, 1000, C[th["about_bg"][0]], C[th["about_bg"][1]], 9, th["about_layout"])
    cv.save(os.path.join(d, f"{th['slug']}-about.jpg"))
    print("art", th["slug"], len(os.listdir(d)), "images")


if __name__ == "__main__":
    sys.path.insert(0, os.path.dirname(__file__))
    from themes import THEMES
    want = sys.argv[1:]
    for th in THEMES:
        if not want or th["slug"] in want:
            build(th)
