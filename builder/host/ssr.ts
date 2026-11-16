/**
 * Server bundle (web/web-builder/render/render.js) for the Go server, which runs it in an embedded JS
 * engine (internal/webbuilder/ssr.go): publishing without the editor (Pursae MCP, REST
 * documents/{id}/publish with serverRender) and building sections from short specs.
 *
 * Globals (JSON strings in and out):
 *   __wbRender(doc, opts)     upstream renderStatic (patch 0002). opts = { postId, siteUrl, formActionPrefix, sample? };
 *                             a form action is formActionPrefix + encodeURIComponent(sectionId). Recolored remote SVGs
 *                             (svgTint) keep their source colors, as without a DOM.
 *   __wbCatalog()             what can be built: section kinds with their fields, item lists, options and designs,
 *                             palettes, font pairs, corners, icons, decors and layout structures.
 *   __wbCreate(spec)          a complete new section from { kind, design?, name?, content?, items?, style?, bg?, grid?,
 *                             box?, els?, hidden? }: the editor's own defaults (createSection / design presets), then the
 *                             spec on top. Missing ids are generated.
 *   __wbDesign(section, id)   the section in another design of its kind (applyVariant).
 */
import type { DocState, Item, Section, SectionKind } from '@/builder/types'
import { renderStatic } from '@/builder/live/renderStatic'
import { CELL_TYPES, CORNERS, DECORS, FEATURE_ICONS, FLEX_STRUCTURES, FONT_PAIRS, GRID_STRUCTURES, KINDS, PALETTES, createSection, initialDoc, uid } from '@/builder/data'
import { ICON_KEYS } from '@/builder/icons'
import { DEFAULT_VARIANT, LIBRARY, applyVariant, findVariant } from '@/builder/variants'

interface ServerOptions {
  postId: number
  siteUrl: string
  formActionPrefix: string
  sample?: unknown
}

type Obj = Record<string, unknown>
const g = globalThis as unknown as Obj

g.__wbRender = (docJSON: string, optsJSON: string): string => {
  const doc = JSON.parse(docJSON) as DocState
  const o = JSON.parse(optsJSON) as ServerOptions
  const opts: Parameters<typeof renderStatic>[1] = {
    postId: o.postId,
    siteUrl: o.siteUrl,
    formAction: (id: string) => o.formActionPrefix + encodeURIComponent(id),
  }
  if (o.sample !== undefined) (opts as unknown as Obj).sample = o.sample
  return JSON.stringify(renderStatic(doc, opts))
}

g.__wbCatalog = (): string => {
  const kinds = (Object.keys(KINDS) as SectionKind[]).map((kind) => {
    const k = KINDS[kind]
    const lib = LIBRARY.find((x) => x.kind === kind)
    const def = createSection(kind)
    return {
      kind,
      name: k.name,
      blurb: k.blurb,
      fields: k.fields.map((f) => ({ key: f.key, label: f.label, el: f.el, ...(f.multiline ? { multiline: true } : {}), ...(f.hint ? { hint: f.hint } : {}) })),
      items: k.items ? { label: k.items.label, title: k.items.titleLabel, text: k.items.textLabel ?? '', titleEl: k.items.titleEl, textEl: k.items.textEl ?? '', max: k.items.max } : null,
      options: (k.options ?? []).map((o) => ({ key: o.key, label: o.label, choices: o.choices.map((c) => c.value) })),
      designs: (lib?.variants ?? []).map((v) => ({ id: v.id, name: v.name, tags: v.tags, blurb: v.blurb, ...(v.extras?.length ? { extras: v.extras } : {}), ...(v.hide?.length ? { hide: v.hide } : {}) })),
      extraFields: (lib?.fields ?? []).map((f) => ({ key: f.key, label: f.label, el: f.el, default: f.def })),
      defaults: { content: def.content, items: def.items.map(({ id: _id, ...rest }) => rest), style: def.style },
    }
  })
  return JSON.stringify({
    kinds,
    palettes: PALETTES.map((p) => ({ id: p.id, name: p.name })),
    fontPairs: Object.entries(FONT_PAIRS).map(([id, f]) => ({ id, name: f.name, display: f.display })),
    corners: Object.entries(CORNERS).map(([id, c]) => ({ id, name: c.name })),
    featureIcons: Object.keys(FEATURE_ICONS),
    icons: ICON_KEYS,
    decors: DECORS.map((d) => d.id),
    layout: { flex: FLEX_STRUCTURES.map((s) => s.id), grid: GRID_STRUCTURES.map((s) => s.id), cellTypes: CELL_TYPES.map((c) => c.value).filter(Boolean) },
    site: initialDoc.site,
  })
}

