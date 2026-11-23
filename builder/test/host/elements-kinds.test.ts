import { describe, expect, it } from 'vitest'
import { attr, elementKids, parse, textOf, type El, type Tree } from '../../host/elements/html'
import { KIND_ICON, ROW_CAP, autoName, groupKids, humanClass, kindLabel, kindOf, labelWithCount, sectionName, tagName, type Kind } from '../../host/elements/kinds'
import { childIds } from '../../host/elements/ops'

/** Marks every element with data-wbc (numbered in document order) so the tree has ids like an imported page. */
function mark(html: string): string {
  let n = 0
  return html.replace(/<([a-z][a-z0-9]*)(?=[\s>/])/gi, (m, tag: string) => (/^(html|head|body)$/i.test(tag) ? m : `<${tag} data-wbc="${++n}"`))
}

function find(t: Tree, id: string): El {
  let hit: El | undefined
  const walk = (e: El) => {
    if (attr(e, 'id') === id) hit = e
    for (const k of elementKids(e)) walk(k)
  }
  walk(t.root)
  if (!hit) throw new Error(`no #${id}`)
  return hit
}

const TAILWIND = mark(`
<header id="top" class="sticky top-0 z-50 bg-white">
  <div class="mx-auto flex max-w-6xl items-center justify-between px-4">
    <a id="brand" href="/"><img id="logoimg" src="/logo.svg" alt="Acme"></a>
    <nav id="mainnav" class="hidden md:flex"><ul id="navlist" class="flex gap-6"><li><a href="/a">Product</a></li><li><a href="/b">Pricing</a></li><li><a href="/c">Docs</a></li></ul></nav>
    <button id="menubtn" class="md:hidden"><svg id="burger" viewBox="0 0 24 24"><path d="M3 6h18"/></svg></button>
  </div>
</header>
<section id="hero" class="hero relative py-24"><div class="mx-auto max-w-3xl text-center"><span id="pill" class="badge rounded-full">New</span><h1 id="h">Build faster</h1><p id="lead">Ship a site in minutes.</p><a id="cta" href="/start" class="btn">Get started</a></div></section>
<section id="prices" class="py-16"><div id="grid" class="mx-auto grid max-w-5xl grid-cols-3 gap-6">
  <div class="pricing-card rounded-xl border p-6"><h3>Free</h3><p>For hobby</p></div>
  <div class="pricing-card rounded-xl border p-6"><h3>Pro</h3><p>For teams</p></div>
  <div class="pricing-card rounded-xl border p-6"><h3>Max</h3><p>For scale</p></div>
</div></section>
<section id="team-section" class="mx-auto flex max-w-4xl"><div id="plain" class="mx-auto flex gap-4"><span>a</span></div></section>
<footer id="foot"><div id="cols" class="grid grid-cols-4"><div><h4>Company</h4><ul><li><a href="/x">About</a></li></ul></div><div><h4>Legal</h4><ul><li><a href="/y">Terms</a></li></ul></div><div><h4>Help</h4><ul><li><a href="/z">FAQ</a></li></ul></div></div></footer>
<script id="s1" src="/assets/app.js"></script><style id="st">a{color:red}</style><template id="tp"><div>x</div></template><noscript id="ns">no js</noscript>
`)

const WORDPRESS = mark(`
<div id="page" class="site wp-site-blocks">
  <header id="masthead" class="wp-block-template-part site-header" role="banner"><div class="wp-block-group is-layout-flex"><a class="custom-logo-link" href="/"><img id="wplogo" class="custom-logo" src="/l.png" alt="Blog"></a><nav id="wpnav" class="wp-block-navigation" aria-label="Primary"><ul id="wpul" class="wp-block-navigation__container"><li><a href="/1">One</a></li></ul></nav></div></header>
  <main id="mainc" class="wp-block-group"><article id="post" class="post type-post"><h2 class="entry-title">Hello world</h2><blockquote id="bq"><p>Quote me</p></blockquote><hr id="rule" class="wp-block-separator"><figure id="fig" class="wp-block-image"><img src="/p.jpg" alt="Photo"><figcaption>Cap</figcaption></figure>
  <details id="faq"><summary>Why?</summary><p>Because.</p></details><table id="tbl"><tbody><tr><td>1</td></tr></tbody></table></article>
  <aside id="side" class="widget-area" aria-label="Sidebar"><div class="widget">w</div></aside></main>
  <form id="contact" class="wpcf7-form"><input id="in1" type="text" name="your-name" placeholder="Your name"><textarea id="ta" name="msg"></textarea><input id="send" type="submit" value="Send"></form>
  <dialog id="dlg"><p>Hi</p></dialog>
  <div id="role-nav" role="navigation" aria-label="Breadcrumbs"><a href="/">Home</a></div>
  <div id="tabs" role="tablist"><button role="tab">A</button><button role="tab">B</button></div>
  <ul id="posts" class="blog-list"><li><h3>One</h3><p>a</p></li><li><h3>Two</h3><p>b</p></li><li><h3>Three</h3><p>c</p></li><li><h3>Four</h3><p>d</p></li></ul>
</div>`)

