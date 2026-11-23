/**
 * The frame guard (patch 0021-mixed-frames): what the per-run code frames of a MIXED page (code
 * sections and builder sections, upstream CodeSection.tsx of patches 0018 / 0019) need to be as
 * safe as the element editor's canvas (security model: docs/web-builder-code-sections.md):
 *
 *  - edit mode draws a neutralised copy (neutral.ts) in a frame sandboxed WITHOUT allow-scripts,
 *    swept again once loaded; embedded content is drawn as a placeholder (embeds.ts) and a click
 *    on it selects it (target);
 *  - attributes the toolbar writes into the frame are neutralised there too (neutralEl);
 *  - text edited in place is written back from the source (richtext.writeBack), never from the
 *    neutralised DOM (its on… handlers, scripts and ids stay as stored);
 *  - Preview runs the design's scripts only after the author's confirmation, shared with the element
 *    editor (state.ts `scripts` and its grants; ScriptsGate.tsx), and only while it covers what the
 *    page runs now (trust.ts runsNow: a section of another kit or new code makes the frames static
 *    in the same render and asks again).
 */
import type { FrameGuard } from '@/builder/elementsSeam'
import { elementKids, parse } from './html'
import { embedCss, embedTarget } from './embeds'
import { neutralMarkup, neutralise, sweep } from './neutral'
import { mapSource, writeBack } from './richtext'
import { useElState } from './state'
import { ScriptsBanner } from './ScriptsGate'
import { usePageNeeds } from './pageneeds'
import { runsNow } from './trust'

/** The frames run the design's scripts: the user's choice covers what the page runs now. */
function useScripts(): boolean {
  const st = useElState()
  const needs = usePageNeeds()
  return runsNow(st, needs)
}

export const frameGuard: FrameGuard = {
  copy: (markup) => neutralMarkup(markup),
  sweep,
  neutralEl: (el) => neutralise(el, { sweep: true }),
  css: () => embedCss(),
  target: (doc, el, x, y) => embedTarget(doc, el, x, y) ?? el,
  beginText(srcOuter, el) {
    const t = parse(srcOuter)
    const src = elementKids(t.root)[0]
    if (!src || src.tag !== el.localName.toLowerCase()) return () => null
    // The edited DOM's nodes are paired with the source nodes before the edit changes them.
    const map = mapSource(el, t, src)
    return () => writeBack(el, t, map)
  },
  useScripts,
  Banner: ScriptsBanner,
}
