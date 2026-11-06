/**
 * Display conditions model of the conditions dialog (host/conditions.ts, contract §11.3).
 */
import { describe, expect, it } from 'vitest'
import {
  clampPriority,
  emptyRow,
  hasInclude,
  idSource,
  isValidRule,
  parseRule,
  rowRule,
  rowsToRules,
  ruleLabel,
  serverErrorIndex,
  subOptions,
  targetOptions,
  updateRow,
  type ConditionRow,
} from '../../host/conditions'
import { CONDITION_LIMITS } from '../../host/contract'
import { SITE } from './template-fixtures'

const values = (o: { value: string }[]) => o.map((x) => x.value)

describe('rules and rows', () => {
  const rules = [
    'include/general',
    'include/404',
    'include/singular',
    'include/singular/front_page',
    'include/singular/post',
    'exclude/singular/page/12',
    'include/singular/post/in/category/4',
    'include/singular/page/child_of/9',
    'include/singular/book/by_author/3',
    'include/archive',
    'include/archive/home',
    'include/archive/date',
    'include/archive/search',
    'include/archive/author',
    'exclude/archive/author/5',
    'include/archive/type/book',
    'include/archive/category',
    'include/archive/post_tag/77',
  ]

  it.each(rules)('round-trips %s', (rule) => {
    const row = parseRule(rule)
    expect(row).not.toBeNull()
    expect(rowRule(row!)).toBe(rule)
  })

  it('parses into the cascade levels', () => {
    expect(parseRule('include/singular/post/in/category/4')).toEqual({ mode: 'include', group: 'singular', sub: 'post', target: 'in/category', id: '4' })
    expect(parseRule('exclude/archive/type/book')).toEqual({ mode: 'exclude', group: 'archive', sub: 'type/book', target: '', id: '' })
    expect(parseRule('include/archive/author/5')).toEqual({ mode: 'include', group: 'archive', sub: 'author', target: 'id', id: '5' })
    expect(parseRule('  include/general  ')).toEqual({ mode: 'include', group: 'general', sub: '', target: '', id: '' })
  })

  it.each([
    'include',
    'show/general',
    'include/everywhere',
    'include/singular/post/0',
    'include/singular/post/012',
    'include/singular/Post',
    'include/singular/in',
    'include/singular/child_of/4',
    'include/singular/by_author',
    'include/archive/type',
    'include/archive/home/4',
    'include/archive/search/1',
    'include/404/1',
    'include/singular/post/in/category',
  ])('rejects %s', (rule) => {
    expect(isValidRule(rule)).toBe(false)
    expect(parseRule(rule)).toBeNull()
  })

  it('accepts the longest names the grammar allows (well under the byte limit)', () => {
    const long = 'include/singular/' + 'a'.repeat(32) + '/in/' + 'b'.repeat(32) + '/1'
    expect(isValidRule(long)).toBe(true)
    expect(new TextEncoder().encode(long).length).toBeLessThanOrEqual(CONDITION_LIMITS.maxConditionBytes)
  })

  it('an incomplete row has no rule', () => {
    expect(rowRule({ mode: 'include', group: 'singular', sub: 'post', target: 'id', id: '' })).toBeNull()
    expect(rowRule({ mode: 'include', group: 'archive', sub: 'category', target: 'id', id: 'x' })).toBeNull()
  })

  it('rowsToRules drops duplicates, keeps the order and reports incomplete rows', () => {
    const rows: ConditionRow[] = [parseRule('include/general')!, parseRule('exclude/singular/page/12')!, parseRule('include/general')!, { mode: 'include', group: 'singular', sub: 'page', target: 'id', id: '' }]
    const r = rowsToRules(rows)
    expect(r.rules).toEqual(['include/general', 'exclude/singular/page/12'])
    expect(Object.keys(r.errors)).toEqual(['3'])
    expect(r.errors[3]).toMatch(/Choose an item/)
  })

  it('hasInclude and clampPriority', () => {
    expect(hasInclude(['exclude/general'])).toBe(false)
    expect(hasInclude(['exclude/general', 'include/404'])).toBe(true)
    expect(hasInclude([])).toBe(false)
    expect(clampPriority(1000)).toBe(100)
    expect(clampPriority(-1000)).toBe(-100)
    expect(clampPriority(3.7)).toBe(3)
    expect(clampPriority(Number.NaN)).toBe(0)
  })

  it('maps server error paths', () => {
    expect(serverErrorIndex('/conditions/3')).toBe(3)
    expect(serverErrorIndex('/priority')).toBe('priority')
    expect(serverErrorIndex('/doc')).toBeNull()
  })
})

