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
  // Optional, for themes that are not shops (the forum themes); a store spec leaves them out.
  dark?: boolean // every band uses the dark palette (light text on paper / cream)
  band?: string // call-to-action background (default: ink)
  links?: Links // buttons and links (default: the shop's)
  css?: string // header custom CSS (forum tokens): printed on every page that uses the header
  slugs?: { about: string; contact: string } // page slugs (default: about, contact)
}

interface Links {
  button: [string, string] // header button: label, href
  bannerLink?: [string, string] // announcement link: label, href
  hero: [string, string] // hero buttons: primary href, secondary href
  story: string
  cta: [string, string]
  notFound: { title: string; body: string; primary: string; primaryHref: string; secondary: string; secondaryHref: string }
}

const SHOP_LINKS: Links = {
  button: ['Shop', '/shop/'],
  hero: ['/shop/', '/about/'],
  story: '/about/',
  cta: ['/shop/', '/shop/'],
  notFound: { title: 'Keep exploring', body: 'The shop is only a click away.', primary: 'Go to the shop', primaryHref: '/shop/', secondary: 'Back to the home page', secondaryHref: '/' },
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
  // Pursae Forum themes: the header, hero and tiles link the forum (demo forum slugs in demo/demo.json),
  // the two pages are the guidelines and About, and the header CSS sets the forum's --pf-* tokens.
  {
    slug: 'guild-hall',
    site: { brand: '#C93591', fontPair: 'modern', corners: 'soft', headingFont: 'Chakra Petch', headingWeight: 700, bodyFont: 'Inter', bodyWeight: 400 },
    paper: '#120A26', cream: '#1B1035', deep: '#0B0618', ink: '#F4F1FF', dark: true, band: '#2B1757',
    banner: 'New recruits welcome · read the guild rules, then say hi in Introductions',
    hero: { eyebrow: 'Guild Hall community', title: 'Find your squad.', accent: 'Play better together.', body: 'Looking-for-group posts, builds and guides, clip reviews and a tavern for everything else. Run by players, for players.', primary: 'Join the guild', secondary: 'Browse the forums' },
    perks: { eyebrow: 'Why players stay', title: 'A community that has your back.', body: 'No pay-to-win, no toxicity, no threads lost in a chat scroll.', items: [['Real squads', 'Post your rank, role and schedule, and find people who play like you.', 'users'], ['Guides that last', 'Builds and strategies kept current by the players who run them.', 'book'], ['Fair moderation', 'Clear rules, active moderators and zero tolerance for harassment.', 'shield'], ['Free forever', 'Free to join, and no ads between the posts.', 'heart']] },
    cats: { eyebrow: 'Pick a channel', title: 'Where do you want to drop in?', items: [['Looking for Group', 'Teammates for ranked, raids and co-op nights.', '/forums/f/looking-for-group/'], ['Guides and Builds', 'Loadouts, tier lists and walkthroughs.', '/forums/f/guides-and-builds/'], ['Clips and Highlights', 'Your best plays and your finest fails.', '/forums/f/clips-and-highlights/'], ['Off-Topic Tavern', 'Music, memes and everything else.', '/forums/f/off-topic-tavern/']] },
    story: { eyebrow: 'Guild rules', title: 'Good games start with good people.', body: 'Be kind, tag your spoilers, post in the right channel. A few short rules keep the hall friendly for new players and veterans alike.', link: 'Read the guild rules' },
    quote: { quote: 'Posted in Looking for Group on a Tuesday, had a full raid team by Friday. Six months later we still play every week.', author: 'Nyx', role: 'Raid leader, member since season 3' },
    cta: { title: 'Your squad is waiting.', body: 'Create a free account in a minute and post your first LFG tonight.', primary: 'Join the guild', secondary: 'Browse the forums' },
    footerNote: '© Guild Hall · A community for players, run by players',
    notFound: { title: 'You fell off the map', body: 'This page does not exist, or it moved to a new zone.' },
    about: { title: 'Guild rules', body: 'A few short rules keep the hall a good place to hang out. Moderators apply them fairly and in the open.', points: [['Be kind', 'Criticise plays, never people. No slurs, no harassment, no dogpiling.', 'heart'], ['Tag spoilers', 'Story beats and leaks go behind a spoiler tag, never in a title.', 'shield'], ['Right channel', 'Post where your topic belongs; moderators may move threads that wander.', 'book'], ['No cheats, no trading', 'No cheat links, account sales or real-money trades.', 'lock']] },
    contact: { title: 'About Guild Hall', body: 'Guild Hall started as a raid group chat and grew into a home for players of every game. Questions, partnership ideas or something a moderator should see? Write to the team.' },
    links: {
      button: ['Join', '/forums/register/'],
      bannerLink: ['Log in', '/forums/login/'],
      hero: ['/forums/register/', '/forums/'],
      story: '/guidelines/',
      cta: ['/forums/register/', '/forums/'],
      notFound: { title: 'Respawn at the hall', body: 'Every channel is one click away.', primary: 'Open the forums', primaryHref: '/forums/', secondary: 'Back to the home page', secondaryHref: '/' },
    },
    slugs: { about: 'guidelines', contact: 'about' },
    css: [
      'body.wp-theme-hello-pursae{background:#120A26;color:#F4F1FF}',
      "body.wp-theme-hello-pursae .site-main .entry-title{color:#F4F1FF;font-family:'Chakra Petch',Inter,system-ui,sans-serif}",
      "body .pf{--pf-accent:#B6F23A;--pf-accent-ink:#1B1035;--pf-bg:transparent;--pf-surface:#1B1035;--pf-text:#F4F1FF;--pf-muted:#B9B0D9;--pf-border:rgba(244,241,255,.14);--pf-radius:10px;--pf-font:Inter,system-ui,sans-serif;--pf-font-heading:'Chakra Petch',Inter,system-ui,sans-serif;--pf-danger:#FF7A8F;--pf-success:#8BE36A;--pf-warning:#F5C451;--pf-shadow:none}",
      'body .pf .pf-input,body .pf .pf-textarea,body .pf input[type=text],body .pf input[type=email],body .pf input[type=password],body .pf input[type=search],body .pf input[type=url],body .pf textarea,body .pf select{background:#120A26;color:#F4F1FF;border-color:rgba(244,241,255,.22)}',
    ].join('\n'),
  },
  {
    slug: 'study-circle',
    site: { brand: '#0F766E', fontPair: 'plain', corners: 'round', headingFont: 'Lexend', headingWeight: 600, bodyFont: 'Nunito', bodyWeight: 400 },
    paper: '#FFFFFF', cream: '#F3FAF8', deep: '#12313A', ink: '#12313A',
    banner: 'Exam season study rooms are open · new members get a study buddy',
    hero: { eyebrow: 'Study Circle', title: 'Ask, answer,', accent: 'learn together.', body: 'A friendly forum for students and lifelong learners: subject rooms, study tips and exam prep, with tutors and peers who explain things kindly.', primary: 'Join the circle', secondary: 'Browse subjects' },
    perks: { eyebrow: 'How it works', title: 'Small questions, big progress.', body: 'Post where you are stuck. Someone has been there before.', items: [['Ask anything', 'No question is too basic. Show what you tried and get a clear answer.', 'help'], ['Explain to learn', 'Answering others is the fastest way to master a topic.', 'book'], ['Friendly tutors', 'Volunteer tutors and moderators keep threads helpful and correct.', 'users'], ['Study together', 'Weekly study threads and exam countdowns keep you on track.', 'calendar']] },
    cats: { eyebrow: 'Subject rooms', title: 'Find your subject.', items: [['Mathematics', 'Algebra to calculus, proofs and practice problems.', '/forums/f/mathematics/'], ['Sciences', 'Biology, chemistry and physics questions.', '/forums/f/sciences/'], ['Languages', 'Grammar help, conversation partners and resources.', '/forums/f/languages/'], ['Writing and Humanities', 'Essays, history and literature.', '/forums/f/writing-and-humanities/'], ['Exam Prep', 'Schedules, past papers and last-week nerves.', '/forums/f/exam-prep/'], ['Study Tips', 'Notes, focus and memory techniques that work.', '/forums/f/study-tips/']] },
    story: { eyebrow: 'Community guidelines', title: 'A kind place to not know things yet.', body: 'We explain instead of judging, we show our working, and we never post answers to live exams. Read the guidelines before your first post.', link: 'Read the guidelines' },
    quote: { quote: 'I was failing statistics in October. By June I was the one answering questions in the Mathematics room.', author: 'Lena M.', role: 'Second-year psychology student' },
    cta: { title: 'Stuck on something tonight?', body: 'Join for free and post your question; most get a helpful answer within the hour.', primary: 'Join the circle', secondary: 'Browse subjects' },
    footerNote: '© Study Circle · Ask, answer, learn together',
    notFound: { title: 'This page skipped class', body: 'We could not find it. The subject rooms are a good place to start.' },
    about: { title: 'Community guidelines', body: 'Study Circle works because everyone follows a few simple guidelines. Moderators and tutors apply them kindly and consistently.', points: [['Show your working', 'Share what you tried so helpers can see where you got stuck.', 'pencil'], ['Explain, do not just answer', 'Good replies teach the method, not only the result.', 'book'], ['Academic honesty', 'No answers to live exams or graded work, no paid essay offers.', 'shield'], ['Be patient and kind', 'Everyone was a beginner once. Assume good intent.', 'heart']] },
    contact: { title: 'About Study Circle', body: 'Study Circle is run by volunteer tutors, teachers and students. Want to become a tutor, suggest a subject room or report a problem? Send the team a message.' },
    links: {
      button: ['Join', '/forums/register/'],
      bannerLink: ['Log in', '/forums/login/'],
      hero: ['/forums/register/', '/forums/'],
      story: '/guidelines/',
      cta: ['/forums/register/', '/forums/'],
      notFound: { title: 'Back to studying', body: 'Every subject room is one click away.', primary: 'Open the forums', primaryHref: '/forums/', secondary: 'Back to the home page', secondaryHref: '/' },
    },
    slugs: { about: 'guidelines', contact: 'about' },
    css: [
      'body.wp-theme-hello-pursae.pursae-forum{background:#F3FAF8}',
      'body.wp-theme-hello-pursae .site-main .entry-title{color:#12313A;font-family:Lexend,Nunito,system-ui,sans-serif}',
      'body .pf{--pf-accent:#0F766E;--pf-accent-ink:#FFFFFF;--pf-surface:#FFFFFF;--pf-text:#12313A;--pf-muted:#4F6B73;--pf-border:#D5E7E3;--pf-radius:14px;--pf-font:Nunito,system-ui,sans-serif;--pf-font-heading:Lexend,Nunito,system-ui,sans-serif;--pf-shadow:0 1px 2px rgba(18,49,58,.06),0 8px 24px rgba(18,49,58,.06)}',
    ].join('\n'),
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
  const L = spec.links ?? SHOP_LINKS
  const base: Section['style']['palette'] = spec.dark ? 'dark' : 'light'
  const band = spec.band ?? spec.ink
  const doc = (sections: Section[]): DocState => ({ site: { ...spec.site }, sections })
  const star = (id: Parameters<typeof starterSections>[0], type?: Parameters<typeof starterSections>[1]) =>
    starterSections(id, type).map((s) => tone(s, spec.paper, spec.ink, base))

  // Header: an announcement strip and the navigation bound to the Primary Menu.
  const banner = createSection('banner', L.bannerLink ? { text: spec.banner, link: L.bannerLink[0], linkHref: L.bannerLink[1] } : { text: spec.banner, link: '' })
  tone(banner, spec.deep, spec.ink, 'dark')
  const nav = createSection('nav', { menu: 'primary', logoSource: 'site', primary: L.button[0], primaryHref: L.button[1] })
  nav.name = 'Header'
  nav.items = []
  tone(nav, spec.paper, spec.ink, base)
  if (spec.css) nav.style.css = spec.css
  const header = doc([banner, nav])

  const footer = createSection('footer', { menu: 'footer', logoSource: 'site', note: spec.footerNote })
  footer.name = 'Footer'
  footer.items = []
  tone(footer, spec.deep, spec.ink, 'dark')
  const footerDoc = doc([footer])

  // Front page.
  const hero = createSection('hero', { ...spec.hero, visual: 'image', image: '{{media:hero}}', primaryHref: L.hero[0], secondaryHref: L.hero[1] })
  hero.items = []
  tone(hero, spec.cream, spec.ink, base)
  hero.style.padding = 96
  const cats = createSection('features', { eyebrow: spec.cats.eyebrow, title: spec.cats.title, body: '' })
  items(cats, spec.cats.items, true)
  tone(cats, spec.paper, spec.ink, base)
  const perks = createSection('features', { eyebrow: spec.perks.eyebrow, title: spec.perks.title, body: spec.perks.body })
  items(perks, spec.perks.items)
  tone(perks, spec.cream, spec.ink, base)
  const story = createSection('media', { eyebrow: spec.story.eyebrow, title: spec.story.title, body: spec.story.body, primary: spec.story.link, primaryHref: L.story, image: '{{media:about}}', imageSide: 'left' })
  tone(story, spec.paper, spec.ink, base)
  const quote = createSection('quote', spec.quote)
  tone(quote, spec.cream, spec.ink, base)
  const cta = createSection('cta', { ...spec.cta, primaryHref: L.cta[0], secondaryHref: L.cta[1] })
  tone(cta, band, spec.ink, 'brand')
  const front = doc([hero, cats, perks, story, quote, cta])

  const notFoundText = createSection('text', spec.notFound)
  tone(notFoundText, spec.paper, spec.ink, base)
  notFoundText.style.align = 'center'
  const nfCta = createSection('cta', { ...L.notFound })
  tone(nfCta, band, spec.ink, 'brand')
  const notFound = doc([notFoundText, nfCta])

  // Pages.
  const aboutHero = createSection('text', { title: spec.about.title, body: spec.about.body })
  tone(aboutHero, spec.cream, spec.ink, base)
  const aboutPoints = createSection('features', { eyebrow: '', title: '', body: '' })
  items(aboutPoints, spec.about.points)
  tone(aboutPoints, spec.paper, spec.ink, base)
  const aboutImg = createSection('media', { eyebrow: '', title: spec.story.title, body: spec.story.body, primary: '', image: '{{media:about}}', imageSide: 'right' })
  tone(aboutImg, spec.cream, spec.ink, base)
  const aboutCta = createSection('cta', { ...spec.cta, primaryHref: L.cta[0], secondaryHref: L.cta[1] })
  tone(aboutCta, band, spec.ink, 'brand')
  const about = doc([aboutHero, aboutPoints, aboutImg, aboutCta])

  const contactForm = createSection('form', { eyebrow: 'Contact', title: spec.contact.title, body: spec.contact.body, primary: 'Send message' })
  tone(contactForm, spec.cream, spec.ink, base)
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
      { key: 'about', slug: spec.slugs?.about ?? 'about', title: spec.about.title, doc: about, page: { layout: 'site' } },
      { key: 'contact', slug: spec.slugs?.contact ?? 'contact', title: spec.contact.title, doc: contact, page: { layout: 'site' } },
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
