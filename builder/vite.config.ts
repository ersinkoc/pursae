/**
 * Pursae Web Builder — editor build (our own config; the upstream vite.config.ts, its Figma
 * plugins, .figma/make/site.json and the ai-images / clone-site dev middlewares are never loaded).
 *
 * Sources: builder/.work/upstream (a scratch clone of the upstream submodule at the pinned SHA
 * with builder/patches/*.patch applied, prepared by scripts/build.mjs) and builder/host.
 * Output: ../web/web-builder/editor/ (assets + .vite/manifest.json, folded into
 * web/web-builder/manifest.json by scripts/build.mjs).
 *
 * Run it through scripts/build.mjs (`make builder`), which prepares the sources first.
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig, type Plugin } from 'vite'
import { noAi } from './vite/noai.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
export const WORK = path.join(here, '.work', 'upstream')
export const UPSTREAM_SRC = path.join(WORK, 'src')
export const HOST = path.join(here, 'host')
export const OUT = path.resolve(here, '..', 'web', 'web-builder')

/** The editor entry: the CMS host entry once it exists (F2), else upstream's own main.tsx. */
function editorEntry(): string {
  const host = path.join(HOST, 'editor-entry.tsx')
  return fs.existsSync(host) ? host : path.join(UPSTREAM_SRC, 'main.tsx')
}

/**
 * Upstream src/index.css starts with a Google Fonts @import (Inter, Instrument Serif,
 * JetBrains Mono, Space Grotesk). Swap it for the @font-face rules of local @fontsource files
 * (woff2 only; every supported browser has it) so the editor never calls Google; fail the build
 * when the import moved, so a font change upstream is noticed.
 * Also register the upstream and host sources explicitly for Tailwind's class scan (the scratch
 * clone lives in a git-ignored directory).
 */
function localFonts(): Plugin {
  const indexCss = path.join(UPSTREAM_SRC, 'index.css').split(path.sep).join('/')
  const googleImport = /^@import\s+url\(\s*['"]?https:\/\/fonts\.googleapis\.com\/css2\?[^)]*\)\s*;[ \t]*\r?\n/m
  const fontsource = [
    '@fontsource/inter/400.css',
    '@fontsource/inter/500.css',
    '@fontsource/inter/600.css',
    '@fontsource/inter/700.css',
    '@fontsource/instrument-serif/400.css',
    '@fontsource/instrument-serif/400-italic.css',
    '@fontsource/jetbrains-mono/400.css',
    '@fontsource/jetbrains-mono/500.css',
    '@fontsource/space-grotesk/500.css',
    '@fontsource/space-grotesk/600.css',
    '@fontsource/space-grotesk/700.css',
  ]
  const tailwindImport = /^@import\s+['"]tailwindcss['"]\s*;[ \t]*$/m
  return {
    name: 'pursae-local-fonts',
    enforce: 'pre',
    transform(code, id) {
      if (id.split('?')[0] !== indexCss) return null
      if (!googleImport.test(code)) {
        throw new Error('[fonts] upstream src/index.css no longer starts with the Google Fonts @import; update localFonts() in builder/vite.config.ts.')
      }
      let out = code.replace(googleImport, '')
      if (/fonts\.(googleapis|gstatic)\.com/.test(out)) throw new Error('[fonts] upstream src/index.css still references Google Fonts after the swap.')
      if (!tailwindImport.test(out)) throw new Error("[fonts] upstream src/index.css has no `@import 'tailwindcss';` line.")
      const cssDir = path.dirname(path.join(UPSTREAM_SRC, 'index.css'))
      const relTo = (p: string) => path.relative(cssDir, p).split(path.sep).join('/')
      // @font-face rules must follow every @import, so they go after the tailwindcss import.
      const faces = fontsource.map((f) => {
        const file = path.join(here, 'node_modules', f)
        if (!fs.existsSync(file)) throw new Error(`[fonts] ${f} is missing; run npm ci in builder/.`)
        const css = fs.readFileSync(file, 'utf8')
          .replace(/,\s*url\([^)]*\.woff\)\s*format\(['"]woff['"]\)/g, '')
          .replace(/url\(\.\/files\/([^)]+)\)/g, (_m, name: string) => `url('${relTo(path.join(path.dirname(file), 'files', name))}')`)
        if (/\.woff\)/.test(css) || !/\.woff2'\)/.test(css)) throw new Error(`[fonts] unexpected @font-face format in ${f}`)
        return css
      })
      out = out.replace(tailwindImport, (m) => `${m}\n@source "${relTo(UPSTREAM_SRC)}";\n@source "${relTo(HOST)}";\n${faces.join('\n')}`)
      return { code: out, map: null }
    },
  }
}

export default defineConfig({
  root: WORK,
  base: './',
  publicDir: false,
  clearScreen: false,
  logLevel: 'warn',
  plugins: [noAi({ upstreamSrc: UPSTREAM_SRC, stubDir: path.join(HOST, 'ai-stub') }), localFonts(), react(), tailwindcss()],
  resolve: {
    alias: [{ find: /^@\//, replacement: UPSTREAM_SRC + '/' }],
  },
  define: {
    __WB_AI__: 'false',
  },
  build: {
    outDir: path.join(OUT, 'editor'),
    emptyOutDir: true,
    manifest: true,
    sourcemap: false,
    copyPublicDir: false,
    reportCompressedSize: false,
    chunkSizeWarningLimit: 2048,
    rolldownOptions: {
      input: { index: editorEntry() },
    },
  },
})
