/*
 * Pursae HTML import extractor (internal/webbuilder/extract.go). Runs inside the sandboxed frame that
 * renders the imported page (allow-scripts, no same-origin), at a fixed viewport width, after the page
 * and its fonts and images have loaded. It reads the browser's computed styles and boxes and describes
 * the page in Pursae Web Builder terms — free-form nodes (box, heading, text, button, link, image, icon,
 * input, divider) with NodeLayout and ElStyle values — then posts it to the parent frame:
 *
 *   parent.postMessage({ pursaeExtract: 1, width, data }, '*')
 *
 * data = { v: 1, width, seo: {...}, bodyBg, sections: [{ tag, id, name, bg, sticky, root: Node }],
 *          hidden: [paths hidden at this width], warnings: [] }
 * Node = { n, p (DOM path), tag?, t?, x?, href?, lay?, es?, k?: [Node] }
 *
 * Everything here is a description; the server validates and sanitizes every value again.
 */
(function () {
  'use strict'
  var MAX_NODES = 3000
  var INLINE = { a: 1, b: 1, strong: 1, i: 1, em: 1, u: 1, s: 1, del: 1, ins: 1, span: 1, br: 1, code: 1, small: 1, mark: 1, sub: 1, sup: 1, abbr: 1, time: 1, q: 1, cite: 1, kbd: 1, label: 1, font: 1, wbr: 1 }
  var SKIP = { script: 1, style: 1, link: 1, meta: 1, noscript: 1, template: 1, head: 1, title: 1, base: 1, iframe: 1, object: 1, embed: 1, canvas: 1, audio: 1, source: 1, track: 1, map: 1, dialog: 1 }
  var PARTS = { header: 1, nav: 1, section: 1, footer: 1, aside: 1, article: 1, form: 1, figure: 1 }
  var warnings = []
  var hidden = []
  var count = 0

  function warn(w) { if (warnings.indexOf(w) < 0) warnings.push(w) }
  function px(v) { var n = parseFloat(v); return isFinite(n) ? n : 0 }
  function r1(n) { return Math.round(n * 10) / 10 }
  function r0(n) { return Math.round(n) }

  // Colors: computed values may be rgb(), rgba(), color(), oklch()…; a 1×1 canvas turns any of them into rgba.
  var cv = document.createElement('canvas'); cv.width = cv.height = 1
  var cx = cv.getContext('2d', { willReadFrequently: true })
  var colorCache = {}
  function color(c) {
    if (!c || c === 'transparent' || c === 'none') return ''
    if (colorCache[c] !== undefined) return colorCache[c]
    var out = ''
    var m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)$/.exec(c)
    var rgb
    if (m) {
      var a = m[4] === undefined ? 1 : m[4].slice(-1) === '%' ? parseFloat(m[4]) / 100 : parseFloat(m[4])
      rgb = [+m[1], +m[2], +m[3], a]
    } else {
      try {
        cx.clearRect(0, 0, 1, 1); cx.fillStyle = '#000'; cx.fillStyle = c; cx.fillRect(0, 0, 1, 1)
        var d = cx.getImageData(0, 0, 1, 1).data
        rgb = [d[0], d[1], d[2], d[3] / 255]
      } catch (e) { rgb = null }
    }
    if (rgb && rgb[3] > 0.01) {
      out = rgb[3] >= 0.995
        ? '#' + [0, 1, 2].map(function (i) { return ('0' + Math.round(rgb[i]).toString(16)).slice(-2) }).join('')
        : 'rgba(' + Math.round(rgb[0]) + ',' + Math.round(rgb[1]) + ',' + Math.round(rgb[2]) + ',' + (Math.round(rgb[3] * 100) / 100) + ')'
    }
    colorCache[c] = out
    return out
  }
  // Colors inside gradients and shadows.
  function colorsIn(s) {
    return s.replace(/(rgba?\([^)]*\)|oklch\([^)]*\)|oklab\([^)]*\)|lab\([^)]*\)|lch\([^)]*\)|hsla?\([^)]*\)|color\([^)]*\))/g, function (c) { return color(c) || 'transparent' })
  }

  function isHidden(el, cs) {
    return cs.display === 'none' || cs.visibility === 'hidden' || cs.visibility === 'collapse' || el.hidden
  }
  function rect(el) { return el.getBoundingClientRect() }
  function path(el) {
    var p = []
    while (el && el !== document.body && el.parentElement) {
      var i = 0, s = el
      while ((s = s.previousElementSibling)) i++
      p.unshift(i)
      el = el.parentElement
    }
    return p.join('.')
  }
  function kids(el) {
    var out = []
    var pcs = getComputedStyle(el)
    var clips = /hidden|clip/.test(pcs.overflowX + pcs.overflowY)
    var pr = clips ? rect(el) : null
    for (var c = el.firstElementChild; c; c = c.nextElementSibling) {
      var tag = c.tagName.toLowerCase()
      if (SKIP[tag]) { if (tag === 'iframe' || tag === 'canvas' || tag === 'object' || tag === 'embed') warn('iframes, canvases and embeds were left out'); continue }
      var cs = getComputedStyle(c)
      if (isHidden(c, cs)) continue
      // Clipped away by its box (a collapsed accordion or tab panel, a slider's other slides): not shown.
      if (pr) {
        var cr = rect(c)
        if (pr.height < 1 || pr.width < 1 || cr.bottom <= pr.top + 1 || cr.top >= pr.bottom - 1 || cr.right <= pr.left + 1 || cr.left >= pr.right - 1) {
          warn('collapsed or clipped content (closed accordions, other tabs and slides) keeps only what is shown')
          continue
        }
      }
      out.push(c)
    }
    return out
  }
  function ownText(el) {
    var t = ''
    for (var c = el.firstChild; c; c = c.nextSibling) if (c.nodeType === 3) t += c.data
    return t.replace(/\s+/g, ' ').trim()
  }
  function hasVisual(cs) {
    return color(cs.backgroundColor) !== '' || (cs.backgroundImage && cs.backgroundImage !== 'none') ||
      px(cs.borderTopWidth) + px(cs.borderRightWidth) + px(cs.borderBottomWidth) + px(cs.borderLeftWidth) > 0 ||
      (cs.boxShadow && cs.boxShadow !== 'none')
  }
  function padding(cs) {
    var b = { t: r0(px(cs.paddingTop)), r: r0(px(cs.paddingRight)), b: r0(px(cs.paddingBottom)), l: r0(px(cs.paddingLeft)) }
    return b.t || b.r || b.b || b.l ? b : null
  }
  function border(cs) {
    var sides = ['Top', 'Right', 'Bottom', 'Left'].map(function (s) {
      var w = px(cs['border' + s + 'Width'])
      var st = cs['border' + s + 'Style']
      if (!w || st === 'none' || st === 'hidden') return ''
      var c = color(cs['border' + s + 'Color'])
      return c ? r1(w) + 'px ' + st + ' ' + c : ''
    })
    if (!sides.some(Boolean)) return ''
    if (sides.every(function (s) { return s === sides[0] })) return sides[0]
    return ['top', 'right', 'bottom', 'left'].map(function (n, i) { return sides[i] ? 'border-' + n + ':' + sides[i] : '' }).filter(Boolean).join(';')
  }
  function radius(cs, r) {
    var v = cs.borderTopLeftRadius
    if (!v || v === '0px') return 0
    if (v.indexOf('%') > 0) return r0(Math.min(r.width, r.height) * parseFloat(v) / 100)
    return r0(Math.min(px(v), Math.max(r.width, r.height)))
  }
  // family is the first concrete font of the stack (generic keywords and emoji fonts skipped); a bare
  // generic serif or monospace stack becomes a common font of that kind.
  var GENERIC = /^(serif|sans-serif|monospace|system-ui|ui-sans-serif|ui-serif|ui-monospace|ui-rounded|-apple-system|BlinkMacSystemFont|cursive|fantasy|math|emoji|fangsong)$/i
  function family(cs) {
    var list = (cs.fontFamily || '').split(',').map(function (f) { return f.trim().replace(/^["']|["']$/g, '') }).filter(Boolean)
    for (var i = 0; i < list.length; i++) {
      var f = list[i]
      if (GENERIC.test(f) || /emoji|symbol/i.test(f)) continue
      return f
    }
    if (list.some(function (f) { return /^(ui-)?serif$/i.test(f) })) return 'Georgia'
    if (list.some(function (f) { return /^(ui-)?monospace$/i.test(f) })) return 'Courier New'
    return ''
  }

  // Look shared by every node: background, border, radius, shadow, padding, opacity.
  function boxLook(cs, r, es) {
    var bg = color(cs.backgroundColor)
    if (bg) es.bg = bg
    var bi = cs.backgroundImage
    if (bi && bi !== 'none') {
      es.bgImage = colorsIn(bi)
      if (cs.backgroundSize && cs.backgroundSize !== 'auto') es.bgSize = cs.backgroundSize
      if (cs.backgroundPosition) es.bgPos = cs.backgroundPosition
    }
    var b = border(cs)
    if (b) es.border = b
    var rad = radius(cs, r)
    if (rad) es.radius = rad
    if (cs.boxShadow && cs.boxShadow !== 'none') es.shadow = colorsIn(cs.boxShadow)
    var p = padding(cs)
    if (p) es.padding = p
    var op = parseFloat(cs.opacity)
    if (op < 1 && op >= 0.05) es.opacity = Math.round(op * 100) / 100
    else if (op < 0.05) warn('content that only appears with an animation is imported visible')
  }
  // Typography of a text-like node.
  function typeLook(cs, es) {
    var c = color(cs.color)
    if (c) es.color = c
    var f = family(cs)
    if (f) es.font = f
    var fs = px(cs.fontSize)
    if (fs) es.fontSize = r1(fs)
    var fw = parseInt(cs.fontWeight, 10)
    if (fw) es.fontWeight = fw
    var lh = cs.lineHeight
    if (lh && lh !== 'normal' && fs) es.lineHeight = Math.round(px(lh) / fs * 100) / 100
    var ls = cs.letterSpacing
    if (ls && ls !== 'normal' && fs && px(ls) !== 0) es.letterSpacing = Math.round(px(ls) / fs * 1000) / 1000
    var ta = cs.textAlign
    if (ta === 'center' || ta === 'right') es.align = ta
    else if (ta === 'end') es.align = 'right'
    if (cs.textTransform === 'uppercase' || cs.textTransform === 'lowercase' || cs.textTransform === 'capitalize') es.transform = cs.textTransform
    if (cs.fontStyle === 'italic') es.italic = true
    if ((cs.textDecorationLine || '').indexOf('underline') >= 0) es.underline = true
  }

  function esc(s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;') }
  // Inline content in the editor's rich-text subset: b i u s code a br (colors and sizes of inline spans are dropped).
  function rich(el) {
    var out = ''
    for (var c = el.firstChild; c; c = c.nextSibling) {
      if (c.nodeType === 3) { out += esc(c.data.replace(/\s+/g, ' ')); continue }
      if (c.nodeType !== 1) continue
      var tag = c.tagName.toLowerCase()
      if (tag === 'br') { out += '<br>'; continue }
      if (SKIP[tag] || tag === 'img' || tag === 'svg') continue
      var ccs = getComputedStyle(c)
      if (isHidden(c, ccs)) continue
      var inner = rich(c)
      if (!inner) continue
      var wrap = []
      if (tag === 'a' && c.getAttribute('href')) { out += '<a href="' + esc(c.getAttribute('href')).replace(/"/g, '&quot;') + '">' + inner + '</a>'; continue }
      if (tag === 'code' || tag === 'kbd') wrap.push('code')
      if (parseInt(ccs.fontWeight, 10) >= 600 && parseInt(getComputedStyle(el).fontWeight, 10) < 600) wrap.push('b')
      if (ccs.fontStyle === 'italic' && getComputedStyle(el).fontStyle !== 'italic') wrap.push('i')
      if ((ccs.textDecorationLine || '').indexOf('line-through') >= 0) wrap.push('s')
      if (tag === 'u') wrap.push('u')
      var o = '', cl = ''
      var cc = color(ccs.color)
      if (cc && cc !== color(getComputedStyle(el).color)) {
        // an accent word keeps its color (a span with a color is part of the rich-text subset)
        var rgb = /^#/.test(cc) ? 'rgb(' + parseInt(cc.slice(1, 3), 16) + ', ' + parseInt(cc.slice(3, 5), 16) + ', ' + parseInt(cc.slice(5, 7), 16) + ')' : cc.replace(/,/g, ', ')
        o += '<span style="color: ' + rgb + ';">'; cl = '</span>'
      }
      wrap.forEach(function (w) { o += '<' + w + '>'; cl = '</' + w + '>' + cl })
      out += o + inner + cl
    }
    return out
  }
  function cleanRich(h) {
    h = h.replace(/^\s+|\s+$/g, '')
    return /<(b|i|u|s|code|a|br|span)\b/.test(h) ? h : h.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&')
  }

  // contentSized: the element is exactly as wide as its content (a chip, a button group), so it must
  // not get a fixed width — another font would make its content wider than the box.
  function contentSized(el, r) {
    var old = el.style.width, oldMax = el.style.maxWidth
    el.style.width = 'max-content'; el.style.maxWidth = 'none'
    var w = el.getBoundingClientRect().width
    el.style.width = old; el.style.maxWidth = oldMax
    return Math.abs(w - r.width) <= 2
  }

  // fitText keeps a text element's box when it is taller than its text (a fixed-height badge, a centered
  // number tile, a tall button): the space around the text becomes padding, which the editor keeps.
  function fitText(el, cs, r, es) {
    // Lines of text × line height is the height the text needs; what the box has beyond that (a
    // fixed-height badge, a centered number tile) becomes padding, centered when the box centers.
    var rg = document.createRange()
    rg.selectNodeContents(el)
    var rects = rg.getClientRects()
    if (!rects.length) return
    var tops = []
    for (var i = 0; i < rects.length; i++) {
      var t = Math.round(rects[i].top)
      if (rects[i].width > 0 && !tops.some(function (x) { return Math.abs(x - t) < 3 })) tops.push(t)
    }
    var fs = px(cs.fontSize)
    var lh = cs.lineHeight === 'normal' ? fs * 1.2 : px(cs.lineHeight)
    var need = Math.max(1, tops.length) * lh
    var inner = r.height - px(cs.borderTopWidth) - px(cs.borderBottomWidth) - px(cs.paddingTop) - px(cs.paddingBottom)
    var extra = inner - need
    if (extra <= 3) return
    var p = es.padding || { t: 0, r: r0(px(cs.paddingRight)), b: 0, l: r0(px(cs.paddingLeft)) }
    var centered = /flex|grid/.test(cs.display) && (cs.alignItems === 'center' || cs.alignContent === 'center')
    var top = centered ? extra / 2 : 0
    p.t = r0(px(cs.paddingTop) + top)
    p.b = r0(px(cs.paddingBottom) + extra - top)
    if (/flex|grid/.test(cs.display) && (cs.justifyContent === 'center' || cs.justifyItems === 'center')) es.align = 'center'
    es.padding = p
  }

  // isLeaf: only text and inline phrasing content (no pictures, no blocks).
  function isLeaf(el) {
    for (var c = el.firstChild; c; c = c.nextSibling) {
      if (c.nodeType !== 1) continue
      var tag = c.tagName.toLowerCase()
      if (SKIP[tag]) continue
      if (!INLINE[tag]) return false
      var d = getComputedStyle(c).display
      if (d !== 'inline' && d !== 'contents' && !(d === 'inline-block' && !hasVisual(getComputedStyle(c)))) {
        if (d !== 'none') return false
      }
      if (!isLeaf(c)) return false
    }
    return true
  }
  function looksLikeButton(cs) {
    if (color(cs.backgroundColor)) return true
    if (cs.backgroundImage && cs.backgroundImage !== 'none') return true
    var bw = px(cs.borderTopWidth) + px(cs.borderBottomWidth)
    return bw > 0 && (px(cs.paddingTop) >= 4 || px(cs.paddingLeft) >= 8)
  }
  function svgData(el, cs) {
    var clone = el.cloneNode(true)
    // <use href="#symbol"> points into the page: bring the symbol's drawing into the icon itself.
    Array.prototype.slice.call(clone.querySelectorAll('use')).forEach(function (u) {
      var ref = (u.getAttribute('href') || u.getAttribute('xlink:href') || '').replace(/^#/, '')
      var sym = ref && document.getElementById(ref)
      if (!sym) return
      var g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      Array.prototype.slice.call(sym.childNodes).forEach(function (c) { g.appendChild(c.cloneNode(true)) })
      if (sym.getAttribute('viewBox') && !clone.getAttribute('viewBox')) clone.setAttribute('viewBox', sym.getAttribute('viewBox'))
      u.parentNode.replaceChild(g, u)
    })
    var c = color(cs.color) || '#000000'
    clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
    var r = rect(el)
    if (!clone.getAttribute('width')) clone.setAttribute('width', r0(r.width))
    if (!clone.getAttribute('height')) clone.setAttribute('height', r0(r.height))
    var s = new XMLSerializer().serializeToString(clone).replace(/currentColor/g, c)
    if (s.length > 24000) { warn('large inline SVGs were left out'); return '' }
    return 'data:image/svg+xml,' + encodeURIComponent(s)
  }
  function imgSrc(el) {
    var s = el.currentSrc || el.src || ''
    if (/^data:image\/svg/i.test(s)) return ''
    if (/^data:/i.test(s) && s.length > 32000) { warn('large inline images were left out (upload them to the Media Library)'); return '' }
    return s
  }

  // node describes el; parentDir is the layout of the box it sits in, ctx its content box.
  function node(el, parentDir, ctx, linkHref) {
    if (count >= MAX_NODES) { warn('the page was cut at ' + MAX_NODES + ' elements'); return null }
    var tag = el.tagName.toLowerCase()
    var cs = getComputedStyle(el)
    if (isHidden(el, cs)) return null
    var r = rect(el)
    if ((r.width < 1 || r.height < 1) && tag !== 'img' && !(cs.overflow === 'visible' && el.firstElementChild && r.width >= 1)) return null
    // Skip links and visually hidden helpers (moved off screen or clipped to a pixel).
    if ((cs.position === 'absolute' || cs.position === 'fixed') && (r.bottom <= 0 || r.right <= 0 || r.width <= 2 || r.height <= 2)) return null
    // An invisible layer placed over its box is an inactive frame of an animated scene, not content.
    if ((cs.position === 'absolute' || cs.position === 'fixed') && parseFloat(cs.opacity) < 0.05) return null
    if (cs.position === 'fixed') {
      var vw0 = document.documentElement.clientWidth
      if (!(r.top <= 4 && r.width >= vw0 - 4)) {
        warn('fixed overlays (cookie notices, chat buttons, floating bars) were left out')
        return null
      }
    }
    if ((cs.position === 'absolute' || cs.position === 'fixed') && !el.textContent.trim() && !el.querySelector('img,svg')) {
      if (!hasVisual(cs)) return null
      warn('decorative layers placed with position: absolute were left out')
      return null
    }
    count++
    var n = { p: path(el) }
    var es = {}
    if (tag === 'picture') { var pi = el.querySelector('img'); if (!pi) return null; el = pi; tag = 'img'; cs = getComputedStyle(el); r = rect(el) }
    if (tag === 'img' || tag === 'video') {
      var src = tag === 'img' ? imgSrc(el) : (el.getAttribute('poster') || '')
      if (!src) return null
      n.n = 'image'; n.t = src; n.x = el.getAttribute('alt') || ''
      if (!n.x && tag === 'img') warn('some images have no alt text (add it for SEO and accessibility)')
      boxLook(cs, r, es)
      if (cs.objectFit === 'contain') es.fit = 'contain'
      if (r.height > 0) n.lay = { ratio: Math.round(r.width / r.height * 1000) / 1000 }
      if (ctx && parentDir === 'column' && r.width < ctx.w - 2) es.width = r0(r.width)
    } else if (tag === 'svg') {
      var d = svgData(el, cs)
      if (!d) return null
      n.n = 'icon'; n.t = d
      es.width = r0(r.width)
      if (r.height > 0) n.lay = { ratio: Math.round(r.width / r.height * 1000) / 1000 }
    } else if (tag === 'hr') {
      n.n = 'divider'
      var hc = color(cs.borderTopColor) || color(cs.backgroundColor)
      if (hc) es.bg = hc
      es.height = Math.max(1, r0(px(cs.borderTopWidth) || r.height))
    } else if (tag === 'input' || tag === 'textarea' || tag === 'select') {
      var ty = (el.getAttribute('type') || '').toLowerCase()
      if (ty === 'hidden') return null
      if (ty === 'submit' || ty === 'button') { n.n = 'button'; n.t = el.value || 'Submit'; typeLook(cs, es) }
      else { n.n = 'input'; n.t = el.getAttribute('placeholder') || el.getAttribute('aria-label') || el.getAttribute('name') || ''; if (tag === 'textarea') n.tag = 'textarea'; typeLook(cs, es) }
      boxLook(cs, r, es)
      es.width = r0(r.width); es.height = r0(r.height)
    } else if (tag === 'button' && isLeaf(el)) {
      n.n = 'button'; n.t = el.textContent.replace(/\s+/g, ' ').trim()
      boxLook(cs, r, es); typeLook(cs, es)
      fitText(el, cs, r, es)
      if (!n.t) return null
    } else if (/^h[1-6]$/.test(tag) && isLeaf(el)) {
      n.n = 'heading'; n.tag = tag; n.t = cleanRich(rich(el))
      boxLook(cs, r, es); typeLook(cs, es)
    } else if (isLeaf(el) && el.textContent.trim()) {
      var href = tag === 'a' ? el.getAttribute('href') : ''
      if (href && looksLikeButton(cs)) { n.n = 'button'; n.t = el.textContent.replace(/\s+/g, ' ').trim(); n.href = href }
      else if (href) { n.n = 'link'; n.t = cleanRich(rich(el)); n.href = href }
      else if (linkHref) { n.n = 'link'; n.t = cleanRich(rich(el)); n.href = linkHref; linkHref = '' }
      else { n.n = 'text'; n.t = cleanRich(rich(el)) }
      if (tag === 'li') es.list = cs.listStyleType && cs.listStyleType !== 'none' ? cs.listStyleType : undefined
      if (tag === 'blockquote' || tag === 'q') n.tag = 'blockquote'
      boxLook(cs, r, es); typeLook(cs, es)
      if (es.list === undefined) delete es.list
      fitText(el, cs, r, es)
    } else {
      // A container.
      n.n = 'box'
      var list = kids(el)
      var cHref = tag === 'a' ? el.getAttribute('href') : linkHref
      var inner = {
        l: r.left + px(cs.borderLeftWidth) + px(cs.paddingLeft),
        t: r.top + px(cs.borderTopWidth) + px(cs.paddingTop),
        w: el.clientWidth - px(cs.paddingLeft) - px(cs.paddingRight),
      }
      inner.r = inner.l + inner.w
      var lay = layout(cs, list)
      // Rounded corners that clip their content (a card with a photo) clip in the editor too.
      if ((cs.overflow === 'hidden' || cs.overflowX === 'hidden' || cs.overflow === 'clip') && px(cs.borderTopLeftRadius) > 0) lay.clip = true
      n.lay = lay
      boxLook(cs, r, es)
      var c2 = color(cs.color)
      if (c2) es.color = c2
      var f = family(cs)
      if (f) es.font = f
      var runway = list.some(function (c) { return getComputedStyle(c).position === 'sticky' })
      if (runway) warn('scroll-driven scenes (pinned while you scroll) are imported in their first frame')
      if (runway) { /* a pinned scene's scroll runway is not part of the look */ }
      else if (px(cs.minHeight) > 0 && px(cs.minHeight) < 2400) es.height = r0(px(cs.minHeight))
      else if (!list.length && (es.bgImage || es.bg)) es.height = r0(r.height)
      else if (list.length) {
        var top = Infinity, bot = -Infinity
        list.forEach(function (c) { var q = rect(c); if (q.height > 0) { top = Math.min(top, q.top); bot = Math.max(bot, q.bottom) } })
        var used = (bot - top) + px(cs.paddingTop) + px(cs.paddingBottom) + px(cs.borderTopWidth) + px(cs.borderBottomWidth)
        if (isFinite(used) && r.height - used > 6) es.height = r0(r.height)
      }
      var k = []
      // Loose text next to elements becomes text nodes of its own.
      var loose = ownText(el)
      if (loose && list.length) {
        var tn = { n: 'text', p: n.p + '.t', t: loose, es: {} }
        typeLook(cs, tn.es)
        k.push(tn)
      } else if (loose && !list.length) {
        n.n = 'text'; n.t = loose; typeLook(cs, es); delete n.lay
      }
      var prevBottom = inner.t
      for (var i = 0; i < list.length; i++) {
        var c = list[i]
        var ch = node(c, lay.dir, inner, cHref)
        if (!ch) continue
        if (cHref && ch.href === cHref) cHref = ''
        var cr = rect(c)
        var ces = ch.es || (ch.es = {})
        var ccs = getComputedStyle(c)
        var placed = ccs.position === 'absolute' || ccs.position === 'fixed'
        if (placed) {
          // A layer placed over its box (a badge, a decorative shape, a stacked screenshot): in the
          // editor it sits in the flow at its measured spot and takes no room — negative bottom margin.
          if (lay.dir !== 'column') {
            // rows and grids cannot hold an overlapping layer: wrap it in a zero-height column cell
            warn('layers placed with position: absolute inside rows or grids are kept where they start')
          }
          var t0 = r0(cr.top - (lay.dir === 'column' ? prevBottom : inner.t))
          // kept inside its box horizontally (a layer hanging off the edge must not widen the page)
          var l0 = Math.max(0, Math.min(r0(cr.left - inner.l), Math.max(0, r0(inner.w) - 8)))
          var w0 = Math.max(1, Math.min(r0(cr.width), r0(inner.w) - l0))
          ces.margin = { t: t0, r: 0, b: -(t0 + r0(cr.height)), l: l0 }
          delete ces.mt
          ces.width = w0
          if (ces.maxWidth && ces.maxWidth > w0) ces.maxWidth = w0
          if (lay.dir === 'column') { ch.lay = ch.lay || {}; ch.lay.self = 'start' }
          if (Object.keys(ces).length === 0) delete ch.es
          k.push(ch)
          continue
        }
        if (lay.dir === 'column') {
          var gap = r0(cr.top - prevBottom)
          if (gap) ces.mt = gap
          prevBottom = Math.max(prevBottom, cr.bottom)
          // Where it sits across the box: stretched, or start / center / end with its own width.
          if (inner.w > 0 && cr.width < inner.w - 2) {
            var lg = cr.left - inner.l, rg = inner.r - cr.right
            var self = Math.abs(lg - rg) <= 2 ? 'center' : rg < lg ? 'end' : 'start'
            ch.lay = ch.lay || {}
            ch.lay.self = self
            var blockish = /^(block|flex|grid|table|list-item|flow-root)$/.test(ccs.display)
            if ((ch.n === 'box' || (blockish && (ch.n === 'text' || ch.n === 'heading'))) && !contentSized(c, cr)) ces.maxWidth = r0(cr.width)
          }
        } else if (lay.dir === 'row') {
          // Items that grow or take a set share keep it; content-sized items stay content-sized.
          var grows = lay.shares || parseFloat(ccs.flexGrow) > 0 || /%$/.test(ccs.flexBasis) || (ccs.flexBasis !== 'auto' && px(ccs.flexBasis) > 0)
          if ((ch.n === 'box' || ch.n === 'image') && inner.w > 0 && grows) {
            ch.lay = ch.lay || {}
            ch.lay.basis = Math.max(1, Math.min(100, Math.round(cr.width / inner.w * 1000) / 10))
          } else if (ch.n === 'box' && inner.w > 0 && cr.width > inner.w * 0.25 && !grows && !contentSized(c, cr)) {
            // a fixed-width column (w-1/2, w-[480px]): keep its width
            ces.maxWidth = r0(cr.width); ces.width = r0(cr.width)
          }
          if (ch.n === 'image' && !ces.width && !grows) ces.width = r0(cr.width)
        }
        // Horizontal margins of a row or grid child become its own margin.
        var ml = r0(px(ccs.marginLeft)), mr = r0(px(ccs.marginRight))
        if (lay.dir !== 'column' && (ml > 0 || mr > 0) && ccs.marginLeft !== 'auto') ces.margin = { t: 0, r: mr, b: 0, l: ml }
        if (Object.keys(ces).length === 0) delete ch.es
        k.push(ch)
      }
      if (!k.length && !es.bg && !es.bgImage && !es.border && n.n === 'box') { count--; return null }
      // A wrapper without a look of its own around a single box collapses into it.
      if (k.length === 1 && k[0].n === 'box' && !hasVisual(cs) && !padding(cs) && !PARTS[tag] && n.p) {
        var only = k[0]
        only.es = only.es || {}
        if (!only.es.color && es.color) only.es.color = es.color
        if (!only.es.font && es.font) only.es.font = es.font
        return only
      }
      if (k.length) n.k = k
      if (n.n === 'text') delete n.k
    }
    if (n.n === 'text' || n.n === 'heading' || n.n === 'link') {
      if (!n.t) return null
    }
    if (cs.position === 'sticky' && (n.n === 'box')) n.sticky = 1
    for (var key in es) if (es[key] === undefined || es[key] === '') delete es[key]
    if (Object.keys(es).length) n.es = es
    return n
  }

  function layout(cs, list) {
    var lay = { dir: 'column' }
    var d = cs.display
    if (d === 'flex' || d === 'inline-flex') {
      lay.dir = cs.flexDirection.indexOf('column') === 0 ? 'column' : 'row'
      if (cs.flexWrap === 'wrap') lay.wrap = true
      if (lay.dir === 'row') {
        var g = px(cs.columnGap); if (g) lay.gap = r0(g)
        var gy = px(cs.rowGap); if (gy) lay.gapY = r0(gy)
        var ai = cs.alignItems
        lay.align = ai === 'center' ? 'center' : (ai === 'flex-end' || ai === 'end') ? 'end' : (ai === 'stretch' || ai === 'normal') ? 'stretch' : 'start'
        if (list.length > 1) lay.stack = true // refined with the phone-width layout on the server
      }
      var jc = cs.justifyContent
      if (jc === 'center') lay.justify = 'center'
      else if (jc === 'flex-end' || jc === 'end' || jc === 'right') lay.justify = 'end'
      else if (/space-(between|around|evenly)/.test(jc)) lay.justify = 'space-between'
      if (lay.dir === 'column') {
        if (cs.alignItems === 'center') lay.align = 'center'
      }
    } else if (d === 'grid' || d === 'inline-grid') {
      lay.dir = 'grid'
      var tracks = (cs.gridTemplateColumns || '').split(/\s+(?![^(]*\))/).filter(function (x) { return x && x !== 'none' })
      var cols = tracks.length
      lay.cols = Math.max(1, Math.min(12, cols || 1))
      // One row of unequal columns (a header: logo | menu | actions, a 2fr/1fr split): the editor's grid
      // has equal columns, so it becomes a row whose cells keep their measured share.
      var widths = tracks.map(px).filter(function (w) { return w > 0 })
      if (widths.length === cols && cols > 1 && list.length <= cols && Math.max.apply(null, widths) / Math.max(1, Math.min.apply(null, widths)) > 1.2) {
        lay.dir = 'row'
        lay.shares = true
        delete lay.cols
        var gx0 = px(cs.columnGap); if (gx0) lay.gap = r0(gx0)
        lay.align = cs.alignItems === 'center' ? 'center' : (cs.alignItems === 'end' || cs.alignItems === 'flex-end') ? 'end' : (cs.alignItems === 'start' || cs.alignItems === 'flex-start') ? 'start' : 'stretch'
        lay.stack = true
        return lay
      }
      var gx = px(cs.columnGap); if (gx) lay.gap = r0(gx)
      var gyy = px(cs.rowGap); if (gyy) lay.gapY = r0(gyy)
      if (list.some(function (c) { var s = getComputedStyle(c).gridColumnEnd; return s && s.indexOf('span') === 0 && s !== 'span 1' })) warn('grid items spanning several columns take one column each')
    }
    return lay
  }

  // The page's top-level parts: single wrappers are opened, main is flattened.
  function parts() {
    var cur = document.body
    for (;;) {
      var k = kids(cur)
      if (k.length === 1 && !PARTS[k[0].tagName.toLowerCase()] && !hasVisual(getComputedStyle(k[0])) && !ownText(cur)) { cur = k[0]; continue }
      break
    }
    var out = []
    // Wrappers without a look of their own that hold page parts (main, a div around several sections)
    // are opened, recursively, so every section of the page becomes a section in the editor.
    // A wrapper with a background of its own passes it on: its color to every part inside, its
    // background image (a gradient fading in at the top) to the first one.
    function open(c, depth, inh) {
      var tag = c.tagName.toLowerCase()
      var ck = kids(c)
      var ccs = getComputedStyle(c)
      var holdsParts = ck.filter(function (x) { return PARTS[x.tagName.toLowerCase()] }).length
      var plainWrap = (tag === 'main' || tag === 'div') && !hasVisual(ccs) && !padding(ccs)
      var lookWrap = tag === 'div' && holdsParts >= 2 && !padding(ccs) && !(ccs.boxShadow && ccs.boxShadow !== 'none')
      if (depth < 4 && (plainWrap || lookWrap) && ck.length > 1 && (tag === 'main' || holdsParts >= 2)) {
        var next = { bg: color(ccs.backgroundColor) || (inh && inh.bg) || '', img: ccs.backgroundImage !== 'none' ? colorsIn(ccs.backgroundImage) : '' }
        ck.forEach(function (x, i) { open(x, depth + 1, { bg: next.bg, img: i === 0 ? next.img : '' }) })
      } else out.push({ el: c, inh: inh || null })
    }
    kids(cur).forEach(function (c) { open(c, 0, null) })
    return out
  }

  // Layout of every flex and grid element at this width: path -> [direction, columns]; the import
  // compares it with the desktop to know which rows stack on phones and how many columns grids keep.
  function layouts() {
    var out = {}, n = 0
    var all = document.body.querySelectorAll('*')
    for (var i = 0; i < all.length && n < 4000; i++) {
      var el = all[i]
      var cs = getComputedStyle(el)
      if (cs.display === 'none') continue
      var d = cs.display
      if (d === 'flex' || d === 'inline-flex') {
        var stacked = cs.flexDirection.indexOf('column') === 0
        // a wrapping row whose items each take a full line is stacked too
        if (!stacked && cs.flexWrap === 'wrap') {
          var ks = Array.prototype.filter.call(el.children, function (c) { return getComputedStyle(c).display !== 'none' })
          if (ks.length > 1) {
            var tops = ks.map(function (c) { return Math.round(c.getBoundingClientRect().top) })
            stacked = tops.every(function (t, j) { return j === 0 || t > tops[j - 1] })
          }
        }
        out[path(el)] = [stacked ? 'column' : 'row', 0]; n++
      } else if (d === 'grid' || d === 'inline-grid') {
        var cols = (cs.gridTemplateColumns || '').split(/\s+(?![^(]*\))/).filter(function (x) { return x && x !== 'none' }).length
        out[path(el)] = ['grid', cols || 1]; n++
      }
    }
    return out
  }

  // Phone sizes of text, boxes and pictures: path -> [fontSize, lineHeight, padT, padR, padB, padL, ratio].
  function metrics() {
    var out = {}, n = 0
    var all = document.body.querySelectorAll('*')
    for (var i = 0; i < all.length && n < 6000; i++) {
      var el = all[i]
      var tag = el.tagName.toLowerCase()
      if (SKIP[tag]) continue
      var cs = getComputedStyle(el)
      if (cs.display === 'none') continue
      var r = rect(el)
      var fs = px(cs.fontSize)
      var lh = cs.lineHeight !== 'normal' && fs ? Math.round(px(cs.lineHeight) / fs * 100) / 100 : 0
      var ratio = (tag === 'img' || tag === 'video' || tag === 'svg') && r.height > 0 ? Math.round(r.width / r.height * 1000) / 1000 : 0
      var pt = r0(px(cs.paddingTop)), pb = r0(px(cs.paddingBottom))
      if (el.textContent.trim() && isLeaf(el)) {
        // the same measure as fitText on the desktop: the space around the text
        var es = { padding: { t: pt, r: 0, b: pb, l: 0 } }
        fitText(el, cs, r, es)
        pt = es.padding.t; pb = es.padding.b
      }
      out[path(el)] = [r1(fs), lh, pt, r0(px(cs.paddingRight)), pb, r0(px(cs.paddingLeft)), ratio, r0(r.width), r0(el.clientWidth - px(cs.paddingLeft) - px(cs.paddingRight))]
      n++
    }
    return out
  }

  function hiddenPaths() {
    // Elements of the page hidden at this width (the import compares two widths).
    var list = []
    var all = document.body.querySelectorAll('*')
    for (var i = 0; i < all.length && list.length < 2000; i++) {
      var el = all[i]
      if (SKIP[el.tagName.toLowerCase()]) continue
      var hcs = getComputedStyle(el)
      if (hcs.display === 'none') { list.push(path(el)); continue }
      // also hidden: an off-screen drawer, an invisible layer that cannot be clicked
      var hr = el.getBoundingClientRect(), vw1 = document.documentElement.clientWidth
      if (hcs.visibility === 'hidden' || (parseFloat(hcs.opacity) < 0.05 && hcs.pointerEvents === 'none') ||
        (hr.width > 0 && (hr.right <= 0 || hr.left >= vw1)) || ((hcs.position === 'fixed' || hcs.position === 'absolute') && hr.bottom <= 0)) list.push(path(el))
    }
    return list
  }

  function seo() {
    function meta(sel) { var m = document.querySelector(sel); return m ? (m.getAttribute('content') || '').trim() : '' }
    var canon = document.querySelector('link[rel="canonical"]')
    return {
      title: (document.title || '').trim(),
      description: meta('meta[name="description"]'),
      lang: document.documentElement.getAttribute('lang') || '',
      robots: meta('meta[name="robots"]'),
      ogTitle: meta('meta[property="og:title"]'),
      ogDescription: meta('meta[property="og:description"]'),
      ogImage: meta('meta[property="og:image"]'),
      canonical: canon ? canon.getAttribute('href') || '' : '',
      h1: Array.prototype.map.call(document.querySelectorAll('h1'), function (h) { return h.textContent.replace(/\s+/g, ' ').trim() }),
      headings: Array.prototype.map.call(document.querySelectorAll('h1,h2,h3,h4,h5,h6'), function (h) { return h.tagName.toLowerCase() }),
      imagesWithoutAlt: Array.prototype.filter.call(document.images, function (i) { return !i.getAttribute('alt') }).length,
    }
  }

  function run() {
    var bodyCs = getComputedStyle(document.body)
    var bodyBg = color(bodyCs.backgroundColor) || color(getComputedStyle(document.documentElement).backgroundColor) || '#ffffff'
    var secs = []
    parts().forEach(function (part, i) {
      var el = part.el
      var cs = getComputedStyle(el)
      var root = node(el, 'column', null, '')
      if (!root) return
      if (root.n !== 'box') root = { n: 'box', p: root.p + '.w', lay: { dir: 'column' }, k: [root] }
      // A part narrower than the page (max-width + margin: auto) keeps its width and place.
      var rr = rect(el), vw = document.documentElement.clientWidth
      if (rr.width < vw - 2) {
        root.es = root.es || {}
        root.es.maxWidth = r0(rr.width)
        root.lay = root.lay || {}
        var lg = rr.left, rg = vw - rr.right
        root.lay.self = Math.abs(lg - rg) <= 2 ? 'center' : rg < lg ? 'end' : 'start'
        var mt = r0(px(cs.marginTop)), mb = r0(px(cs.marginBottom))
        if (mt || mb) root.es.margin = { t: mt, r: 0, b: mb, l: 0 }
      }
      // The part's own background is the section's; text color and font travel with the root box.
      root.es = root.es || {}
      if (!root.es.color) root.es.color = color(bodyCs.color)
      if (!root.es.font && family(bodyCs)) root.es.font = family(bodyCs)
      if (part.inh) {
        if (!root.es.bg && part.inh.bg) root.es.bg = part.inh.bg
        if (!root.es.bgImage && part.inh.img) { root.es.bgImage = part.inh.img; root.es.bgSize = 'auto'; root.es.bgPos = '0% 0%' }
      }
      var bg = root.es.bg && /^#/.test(root.es.bg) ? root.es.bg : bodyBg
      // Name in the editor's navigator: the part's first heading, else its first words.
      var h = el.querySelector('h1,h2,h3,h4')
      var label = (h ? h.textContent : (el.innerText || el.textContent || '')).replace(/\s+/g, ' ').trim().replace(/^[“"'«]+/, '')
      if (!h && label.length > 40) label = label.slice(0, 40).replace(/\s+\S*$/, '') + '…'
      secs.push({ tag: el.tagName.toLowerCase(), id: el.id || '', name: label.slice(0, 60), bg: bg,
        sticky: cs.position === 'sticky' || cs.position === 'fixed' ? 1 : 0, root: root })
    })
    return { v: 1, width: window.innerWidth, seo: seo(), bodyBg: bodyBg, sections: secs, hidden: hiddenPaths(), layouts: layouts(), metrics: window.innerWidth < 700 ? metrics() : {}, warnings: warnings, nodes: count }
  }

  function settle() {
    var imgs = Array.prototype.slice.call(document.images)
    var waits = imgs.map(function (im) {
      if (im.loading === 'lazy') im.loading = 'eager'
      return im.complete ? Promise.resolve() : new Promise(function (res) { im.addEventListener('load', res); im.addEventListener('error', res) })
    })
    var fonts = document.fonts && document.fonts.ready ? document.fonts.ready : Promise.resolve()
    var timeout = new Promise(function (res) { setTimeout(res, 15000) })
    return Promise.race([Promise.all(waits.concat([fonts])), timeout])
      .then(scrollThrough)
      .then(function () { return new Promise(function (res) { setTimeout(res, 600) }) })
  }
  // Scroll the page from top to bottom and back, so content that appears on scroll (reveal
  // animations, lazy images) is in its final state when it is measured.
  function scrollThrough() {
    return new Promise(function (res) {
      var y = 0, h = document.documentElement.scrollHeight, step = Math.max(300, Math.round(window.innerHeight * 0.6))
      function next() {
        y += step
        window.scrollTo(0, y)
        if (y < h && y < 60000) setTimeout(next, 60)
        else setTimeout(function () { window.scrollTo(0, 0); setTimeout(res, 500) }, 300)
      }
      next()
    })
  }

  function go() {
    settle().then(function () {
      var data
      try { data = run() } catch (e) { data = { v: 1, error: String(e && e.message || e) } }
      parent.postMessage({ pursaeExtract: 1, width: window.innerWidth, data: data }, '*')
    })
  }
  if (document.readyState === 'complete') go()
  else window.addEventListener('load', go)
})()
