/**
 * Editor UI language (upstream src/builder/i18n.ts from patch 0004, host/locale.ts): English by
 * default, Turkish for tr_TR, and published output that never depends on the editor's language.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

type I18n = typeof import('@/builder/i18n')

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.resetModules()
})

/** A fresh copy of the builder modules, evaluated with the given boot locale (as in the editor). */
async function bootWith(locale: string | undefined): Promise<I18n> {
  vi.resetModules()
  vi.stubGlobal('window', { __WEB_BUILDER__: locale === undefined ? {} : { locale } })
  await import('../../host/locale')
  return import('@/builder/i18n')
}

/** A fresh copy of the builder modules with the UI language set first (no window: like Node). */
async function fresh(locale: string): Promise<I18n> {
  vi.resetModules()
  const i = await import('@/builder/i18n')
  i.setLocale(locale)
  return i
}

/** Deterministic Math.random, so generated ids repeat between two renders. */
function seedRandom() {
  let x = 0
  vi.spyOn(Math, 'random').mockImplementation(() => ((x = (x * 9301 + 49297) % 233280) / 233280))
}

describe('t()', () => {
  it('maps locales to a catalog language by their language part', async () => {
    const { langOf } = await import('@/builder/i18n')
    expect(langOf('tr_TR')).toBe('tr')
    expect(langOf('tr-TR')).toBe('tr')
    expect(langOf('TR')).toBe('tr')
    expect(langOf('en_US')).toBe('en')
    expect(langOf('de_DE')).toBe('en')
    expect(langOf('')).toBe('en')
    expect(langOf(undefined)).toBe('en')
  })

  it('is English by default and outside a browser', async () => {
    const { t, lang } = await import('@/builder/i18n')
    expect(lang()).toBe('en')
    expect(t('Undo')).toBe('Undo')
    expect(t('{label} changed', { label: 'Color' })).toBe('Color changed')
  })

  it('follows the boot locale: tr_TR is Turkish, other locales English', async () => {
    let i = await bootWith('tr_TR')
    expect(i.lang()).toBe('tr')
    expect(i.t('Undo')).toBe('Geri al')
    expect(i.t('{label} changed', { label: 'Renk' })).toBe('Renk değişti')
    expect(i.intlLocale()).toBe('tr-TR')
    // A source string without an entry stays English.
    expect(i.t('Not in any catalog')).toBe('Not in any catalog')

    i = await bootWith('de_DE')
    expect(i.t('Undo')).toBe('Undo')
    i = await bootWith(undefined)
    expect(i.t('Undo')).toBe('Undo')
  })

  it('keeps unknown placeholders and blanks missing values', async () => {
    const { t } = await import('@/builder/i18n')
    expect(t('{a} and {b}', { a: 1 })).toBe('1 and {b}')
    expect(t('{a}!', { a: undefined })).toBe('!')
  })

  it('withSourceLocale() returns the English source strings', async () => {
    const i = await bootWith('tr_TR')
    expect(i.withSourceLocale(() => i.t('Undo'))).toBe('Undo')
    expect(i.t('Undo')).toBe('Geri al')
  })

  it('tMatch() reads placeholders back from a translated string', async () => {
    const i = await bootWith('tr_TR')
    expect(i.tMatch(i.t('{name} added', { name: 'Karşılama' }), '{name} added')).toEqual({ name: 'Karşılama' })
    expect(i.tMatch('Karşılama silindi', '{name} added')).toBeNull()
  })
})

describe('section kinds and published output', () => {
  it('labels built at module load follow the boot locale', async () => {
    await fresh('tr_TR')
    const tr = await import('@/builder/data')
    await fresh('en_US')
    const en = await import('@/builder/data')
    expect(en.KINDS.faq.name).toBe('FAQ')
    expect(tr.KINDS.faq.name).toBe('Sık sorulanlar')
  })

  it('default page content is English in every UI language', async () => {
    await fresh('tr_TR')
    const { createSection } = await import('@/builder/data')
    const hero = createSection('hero')
    expect(hero.content.title).toBe('WordPress,')
    expect(hero.content.accent).toBe('at the speed of Go.')
  })

  it('renderStatic gives the same HTML whatever the editor language', async () => {
    const render = async (locale: string) => {
      await fresh(locale)
      vi.restoreAllMocks()
      seedRandom()
      const { KINDS, createSection, initialDoc } = await import('@/builder/data')
      const { renderStatic } = await import('@/builder/live/renderStatic')
      const opts = { postId: 42, siteUrl: 'https://example.com', formAction: (id: string) => `https://example.com/forms/${id}` }
      const sections = (Object.keys(KINDS) as (keyof typeof KINDS)[]).map((k) => createSection(k))
      return renderStatic({ site: initialDoc.site, sections }, opts)
    }
    const en = await render('en_US')
    const tr = await render('tr_TR')
    expect(tr.html).toBe(en.html)
    expect(en.html).not.toMatch(/[çğıöşüÇĞİÖŞÜ]/)
  })
})
