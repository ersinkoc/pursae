/**
 * Running a design's scripts in the editor is the author's explicit choice (security model,
 * docs/web-builder-code-sections.md): the scripts run in the editor's origin, with the author's
 * access. The answer lives in the element editor's state (state.ts `scripts` and the grants): the
 * user trusts everything the page runs — its kits and its inline code, remembered per user on the
 * server, trust.ts — or chose "This session only" for what the page runs now (never stored).
 *
 * TrustPrompt is the element editor canvas's question (non-blocking, over the canvas: Always for
 * this design / This session only / Not now). ScriptsConsent is the confirmation the mixed pages'
 * Preview asks (patch 0021, guard.ts); ScriptsBanner is their preview's notice that the scripts
 * are not running, with the button that asks (none for a user who cannot run scripts).
 */
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ShieldAlert } from 'lucide-react'
import { t } from '../i18n'
import { tc } from './canvas-tr'
import { getElState, setElState, useElState } from './state'
import { covers, grantsOf, missing, pageTrust, runsNow, setKitTrust, type Grants, type Needs, type TrustList } from './trust'
import { usePageNeeds } from './pageneeds'

type Put = (slug: string, on: boolean, code?: readonly string[]) => Promise<TrustList>

/**
 * "Always for this design": every kit of the page not trusted yet is trusted on the server (per
 * user), with the page's inline code not approved yet (stored with each of them; with the first
 * kit of the page when only code is missing); then the scripts run (Live). When a request fails,
 * the trust the server confirmed so far is kept (its answer is the user's whole list) and the
 * error is thrown; nothing runs.
 */
export async function trustAlways(needs: Needs, put: Put = setKitTrust): Promise<void> {
  const s = getElState()
  if (!s.canRun || !needs.kits.length) return
  const m = missing(needs, grantsOf(s), false)
  const targets = m.kits.length ? m.kits : m.code.length ? [needs.kits[0]] : []
  let list: TrustList | null = null
  try {
    for (const k of targets) list = await put(k, true, m.code)
  } finally {
    if (list) setElState({ trust: new Set(list.trust), trustCode: list.code })
  }
  if (!covers(needs, grantsOf(getElState()), false)) throw new Error(tc('The server did not confirm the trust. Try again.'))
  setElState({ scripts: true, live: true, prompt: false, editing: null })
}

/** "This session only": what the page runs now may run until the editor is closed; nothing is stored. */
export function trustSession(needs: Needs) {
  const s = getElState()
  if (!s.canRun) return
  setElState({
    sessionKits: new Set([...s.sessionKits, ...needs.kits]),
    sessionCode: new Set([...s.sessionCode, ...needs.code]),
    scripts: true,
    live: true,
    prompt: false,
    editing: null,
  })
}

/** "Not now (safe mode)": the prompt goes; Live in the bottom bar asks again. */
export function trustLater() {
  setElState({ prompt: false })
}

/**
 * The page's code changed: when the grants no longer cover what it runs (a section of another kit
 * was inserted, code was pasted or edited), the scripts stop — the canvas already drew the change
 * static, as runsNow said in render — and the prompt asks again.
 */
export function reconcileTrust(needs: Needs) {
  const s = getElState()
  if (!s.scripts || covers(needs, grantsOf(s))) return
  setElState({ scripts: false, live: false, interact: false, editing: null, prompt: s.canRun && needs.scripts })
}

/**
 * "Stop always running this design's scripts": the page's trusted kits are no longer trusted
 * (server), and the canvas goes back to Safe mode at once (a session answer goes too).
 */
export async function revokeTrust(kits: readonly string[], put: Put = setKitTrust): Promise<void> {
  let list: TrustList | null = null
  try {
    for (const k of kits) if (getElState().trust.has(k)) list = await put(k, false)
  } finally {
    const trust = new Set(list ? list.trust : getElState().trust)
    for (const k of kits) if (list) trust.delete(k)
    setElState({ trust, trustCode: list ? list.code : getElState().trustCode, sessionKits: new Set(), sessionCode: new Set(), scripts: false, live: false, interact: false, prompt: false, editing: null })
  }
}

/** What the prompt offers for a page: "Always" needs kits to remember; nothing for a user who cannot run scripts or when nothing is missing. */
export function promptChoices(needs: Needs, g: Grants, canRun: boolean): ('always' | 'session' | 'later')[] {
  const pt = pageTrust(needs, g, canRun)
  if (pt === 'blocked' || covers(needs, g)) return []
  return pt === 'untrusted' ? ['always', 'session', 'later'] : ['session', 'later']
}

/**
 * The canvas's question before the design's scripts run: not a modal wall, the page stays
 * editable in Safe mode under it. busy: a trust request is in flight; error: it failed.
 */