describe('scope cascade from the boot post types and taxonomies', () => {
  it('singular lists all, front page and every viewable type', () => {
    expect(values(subOptions(SITE, 'singular'))).toEqual(['', 'front_page', 'post', 'page', 'book'])
    expect(subOptions(SITE, 'general')).toEqual([])
    expect(subOptions(SITE, '404')).toEqual([])
  })

  it('archive lists fixed scopes, type archives (archive URL only) and taxonomies', () => {
    expect(values(subOptions(SITE, 'archive'))).toEqual(['', 'home', 'date', 'search', 'author', 'type/post', 'type/book', 'category', 'post_tag'])
  })

  it('keeps a stored value the site does not list', () => {
    expect(values(subOptions(SITE, 'singular', 'movie'))).toContain('movie')
    expect(values(subOptions(SITE, 'archive', 'type/movie'))).toContain('type/movie')
  })

  it('targets: in/<tax> only for the taxonomies of the type, child_of only when hierarchical', () => {
    expect(values(targetOptions(SITE, { group: 'singular', sub: 'post' }))).toEqual(['', 'id', 'in/category', 'in/post_tag', 'by_author'])
    expect(values(targetOptions(SITE, { group: 'singular', sub: 'page' }))).toEqual(['', 'id', 'child_of', 'by_author'])
    expect(values(targetOptions(SITE, { group: 'singular', sub: 'front_page' }))).toEqual([])
    expect(values(targetOptions(SITE, { group: 'archive', sub: 'category' }))).toEqual(['', 'id'])
    expect(values(targetOptions(SITE, { group: 'archive', sub: 'author' }))).toEqual(['', 'id'])
    expect(values(targetOptions(SITE, { group: 'archive', sub: 'home' }))).toEqual([])
    expect(values(targetOptions(SITE, { group: 'archive', sub: 'type/book' }))).toEqual([])
  })

  it('changing a level resets the levels below', () => {
    const row = parseRule('include/singular/post/in/category/4')!
    expect(updateRow(row, { sub: 'page' })).toMatchObject({ sub: 'page', target: '', id: '' })
    expect(updateRow(row, { group: 'archive' })).toMatchObject({ group: 'archive', sub: '', target: '', id: '' })
    expect(updateRow(row, { target: 'id' })).toMatchObject({ target: 'id', id: '' })
    expect(updateRow(row, { mode: 'exclude' })).toMatchObject({ mode: 'exclude', sub: 'post', target: 'in/category', id: '4' })
    expect(updateRow(row, { id: '8', idLabel: 'News' })).toMatchObject({ id: '8', idLabel: 'News' })
    expect(emptyRow('exclude')).toEqual({ mode: 'exclude', group: 'general', sub: '', target: '', id: '' })
  })

  it('id search goes to the matching core collection, custom types are typed in', () => {
    const src = (rule: string) => idSource(SITE, parseRule(rule)!)
    expect(src('include/singular/post/1')).toBe('posts')
    expect(src('include/singular/page/1')).toBe('pages')
    expect(src('include/singular/page/child_of/1')).toBe('pages')
    expect(src('include/singular/book/1')).toBeNull()
    expect(src('include/singular/post/in/category/1')).toBe('categories')
    expect(src('include/singular/post/in/post_tag/1')).toBe('tags')
    expect(src('include/singular/book/in/genre/1')).toBeNull()
    expect(src('include/singular/post/by_author/1')).toBe('users')
    expect(src('include/archive/author/1')).toBe('users')
    expect(src('include/archive/category/1')).toBe('categories')
    expect(src('include/archive/post_tag/1')).toBe('tags')
    expect(src('include/general')).toBeNull()
  })
})

describe('human labels', () => {
  it.each([
    ['include/general', 'Entire Site'],
    ['include/404', '404 Page'],
    ['include/singular', 'All Singular'],
    ['include/singular/front_page', 'Front Page'],
    ['include/singular/post', 'Posts'],
    ['include/singular/page/12', 'Pages: #12'],
    ['include/singular/post/in/category/4', 'Posts in Categories: #4'],
    ['include/singular/page/child_of/9', 'Pages: child of #9'],
    ['include/singular/book/by_author/3', 'Books by #3'],
    ['include/archive', 'All Archives'],
    ['include/archive/home', 'Posts Page'],
    ['include/archive/search', 'Search Results'],
    ['include/archive/type/book', 'Books Archive'],
    ['include/archive/category', 'Categories'],
    ['include/archive/author/5', 'Author Archives: #5'],
    ['exclude/singular/page/12', 'Excluded: Pages: #12'],
  ])('%s → %s', (rule, label) => {
    expect(ruleLabel(SITE, rule)).toBe(label)
  })

  it('uses a known item title', () => {
    expect(ruleLabel(SITE, 'exclude/singular/page/12', 'About')).toBe('Excluded: Pages: “About”')
  })

  it('shows an invalid rule as is', () => {
    expect(ruleLabel(SITE, 'include/nowhere')).toBe('include/nowhere')
  })
})
