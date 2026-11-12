/**
 * Dialogs and builder-menu rows of the host tools (tools.ts): Schedule, Revisions and, in a popup
 * template, Popup settings. Same look as SiteSettingsDialog / PageToolsDialog.
 */
import { useEffect, useRef, useSyncExternalStore, type ReactNode } from 'react'
import { CalendarClock, Check, History, LoaderCircle, PanelTop, X } from 'lucide-react'
import { POPUP_LIMITS, type PopupFrequency, type PopupPosition, type PopupTrigger } from './contract'
import { t } from './i18n'
import { fromLocalInput, toLocalInput, type PopupController, type RevisionsController, type ScheduleController } from './tools'

const BTN = 'inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-4 text-[13px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-accent/70 disabled:opacity-50'
const PRIMARY = BTN + ' bg-accent text-white hover:brightness-110'
const SECONDARY = BTN + ' border border-white/15 text-ink-100 hover:bg-white/5'
const FIELD = 'h-9 w-full rounded-lg border border-white/12 bg-ink-900 px-3 text-[13px] text-ink-100 outline-none placeholder:text-ink-500 focus:border-accent focus-visible:ring-2 focus-visible:ring-accent/40'
const LABEL = 'mb-1.5 block text-[12px] font-semibold text-ink-200'
const HINT = 'mt-1 text-[11.5px] leading-snug text-ink-500'
const ROW = 'flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-[12.5px] font-medium text-ink-100 outline-none hover:bg-white/[0.07] focus-visible:bg-white/[0.07]'

interface Subscribable<S> {
  subscribe: (f: () => void) => () => void
  getState: () => S
}
const useStore = <S,>(c: Subscribable<S>) => useSyncExternalStore(c.subscribe, c.getState, c.getState)

export function MenuRow({ icon, label, onClick, close }: { icon: ReactNode; label: string; onClick: () => void; close: () => void }) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={() => {
        close()
        onClick()
      }}
      className={ROW}
    >
      {icon}
      {label}
    </button>
  )
}

export const ScheduleMenuRow = ({ c, close }: { c: ScheduleController; close: () => void }) =>
  c.available ? <MenuRow icon={<CalendarClock size={14} className="text-ink-300" />} label={t('Schedule publish')} onClick={c.open} close={close} /> : null
export const RevisionsMenuRow = ({ c, close }: { c: RevisionsController; close: () => void }) => (
  <MenuRow icon={<History size={14} className="text-ink-300" />} label={t('Revisions')} onClick={c.open} close={close} />
)
export const PopupMenuRow = ({ c, close }: { c: PopupController; close: () => void }) => (
  <MenuRow icon={<PanelTop size={14} className="text-ink-300" />} label={t('Popup settings')} onClick={c.open} close={close} />
)

function Shell({ title, icon, onClose, busy, footer, children }: { title: string; icon: ReactNode; onClose: () => void; busy?: boolean; footer: ReactNode; children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => box.current?.focus(), [])
  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={onClose}>
      <div
        ref={box}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onClose()
        }}
        className="flex max-h-[min(88vh,720px)] w-[min(560px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_30px_80px_-12px_rgba(0,0,0,.8)] outline-none"
      >
        <header className="flex shrink-0 items-center gap-3 border-b border-white/10 px-5 py-3.5">
          {icon}
          <h2 className="flex-1 text-[15px] font-semibold">{title}</h2>
          <button type="button" aria-label={t('Close')} disabled={busy} onClick={onClose} className="grid size-7 place-items-center rounded-md text-ink-400 hover:bg-white/5 hover:text-white disabled:opacity-40">
            <X size={16} />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">{children}</div>
        <footer className="flex shrink-0 items-center gap-3 border-t border-white/10 px-5 py-3">{footer}</footer>
      </div>
    </div>
  )
}

const Status = ({ error, ok }: { error: string; ok?: string }) => (
  <p role={error ? 'alert' : 'status'} className={'min-w-0 flex-1 truncate text-[12.5px] ' + (error ? 'text-red-300' : 'text-emerald-300')}>
    {error || ok || ''}
  </p>
)

/* ---------- Schedule ---------- */