const THREE = mark(`
<canvas id="cv" class="webgl"></canvas>
<div id="ui" class="overlay"><button id="theme" aria-label="Toggle dark mode"><svg id="sun" width="20" height="20" viewBox="0 0 20 20"><circle r="4"/></svg></button></div>
<svg id="art" width="480" height="320" viewBox="0 0 480 320"><path d="M0 0"/></svg>
<svg id="art2" viewBox="0 0 400 400"><path d="1"/><path d="2"/><path d="3"/><path d="4"/><path d="5"/><path d="6"/><path d="7"/><path d="8"/><path d="9"/><path d="10"/></svg>
<iframe id="yt" src="https://www.youtube.com/embed/abc"></iframe><iframe id="vm" src="https://player.vimeo.com/video/1"></iframe>
<iframe id="map" src="https://www.google.com/maps/embed?pb=1"></iframe><iframe id="other" src="https://example.com/x" title="Widget"></iframe>
<audio id="au" src="/a.mp3"></audio>
<div id="sw" class="swiper"><div class="swiper-wrapper"><div class="swiper-slide">1</div><div class="swiper-slide">2</div></div></div>
<div id="car" class="product-carousel"><div>1</div></div>
<script id="mod" type="module" src="https://cdn.jsdelivr.net/npm/three@0.160/build/three.module.js"></script>
`)

const CASES: [string, string, Kind, string, string][] = [
  // [page name, element id, expected kind, expected label, html]
  ['tailwind', 'top', 'header', 'Header', TAILWIND],
  ['tailwind', 'mainnav', 'menu', 'Menu', TAILWIND],
  ['tailwind', 'navlist', 'menuitems', 'Menu items', TAILWIND],
  ['tailwind', 'logoimg', 'logo', 'Logo', TAILWIND],
  ['tailwind', 'burger', 'icon', 'Icon', TAILWIND],
  ['tailwind', 'hero', 'hero', 'Hero', TAILWIND],
  ['tailwind', 'pill', 'badge', 'Badge', TAILWIND],
  ['tailwind', 'h', 'heading', 'Heading', TAILWIND],
  ['tailwind', 'cta', 'link', 'Link', TAILWIND],
  ['tailwind', 'prices', 'section', 'Section', TAILWIND],
  ['tailwind', 'grid', 'cards', 'Cards', TAILWIND],
  ['tailwind', 'plain', 'flex', 'Flexbox', TAILWIND],
  ['tailwind', 'cols', 'grid', 'Grid', TAILWIND], // footer columns are link lists, not cards
  ['tailwind', 'foot', 'footer', 'Footer', TAILWIND],
  ['tailwind', 's1', 'code', 'Code', TAILWIND],
  ['tailwind', 'st', 'code', 'Code', TAILWIND],
  ['tailwind', 'tp', 'code', 'Code', TAILWIND],
  ['tailwind', 'ns', 'code', 'Code', TAILWIND],
  ['wordpress', 'masthead', 'header', 'Header', WORDPRESS],
  ['wordpress', 'wplogo', 'logo', 'Logo', WORDPRESS],
  ['wordpress', 'wpnav', 'menu', 'Menu', WORDPRESS],
  ['wordpress', 'wpul', 'menuitems', 'Menu items', WORDPRESS],
  ['wordpress', 'mainc', 'main', 'Main content', WORDPRESS],
  ['wordpress', 'post', 'article', 'Article', WORDPRESS],
  ['wordpress', 'bq', 'quote', 'Quote', WORDPRESS],
  ['wordpress', 'rule', 'divider', 'Divider', WORDPRESS],
  ['wordpress', 'fig', 'figure', 'Figure', WORDPRESS],
  ['wordpress', 'faq', 'accordion', 'Accordion', WORDPRESS],
  ['wordpress', 'tbl', 'table', 'Table', WORDPRESS],
  ['wordpress', 'side', 'aside', 'Sidebar', WORDPRESS],
  ['wordpress', 'contact', 'form', 'Form', WORDPRESS],
  ['wordpress', 'in1', 'field', 'Field', WORDPRESS],
  ['wordpress', 'ta', 'field', 'Field', WORDPRESS],
  ['wordpress', 'send', 'inputbutton', 'Button', WORDPRESS],
  ['wordpress', 'dlg', 'popup', 'Popup', WORDPRESS],
  ['wordpress', 'role-nav', 'menu', 'Menu', WORDPRESS],
  ['wordpress', 'tabs', 'tabs', 'Tabs', WORDPRESS],
  ['wordpress', 'posts', 'cards', 'Cards', WORDPRESS],
  ['three', 'cv', 'canvas', '3D / Canvas', THREE],
  ['three', 'sun', 'icon', 'Icon', THREE],
  ['three', 'art', 'graphic', 'Graphic', THREE],
  ['three', 'art2', 'graphic', 'Graphic', THREE],
  ['three', 'yt', 'video', 'Video / embed', THREE],
  ['three', 'vm', 'video', 'Video / embed', THREE],
  ['three', 'map', 'map', 'Map', THREE],
  ['three', 'other', 'video', 'Video / embed', THREE],
  ['three', 'au', 'audio', 'Audio', THREE],
  ['three', 'sw', 'slider', 'Slider', THREE],
  ['three', 'car', 'slider', 'Slider', THREE],
  ['three', 'mod', 'code', 'Code', THREE],
]

