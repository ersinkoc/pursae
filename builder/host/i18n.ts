/**
 * UI strings of the CMS host's own Theme Builder controls (TemplateBar, ConditionsDialog,
 * PreviewPicker): English source strings through t(), translated with the host catalog
 * (./i18n-tr.ts) in the editor's UI language.
 *
 * The language is the builder's (upstream src/builder/i18n.ts, set by ./locale from the boot
 * config's `locale`), so host and builder strings always agree. The host catalog is separate from
 * upstream's because builder/host owns its strings; test/host/i18n-host.test.ts checks that every
 * literal t() call of builder/host has an entry with the same {placeholders}.
 */
import { lang, type Vars } from '@/builder/i18n'
import tr from './i18n-tr'

export const HOST_CATALOGS: Readonly<Record<string, Readonly<Record<string, string>>>> = { tr }

const fill = (msg: string, vars?: Vars) => (vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg)

/** The host UI string `source` (English) in the editor's language, with {placeholders} filled. */
export function t(source: string, vars?: Vars): string {
  const l = lang()
  const msg = l === 'en' ? source : HOST_CATALOGS[l]?.[source] || source
  return fill(msg, vars)
}
