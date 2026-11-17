/**
 * In-page side of the Playwright regression (editor-xss.mjs): builds the element editor's frame
 * from every payload with the editor's own modules (frame.ts, neutral.ts), the way the canvas
 * does, edits it through the reconciler (FrameSync), and reports which payloads reached the
 * editor's REST nonce (window.hit).
 */
import type { Section } from '@/builder/types'
import { FrameSync, frameDoc } from '../../host/elements/frame'
import { sweep } from '../../host/elements/neutral'
import { HEAD_PAYLOADS, PAYLOADS, hitJs } from './payloads'

declare global {
  interface Window {
    hits: string[]
    hit: (n: string) => void
    WB: { run: typeof run; names: string[] }
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

const sec = (id: string, html: string, head = ''): Section => ({ id, kind: 'code', name: id, content: { html, head, wrap: '' }, items: [], style: { hideOn: [] } }) as unknown as Section

/** The page sections: one per payload, in a div the edit changes, named for the scenario. */
function sections(tag: string, cls: string, extra = ''): Record<string, string> {
  const out: Record<string, string> = {}
  PAYLOADS.forEach(([n, p], i) => {
    out['s' + i] = `<div data-wbc="${i}" class="${cls}">${p(hitJs(`${n}@${tag}`))}${extra ? p(hitJs(`${n}@${tag}${extra}`)) : ''}</div>`
  })
  return out
}

export interface Result {
  scenario: string
  loadHits: string[]
  editHits: string[]
  reconciled: boolean | null
}

/**
 * One scenario. static: the frame's copy is neutralised (the editor's static mode); sandbox: the
 * frame's sandbox attribute; edit: after loading, every section is changed through the reconciler
 * (static) — or, for the control, through the old createContextualFragment path with raw markup.
 */
async function run(scenario: string, opts: { static: boolean; sandbox: string; wait?: number }): Promise<Result> {
  window.hits = []
  const wait = opts.wait ?? 1500
  const prev = sections(scenario, 'a')
  const heads = HEAD_PAYLOADS.map(([n, p]) => p(hitJs(`${n}@${scenario}`))).join('')
  const secs = [...Object.entries(prev).map(([id, html]) => sec(id, html)), sec('head', '<p>x</p>', heads)]
  const doc = frameDoc(secs, { header: [], footer: [] }, opts.static ? 'static' : 'live', '', location.origin + '/')
  const f = document.createElement('iframe')
  f.setAttribute('sandbox', opts.sandbox)
  f.style.cssText = 'width:1200px;height:800px'
  const loaded = new Promise<void>((r) => f.addEventListener('load', () => r(), { once: true }))
  f.srcdoc = doc
  document.body.appendChild(f)
  await loaded
  const d = f.contentDocument
  let sync: FrameSync | null = null
  if (d && opts.static) {
    sweep(d)
    sync = new FrameSync(d)
    sync.collect()
  }
  await sleep(wait)
  const loadHits = [...window.hits]
  window.hits = []
  let reconciled: boolean | null = null
  const next = sections(scenario, 'b', '+edit')
  if (sync) {
    const before: Record<string, string> = { ...prev, head: '<p>x</p>' }
    reconciled = sync.update(before, { ...next, head: '<p>x</p>' })
  } else if (d) {
    // control: the old reconciler (createContextualFragment of the markup as stored)
    const r = d.createRange()
    r.selectNodeContents(d.body)
    d.body.appendChild(r.createContextualFragment(Object.values(next).join('')))
  }
  await sleep(wait)
  const editHits = [...window.hits]
  f.remove()
  return { scenario, loadHits, editHits, reconciled }
}

window.WB = { run, names: PAYLOADS.map(([n]) => n).concat(HEAD_PAYLOADS.map(([n]) => n)) }
