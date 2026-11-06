/** Runs the theme-part generator (test/gen/themegen.gen.ts): `npx vitest run --config vitest.gen.config.ts`. */
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
  test: { include: ['test/gen/**/*.gen.ts'], environment: 'node' },
})
