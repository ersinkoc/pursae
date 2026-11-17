/**
 * Running a design's scripts in the editor is the author's explicit choice (security model,
 * docs/web-builder-code-sections.md): the scripts run in the editor's origin, with the author's
 * access. ScriptsConsent asks once per editor session (the answer lives in the element editor's
 * state, state.ts `scripts`, never stored); ScriptsBanner is the preview's notice that the
 * design's scripts are not running, with the button that asks. The element editor's canvas
 * (ElementsCanvas) and the per-run frames of mixed pages (patch 0021, guard.ts) share both.
 */
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { t } from '../i18n'
import { setElState } from './state'

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
  return (
    <>
      <div data-wb-scripts-off="" className="absolute left-1/2 top-16 z-30 flex max-w-[92%] -translate-x-1/2 items-center gap-3 rounded-lg bg-[#1d2327] px-3 py-2 text-[12px] text-ink-200 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <span>{t('The design’s scripts are not running in this preview.')}</span>
        <button type="button" data-wb-scripts-ask="" onClick={() => setAsk(true)} className="shrink-0 rounded-md bg-amber-400/20 px-2 py-1 text-amber-200 hover:bg-amber-400/30">
          {t('Run them…')}
        </button>
      </div>
      {ask && (
        <ScriptsConsent
          fixed
          onCancel={() => setAsk(false)}
          onRun={() => {
            setElState({ scripts: true })
            setAsk(false)
          }}
        />
      )}
    </>
  )
}
