#!/usr/bin/env node
/**
 * Builds the Pursae Web Builder into ../web/web-builder/ (committed dist, so `go build` and the
 * Docker image need no Node):
 *
 *   editor/            the editor (React), every upstream src/builder/ai/** module stubbed out
 *   site/runtime.*.js  the public runtime (IIFE, no dependencies; upstream src/builder/live/runtime.ts)
 *   site/wb.*.css      the published-page stylesheet (scripts/live.mjs)
 *   manifest.json      entry files per bundle (contract: Manifest)
 *   BUILD.json         upstream SHA, patch hashes, API and renderer versions (contract: BuildInfo)
 *
 * Steps: check the submodule is at UPSTREAM_SHA → clone it into .work/upstream → apply
 * patches/*.patch in name order with `git apply --3way` (zero patches is fine) → clean the output
 * → vite build (editor, runtime), site css → manifest + BUILD.json → renderStatic fixtures
 * (internal/webbuilder/testdata/render, scripts/live.mjs) → test/audit.mjs.
 *
 * Usage: node scripts/build.mjs [--watch | --prepare]   (--watch rebuilds the editor on source
 * changes; edits to upstream/ or patches/ need a restart because the sources are a patched copy.
 * --prepare stops after the patched copy in .work/upstream, which the host unit tests import, and
 * leaves the dist alone: `make builder-test` runs it.)
 */
import { spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildSiteCss, exportFixtures } from './live.mjs'

const BUILDER = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.resolve(BUILDER, '..')
const SUBMODULE = path.join(BUILDER, 'upstream')
const WORK = path.join(BUILDER, '.work', 'upstream')
const PATCHES = path.join(BUILDER, 'patches')
const OUT = path.join(REPO, 'web', 'web-builder')
const CONTRACT = path.join(BUILDER, 'host', 'contract.ts')
const RUNTIME_ENTRY = path.join(WORK, 'src', 'builder', 'live', 'runtime.ts')

const watch = process.argv.includes('--watch')
const prepareOnly = process.argv.includes('--prepare')

function die(msg) {
  console.error(`\n[web-builder] build failed: ${msg}\n`)
  process.exit(1)
}

function git(args, opts = {}) {
  const r = spawnSync('git', args, { encoding: 'utf8', ...opts })
  if (r.status !== 0 && !opts.allowFail) die(`git ${args.join(' ')}\n${r.stderr || r.stdout}`)
  return r
}

const log = (msg) => console.log(`[web-builder] ${msg}`)

/* ---------- 1. Upstream at the pinned SHA ---------- */

const nodeMajor = Number(process.versions.node.split('.')[0])
if (nodeMajor < 22) die(`Node ${process.versions.node} is too old; use Node 22 or newer (builder/.nvmrc).`)

const sha = fs.readFileSync(path.join(BUILDER, 'UPSTREAM_SHA'), 'utf8').trim()
if (!/^[0-9a-f]{40}$/.test(sha)) die(`builder/UPSTREAM_SHA must hold one 40-character commit SHA, got "${sha}".`)

if (!fs.existsSync(path.join(SUBMODULE, '.git'))) die('builder/upstream is not checked out. Run: git submodule update --init builder/upstream')
const head = git(['-C', SUBMODULE, 'rev-parse', 'HEAD']).stdout.trim()
if (head !== sha) die(`builder/upstream is at ${head}, builder/UPSTREAM_SHA says ${sha}. Run \`git submodule update builder/upstream\`, or \`make builder-bump SHA=…\` to move the pin.`)
// The gitlink the parent repo records (index) must match too, so a commit ships what it builds.
const gitlink = git(['-C', REPO, 'ls-files', '-s', '--', 'builder/upstream'], { allowFail: true }).stdout.trim().split(/\s+/)[1]
if (gitlink && gitlink !== sha) die(`the builder/upstream gitlink is ${gitlink}, builder/UPSTREAM_SHA says ${sha}; stage both together (make builder-bump).`)

/* ---------- 2. Patched scratch copy ---------- */

fs.rmSync(path.join(BUILDER, '.work'), { recursive: true, force: true })
fs.mkdirSync(path.dirname(WORK), { recursive: true })
git(['clone', '--quiet', '--shared', '--no-checkout', SUBMODULE, WORK])
git(['-C', WORK, '-c', 'advice.detachedHead=false', 'checkout', '--quiet', '--detach', sha])

const patchFiles = fs.existsSync(PATCHES)
  ? fs.readdirSync(PATCHES).filter((f) => f.endsWith('.patch')).sort()
  : []
const patches = {}
for (const f of patchFiles) {
  const file = path.join(PATCHES, f)
  const r = git(['-C', WORK, 'apply', '--3way', '--whitespace=nowarn', file], { allowFail: true })
  if (r.status !== 0) die(`patch ${f} does not apply to upstream ${sha.slice(0, 7)}:\n${r.stderr || r.stdout}`)
  const conflicts = git(['-C', WORK, 'diff', '--name-only', '--diff-filter=U']).stdout.trim()
  if (conflicts) die(`patch ${f} left conflicts in: ${conflicts.split('\n').join(', ')}`)
  patches[f] = crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')
}
log(`upstream ${sha.slice(0, 7)}, ${patchFiles.length} patch(es) applied${patchFiles.length ? ': ' + patchFiles.join(', ') : ''}`)
if (prepareOnly) process.exit(0)

