/**
 * Generates web-builder/parts.json of the library themes that ship Theme Builder parts
 * (docs/developer/web-builder-theme-library.md). Run with `npm run gen:themes` from builder/; the
 * output is committed in library/themes/<slug>/web-builder/. Every section comes from the editor's own
 * factories (createSection, starterSections), so the docs are exactly what the editor would write.
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { it } from 'vitest'
import { createSection, uid } from '@/builder/data'
import { starterSections } from '@/builder/dynamic/starters'
import type { DocState, Section, SiteSettings } from '@/builder/types'

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..')

interface Spec {
  slug: string
  site: SiteSettings
  paper: string // page background
  cream: string // alternate band
  deep: string // footer / dark band
  ink: string // heading colour
  banner: string
  hero: { eyebrow: string; title: string; accent: string; body: string; primary: string; secondary: string }
  perks: { eyebrow: string; title: string; body: string; items: [string, string, string][] }
  cats: { eyebrow: string; title: string; items: [string, string, string][] } // title, text, href
  story: { eyebrow: string; title: string; body: string; link: string }
  quote: { quote: string; author: string; role: string }
  cta: { title: string; body: string; primary: string; secondary: string }
  footerNote: string
  notFound: { title: string; body: string }
  about: { title: string; body: string; points: [string, string, string][] }
  contact: { title: string; body: string }
}

const SPECS: Spec[] = [
  {
    slug: 'kite-and-crayon',
    site: { brand: '#EE5A3C', fontPair: 'plain', corners: 'round', headingFont: 'Outfit', headingWeight: 700, bodyFont: 'Figtree', bodyWeight: 400 },
    paper: '#FFFFFF', cream: '#FFF8EE', deep: '#1E2033', ink: '#26283B',
    banner: 'Free shipping on orders over €40 · gift wrapping is on us',
    hero: { eyebrow: 'Toys for curious minds', title: 'Play that grows', accent: 'with every child.', body: 'Wooden toys, games, art supplies and room decor chosen by parents, tested by kids and packed with care.', primary: 'Shop all toys', secondary: 'Our promise' },
    perks: { eyebrow: 'Why families pick us', title: 'Made to be played with, again and again.', body: 'Every toy goes through a play test before it reaches our shelves.', items: [['Safe by design', 'Non-toxic paints, rounded edges and certified materials.', 'shield'], ['Built to last', 'Solid wood and sturdy stitching that survive siblings.', 'box'], ['Gift ready', 'Free wrapping and a handwritten card on request.', 'gift'], ['Easy returns', 'Thirty days to change your mind, no questions.', 'rotate']] },
    cats: { eyebrow: 'Shop by category', title: 'Find the right kind of fun.', items: [['Toys', 'Stackers, blocks and pull-alongs.', '/product-category/toys/'], ['Games & Puzzles', 'Family game night, sorted.', '/product-category/games/'], ['Arts & Crafts', 'Crayons, paint and sketchbooks.', '/product-category/art/'], ["Kids' Room", 'Night lights, backpacks and more.', '/product-category/room/']] },
    story: { eyebrow: 'Our promise', title: 'Toys we would give our own kids.', body: 'We are a small team of parents and teachers. If we would not put it in our own playroom, it does not go in the shop.', link: 'Read our promise' },
    quote: { quote: 'The wooden stacker has survived two toddlers and a dog. Worth every cent.', author: 'Mara L.', role: 'Parent of two' },
    cta: { title: 'Birthday coming up?', body: 'Our birthday bundle comes wrapped, with a card, ready to give.', primary: 'See the bundle', secondary: 'Browse everything' },
    footerNote: '© Kite & Crayon · Toys for curious minds',
    notFound: { title: 'This page wandered off', body: 'We could not find what you were looking for. Maybe a toy knows where it went.' },
    about: { title: 'Our promise', body: 'Every toy in the shop is tested by real children and checked by real parents before we sell it.', points: [['Tested by kids', 'A toy that is not played with does not make the shelf.', 'smile'], ['Checked by parents', 'We read the labels so you do not have to.', 'shield'], ['Packed with care', 'Plastic-free packaging, always.', 'box']] },
    contact: { title: 'Say hello', body: 'Questions about an order or a gift idea? Write to us and we answer within one working day.' },
  },
  {
    slug: 'good-boy-supply',
    site: { brand: '#F2643D', fontPair: 'plain', corners: 'round', headingFont: 'Fredoka', headingWeight: 600, bodyFont: 'Nunito', bodyWeight: 400 },
    paper: '#FFFFFF', cream: '#FFF7E6', deep: '#121A3B', ink: '#1C2752',
    banner: 'Free delivery over €45 · treats tested by real dogs and cats',
    hero: { eyebrow: 'Gear and treats for very good pets', title: 'Everything your pet', accent: 'will tail-wag for.', body: 'Beds, leashes, toys and treats picked by owners, approved by our in-house testers.', primary: 'Shop for dogs', secondary: 'Shop for cats' },
    perks: { eyebrow: 'Why owners trust us', title: 'Pet-tested, owner-approved.', body: 'Our testers chew, scratch and nap on everything first.', items: [['Tested by pets', 'Real dogs and cats review every new product.', 'heart'], ['Vet-friendly', 'Ingredients and materials we are happy to explain.', 'shield'], ['Fast delivery', 'Most orders leave the warehouse the same day.', 'truck'], ['Easy returns', 'Not a fit? Send it back within thirty days.', 'rotate']] },
    cats: { eyebrow: 'Shop by pet', title: 'Find the good stuff.', items: [['Dogs', 'Beds, leashes and harnesses.', '/product-category/dogs/'], ['Cats', 'Trees, scratchers and wands.', '/product-category/cats/'], ['Toys', 'Tug, squeak and chase.', '/product-category/toys/'], ['Treats & Care', 'Snacks and grooming.', '/product-category/care/']] },
    story: { eyebrow: 'Our testers', title: 'Meet the pets behind the picks.', body: 'Every product spends two weeks with our tester pack before it is allowed on the shelf. Their reviews are brutally honest.', link: 'Meet the testers' },
    quote: { quote: 'The orthopedic bed ended the midnight pacing. Biscuit sleeps till seven now.', author: 'Jonas K.', role: 'Biscuit’s human' },
    cta: { title: 'New puppy at home?', body: 'The puppy starter kit has everything for week one, in one box.', primary: 'See the starter kit', secondary: 'Browse everything' },
    footerNote: '© Good Boy Supply · Gear and treats for very good pets',
    notFound: { title: 'Oops, that page ran off', body: 'We sniffed around but could not find it. Try the shop instead.' },
    about: { title: 'Our testers', body: 'Four dogs, three cats and one very opinionated parrot test every product we sell.', points: [['Honest reviews', 'If it gets chewed in a day, we do not stock it.', 'heart'], ['Real homes', 'Testing happens on couches, not in labs.', 'home'], ['Happy tails', 'Only products with a wag make the cut.', 'smile']] },
    contact: { title: 'Get in touch', body: 'Need help choosing a harness size or tracking a parcel? Send us a note.' },
  },
  {
    slug: 'fernwood',
    site: { brand: '#C2613F', fontPair: 'elegant', corners: 'soft', headingFont: 'Fraunces', headingWeight: 600, bodyFont: 'Albert Sans', bodyWeight: 400 },
    paper: '#FFFDF8', cream: '#F6F1E7', deep: '#132A1D', ink: '#1F3D2B',
    banner: 'Free shipping on plants over €50 · each one arrives healthy or we replace it',
    hero: { eyebrow: 'Plants, planters and good advice', title: 'Bring a little', accent: 'greenhouse home.', body: 'Healthy houseplants, handmade planters and simple care guides from a family-run greenhouse.', primary: 'Shop houseplants', secondary: 'Visit our greenhouse' },
    perks: { eyebrow: 'Grown with care', title: 'Plants that arrive happy.', body: 'We grow, pack and ship every plant ourselves.', items: [['Healthy guarantee', 'If it arrives unhappy, we send a new one.', 'leaf'], ['Care guides', 'Light, water and feeding made simple.', 'book'], ['Plastic-light packing', 'Recycled paper and compostable fill.', 'recycle'], ['Gift ready', 'Wrapped and with a handwritten note.', 'gift']] },
    cats: { eyebrow: 'Shop by category', title: 'Everything a plant needs.', items: [['Houseplants', 'From easy-going to adventurous.', '/product-category/houseplants/'], ['Planters', 'Terracotta, ceramic and hanging.', '/product-category/planters/'], ['Care & Tools', 'Watering cans, food and shears.', '/product-category/care/'], ['Gifts', 'Boxes that keep on growing.', '/product-category/gifts/']] },
    story: { eyebrow: 'Our greenhouse', title: 'A family greenhouse, open to everyone.', body: 'Three generations of growers, one glasshouse and a stubborn belief that everybody can keep a plant alive.', link: 'Read our story' },
    quote: { quote: 'My monstera has doubled in size since the care card arrived. I finally feel like a plant person.', author: 'Elif T.', role: 'Customer since 2024' },
    cta: { title: 'Not sure where to start?', body: 'The plant gift box pairs an easy plant with a handmade pot and a care card.', primary: 'See the gift box', secondary: 'Browse everything' },
    footerNote: '© Fernwood · Plants, planters and good advice',
    notFound: { title: 'This path leads nowhere', body: 'The page you wanted has been repotted somewhere else. Let us help you find it.' },
    about: { title: 'Our greenhouse', body: 'We grow most of what we sell ourselves, and the rest comes from growers we have known for years.', points: [['Grown locally', 'Acclimatised to indoor life before they ship.', 'leaf'], ['Shared know-how', 'Care cards in every box.', 'book'], ['Low waste', 'Reused pots and recycled packaging.', 'recycle']] },
    contact: { title: 'Ask a grower', body: 'Yellow leaves? Not sure which pot? Write to us and a grower will answer.' },
  },
]

/** Section look: palette light on the theme's paper or cream, headings in the theme's ink colour. */
function tone(s: Section, bg: string, ink: string, palette: Section['style']['palette'] = 'light'): Section {
  s.style = { ...s.style, palette, bg, titleColor: palette === 'dark' ? undefined : ink }
  return s
}