/** Ids for items (and nested container sections) the spec left without one. */
function fillIds(items: Item[], prefix: string): Item[] {
  return items.map((it) => {
    const out: Item = { ...it, title: it.title ?? '', text: it.text ?? '', id: it.id || uid(prefix) }
    if (out.sub) out.sub = fillSection(out.sub)
    return out
  })
}

function fillSection(s: Section): Section {
  const base = createSection(s.kind)
  const out: Section = { ...base, ...s, id: s.id || base.id, content: { ...(s.content ?? {}) }, style: { ...base.style, ...(s.style ?? {}), hideOn: s.style?.hideOn ?? [] } }
  out.items = s.items ? fillIds(s.items, s.kind === 'custom' ? 'n' : s.kind === 'layout' ? 'c' : 'i') : base.items
  return out
}

g.__wbCreate = (specJSON: string): string => {
  const spec = JSON.parse(specJSON) as Obj
  const kind = spec.kind as SectionKind
  if (!KINDS[kind]) throw new Error(`unknown section kind "${String(kind)}"; kinds: ${Object.keys(KINDS).join(', ')}`)
  let s = createSection(kind, (spec.content as Record<string, string>) ?? undefined)
  const design = typeof spec.design === 'string' && spec.design ? spec.design : ''
  if (design && design !== DEFAULT_VARIANT) {
    if (!findVariant(kind, design)) {
      const ids = LIBRARY.find((x) => x.kind === kind)?.variants.map((v) => v.id) ?? []
      throw new Error(`unknown design "${design}" for ${kind}; designs: ${ids.join(', ') || 'none'}`)
    }
    s = applyVariant(s, design)
    // applyVariant fills a design's extra fields with sample text; the spec's own values win.
    s.content = { ...s.content, ...((spec.content as Record<string, string>) ?? {}) }
  }
  if (typeof spec.name === 'string' && spec.name) s.name = spec.name
  if (spec.hidden === true) s.hidden = true
  if (Array.isArray(spec.items)) s.items = fillIds(spec.items as Item[], kind === 'custom' ? 'n' : kind === 'layout' ? 'c' : 'i')
  if (spec.style && typeof spec.style === 'object') s.style = { ...s.style, ...(spec.style as Obj), hideOn: ((spec.style as Obj).hideOn as Section['style']['hideOn']) ?? s.style.hideOn }
  for (const key of ['bg', 'grid', 'box'] as const) {
    const v = spec[key]
    if (v && typeof v === 'object') (s as unknown as Obj)[key] = { ...((s as unknown as Obj)[key] as Obj), ...(v as Obj) }
  }
  if (spec.els && typeof spec.els === 'object') s.els = { ...(s.els ?? {}), ...(spec.els as Section['els']) }
  return JSON.stringify(s)
}

/** __wbDesign(section, design): the section switched to another design of its kind (content and items kept). */
g.__wbDesign = (sectionJSON: string, design: string): string => {
  const s = JSON.parse(sectionJSON) as Section
  if (!findVariant(s.kind, design)) {
    const ids = LIBRARY.find((x) => x.kind === s.kind)?.variants.map((v) => v.id) ?? []
    throw new Error(`unknown design "${design}" for ${s.kind}; designs: ${ids.join(', ') || 'none'}`)
  }
  return JSON.stringify(applyVariant(s, design))
}