/* ---------- 3. Versions from the contract ---------- */

const contract = fs.readFileSync(CONTRACT, 'utf8')
const constant = (name) => {
  const m = new RegExp(`export const ${name} = (\\d+)`).exec(contract)
  if (!m) die(`builder/host/contract.ts has no \`export const ${name} = <number>\`.`)
  return Number(m[1])
}
const apiVersion = constant('API_VERSION')
const rendererVersion = constant('RENDERER_VERSION')

/* ---------- 4. Clean output ---------- */

fs.rmSync(OUT, { recursive: true, force: true })
fs.mkdirSync(OUT, { recursive: true })

/* ---------- 5. Vite builds ---------- */

const { build } = await import('vite')
const configFile = path.join(BUILDER, 'vite.config.ts')

function readEditorManifest() {
  const file = path.join(OUT, 'editor', '.vite', 'manifest.json')
  if (!fs.existsSync(file)) die('vite wrote no editor manifest.')
  const m = JSON.parse(fs.readFileSync(file, 'utf8'))
  const entries = Object.values(m).filter((c) => c.isEntry)
  if (entries.length !== 1) die(`expected one editor entry chunk, found ${entries.length}.`)
  const e = entries[0]
  // CSS of the entry and of every statically imported chunk, in order, without duplicates.
  const css = []
  const seen = new Set()
  const walk = (key) => {
    if (seen.has(key)) return
    seen.add(key)
    const c = m[key]
    for (const imp of c.imports ?? []) walk(imp)
    for (const f of c.css ?? []) if (!css.includes(f)) css.push(f)
  }
  walk(Object.keys(m).find((k) => m[k] === e))
  fs.rmSync(path.join(OUT, 'editor', '.vite'), { recursive: true, force: true })
  return { js: e.file, css }
}

let runtimeFile = ''
let siteCssFile = ''
function writeManifest(editor) {
  const manifest = { editor, site: { runtime: runtimeFile, css: siteCssFile } }
  fs.writeFileSync(path.join(OUT, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
}

async function buildRuntime() {
  const dir = path.join(OUT, 'site')
  if (!fs.existsSync(RUNTIME_ENTRY)) {
    // Until the live-render patch adds the real runtime: an inert file, named by content hash.
    const code = '/* Pursae Web Builder site runtime: placeholder until the live-render patch adds src/builder/live/runtime.ts. */\n'
    runtimeFile = `runtime.${crypto.createHash('sha256').update(code).digest('hex').slice(0, 8)}.js`
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, runtimeFile), code)
    log(`site runtime: site/${runtimeFile} (placeholder)`)
    return
  }
  await build({
    configFile: false,
    root: WORK,
    base: './',
    publicDir: false,
    logLevel: 'warn',
    clearScreen: false,
    build: {
      outDir: dir,
      emptyOutDir: true,
      copyPublicDir: false,
      reportCompressedSize: false,
      sourcemap: false,
      minify: true,
      rolldownOptions: {
        input: RUNTIME_ENTRY,
        output: { format: 'iife', entryFileNames: 'runtime.[hash].js' },
      },
    },
  })
  const files = fs.readdirSync(dir).filter((f) => /^runtime\.[\w-]+\.js$/.test(f))
  if (files.length !== 1) die(`expected one site runtime file, found ${files.length}.`)
  runtimeFile = files[0]
  log(`site runtime: site/${runtimeFile}`)
}

function writeBuildInfo() {
  const info = { upstreamSha: sha, patches, apiVersion, rendererVersion }
  fs.writeFileSync(path.join(OUT, 'BUILD.json'), JSON.stringify(info, null, 2) + '\n')
}

function audit() {
  const r = spawnSync(process.execPath, [path.join(BUILDER, 'test', 'audit.mjs')], { stdio: 'inherit' })
  if (r.status !== 0) die('audit failed (see above).')
}

await buildRuntime()
siteCssFile = await buildSiteCss({ work: WORK, siteDir: path.join(OUT, 'site') })
writeBuildInfo()

if (!watch) {
  await build({ configFile, mode: 'production' })
  const editor = readEditorManifest()
  writeManifest(editor)
  log(`editor: editor/${editor.js} + ${editor.css.length} stylesheet(s)`)
  await exportFixtures({ builder: BUILDER, work: WORK, outDir: path.join(REPO, 'internal', 'webbuilder', 'testdata', 'render'), siteCssFile: path.join(OUT, 'site', siteCssFile) })
  audit()
  log(`done → ${path.relative(REPO, OUT)}/`)
} else {
  const watcher = await build({ configFile, mode: 'development', build: { watch: {}, minify: false } })
  watcher.on('event', (e) => {
    if (e.code === 'BUNDLE_END') {
      writeManifest(readEditorManifest())
      log('editor rebuilt')
    } else if (e.code === 'ERROR') {
      console.error(e.error)
    }
    if (e.result?.close) e.result.close()
  })
  log('watching builder/host and the patched upstream copy (restart after changing upstream/ or patches/)')
}
