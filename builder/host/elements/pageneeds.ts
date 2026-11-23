/**
 * What the open page runs (trust.ts needsOf), from the builder's live document: its visible code
 * sections and the locked header and footer parts (Boot.codeParts). The canvas and the mixed
 * pages' frames read it in render, so a change of the code sections (an insert, a paste, Edit
 * code, undo) is judged before a frame draws it.
 */
import { useMemo } from 'react'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import type { CmsHost } from '../cms-host'
import { needsOf, type Needs } from './trust'

const partsOf = () => {
  const p = typeof window !== 'undefined' ? (window.__WEB_BUILDER_HOST__ as CmsHost | undefined)?.boot.codeParts : null
  return { header: p?.header?.sections ?? [], footer: p?.footer?.sections ?? [] }
}

/** The needs of a document's visible code sections and the parts. */
export function docNeeds(sections: readonly Section[], parts: { header: readonly unknown[]; footer: readonly unknown[] } = partsOf()): Needs {
  return needsOf(parts.header as never, sections.filter((s) => s.kind === 'code' && !s.hidden) as never, parts.footer as never)
}

/** docNeeds of the builder's current document. */
export function usePageNeeds(): Needs {
  const b = useB()
  return useMemo(() => docNeeds(b.doc.sections), [b.doc.sections])
}
