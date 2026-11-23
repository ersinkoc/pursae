/**
 * In-page side of the Playwright regression (editor-xss.mjs): builds the element editor's frame
 * from every payload with the editor's own modules (frame.ts, neutral.ts), the way the canvas
 * does, edits it through the reconciler (FrameSync), and reports which payloads reached the
 * editor's REST nonce (window.hit).
 */
import type { Section } from '@/builder/types'
import { FrameSync, frameDoc } from '../../host/elements/frame'
import { sweep } from '../../host/elements/neutral'
import { codeFrameDoc } from '@/builder/codeFrameDoc'
import { frameGuard } from '../../host/elements/guard'
import { HEAD_PAYLOADS, PAYLOADS, hitJs } from './payloads'
import { frameModeOf, initialScripts, sandboxOf } from '../../host/elements/trust'

declare global {
  interface Window {
    hits: string[]
    hit: (n: string) => void
    WB: { run: typeof run; runMixed: typeof runMixed; runTrust: typeof runTrust; names: string[] }
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

/** A frame of a mixed page's run, loaded like CodeFrame (patch 0021) loads it. */
async function loadRun(html: string, sandbox: string, guarded: boolean): Promise<HTMLIFrameElement> {
  const f = document.createElement('iframe')
  f.setAttribute('sandbox', sandbox)
  f.style.cssText = 'width:1200px;height:600px'
  const loaded = new Promise<void>((r) => f.addEventListener('load', () => r(), { once: true }))
  f.srcdoc = html
  document.body.appendChild(f)
  await loaded
  if (guarded && f.contentDocument) frameGuard.sweep(f.contentDocument)
  return f
}

/**
 * A MIXED page (patch 0021): code sections, a builder hero section (React in the editor's own
 * document, between the two runs) and code sections again; each run of code sections is one frame
 * (CodeSection.tsx). guard: the frame guard's static frame (edit mode, or Preview without the
 * author's confirmation); else the old frame (markup as stored, allow-scripts) as the control.
 * edit: like CodeFrame — the toolbar writes attributes into the frame (neutralised there), and a
 * structural change reloads the frame with the run's new markup.
 */
async function runMixed(scenario: string, opts: { guard: boolean; edit: boolean; sandbox: string; wait?: number }): Promise<Result> {
  window.hits = []
  const wait = opts.wait ?? 1500
  const all = Object.entries(sections(scenario, 'a'))
  const half = Math.ceil(all.length / 2)
  const heads = HEAD_PAYLOADS.map(([n, p]) => p(hitJs(`${n}@${scenario}`))).join('\n')
  const runA = all.slice(0, half).map(([id, html], i) => sec(id, html, i === 0 ? heads : ''))
  const runB = all.slice(half).map(([id, html]) => sec(id, html))
  const hero = document.createElement('section')
  hero.setAttribute('data-wb-builder-hero', '')
  hero.innerHTML = '<h1>Builder hero</h1><p>A builder section between the runs.</p>'
  const g = opts.guard ? frameGuard : null
  const frames: HTMLIFrameElement[] = []
  frames.push(await loadRun(codeFrameDoc(runA, opts.edit, g), opts.sandbox, !!g))
  document.body.appendChild(hero)
  frames.push(await loadRun(codeFrameDoc(runB, opts.edit, g), opts.sandbox, !!g))
  await sleep(wait)
  const loadHits = [...window.hits]
  window.hits = []
  // toolbar attribute writes: a link's address, an image's source (applied to the frame, then neutralised)
  for (const f of frames) {
    const d = f.contentDocument
    if (!d) continue
    for (const el of Array.from(d.querySelectorAll<HTMLElement>('[data-wbc]'))) {
      el.setAttribute('data-x', '1')
      if (el.localName === 'div') {
        const a = d.createElement('a')
        a.setAttribute('href', `javascript:${hitJs(`toolbar-href@${scenario}`)}`)
        a.textContent = 'x'
        el.appendChild(a)
        if (g) g.neutralEl(a)
        a.click()
      }
    }
  }
  // a structural change: every run is drawn again with its new markup
  const next = Object.entries(sections(scenario, 'b', '+edit'))
  const nextA = next.slice(0, half).map(([id, html], i) => sec(id, html, i === 0 ? heads : ''))
  const nextB = next.slice(half).map(([id, html]) => sec(id, html))
  for (const f of frames) f.remove()
  frames.length = 0
  frames.push(await loadRun(codeFrameDoc(nextA, opts.edit, g), opts.sandbox, !!g))
  frames.push(await loadRun(codeFrameDoc(nextB, opts.edit, g), opts.sandbox, !!g))
  await sleep(wait)
  const editHits = [...window.hits]
  for (const f of frames) f.remove()
  hero.remove()
  return { scenario, loadHits, editHits, reconciled: null }
}

/**
 * The canvas's first frame for a page, as the editor decides it from the boot object before it
 * makes the frame (trust.ts initialScripts → frameModeOf → sandboxOf): a design the user does not
 * trust must load static and inert (the prompt shows, nothing has run); a trusted one loads live.
 */
async function runTrust(scenario: string, o: { canRunScripts: boolean; scriptTrust: string[]; kit?: string; wait?: number }): Promise<Result & { live: boolean; prompt: boolean }> {
  const bootObj = {
    caps: { unfilteredHtml: o.canRunScripts },
    canRunScripts: o.canRunScripts,
    scriptTrust: o.scriptTrust,
    codeParts: null,
    document: { doc: { sections: [{ kind: 'code', content: { html: '<script>x()</script>', ...(o.kit ? { kit: o.kit } : {}) } }] }, draft: null },
  } as unknown as Parameters<typeof initialScripts>[0]
  const s = initialScripts(bootObj)
  const fmode = frameModeOf(false, s.live, s.scripts)
  const res = await run(scenario, { static: fmode === 'static', sandbox: sandboxOf(s.scripts), wait: o.wait })
  return { ...res, live: fmode === 'live', prompt: s.prompt }
}

window.WB = { run, runMixed, runTrust, names: PAYLOADS.map(([n]) => n).concat(HEAD_PAYLOADS.map(([n]) => n)) }