const ICONS = ['shield', 'box', 'puzzle', 'zap', 'timer', 'gauge'] // the editor's FEATURE_ICONS

/** Cards: [title, text, href?]; icons cycle through the editor's set. */
function items(s: Section, list: [string, string, string?][], hrefs = false) {
  s.items = list.map(([title, text, extra], n) => ({ id: uid('i'), title, text, icon: ICONS[n % ICONS.length], ...(hrefs && extra ? { href: extra } : {}) }))
}

function build(spec: Spec) {
  const doc = (sections: Section[]): DocState => ({ site: { ...spec.site }, sections })
  const star = (id: Parameters<typeof starterSections>[0], type?: Parameters<typeof starterSections>[1]) =>
    starterSections(id, type).map((s) => tone(s, spec.paper, spec.ink))

  // Header: an announcement strip and the navigation bound to the Primary Menu.
  const banner = createSection('banner', { text: spec.banner, link: '' })
  tone(banner, spec.deep, spec.ink, 'dark')
  const nav = createSection('nav', { menu: 'primary', logoSource: 'site', primary: 'Shop', primaryHref: '/shop/' })
  nav.name = 'Header'
  nav.items = []
  tone(nav, spec.paper, spec.ink)
  const header = doc([banner, nav])

  const footer = createSection('footer', { menu: 'footer', logoSource: 'site', note: spec.footerNote })
  footer.name = 'Footer'
  footer.items = []
  tone(footer, spec.deep, spec.ink, 'dark')
  const footerDoc = doc([footer])

  // Front page.
  const hero = createSection('hero', { ...spec.hero, visual: 'image', image: '{{media:hero}}', primaryHref: '/shop/', secondaryHref: '/about/' })
  hero.items = []
  tone(hero, spec.cream, spec.ink)
  hero.style.padding = 96
  const cats = createSection('features', { eyebrow: spec.cats.eyebrow, title: spec.cats.title, body: '' })
  items(cats, spec.cats.items, true)
  tone(cats, spec.paper, spec.ink)
  const perks = createSection('features', { eyebrow: spec.perks.eyebrow, title: spec.perks.title, body: spec.perks.body })
  items(perks, spec.perks.items)
  tone(perks, spec.cream, spec.ink)
  const story = createSection('media', { eyebrow: spec.story.eyebrow, title: spec.story.title, body: spec.story.body, primary: spec.story.link, primaryHref: '/about/', image: '{{media:about}}', imageSide: 'left' })
  tone(story, spec.paper, spec.ink)
  const quote = createSection('quote', spec.quote)
  tone(quote, spec.cream, spec.ink)
  const cta = createSection('cta', { ...spec.cta, primaryHref: '/shop/', secondaryHref: '/shop/' })
  tone(cta, spec.ink, spec.ink, 'brand')
  const front = doc([hero, cats, perks, story, quote, cta])

  const notFoundText = createSection('text', spec.notFound)
  tone(notFoundText, spec.paper, spec.ink)
  notFoundText.style.align = 'center'
  const nfCta = createSection('cta', { title: 'Keep exploring', body: 'The shop is only a click away.', primary: 'Go to the shop', primaryHref: '/shop/', secondary: 'Back to the home page', secondaryHref: '/' })
  tone(nfCta, spec.ink, spec.ink, 'brand')
  const notFound = doc([notFoundText, nfCta])

  // Pages.
  const aboutHero = createSection('text', { title: spec.about.title, body: spec.about.body })
  tone(aboutHero, spec.cream, spec.ink)
  const aboutPoints = createSection('features', { eyebrow: '', title: '', body: '' })
  items(aboutPoints, spec.about.points)
  tone(aboutPoints, spec.paper, spec.ink)
  const aboutImg = createSection('media', { eyebrow: '', title: spec.story.title, body: spec.story.body, primary: '', image: '{{media:about}}', imageSide: 'right' })
  tone(aboutImg, spec.cream, spec.ink)
  const aboutCta = createSection('cta', { ...spec.cta, primaryHref: '/shop/', secondaryHref: '/shop/' })
  tone(aboutCta, spec.ink, spec.ink, 'brand')
  const about = doc([aboutHero, aboutPoints, aboutImg, aboutCta])

  const contactForm = createSection('form', { eyebrow: 'Contact', title: spec.contact.title, body: spec.contact.body, primary: 'Send message' })
  tone(contactForm, spec.cream, spec.ink)
  const contact = doc([contactForm])

  const tpl = (key: string, type: string, title: string, conditions: string[], d: DocState, priority = 0) => ({ key, type, title, conditions, priority, doc: d })
  return {
    format: 'web-builder-theme',
    version: 1,
    templates: [
      tpl('header', 'header', 'Header', ['include/general'], header),
      tpl('footer', 'footer', 'Footer', ['include/general'], footerDoc),
      tpl('front-page', 'front-page', 'Front page', ['include/singular/front_page'], front),
      tpl('single-post', 'single-post', 'Single post', ['include/singular/post'], doc(star('single', 'single-post'))),
      tpl('single-page', 'single-page', 'Single page', ['include/singular/page'], doc(star('single', 'single-page'))),
      tpl('archive', 'archive', 'Archive', ['include/archive'], doc(star('archive'))),
      tpl('search-results', 'search-results', 'Search results', ['include/archive/search'], doc(star('search'))),
      tpl('error-404', 'error-404', '404 page', ['include/404'], notFound),
    ],
    pages: [
      { key: 'about', slug: 'about', title: spec.about.title, doc: about, page: { layout: 'site' } },
      { key: 'contact', slug: 'contact', title: spec.contact.title, doc: contact, page: { layout: 'site' } },
    ],
  }
}

it('writes the theme parts', () => {
  for (const spec of SPECS) {
    const dir = path.join(REPO, 'library', 'themes', spec.slug, 'web-builder')
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, 'parts.json'), JSON.stringify(build(spec), null, 1) + '\n')
  }
})
