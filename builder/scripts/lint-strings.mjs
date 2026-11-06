#!/usr/bin/env node
/**
 * UI string lint for the Pursae Web Builder (run by `make builder-test`).
 *
 * The editor UI is English with t('English source') (upstream src/builder/i18n.ts, added by
 * patches/0004-i18n.patch); Turkish lives only in the catalog src/builder/i18n/tr.ts. This lint
 * scans the string literals, template texts and JSX texts of the patched upstream sources (the
 * same tree `make builder` builds) and of builder/host, and fails when:
 *
 *  - a string still holds Turkish: a Turkish letter (ç ğ ı ö ş ü İ …), or a word that only
 *    occurs in the Turkish side of the catalog (e.g. "panelinde", "silindi");
 *  - a t('…') call uses a source string the Turkish catalog has no entry for;
 *  - a catalog entry's {placeholders} differ from its source string's.
 *
 * Not scanned: src/builder/ai/** (stubbed out of the build, see vite/noai.ts), the catalogs, and
 * comments. A line can opt out with an `i18n-ignore` comment on it or on the line above (for data
 * that is not UI text, e.g. a glyph subset).
 *
 * Usage: node scripts/lint-strings.mjs [--src <patched upstream dir>]
 *   Without --src a scratch copy of builder/upstream at UPSTREAM_SHA with patches/*.patch applied
 *   is made in a temporary directory (the submodule must be checked out).
 */
import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const BUILDER = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const HOST = path.join(BUILDER, 'host')
const require = createRequire(path.join(BUILDER, 'package.json'))
const ts = require('typescript')

const TURKISH_LETTER = /[çğıöşüÇĞİÖŞÜâîû]/
const WORD = /[A-Za-zçğıöşüÇĞİÖŞÜâîû]+/g
/** Turkish catalog words that are also code or names: CSS var(), the pursae.dev domain, "Bodoni Moda". */
const NOT_TURKISH = new Set(['var', 'dev', 'moda'])

function die(msg) {
  console.error(`[lint-strings] ${msg}`)
  process.exit(1)
}

/* ---------- Sources ---------- */

function git(args, opts = {}) {
  const r = spawnSync('git', args, { encoding: 'utf8', ...opts })
  if (r.status !== 0) die(`git ${args.join(' ')} failed:\n${r.stderr || r.stdout}`)
  return r.stdout
}

function patchedCopy() {
  const submodule = path.join(BUILDER, 'upstream')
  if (!fs.existsSync(path.join(submodule, '.git'))) die('builder/upstream is not checked out. Run: git submodule update --init builder/upstream')
  const sha = fs.readFileSync(path.join(BUILDER, 'UPSTREAM_SHA'), 'utf8').trim()
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'wb-lint-'))
  const work = path.join(dir, 'upstream')
  git(['clone', '--quiet', '--shared', '--no-checkout', submodule, work])
  git(['-C', work, '-c', 'advice.detachedHead=false', 'checkout', '--quiet', '--detach', sha])
  const patches = path.join(BUILDER, 'patches')
  for (const f of fs.readdirSync(patches).filter((x) => x.endsWith('.patch')).sort()) {
    git(['-C', work, 'apply', '--3way', '--whitespace=nowarn', path.join(patches, f)])
  }
  return { root: work, cleanup: () => fs.rmSync(dir, { recursive: true, force: true }) }
}

function listFiles(dir, skip) {
  const out = []
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (skip(p)) continue
    if (e.isDirectory()) out.push(...listFiles(p, skip))
    else if (/\.(ts|tsx)$/.test(e.name) && !e.name.endsWith('.d.ts')) out.push(p)
  }
  return out
}

const parse = (file) =>
  ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)

/* ---------- Catalog ---------- */

function readCatalog(file) {
  if (!fs.existsSync(file)) die(`${file} is missing (patch 0004-i18n adds it).`)
  const sf = parse(file)
  const entries = new Map()
  const visit = (n) => {
    if (ts.isPropertyAssignment(n) && ts.isObjectLiteralExpression(n.parent) && (ts.isStringLiteral(n.name) || ts.isIdentifier(n.name)) && ts.isStringLiteral(n.initializer)) {
      entries.set(n.name.text, n.initializer.text)
    }
    ts.forEachChild(n, visit)
  }
  visit(sf)
  return entries
}

