/**
 * The page tools dialog (page-tools.ts): tabs for what the built-in plugins add to a page, shown only
 * for what GET documents/{id}/plugins reports: "Languages" (Pursae Multi Language: translations and
 * missing languages) and "SEO" (Pursae SEO: snippet, focus keyword, robots, social). Also the
 * "Page tools" row of the builder menu (BuilderHost.menuExtra).
 */
import { useEffect, useRef, useSyncExternalStore, type KeyboardEvent } from 'react'
import { Check, ImagePlus, Languages, LoaderCircle, SearchCheck, SlidersHorizontal, X } from 'lucide-react'
import { t } from './i18n'
import { safeEditUrl, seoRating, seoScoreOf, statusLabel, type PageToolsController, type PageToolsTab, type SeoDraft, type SeoRating, type SeoTextField } from './page-tools'

const BTN = 'inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-4 text-[13px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-accent/70 disabled:opacity-50'
const PRIMARY = BTN + ' bg-accent text-white hover:brightness-110'
const SECONDARY = BTN + ' border border-white/15 text-ink-100 hover:bg-white/5'
const SMALL = ' h-8 px-3 text-[12.5px]'
const FIELD = 'h-9 w-full rounded-lg border border-white/12 bg-ink-900 px-3 text-[13px] text-ink-100 outline-none placeholder:text-ink-500 focus:border-accent focus-visible:ring-2 focus-visible:ring-accent/40'
const AREA = 'w-full resize-y rounded-lg border border-white/12 bg-ink-900 px-3 py-2 text-[13px] leading-snug text-ink-100 outline-none placeholder:text-ink-500 focus:border-accent focus-visible:ring-2 focus-visible:ring-accent/40'
const LABEL = 'mb-1.5 block text-[12px] font-semibold text-ink-200'
const HINT = 'mt-1 text-[11.5px] leading-snug text-ink-500'
const SECTION = 'space-y-4 border-b border-white/10 px-5 py-5 last:border-b-0'
const LOCKED = ' disabled:cursor-not-allowed disabled:opacity-50'
const HEADING = 'text-[13px] font-semibold uppercase tracking-wide text-ink-400'

/** The row of the builder menu that opens the dialog; nothing while the page has no tab. */
export function PageToolsMenuRow({ controller, close }: { controller: PageToolsController; close: () => void }) {
  const s = useSyncExternalStore(controller.subscribe, controller.getState, controller.getState)
  if (!controller.available || !s.panels) return null
  return (
    <button
      type="button"
      role="menuitem"
      onClick={() => {
        close()
        controller.open()
      }}
      className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-[12.5px] font-medium text-ink-100 outline-none hover:bg-white/[0.07] focus-visible:bg-white/[0.07]"
    >
      <SlidersHorizontal size={14} className="text-ink-300" />
      {t('Page tools')}
    </button>
  )
}

const tabLabel = (tab: PageToolsTab) => (tab === 'languages' ? t('Languages') : t('SEO'))

function Notice({ children, alert }: { children: string; alert?: boolean }) {
  return (
    <p role={alert ? 'alert' : 'status'} className={'px-5 py-8 text-center text-[13px] ' + (alert ? 'text-red-300' : 'text-ink-400')}>
      {children}
    </p>
  )
}

/* ---------- Languages ---------- */