export function ScheduleDialog({ c }: { c: ScheduleController }) {
  const s = useStore(c)
  const input = useRef<HTMLInputElement>(null)
  if (!s.open) return null
  const apply = () => c.schedule(fromLocalInput(input.current?.value ?? ''))
  return (
    <Shell
      title={t('Schedule publish')}
      icon={<CalendarClock size={17} className="text-accent-soft" />}
      onClose={c.close}
      footer={
        <>
          <Status error="" ok={s.at ? t('Scheduled for {date}', { date: new Date(s.at).toLocaleString() }) : ''} />
          {s.at && (
            <button type="button" className={SECONDARY} onClick={c.clear}>
              {t('Publish immediately')}
            </button>
          )}
          <button type="button" className={PRIMARY} onClick={apply}>
            {t('Set date')}
          </button>
        </>
      }
    >
      <label className={LABEL} htmlFor="wb-schedule-at">
        {t('Publish on')}
      </label>
      <input id="wb-schedule-at" ref={input} type="datetime-local" className={FIELD} defaultValue={toLocalInput(s.at)} />
      <p className={HINT}>{t('With a date in the future, Publish schedules the page: it goes live on that date. A date in the past publishes it now.')}</p>
    </Shell>
  )
}

/* ---------- Revisions ---------- */