const words = (s) => (s.match(WORD) ?? []).filter((w) => !TURKISH_LETTER.test(w) && w.length >= 3).map((w) => w.toLowerCase())
const placeholders = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',')

/* ---------- Scan ---------- */

function scan(file, display, turkishWords, catalog, problems, used) {
  const sf = parse(file)
  const lines = sf.text.split('\n')
  const ignored = (line) => /i18n-ignore/.test(lines[line] ?? '') || /i18n-ignore/.test(lines[line - 1] ?? '')
  const report = (node, text, why) => {
    const line = sf.getLineAndCharacterOfPosition(node.getStart(sf)).line
    if (ignored(line)) return
    problems.push(`${display}:${line + 1}: ${why}: ${JSON.stringify(text.length > 90 ? text.slice(0, 90) + '…' : text)}`)
  }
  const check = (node, text) => {
    if (!text.trim()) return
    if (TURKISH_LETTER.test(text)) return report(node, text, 'Turkish text')
    const w = words(text).find((x) => turkishWords.has(x))
    if (w) report(node, text, `Turkish word "${w}"`)
  }
  const visit = (n) => {
    if (ts.isImportDeclaration(n) || ts.isExportDeclaration(n)) return
    if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 't' && n.arguments[0]) {
      const a = n.arguments[0]
      if (ts.isStringLiteral(a) || ts.isNoSubstitutionTemplateLiteral(a)) {
        used.add(a.text)
        if (catalog && !catalog.has(a.text)) report(a, a.text, 'no Turkish catalog entry for this t() string')
      } else {
        report(a, a.getText(sf), 't() needs a string literal (the catalog key)')
      }
    }
    if (ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n)) check(n, n.text)
    else if (ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n)) check(n, n.text)
    else if (ts.isJsxText(n)) check(n, n.text)
    ts.forEachChild(n, visit)
  }
  visit(sf)
}

/* ---------- Main ---------- */

const argv = process.argv.slice(2)
const srcArg = argv.indexOf('--src') >= 0 ? argv[argv.indexOf('--src') + 1] : null
const tree = srcArg ? { root: path.resolve(srcArg), cleanup: () => {} } : patchedCopy()

try {
  const src = path.join(tree.root, 'src')
  const catalogFile = path.join(src, 'builder', 'i18n', 'tr.ts')
  const catalog = readCatalog(catalogFile)
  const problems = []

  // Words of the Turkish translations that never occur in an English source string.
  const english = new Set([...catalog.keys()].flatMap(words))
  const turkishWords = new Set([...catalog.values()].flatMap(words).filter((w) => !english.has(w) && !NOT_TURKISH.has(w)))

  for (const [en, tr] of catalog) {
    if (placeholders(en) !== placeholders(tr)) problems.push(`src/builder/i18n/tr.ts: placeholders differ: ${JSON.stringify(en)} → ${JSON.stringify(tr)}`)
  }

  const used = new Set()
  const skipUpstream = (p) => {
    const r = path.relative(src, p).split(path.sep).join('/')
    return r === 'builder/ai' || r === 'builder/i18n' || r === 'builder/i18n.ts'
  }
  const upstreamFiles = listFiles(src, skipUpstream)
  for (const f of upstreamFiles) scan(f, 'upstream/src/' + path.relative(src, f).split(path.sep).join('/'), turkishWords, catalog, problems, used)
  const hostFiles = listFiles(HOST, () => false)
  for (const f of hostFiles) scan(f, 'builder/host/' + path.relative(HOST, f).split(path.sep).join('/'), turkishWords, null, problems, used)

  const unused = [...catalog.keys()].filter((k) => !used.has(k))
  if (unused.length) console.warn(`[lint-strings] note: ${unused.length} catalog entr${unused.length === 1 ? 'y is' : 'ies are'} not used by a literal t() call: ${unused.slice(0, 8).map((k) => JSON.stringify(k)).join(', ')}${unused.length > 8 ? ', …' : ''}`)

  if (problems.length) {
    console.error(`[lint-strings] ${problems.length} problem(s): UI strings must be English t('…') with a Turkish entry in src/builder/i18n/tr.ts`)
    for (const p of problems) console.error('  ' + p)
    process.exitCode = 1
  } else {
    console.log(`[lint-strings] ok: ${upstreamFiles.length + hostFiles.length} files, ${catalog.size} catalog entries, ${used.size} t() strings`)
  }
} finally {
  tree.cleanup()
}
