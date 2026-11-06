/**
 * Unit tests of the CMS host (builder/host) — `npm test` / `make builder-test`.
 * `@/` is the patched upstream copy in .work/upstream when `make builder` prepared it, else the
 * pinned submodule (the host tests import upstream types only, and inject the live renderer).
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

const here = path.dirname(fileURLToPath(import.meta.url))
const work = path.join(here, '.work', 'upstream', 'src')
const src = fs.existsSync(work) ? work : path.join(here, 'upstream', 'src')

export default defineConfig({
  root: here,
  resolve: { alias: [{ find: /^@\//, replacement: src + '/' }] },
  test: {
    include: ['test/host/**/*.test.{ts,tsx}'],
    environment: 'node',
  },
})