describe('element kinds on realistic markup', () => {
  const trees = new Map<string, Tree>()
  const tree = (html: string) => trees.get(html) ?? trees.set(html, parse(html)).get(html)!
  it.each(CASES)('%s #%s -> %s (%s)', (_page, id, kind, label, html) => {
    const t = tree(html)
    const k = kindOf(t, find(t, id))
    expect(k).toBe(kind)
    expect(kindLabel(k)).toBe(label)
    expect(KIND_ICON[k]).toBeTruthy() // every kind has an icon
  })

  it('does not call a text-only, a link-only or an unmatched list "Cards"', () => {
    const t = parse(mark('<div id="a"><p>1</p><p>2</p><p>3</p></div><div><ul id="b"><li>a</li><li>b</li><li>c</li></ul></div><div><div id="c" class="slider-input"><div>1</div></div></div>'))
    expect(kindOf(t, find(t, 'a'))).not.toBe('cards')
    expect(kindOf(t, find(t, 'b'))).toBe('list')
    expect(kindOf(t, find(t, 'c'))).not.toBe('slider')
  })

  it('keeps top-level elements sections (except landmarks), and the old kinds', () => {
    const t = parse(mark('<section id="s"><h2 id="h2">T</h2><p id="p">a <b>b</b></p><button id="b">x</button></section>'))
    expect(kindOf(t, find(t, 's'))).toBe('section')
    expect(kindOf(t, find(t, 'h2'))).toBe('heading')
    expect(kindOf(t, find(t, 'p'))).toBe('text')
    expect(kindOf(t, find(t, 'b'))).toBe('button')
  })
})