export function TrustPrompt({ needs, grants, canRun, onAlways, onSession, onLater, busy = false, error = '' }: { needs: Needs; grants: Grants; canRun: boolean; onAlways: () => void; onSession: () => void; onLater: () => void; busy?: boolean; error?: string }) {
  const choices = promptChoices(needs, grants, canRun)
  if (!choices.length) return null
  const btn = 'rounded-md px-2.5 py-1.5 disabled:opacity-50'
  return (
    <div role="dialog" aria-modal="false" aria-labelledby="wb-trust-title" data-wb-trust-prompt="" className="absolute bottom-12 left-1/2 z-40 w-[min(560px,calc(100%-24px))] -translate-x-1/2 rounded-xl border border-amber-300/25 bg-[#1d2327] p-4 text-[12.5px] leading-relaxed text-ink-200 shadow-2xl" onClick={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}>
      <div className="flex gap-3">
        <ShieldAlert size={18} className="mt-0.5 shrink-0 text-amber-300" />
        <div className="min-w-0 flex-1">
          <h2 id="wb-trust-title" className="mb-1 text-[13.5px] font-semibold text-white">
            {tc('This design uses scripts. Run them so the editor matches your site?')}
          </h2>
          <p className="text-ink-400">{tc('Scripts run inside the editor with your admin access: they can do anything you can do on this site, such as change content, settings and users. Only run designs you trust.')}</p>
          {error && (
            <p role="alert" className="mt-2 text-red-300">
              {error}
            </p>
          )}
          <div className="mt-3 flex flex-wrap justify-end gap-2">
            <button type="button" data-wb-trust="later" disabled={busy} onClick={onLater} className={`${btn} hover:bg-white/10`}>
              {tc('Not now (safe mode)')}
            </button>
            <button type="button" data-wb-trust="session" disabled={busy} onClick={onSession} className={`${btn} bg-white/10 text-white hover:bg-white/15`}>
              {tc('This session only')}
            </button>
            {choices.includes('always') && (
              <button type="button" data-wb-trust="always" disabled={busy} onClick={onAlways} className={`${btn} bg-amber-400 font-semibold text-black hover:bg-amber-300`}>
                {tc('Always for this design')}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * The confirmation before the design's scripts run in the editor (Live edit, Preview): they run in
 * the editor's origin, so they can do whatever the author can on this site. Asked once per editor
 * session; never remembered. fixed: over the whole window, in a portal on the editor's body (a mixed
 * page's frame is one part of the builder's canvas, inside its transformed and stacked layers),
 * else over the positioned parent.
 */
export function ScriptsConsent({ onRun, onCancel, fixed = false }: { onRun: () => void; onCancel: () => void; fixed?: boolean }) {
  const run = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    run.current?.focus()
    const key = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      onCancel()
    }
    window.addEventListener('keydown', key, true)
    return () => window.removeEventListener('keydown', key, true)
  }, [onCancel])
  const dialog = (
    <div className={`${fixed ? 'fixed z-[1000]' : 'absolute z-50'} inset-0 grid place-items-center bg-black/50 p-4`} onClick={(e) => (e.stopPropagation(), onCancel())}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="wb-scripts-title" data-wb-scripts-consent="" className="w-full max-w-[440px] rounded-xl border border-white/10 bg-[#1d2327] p-5 text-[12.5px] leading-relaxed text-ink-200 shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <h2 id="wb-scripts-title" className="mb-2 text-[14px] font-semibold text-white">
          {t('Run this design’s scripts?')}
        </h2>
        <p>{t('The design’s scripts will run inside the editor with your admin access: they can do anything you can do on this site, such as change content, settings and users.')}</p>
        <p className="mt-2 text-ink-400">{t('Only continue if you trust where this design came from. Editing works without them; this is asked again in every editor session.')}</p>
        <div className="mt-4 flex flex-wrap justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md px-3 py-1.5 hover:bg-white/10">
            {t('Cancel')}
          </button>
          <button ref={run} type="button" data-wb-scripts-run="" onClick={onRun} className="rounded-md bg-amber-400 px-3 py-1.5 font-semibold text-black hover:bg-amber-300">
            {t('Run this design’s scripts with your admin access')}
          </button>
        </div>
      </div>
    </div>
  )
  return fixed && typeof document !== 'undefined' ? createPortal(dialog, document.body) : dialog
}

/**
 * The preview notice of a frame whose design scripts are not running (no confirmation yet in this
 * editor session), with "Run them…": confirmed, every frame of the editor runs them. Placed below
 * the builder's own "Preview · back to editing" pill.
 */
export function ScriptsBanner() {
  const [ask, setAsk] = useState(false)
  const st = useElState()
  const needs = usePageNeeds()
  const canRun = st.canRun
  // The page changed under a session answer: what it runs now is asked about again.
  useEffect(() => reconcileTrust(needs), [needs, st.scripts])
  if (runsNow(st, needs)) return null
  return (
    <>
      <div data-wb-scripts-off="" className="absolute left-1/2 top-16 z-30 flex max-w-[92%] -translate-x-1/2 items-center gap-3 rounded-lg bg-[#1d2327] px-3 py-2 text-[12px] text-ink-200 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <span>{t('The design’s scripts are not running in this preview.')}</span>
        {canRun ? (
          <button type="button" data-wb-scripts-ask="" onClick={() => setAsk(true)} className="shrink-0 rounded-md bg-amber-400/20 px-2 py-1 text-amber-200 hover:bg-amber-400/30">
            {t('Run them…')}
          </button>
        ) : (
          <span className="text-ink-400">{tc('The design’s scripts run on the published page.')}</span>
        )}
      </div>
      {ask && (
        <ScriptsConsent
          fixed
          onCancel={() => setAsk(false)}
          onRun={() => {
            trustSession(needs)
            setAsk(false)
          }}
        />
      )}
    </>
  )
}
