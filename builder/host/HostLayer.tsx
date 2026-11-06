/**
 * The CMS host's own UI next to the builder: notices / prompts (reload, conflicts, restored draft,
 * lock) and the media library dialog. State lives in HostUi (cms-host.ts).
 */
import { useSyncExternalStore } from 'react'
import type { CmsHost, Notice, NoticeTone } from './cms-host'
import { MediaPicker } from './MediaPicker'

const TONE: Record<NoticeTone, string> = {
  info: 'border-white/10',
  success: 'border-emerald-400/40',
  warning: 'border-amber-400/50',
  error: 'border-red-400/60',
}

function NoticeCard({ n, onAnswer }: { n: Notice; onAnswer: (id: string | null) => void }) {
  return (
    <div role={n.tone === 'error' || n.prompt ? 'alertdialog' : 'status'} aria-live="polite" className={'pointer-events-auto w-[min(440px,calc(100vw-32px))] rounded-xl border bg-ink-850 px-4 py-3 text-ink-100 shadow-[0_20px_60px_-12px_rgba(0,0,0,.7)] ' + TONE[n.tone]}>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          {n.title && <p className="text-[13.5px] font-semibold">{n.title}</p>}
          <p className="text-[13px] leading-snug text-ink-200">{n.message}</p>
        </div>
        {!(n.prompt && n.key === 'reload') && (
          <button type="button" aria-label="Dismiss" onClick={() => onAnswer(null)} className="-mr-1 grid size-6 shrink-0 place-items-center rounded-md text-ink-400 hover:bg-white/5 hover:text-white">
            ×
          </button>
        )}
      </div>
      {n.actions.length > 0 && (
        <div className="mt-2.5 flex flex-wrap justify-end gap-2">
          {n.actions.map((a) => {
            const cls = 'inline-flex h-8 items-center rounded-lg px-3 text-[12.5px] font-semibold ' + (a.primary ? 'bg-accent text-white' : 'border border-white/15 text-ink-200 hover:text-white')
            return a.href ? (
              <a key={a.id} href={a.href} className={cls} target={a.external ? '_blank' : undefined} rel={a.external ? 'noopener noreferrer' : undefined} onClick={() => onAnswer(a.id)}>
                {a.label}
              </a>
            ) : (
              <button key={a.id} type="button" className={cls} onClick={() => onAnswer(a.id)}>
                {a.label}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function HostLayer({ host }: { host: CmsHost }) {
  const ui = host.ui
  const state = useSyncExternalStore(ui.subscribe, ui.getState, ui.getState)
  const images = host.boot.media.types.filter((t) => t.startsWith('image/'))
  return (
    <>
      {state.notices.length > 0 && (
        <div className="pointer-events-none fixed top-[64px] right-4 z-[85] flex flex-col items-end gap-2">
          {state.notices.map((n) => (
            <NoticeCard key={n.id} n={n} onAnswer={(a) => ui.answer(n.id, a)} />
          ))}
        </div>
      )}
      {state.picker && (() => {
        const kind = state.picker.kind
        return (
        <MediaPicker
          key={state.picker.id}
          rest={host.rest}
          accept={images.length ? images.join(',') : 'image/*'}
          upload={host.boot.caps.uploadFiles ? (f) => host.media.upload(f, kind) : null}
          onClose={(a) => ui.closePicker(a)}
        />
        )
      })()}
    </>
  )
}
