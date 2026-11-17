/**
 * Embedded content in the static edit frame (a YouTube or Vimeo player, a map, any iframe, object
 * or embed of a code section): it does not play in the editor. The neutralised copy (neutral.ts)
 * keeps the element itself — its tag, classes, attributes and styles, so its box and the layout
 * around it are the published page's — but loads nothing into it (no src, sandbox="") and marks it
 * data-wb-embed-ph. The frame's editor stylesheet (embedCss) paints the element as a neutral
 * placeholder: an icon and "Embedded content plays on the published page…", as a background image
 * (an SVG with the text in a foreignObject: no node is added to the design's DOM, nothing runs).
 * The stored markup and the published page are unchanged.
 *
 * The placeholder takes no pointer events (a click inside a nested frame would go to the frame's
 * own document, which the editor cannot reach); embedTarget gives the editor the placeholder under
 * the pointer instead of the element behind it, so a click selects the embed and its source can
 * be changed in the Content panel.
 */
import { t } from '../i18n'
import { EMBED_ATTR } from './neutral'

export { EMBED_ATTR }

const escXml = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/** The placeholder's text (English source, translated in the host catalog). */
export const embedText = () => t('Embedded content plays on the published page. Publish and open the page to watch it.')

/** The placeholder image: an icon and the text, centred in the element's box. */
export function embedImage(text = embedText()): string {
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg"><foreignObject width="100%" height="100%">' +
    '<div xmlns="http://www.w3.org/1999/xhtml" style="box-sizing:border-box;width:100%;height:100%;overflow:hidden;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:8px;padding:12px;text-align:center;font:500 13px/1.45 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#50575e">' +
    '<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="#50575e" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" style="flex:none"><rect x="2" y="4" width="20" height="14" rx="2"/><path d="M10 8.5l4.5 2.5L10 13.5z"/><path d="M8 21h8"/></svg>' +
    `<div style="max-width:420px">${escXml(text)}</div></div></foreignObject></svg>`
  return 'data:image/svg+xml,' + encodeURIComponent(svg)
}

/**
 * The frame's placeholder rules. Strong selectors (`:not(#wb#wb)`) win over the design's own
 * background and pointer-events rules; object / embed (which draw nothing once they load nothing)
 * become inline blocks with their width / height, an object's fallback content is not shown.
 */
export function embedCss(text?: string): string {
  const a = `[${EMBED_ATTR}]`
  return (
    `:where(object,embed,applet)${a}{display:inline-block}object${a}>*{display:none!important}` +
    `${a}:not(#wb#wb){background:#eef0f3 url("${embedImage(text)}") center/100% 100% no-repeat!important;pointer-events:none!important;cursor:default}`
  )
}

/**
 * The element an event at (x, y) (the frame's client coordinates) is about: the innermost embed
 * placeholder under the point inside `target` (the element the browser hit, which is behind the
 * placeholder: it takes no pointer events), else `target` itself.
 */
export function embedTarget<T extends Element>(doc: Document, target: T | null, x: number, y: number): T | Element | null {
  if (!target || !doc) return target
  let hit: Element | null = null
  for (const e of Array.from(doc.querySelectorAll(`[${EMBED_ATTR}]`))) {
    if (!target.contains(e)) continue
    const r = e.getBoundingClientRect()
    if (r.width > 0 && r.height > 0 && x >= r.left && x < r.right && y >= r.top && y < r.bottom) hit = e
  }
  return hit ?? target
}
