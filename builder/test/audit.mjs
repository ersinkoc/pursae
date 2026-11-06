#!/usr/bin/env node
/**
 * Audits the built Pursae Web Builder in ../web/web-builder/ (run by scripts/build.mjs, and on its
 * own by `make builder-test`). Exits non-zero on any finding.
 *
 *  - No AI provider strings or AI dev endpoints in the editor or the site runtime (AI is out of
 *    scope: the editor ships with every upstream ai/** module stubbed).
 *  - No remote stylesheet @import (Google Fonts) in shipped CSS; no upstream public/ files.
 *  - manifest.json and BUILD.json have the contract shape and point at files that exist.
 *  - Size budgets.
 */
import fs from 'node:fs'
import path from 'node:path'
import zlib from 'node:zlib'
import { fileURLToPath } from 'node:url'

const BUILDER = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const OUT = path.resolve(BUILDER, '..', 'web', 'web-builder')

/** Strings that must never appear in shipped editor or runtime files (case-insensitive). */
export const FORBIDDEN = [
  'api.anthropic.com',
  'api.openai.com',
  'generativelanguage',
  'api.mistral.ai',
  'anthropic-dangerous',
  'ollama',
  'api/ai-images',
  'api/clone-site',
]

const KB = 1024
const BUDGETS = {
  // gzip sizes
  runtimeGzip: 8 * KB,
  editorJsGzip: 400 * KB,
  editorCssGzip: 64 * KB,
  // raw size of everything under editor/ (fonts included)
  editorTotal: 8 * KB * KB,
}

const problems = []
const fail = (msg) => problems.push(msg)

function walk(dir) {
  const out = []
  if (!fs.existsSync(dir)) return out
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) out.push(...walk(p))
    else out.push(p)
  }
  return out
}

const rel = (p) => path.relative(OUT, p).split(path.sep).join('/')
const gz = (p) => zlib.gzipSync(fs.readFileSync(p), { level: 9 }).length
const fmt = (n) => `${(n / KB).toFixed(1)} KB`
const TEXT = /\.(m?js|css|html|json|svg|txt|map)$/i

function readJSON(name) {
  const file = path.join(OUT, name)
  if (!fs.existsSync(file)) {
    fail(`${name} is missing`)
    return null
  }
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'))
  } catch (e) {
    fail(`${name} is not valid JSON: ${e.message}`)
    return null
  }
}

if (!fs.existsSync(OUT)) {
  console.error(`[audit] ${OUT} does not exist; run make builder first.`)
  process.exit(1)
}

/* ---------- shape ---------- */

const manifest = readJSON('manifest.json')
const buildInfo = readJSON('BUILD.json')

const exists = (bundle, f) => typeof f === 'string' && f !== '' && !f.includes('..') && fs.existsSync(path.join(OUT, bundle, f))
if (manifest) {
  const e = manifest.editor
  if (!e || !exists('editor', e.js)) fail(`manifest.json editor.js does not name a file under editor/: ${JSON.stringify(e?.js)}`)
  if (!Array.isArray(e?.css) || e.css.length === 0) fail('manifest.json editor.css must list at least one stylesheet')
  else for (const c of e.css) if (!exists('editor', c)) fail(`manifest.json editor.css names a missing file: ${c}`)
  if (!manifest.site || !exists('site', manifest.site.runtime)) fail(`manifest.json site.runtime does not name a file under site/: ${JSON.stringify(manifest.site?.runtime)}`)
  if (manifest.site && manifest.site.css !== '' && !exists('site', manifest.site.css)) fail(`manifest.json site.css names a missing file: ${manifest.site.css}`)
}
if (buildInfo) {
  if (!/^[0-9a-f]{40}$/.test(buildInfo.upstreamSha ?? '')) fail('BUILD.json upstreamSha is not a commit SHA')
  if (!buildInfo.patches || typeof buildInfo.patches !== 'object' || Array.isArray(buildInfo.patches)) fail('BUILD.json patches must be an object')
  else for (const [k, v] of Object.entries(buildInfo.patches)) if (!/^[0-9a-f]{64}$/.test(v)) fail(`BUILD.json patch ${k} has no sha256`)
  for (const k of ['apiVersion', 'rendererVersion']) if (!Number.isInteger(buildInfo[k]) || buildInfo[k] < 1) fail(`BUILD.json ${k} must be a positive integer`)
}

/* ---------- forbidden strings ---------- */

const shipped = [...walk(path.join(OUT, 'editor')), ...walk(path.join(OUT, 'site'))]
for (const f of shipped) {
  if (!TEXT.test(f)) continue
  const text = fs.readFileSync(f, 'utf8').toLowerCase()
  for (const s of FORBIDDEN) if (text.includes(s)) fail(`${rel(f)} contains forbidden string "${s}"`)
  if (f.endsWith('.css') && /@import\s+(url\()?\s*['"]?https?:/i.test(text)) fail(`${rel(f)} has a remote @import`)
  // Fonts come from the site's own css2 endpoint (host.fontsCss); a hard-coded remote stylesheet
  // (e.g. in an iframe srcdoc) is blocked by the admin CSP and would call Google from the editor.
  if (/<link[^>]*href=\\?["']https?:\/\/fonts\.(googleapis\.com|bunny\.net)/i.test(text)) fail(`${rel(f)} has a hard-coded remote font stylesheet`)
}
for (const f of walk(OUT)) {
  if (/(^|\/)ai-images(\/|$)/.test(rel(f))) fail(`${rel(f)}: upstream public/ai-images must not ship`)
  if (/\.map$/.test(f)) fail(`${rel(f)}: source maps are not shipped`)
}

/* ---------- budgets ---------- */

const sizes = {}
if (manifest && problems.length === 0) {
  sizes.editorJsGzip = gz(path.join(OUT, 'editor', manifest.editor.js))
  sizes.editorCssGzip = manifest.editor.css.reduce((n, c) => n + gz(path.join(OUT, 'editor', c)), 0)
  sizes.runtimeGzip = gz(path.join(OUT, 'site', manifest.site.runtime))
  sizes.editorTotal = walk(path.join(OUT, 'editor')).reduce((n, f) => n + fs.statSync(f).size, 0)
  for (const [k, v] of Object.entries(sizes)) if (v > BUDGETS[k]) fail(`size budget ${k}: ${fmt(v)} > ${fmt(BUDGETS[k])}`)
}

if (problems.length) {
  console.error('[audit] FAILED:')
  for (const p of problems) console.error(`  - ${p}`)
  process.exit(1)
}
console.log(
  `[audit] ok: ${shipped.length} files, no AI provider strings; editor js ${fmt(sizes.editorJsGzip)} gz, css ${fmt(sizes.editorCssGzip)} gz, ` +
    `editor total ${fmt(sizes.editorTotal)}, runtime ${fmt(sizes.runtimeGzip)} gz`,
)