export function RevisionsDialog({ c }: { c: RevisionsController }) {
  const s = useStore(c)
  if (!s.open) return null
  return (
    <Shell
      title={t('Revisions')}
      icon={<History size={17} className="text-accent-soft" />}
      onClose={c.close}
      busy={!!s.restoring}
      footer={
        <>
          <Status error={s.error} />
          <button type="button" className={SECONDARY} onClick={c.close} disabled={!!s.restoring}>
            {t('Close')}
          </button>
        </>
      }
    >
      {s.loading && !s.items.length ? (
        <p className="py-6 text-center text-[13px] text-ink-400">{t('Loading revisions…')}</p>
      ) : !s.items.length ? (
        <p className="py-6 text-center text-[13px] text-ink-400">{s.error ? '' : t('This page has no published revisions yet.')}</p>
      ) : (
        <ul className="divide-y divide-white/10 overflow-hidden rounded-lg border border-white/10">
          {s.items.map((r, i) => (
            <li key={r.id} className="flex items-center gap-3 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium text-ink-100">
                  {new Date(r.date).toLocaleString()}
                  {i === 0 && <span className="ml-2 rounded bg-white/8 px-1.5 py-0.5 text-[11px] text-ink-300">{t('Current')}</span>}
                </p>
                <p className="truncate text-[12px] text-ink-400">{t('Revision {rev} by {name}', { rev: r.rev, name: r.author?.name || '—' })}</p>
              </div>
              <button type="button" className={SECONDARY + ' h-8 px-3 text-[12.5px]'} disabled={!!s.restoring} onClick={() => void c.restore(r)}>
                {s.restoring === r.id ? <LoaderCircle size={14} className="animate-spin" /> : null}
                {t('Restore')}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Shell>
  )
}

/* ---------- Popup settings ---------- */

const TRIGGERS = (): [PopupTrigger, string][] => [
  ['load', t('On page load')],
  ['scroll', t('On scroll')],
  ['exit', t('On exit intent')],
  ['click', t('On click')],
]
const FREQS = (): [PopupFrequency, string][] => [
  ['always', t('Every page view')],
  ['session', t('Once per session')],
  ['once', t('Only once')],
  ['days', t('Once every few days')],
]
const POSITIONS = (): [PopupPosition, string][] => [
  ['center', t('Center')],
  ['top', t('Top')],
  ['bottom', t('Bottom')],
]

function Num({ id, label, value, min, max, onChange, hint }: { id: string; label: string; value: number; min: number; max: number; onChange: (n: number) => void; hint?: string }) {
  return (
    <div>
      <label className={LABEL} htmlFor={id}>
        {label}
      </label>
      <input id={id} type="number" className={FIELD} min={min} max={max} value={Number.isFinite(value) ? value : ''} onChange={(e) => onChange(Math.trunc(Number(e.target.value)))} />
      {hint && <p className={HINT}>{hint}</p>}
    </div>
  )
}

export function PopupDialog({ c }: { c: PopupController }) {
  const s = useStore(c)
  if (!s.open) return null
  const d = s.draft
  return (
    <Shell
      title={t('Popup settings')}
      icon={<PanelTop size={17} className="text-accent-soft" />}
      onClose={c.close}
      busy={s.saving}
      footer={
        <>
          <Status error={s.error} ok={s.saved ? t('Saved. The changes are live.') : ''} />
          <button type="button" className={SECONDARY} onClick={c.close} disabled={s.saving}>
            {t('Close')}
          </button>
          <button type="button" className={PRIMARY} onClick={() => void c.save()} disabled={!d || s.saving}>
            {s.saving ? <LoaderCircle size={15} className="animate-spin" /> : s.saved ? <Check size={15} /> : null}
            {t('Save changes')}
          </button>
        </>
      }
    >
      {!d ? (
        <p className="py-6 text-center text-[13px] text-ink-400">{s.loading ? t('Loading settings…') : ''}</p>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className={LABEL} htmlFor="wb-popup-trigger">
              {t('Open')}
            </label>
            <select id="wb-popup-trigger" className={FIELD} value={d.trigger} onChange={(e) => c.edit({ trigger: e.target.value as PopupTrigger })}>
              {TRIGGERS().map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </div>
          {(d.trigger === 'load' || d.trigger === 'exit') && <Num id="wb-popup-delay" label={t('Delay (seconds)')} value={d.delay} min={0} max={POPUP_LIMITS.maxDelay} onChange={(n) => c.edit({ delay: n })} />}
          {d.trigger === 'scroll' && <Num id="wb-popup-scroll" label={t('Scroll depth (%)')} value={d.scroll} min={1} max={100} onChange={(n) => c.edit({ scroll: n })} />}
          {d.trigger === 'click' && (
            <div>
              <label className={LABEL} htmlFor="wb-popup-selector">
                {t('Element (#id or .class)')}
              </label>
              <input id="wb-popup-selector" className={FIELD} value={d.selector} placeholder="#open-popup" onChange={(e) => c.edit({ selector: e.target.value })} />
            </div>
          )}
          <div>
            <label className={LABEL} htmlFor="wb-popup-frequency">
              {t('Show')}
            </label>
            <select id="wb-popup-frequency" className={FIELD} value={d.frequency} onChange={(e) => c.edit({ frequency: e.target.value as PopupFrequency })}>
              {FREQS().map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </div>
          {d.frequency === 'days' && <Num id="wb-popup-days" label={t('Days')} value={d.days} min={1} max={POPUP_LIMITS.maxDays} onChange={(n) => c.edit({ days: n })} />}
          <div>
            <label className={LABEL} htmlFor="wb-popup-position">
              {t('Position')}
            </label>
            <select id="wb-popup-position" className={FIELD} value={d.position} onChange={(e) => c.edit({ position: e.target.value as PopupPosition })}>
              {POSITIONS().map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </div>
          <Num id="wb-popup-width" label={t('Width (px)')} value={d.width} min={POPUP_LIMITS.minWidth} max={POPUP_LIMITS.maxWidth} onChange={(n) => c.edit({ width: n })} />
          <label className="flex items-center gap-2 text-[13px] sm:col-span-2">
            <input type="checkbox" checked={d.overlay} onChange={(e) => c.edit({ overlay: e.target.checked })} />
            {t('Close when the backdrop is clicked')}
          </label>
          <label className="flex items-center gap-2 text-[13px] sm:col-span-2">
            <input type="checkbox" checked={d.afterConsent} onChange={(e) => c.edit({ afterConsent: e.target.checked })} />
            {t('Wait until the visitor answered the cookie banner')}
          </label>
          {(d.frequency === 'once' || d.frequency === 'days') && (
            <div className="sm:col-span-2">
              <button type="button" className={SECONDARY} onClick={() => c.edit({ version: d.version + 1 })}>
                {t('Show again to everyone')}
              </button>
              <p className={HINT}>{t('Visitors who already saw this popup see it again after you save.')}</p>
            </div>
          )}
        </div>
      )}
    </Shell>
  )
}