describe('row names', () => {
  const name = (html: string, id: string) => {
    const t = parse(mark(html))
    const el = find(t, id)
    return autoName(t, el)
  }
  it('prefers aria-label, alt, heading, text, id, then a humanised class', () => {
    expect(name('<button id="x" aria-label="Toggle dark mode"><svg></svg></button>', 'x')).toBe('Toggle dark mode')
    expect(name('<img id="x" alt="Team photo" src="a.png">', 'x')).toBe('Team photo')
    expect(name('<div id="x" class="mx-auto"><h2>Our story</h2><p>text</p></div>', 'x')).toBe('Our story')
    expect(name('<div id="x" class="mx-auto">Welcome home</div>', 'x')).toBe('Welcome home')
    expect(name('<div class="mx-auto flex pricing-card" id="x"><span><i></i></span></div>', 'x')).toBe('Pricing card')
    expect(name('<div id="faq-section" class="mx-auto"><span><i></i></span></div>', 'faq-section')).toBe('Faq section')
    expect(name('<div id="x" class="mx-auto flex gap-4 text-center md:p-6"><i></i></div>', 'x')).toBe('') // utilities only
  })
  it('never shows the raw tag.class as a name', () => {
    expect(name('<div id="x" class="mx-auto"><i></i></div>', 'x')).not.toMatch(/mx-auto/)
    const t = parse(mark('<div id="x" class="mx-auto"></div>'))
    expect(tagName(find(t, 'x'))).toBe('div#x') // the tooltip keeps the developer view
  })
  it('humanises classes and skips utility / generated ones', () => {
    expect(humanClass('pricing-card')).toBe('Pricing card')
    expect(humanClass('hero__title')).toBe('Hero title')
    expect(humanClass('wp-block-group')).toBe('Group')
    expect(humanClass('heroSection')).toBe('Hero section')
    for (const c of ['mx-auto', 'flex', 'text-center', 'md:flex', 'p-6', 'css-1a2b3c', 'w-[200px]', 'is-layout-flex', 'container', 'grid-cols-3']) expect(humanClass(c)).toBeNull()
  })
  it('counts repeated children in the label', () => {
    expect(labelWithCount('cards', 6)).toBe('Cards · 6')
    expect(labelWithCount('cards', 1)).toBe('Cards')
    expect(labelWithCount('div', 6)).toBe('Div')
  })
  it('names sections: heading, landmark, aria-label, id or class, Section N', () => {
    const t = parse(TAILWIND)
    const tops = elementKids(t.root)
    const names = tops.map((e, i) => sectionName(t, e, i))
    expect(names[0]).toBe('Header') // a header without a heading of its own
    expect(names[1]).toBe('Build faster')
    expect(names[2]).toBe('Free') // its first heading
    expect(names[3]).toBe('Team section') // id
    expect(names[4]).toBe('Company') // footer: first heading wins
    const w = parse(mark('<div aria-label="Cookie notice"><i></i></div><div id="x1" class="mx-auto"><i></i></div><div class="mx-auto"><i></i></div>'))
    const [a, b, c] = elementKids(w.root)
    expect(sectionName(w, a, 0)).toBe('Cookie notice')
    expect(sectionName(w, b, 1)).toBe('X1')
    expect(sectionName(w, c, 2)).toBe('Section 3')
  })
})

describe('quiet code elements and caps', () => {
  it('groups scripts and styles apart from the blocks', () => {
    const t = parse(TAILWIND)
    const { blocks, quiet } = groupKids(t, childIds(t, t.root))
    expect(quiet.map((id) => t.byId.get(id)!.tag)).toEqual(['script', 'style', 'template', 'noscript'])
    expect(blocks.map((id) => t.byId.get(id)!.tag)).toEqual(['header', 'section', 'section', 'section', 'footer'])
  })

  it('names and types 5,000 elements quickly (a navigator cap keeps the rendered rows small)', () => {
    const rows = Array.from({ length: 1000 }, (_, i) => `<div class="card rounded p-4"><h3>Item ${i}</h3><p>Text ${i}</p><a href="/${i}">Go</a><img src="/i${i}.png" alt="Pic ${i}"></div>`).join('')
    const t = parse(mark(`<section><div class="grid grid-cols-4">${rows}</div></section>`))
    expect(t.byId.size).toBeGreaterThan(5000)
    const t0 = performance.now()
    let cards = 0
    for (const el of t.byId.values()) {
      const k = kindOf(t, el)
      autoName(t, el, k)
      if (k === 'cards') cards++
    }
    const ms = performance.now() - t0
    expect(cards).toBe(1)
    expect(ms).toBeLessThan(2500)
    const grid = [...t.byId.values()].find((e) => kindOf(t, e) === 'cards')!
    const ids = childIds(t, grid)
    expect(ids).toHaveLength(1000)
    expect(Math.min(ids.length, ROW_CAP)).toBe(ROW_CAP) // only ROW_CAP rows render until "Show N more"
    expect(labelWithCount('cards', ids.length)).toBe('Cards · 1000')
  })
})

describe('names keep inline text together', () => {
  it('a heading with inline spans is named as it reads', () => {
    const src = '<section data-wbc="1"><h1 data-wbc="2">Birlikteyiz; çünkü <span data-wbc="3">güvenli iş</span>, <span data-wbc="4">sağlıklı işçi</span> bizim işimiz</h1><div data-wbc="5"><p data-wbc="6">One</p><p data-wbc="7">Two</p></div></section>'
    const tr = parse(src)
    const els = (e: El): El[] => [e, ...elementKids(e).flatMap(els)]
    const all = elementKids(tr.root).flatMap(els)
    const h1 = all.find((e) => e.tag === 'h1')!
    const div = all.find((e) => e.tag === 'div')!
    expect(textOf(tr, h1)).toBe("Birlikteyiz; çünkü güvenli iş, sağlıklı işçi bizim işimiz")
    expect(autoName(tr, h1)).toMatch(/^Birlikteyiz; çünkü güvenli/)
    expect(textOf(tr, div)).toBe("One Two")
  })
})
