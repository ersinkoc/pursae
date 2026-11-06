/**
 * Published-page outputs of the Pursae Web Builder build (patch 0002, upstream src/builder/live/):
 *
 *   buildSiteCss()    web/web-builder/site/wb.<hash>.css — the stylesheet of every builder page, built once per
 *                     release by scanning only the files that draw sections (live/site.css), plus the
 *                     width-range classes of designs that switch on a JS width check (useNarrow breakpoints).
 *   exportFixtures()  renders a fixed set of docs with renderStatic under Node and writes them to
 *                     internal/webbuilder/testdata/render (<name>.doc.json + <name>.render.json), which the Go
 *                     sanitizer tests use; fails on any render exception or contract violation.
 *
 * Called by scripts/build.mjs after the runtime build; needs the patched scratch copy in .work/upstream.
 */
import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { compile, optimize } from '@tailwindcss/node'
import { Scanner } from '@tailwindcss/oxide'

const log = (msg) => console.log(`[web-builder] ${msg}`)

/** useNarrow breakpoints the designs use: `breakpoint: N` options, useNarrow(ref, N) calls and the default. */
function narrowBreakpoints(src) {
  const bps = new Set()
  const files = [path.join(src, 'builder', 'sections.tsx'), ...fs.readdirSync(path.join(src, 'builder', 'variants')).filter((f) => f.endsWith('.tsx')).map((f) => path.join(src, 'builder', 'variants', f))]
  for (const f of files) {
    const code = fs.readFileSync(f, 'utf8')
    for (const m of code.matchAll(/\bbreakpoint:\s*(\d+)/g)) bps.add(Number(m[1]))
    for (const m of code.matchAll(/\buseNarrow\([^,()]+,\s*(\d+)\s*\)/g)) bps.add(Number(m[1]))
    for (const m of code.matchAll(/export function useNarrow\([^)]*px = (\d+)\)/g)) bps.add(Number(m[1]))
  }
  // renderStatic treats ≥ 4000 px as "never" and emits no class for it.
  return [...bps].filter((b) => b > 0 && b < 4000).sort((a, b) => a - b)
}

/** Builds site/wb.<hash>.css into `siteDir`; returns the file name. */
export async function buildSiteCss({ work, siteDir }) {
  const src = path.join(work, 'src')
  const entry = path.join(src, 'builder', 'live', 'site.css')
  if (!fs.existsSync(entry)) throw new Error(`${entry} is missing (patch 0002 adds it)`)
  const bps = narrowBreakpoints(src)
  const inline = bps.flatMap((b) => [`@max-[${b}px]:hidden!`, `@min-[${b}px]:hidden!`]).join(' ')
  const css = fs.readFileSync(entry, 'utf8') + `\n/* useNarrow breakpoints: ${bps.join(', ')} */\n@source inline('${inline}');\n`
  const compiler = await compile(css, { base: path.dirname(entry), onDependency() {} })
  if (compiler.root !== 'none') throw new Error('live/site.css must import tailwindcss with source(none)')
  const scanner = new Scanner({ sources: compiler.sources })
  const candidates = scanner.scan()
  const out = optimize(compiler.build(candidates), { minify: true }).code
  if (/@import\s+(url\()?\s*['"]?https?:/i.test(out) || /fonts\.(googleapis|gstatic)\.com/.test(out)) throw new Error('site css references a remote stylesheet or Google Fonts')
  const name = `wb.${crypto.createHash('sha256').update(out).digest('hex').slice(0, 8)}.css`
  fs.mkdirSync(siteDir, { recursive: true })
  for (const f of fs.readdirSync(siteDir)) if (/^wb\.[\w-]+\.css$/.test(f)) fs.rmSync(path.join(siteDir, f))
  fs.writeFileSync(path.join(siteDir, name), out)
  log(`site css: site/${name} (${(out.length / 1024).toFixed(1)} KB, ${candidates.length} candidates, breakpoints ${bps.join('/')})`)
  return name
}

/** Renders the fixture docs under Node and writes them to `outDir`. */
export async function exportFixtures({ builder, work, outDir, siteCssFile }) {
  const { build } = await import('vite')
  const react = (await import('@vitejs/plugin-react')).default
  const ssrDir = path.join(builder, '.work', 'ssr')
  await build({
    configFile: false,
    root: work,
    logLevel: 'warn',
    clearScreen: false,
    publicDir: false,
    plugins: [react()],
    resolve: { alias: [{ find: /^@\//, replacement: path.join(work, 'src') + '/' }] },
    build: {
      ssr: path.join(work, 'src', 'builder', 'live', 'fixtures.tsx'),
      outDir: ssrDir,
      emptyOutDir: true,
      minify: false,
      sourcemap: false,
      reportCompressedSize: false,
      rolldownOptions: { output: { entryFileNames: 'fixtures.mjs' } },
    },
  })
  const r = spawnSync(process.execPath, [path.join(ssrDir, 'fixtures.mjs'), outDir, siteCssFile], { stdio: 'inherit', cwd: builder })
  if (r.status !== 0) throw new Error('renderStatic fixtures failed (see above)')
}