function LanguagesPanel({ c }: { c: PageToolsController }) {
  const { languages: l } = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const data = l.data
  if (l.load === 'loading' && !data) return <Notice>{t('Loading languages…')}</Notice>
  if (!data) return <Notice alert>{l.error || t('The languages could not be loaded.')}</Notice>
  if (!data.enabled) return <Notice>{t('Languages are not available for this page.')}</Notice>
  const busy = !!l.adding
  return (
    <div>
      <section className={SECTION} aria-labelledby="wb-lang-current">
        <h3 id="wb-lang-current" className={HEADING}>
          {t('This page')}
        </h3>
        <p className="flex items-center gap-2 text-[13px] text-ink-100">
          {data.current ? (
            <>
              <span aria-hidden="true">{data.current.flag}</span>
              <span className="font-medium">{data.current.name}</span>
            </>
          ) : (
            <span className="text-ink-400">{t('No language is set for this page.')}</span>
          )}
        </p>
      </section>

      <section className={SECTION} aria-labelledby="wb-lang-translations">
        <h3 id="wb-lang-translations" className={HEADING}>
          {t('Translations')}
        </h3>
        <ul className="divide-y divide-white/10 overflow-hidden rounded-lg border border-white/10">
          {data.translations.map((tr) => {
            const url = safeEditUrl(tr.editUrl)
            return (
              <li key={tr.postId} className="flex items-center gap-3 px-3 py-2.5">
                <span aria-hidden="true" className="text-[16px] leading-none">
                  {tr.flag}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium text-ink-100">{tr.name}</p>
                  <p className="truncate text-[12px] text-ink-400">
                    {tr.readOnly ? t('You cannot edit this translation.') : `${tr.title || t('(no title)')} · ${statusLabel(tr.status)}`}
                  </p>
                </div>
                {tr.current ? (
                  <span className="rounded-md bg-white/8 px-2 py-1 text-[11.5px] font-semibold text-ink-200">{t('Current')}</span>
                ) : (
                  url && (
                    <a
                      href={url}
                      className={SECONDARY + SMALL}
                      onClick={(e) => {
                        // A plain click flushes pending edits first; modified clicks keep the browser's behaviour.
                        if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
                        e.preventDefault()
                        void c.openTranslation(url)
                      }}
                    >
                      {t('Open')}
                    </a>
                  )
                )}
              </li>
            )
          })}
        </ul>
      </section>

      {data.missing.length > 0 && (
        <section className={SECTION} aria-labelledby="wb-lang-missing">
          <h3 id="wb-lang-missing" className={HEADING}>
            {t('Missing translations')}
          </h3>
          <ul className="divide-y divide-white/10 overflow-hidden rounded-lg border border-white/10">
            {data.missing.map((m) => (
              <li key={m.slug} className="flex items-center gap-3 px-3 py-2.5">
                <span aria-hidden="true" className="text-[16px] leading-none">
                  {m.flag}
                </span>
                <p className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink-100">{m.name}</p>
                {data.canTranslate && (
                  <button type="button" className={SECONDARY + SMALL} disabled={busy} onClick={() => void c.addTranslation(m.slug)}>
                    {l.adding === m.slug && <LoaderCircle size={14} className="animate-spin" />}
                    {t('Add translation')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {data.canTranslate && <p className={HINT}>{t('A translation starts as a draft copy of the published page. Your latest changes are saved as a draft first.')}</p>}
        </section>
      )}
    </div>
  )
}

/* ---------- SEO ---------- */

const RATING_CLASS: Record<SeoRating, string> = {
  good: 'bg-emerald-400/15 text-emerald-300',
  ok: 'bg-amber-400/15 text-amber-300',
  bad: 'bg-red-400/15 text-red-300',
}

function ratingLabel(r: SeoRating): string {
  return r === 'good' ? t('Good') : r === 'ok' ? t('Needs improvement') : t('Poor')
}

function TextField({ c, draft, field, label, hint, area, placeholder }: { c: PageToolsController; draft: SeoDraft; field: SeoTextField; label: string; hint?: string; area?: boolean; placeholder?: string }) {
  const id = 'wb-seo-' + field
  const common = { id, value: draft[field], placeholder, disabled: !c.canEditSeo(field), onChange: (e: { target: { value: string } }) => c.editSeo({ [field]: e.target.value }) }
  return (
    <div>
      <label className={LABEL} htmlFor={id}>
        {label}
      </label>
      {area ? <textarea {...common} rows={3} className={AREA + LOCKED} /> : <input {...common} className={FIELD + LOCKED} />}
      {hint && <p className={HINT}>{hint}</p>}
    </div>
  )
}

function Toggle({ id, checked, label, hint, disabled, onChange }: { id: string; checked: boolean; label: string; hint: string; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <div>
      <label htmlFor={id} className={'flex items-center gap-2 text-[13px] text-ink-100' + (disabled ? ' opacity-50' : '')}>
        <input id={id} type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
        {label}
      </label>
      <p className={HINT}>{hint}</p>
    </div>
  )
}

function SeoPanel({ c }: { c: PageToolsController }) {
  const { seo: s } = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const { data, draft } = s
  if (s.load === 'loading' && !data) return <Notice>{t('Loading SEO settings…')}</Notice>
  if (!data || !draft) return <Notice alert>{s.error || t('The SEO settings could not be loaded.')}</Notice>
  const score = seoScoreOf(data)
  const rating = score === null ? null : seoRating(score)
  const socialOk = c.canEditSeo('og_image')
  const preview = c.previewUrl(draft.og_image)
  return (
    <div>
      <section className={SECTION} aria-labelledby="wb-seo-score">
        <h3 id="wb-seo-score" className={HEADING}>
          {t('SEO score')}
        </h3>
        {score === null || !rating ? (
          <p className="text-[13px] text-ink-400">{t('Not analyzed yet. The score appears after you save.')}</p>
        ) : (
          <p className="flex items-center gap-2.5 text-[13px] text-ink-100">
            <span className={'rounded-md px-2 py-1 text-[12.5px] font-semibold ' + RATING_CLASS[rating]}>{score} / 100</span>
            <span>{ratingLabel(rating)}</span>
          </p>
        )}
        <p className={HINT}>{t('Calculated from the saved page content and these settings.')}</p>
      </section>

      <section className={SECTION} aria-labelledby="wb-seo-snippet">
        <h3 id="wb-seo-snippet" className={HEADING}>
          {t('Search appearance')}
        </h3>
        <TextField c={c} draft={draft} field="focus" label={t('Focus keyword')} hint={t('The word or phrase you want this page to be found for.')} />
        <TextField c={c} draft={draft} field="title" label={t('SEO title')} hint={t('Leave empty to use the template from the SEO settings.')} />
        <TextField c={c} draft={draft} field="description" label={t('Meta description')} area hint={t('A short summary for search results, about 155 characters.')} />
        <TextField c={c} draft={draft} field="canonical" label={t('Canonical URL')} placeholder="https://" hint={c.canEditSeo('canonical') ? t('Only when the same content lives at another address.') : t('You do not have permission to change the canonical URL or search engine settings.')} />
      </section>

      <section className={SECTION} aria-labelledby="wb-seo-robots">
        <h3 id="wb-seo-robots" className={HEADING}>
          {t('Search engines')}
        </h3>
        <Toggle id="wb-seo-noindex" checked={draft.noindex} label={t('Hide this page from search results')} hint={t('Adds noindex: search engines will not list the page.')} disabled={!c.canEditSeo('noindex')} onChange={(v) => c.editSeo({ noindex: v })} />
        <Toggle id="wb-seo-nofollow" checked={draft.nofollow} label={t('Do not follow links on this page')} hint={t('Adds nofollow: search engines will not pass credit through its links.')} disabled={!c.canEditSeo('nofollow')} onChange={(v) => c.editSeo({ nofollow: v })} />
      </section>

      <section className={SECTION} aria-labelledby="wb-seo-social">
        <h3 id="wb-seo-social" className={HEADING}>
          {t('Social sharing')}
        </h3>
        <TextField c={c} draft={draft} field="og_title" label={t('Social title')} hint={t('Leave empty to use the SEO title.')} />
        <TextField c={c} draft={draft} field="og_description" label={t('Social description')} area hint={t('Leave empty to use the meta description.')} />
        <div>
          <label className={LABEL} htmlFor="wb-seo-og_image">
            {t('Social image')}
          </label>
          <div className="flex items-start gap-3">
            <div className="grid h-14 w-24 shrink-0 place-items-center overflow-hidden rounded-lg border border-white/10 bg-ink-900">
              {preview ? <img src={preview} alt="" referrerPolicy="no-referrer" className="max-h-full max-w-full object-contain" /> : <ImagePlus size={18} className="text-ink-500" />}
            </div>
            <div className="min-w-0 flex-1 space-y-2">
              <input id="wb-seo-og_image" className={FIELD + LOCKED} value={draft.og_image} placeholder="https://" disabled={!socialOk} onChange={(e) => c.editSeo({ og_image: e.target.value })} />
              <div className="flex flex-wrap gap-2">
                {c.canPick && socialOk && (
                  <button type="button" className={SECONDARY + SMALL} onClick={() => void c.pickSocialImage()}>
                    {draft.og_image ? t('Change') : t('Select')}
                  </button>
                )}
                {draft.og_image && socialOk && (
                  <button type="button" className={SECONDARY + SMALL} onClick={() => c.editSeo({ og_image: '' })}>
                    {t('Remove')}
                  </button>
                )}
              </div>
            </div>
          </div>
          <p className={HINT}>{socialOk ? t('Shown when the page is shared. 1200 × 630 pixels works best.') : t('You do not have permission to change the social sharing settings.')}</p>
        </div>
      </section>
    </div>
  )
}

/* ---------- Dialog ---------- */

export function PageToolsDialog({ controller: c }: { controller: PageToolsController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (s.open) box.current?.focus()
  }, [s.open])
  if (!s.open) return null
  const tabs = c.tabs
  const busy = s.seo.saving || !!s.languages.adding
  const onSeo = s.tab === 'seo'
  const dirty = c.seoDirty

  const onTabKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = tabs.indexOf(s.tab)
    const next = e.key === 'ArrowRight' ? tabs[(i + 1) % tabs.length] : e.key === 'ArrowLeft' ? tabs[(i - 1 + tabs.length) % tabs.length] : e.key === 'Home' ? tabs[0] : e.key === 'End' ? tabs[tabs.length - 1] : null
    if (!next) return
    e.preventDefault()
    c.select(next)
    requestAnimationFrame(() => document.getElementById('wb-pt-tab-' + next)?.focus())
  }

  let status = ''
  let statusError = false
  if (onSeo) {
    if (s.seo.data && s.seo.error) {
      status = s.seo.error
      statusError = true
    } else if (s.seo.saved) status = t('Saved.')
  } else if (s.languages.data && s.languages.error) {
    status = s.languages.error
    statusError = true
  }

  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={() => c.close()}>
      <div
        ref={box}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={t('Page tools')}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === 'Escape') c.close()
        }}
        className="flex max-h-[min(88vh,760px)] w-[min(640px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_30px_80px_-12px_rgba(0,0,0,.8)] outline-none"
      >
        <header className="flex shrink-0 items-center gap-3 border-b border-white/10 px-5 py-3.5">
          <SlidersHorizontal size={17} className="text-accent-soft" />
          <h2 className="flex-1 text-[15px] font-semibold">{t('Page tools')}</h2>
          <button type="button" aria-label={t('Close')} disabled={busy} onClick={c.close} className="grid size-7 place-items-center rounded-md text-ink-400 hover:bg-white/5 hover:text-white disabled:opacity-40">
            <X size={16} />
          </button>
        </header>
        {tabs.length > 1 && (
          <div role="tablist" aria-label={t('Page tools')} onKeyDown={onTabKey} className="flex shrink-0 gap-1 border-b border-white/10 px-4 pt-2">
            {tabs.map((tab) => {
              const on = tab === s.tab
              const Icon = tab === 'languages' ? Languages : SearchCheck
              return (
                <button
                  key={tab}
                  id={'wb-pt-tab-' + tab}
                  type="button"
                  role="tab"
                  aria-selected={on}
                  aria-controls="wb-pt-panel"
                  tabIndex={on ? 0 : -1}
                  disabled={busy}
                  onClick={() => c.select(tab)}
                  className={'-mb-px inline-flex h-9 items-center gap-1.5 border-b-2 px-3 text-[13px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-accent/70 disabled:opacity-50 ' + (on ? 'border-accent text-white' : 'border-transparent text-ink-400 hover:text-ink-100')}
                >
                  <Icon size={14} />
                  {tabLabel(tab)}
                </button>
              )
            })}
          </div>
        )}
        <div id="wb-pt-panel" role={tabs.length > 1 ? 'tabpanel' : undefined} aria-labelledby={tabs.length > 1 ? 'wb-pt-tab-' + s.tab : undefined} className="min-h-0 flex-1 overflow-y-auto">
          {s.tab === 'languages' ? <LanguagesPanel c={c} /> : <SeoPanel c={c} />}
        </div>
        <footer className="flex shrink-0 items-center gap-3 border-t border-white/10 px-5 py-3">
          <p role={statusError ? 'alert' : 'status'} className={'min-w-0 flex-1 truncate text-[12.5px] ' + (statusError ? 'text-red-300' : 'text-emerald-300')}>
            {status}
          </p>
          <button type="button" className={SECONDARY} onClick={c.close} disabled={busy}>
            {t('Close')}
          </button>
          {onSeo && (
            <button type="button" className={PRIMARY} onClick={() => void c.saveSeo()} disabled={!dirty || s.seo.saving}>
              {s.seo.saving ? <LoaderCircle size={15} className="animate-spin" /> : s.seo.saved && !dirty ? <Check size={15} /> : null}
              {t('Save changes')}
            </button>
          )}
        </footer>
      </div>
    </div>
  )
}
