/**
 * The editor's UI language must be set before any builder module is evaluated: some labels are
 * translated at module load (dynamic widget labels in dynamic/model.ts, section kinds in data.ts).
 * host/editor-entry.tsx imports only ./locale (and the stylesheet) statically and loads the editor with a dynamic
 * import, so the bundler cannot run a chunk with those modules before setLocale().
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'

const here = path.dirname(fileURLToPath(import.meta.url))

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
})

/** Builder modules evaluated after host/locale.ts with the given boot locale, as in the editor. */
async function modelWith(locale: string) {
  vi.resetModules()
  vi.stubGlobal('window', { __WEB_BUILDER__: { locale } })
  await import('../../host/locale')
  return import('@/builder/dynamic/model')
}

describe('editor entry', () => {
  it('imports only ./locale statically and the editor dynamically', () => {
    const src = fs.readFileSync(path.join(here, '..', '..', 'host', 'editor-entry.tsx'), 'utf8')
    const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
    const statics = [...code.matchAll(/^\s*import\s+(?:[^'"]+\s+from\s+)?['"]([^'"]+)['"]/gm)].map((m) => m[1])
    expect(statics.filter((s) => !s.endsWith('.css'))).toEqual(['./locale'])
    expect(code).toMatch(/import\(\s*['"]\.\/editor-main['"]\s*\)/)
    expect(code).toMatch(/import\(\s*['"]\.\/library-main['"]\s*\)/)
  })

  it('module-load labels follow the boot locale', async () => {
    const en = await modelWith('en_US')
    expect(en.dynLabel('post-image')).toBe('Featured image')
    expect(en.DYN_GROUP_LABELS.post).toBe('Post')

    const tr = (await import('@/builder/i18n/tr')).default
    const trModel = await modelWith('tr_TR')
    expect(trModel.dynLabel('post-image')).toBe(tr['Featured image'])
    expect(trModel.dynLabel('post-image')).not.toBe('Featured image')
  })
})
