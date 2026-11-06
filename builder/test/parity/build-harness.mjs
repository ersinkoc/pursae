/**
 * Builds the parity harness (harness/live.tsx) with the editor's own Vite config (same plugins: AI
 * stubs, local fonts, React, Tailwind) into builder/.work/parity/live/, and returns its entry files.
 * Needs the patched upstream copy in builder/.work/upstream that `make builder` prepares, and fails
 * when web/web-builder/BUILD.json does not match builder/patches (the dist and fixtures are stale).
 */
import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export const PARITY = path.dirname(fileURLToPath(import.meta.url))
export const BUILDER = path.resolve(PARITY, '..', '..')
export const REPO = path.resolve(BUILDER, '..')
export const WORK = path.join(BUILDER, '.work')
export const OUT = path.join(WORK, 'parity', 'live')

/** Throws with a hint when the scratch copy or the dist is missing or stale. */
export function checkPrepared() {
  if (!fs.existsSync(path.join(WORK, 'upstream', 'src', 'builder', 'live', 'renderStatic.tsx'))) {
    throw new Error('builder/.work/upstream is missing; run `make builder` first.')
  }
  const info = JSON.parse(fs.readFileSync(path.join(REPO, 'web', 'web-builder', 'BUILD.json'), 'utf8'))
  const dir = path.join(BUILDER, 'patches')
  const now = Object.fromEntries(
    fs.readdirSync(dir).filter((f) => f.endsWith('.patch')).sort()
      .map((f) => [f, crypto.createHash('sha256').update(fs.readFileSync(path.join(dir, f))).digest('hex')]),
  )
  if (JSON.stringify(now) !== JSON.stringify(info.patches)) {
    throw new Error('web/web-builder/BUILD.json does not match builder/patches; run `make builder` first.')
  }
  // The scratch copy must be at least as new as the build that wrote the fixtures.
  const work = fs.statSync(path.join(WORK, 'upstream', 'src', 'builder', 'live', 'renderStatic.tsx')).mtimeMs
  const newest = Math.max(...Object.keys(now).map((f) => fs.statSync(path.join(dir, f)).mtimeMs))
  if (work < newest) throw new Error('builder/.work/upstream is older than builder/patches; run `make builder` first.')
}

/** Vite build of the harness; returns { js, css[] } relative to OUT. */
export async function buildHarness() {
  checkPrepared()
  const { build } = await import('vite')
  await build({
    configFile: path.join(BUILDER, 'vite.config.ts'),
    mode: 'production',
    logLevel: 'warn',
    build: {
      outDir: OUT,
      emptyOutDir: true,
      manifest: true,
      minify: false,
      // A string replaces the editor config's input object (mergeConfig merges objects).
      rolldownOptions: { input: path.join(PARITY, 'harness', 'live.tsx') },
    },
  })
  const m = JSON.parse(fs.readFileSync(path.join(OUT, '.vite', 'manifest.json'), 'utf8'))
  const entries = Object.entries(m).filter(([, c]) => c.isEntry)
  if (entries.length !== 1 || !/harness\/live\.tsx$/.test(entries[0][1].src ?? '')) throw new Error(`parity harness: expected the one live.tsx entry, got ${entries.map(([k]) => k).join(', ')}`)
  // CSS of the entry and every statically imported chunk, in order (as scripts/build.mjs does).
  const css = []
  const seen = new Set()
  const walk = (key) => {
    if (seen.has(key)) return
    seen.add(key)
    for (const imp of m[key].imports ?? []) walk(imp)
    for (const f of m[key].css ?? []) if (!css.includes(f)) css.push(f)
  }
  walk(entries[0][0])
  const entry = { js: entries[0][1].file, css }
  fs.writeFileSync(path.join(OUT, 'entry.json'), JSON.stringify(entry, null, 2))
  return entry
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const e = await buildHarness()
  console.log(`[parity] harness built: ${JSON.stringify(e)}`)
}
