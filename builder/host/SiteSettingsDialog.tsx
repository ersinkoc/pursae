/**
 * The site settings dialog (site-settings.ts): site identity (title, tagline, site icon, logo), the
 * homepage (latest posts or a static page) and additional CSS, the settings the Customizer manages
 * while the builder is off. Also the "Site settings" row of the builder menu (BuilderHost.menuExtra).
 */
import { useEffect, useRef, useSyncExternalStore } from 'react'
import { Check, ImagePlus, LoaderCircle, Settings2, X } from 'lucide-react'
import { t } from './i18n'
import type { SiteImage, SiteSettingsController } from './site-settings'

const BTN = 'inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-4 text-[13px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-accent/70 disabled:opacity-50'
const PRIMARY = BTN + ' bg-accent text-white hover:brightness-110'
const SECONDARY = BTN + ' border border-white/15 text-ink-100 hover:bg-white/5'
const FIELD = 'h-9 w-full rounded-lg border border-white/12 bg-ink-900 px-3 text-[13px] text-ink-100 outline-none placeholder:text-ink-500 focus:border-accent focus-visible:ring-2 focus-visible:ring-accent/40'
const LABEL = 'mb-1.5 block text-[12px] font-semibold text-ink-200'
const HINT = 'mt-1 text-[11.5px] leading-snug text-ink-500'
const SECTION = 'space-y-4 border-b border-white/10 px-5 py-5 last:border-b-0'

/** The row of the builder menu that opens the dialog. */
export function SiteSettingsMenuRow({ controller, close }: { controller: SiteSettingsController; close: () => void }) {
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
      <Settings2 size={14} className="text-ink-300" />
      {t('Site settings')}
    </button>
  )
}

function ImageField({ label, hint, image, onPick, onClear, canPick, wide }: { label: string; hint: string; image: SiteImage | null; onPick: () => void; onClear: () => void; canPick: boolean; wide?: boolean }) {
  return (
    <div>
      <span className={LABEL}>{label}</span>
      <div className="flex items-center gap-3">
        <div className={'grid shrink-0 place-items-center overflow-hidden rounded-lg border border-white/10 bg-ink-900 ' + (wide ? 'h-14 w-32' : 'size-14')}>
          {image ? <img src={image.url} alt="" className="max-h-full max-w-full object-contain" /> : <ImagePlus size={18} className="text-ink-500" />}
        </div>
        <div className="flex flex-wrap gap-2">
          {canPick && (
            <button type="button" className={SECONDARY + ' h-8 px-3 text-[12.5px]'} onClick={onPick}>
              {image ? t('Change') : t('Select')}
            </button>
          )}
          {image && (
            <button type="button" className={SECONDARY + ' h-8 px-3 text-[12.5px]'} onClick={onClear}>
              {t('Remove')}
            </button>
          )}
        </div>
      </div>
      <p className={HINT}>{hint}</p>
    </div>
  )
}

function Form({ c }: { c: SiteSettingsController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const { data, draft } = s
  if (s.loading && !data) return <p className="p-8 text-center text-[13px] text-ink-400">{t('Loading settings…')}</p>
  if (!data || !draft) return <p role="alert" className="p-8 text-center text-[13px] text-ink-300">{s.error}</p>
  const staticFront = draft.show_on_front === 'page'
  return (
    <div>
      <section className={SECTION} aria-labelledby="wb-site-identity">
        <h3 id="wb-site-identity" className="text-[13px] font-semibold uppercase tracking-wide text-ink-400">
          {t('Site identity')}
        </h3>
        <div>
          <label className={LABEL} htmlFor="wb-site-title">
            {t('Site title')}
          </label>
          <input id="wb-site-title" className={FIELD} value={draft.blogname} maxLength={200} onChange={(e) => c.edit({ blogname: e.target.value })} />
        </div>
        <div>
          <label className={LABEL} htmlFor="wb-site-tagline">
            {t('Tagline')}
          </label>
          <input id="wb-site-tagline" className={FIELD} value={draft.blogdescription} maxLength={300} onChange={(e) => c.edit({ blogdescription: e.target.value })} />
          <p className={HINT}>{t('In a few words, explain what this site is about.')}</p>
        </div>
        {data.supports_logo && (
          <ImageField
            wide
            label={t('Logo')}
            hint={t('Shown by the header of your theme.')}
            image={draft.custom_logo}
            canPick={c.canPick}
            onPick={() => void c.pick('custom_logo')}
            onClear={() => c.edit({ custom_logo: null })}
          />
        )}
        <ImageField
          label={t('Site icon')}
          hint={t('The icon in browser tabs and bookmarks. It should be square, at least 512 × 512 pixels.')}
          image={draft.site_icon}
          canPick={c.canPick}
          onPick={() => void c.pick('site_icon')}
          onClear={() => c.edit({ site_icon: null })}
        />
      </section>

      <section className={SECTION} aria-labelledby="wb-site-home">
        <h3 id="wb-site-home" className="text-[13px] font-semibold uppercase tracking-wide text-ink-400">
          {t('Homepage')}
        </h3>
        <fieldset className="space-y-2">
          <legend className="sr-only">{t('Your homepage displays')}</legend>
          <label className="flex items-center gap-2 text-[13px] text-ink-100">
            <input type="radio" name="wb-show-on-front" checked={!staticFront} onChange={() => c.edit({ show_on_front: 'posts' })} />
            {t('Your latest posts')}
          </label>
          <label className="flex items-center gap-2 text-[13px] text-ink-100">
            <input type="radio" name="wb-show-on-front" checked={staticFront} onChange={() => c.edit({ show_on_front: 'page' })} />
            {t('A static page')}
          </label>
        </fieldset>
        {staticFront && (
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className={LABEL} htmlFor="wb-page-front">
                {t('Homepage')}
              </label>
              <select id="wb-page-front" className={FIELD} value={draft.page_on_front} onChange={(e) => c.edit({ page_on_front: Number(e.target.value) })}>
                <option value={0}>{t('— Select —')}</option>
                {data.pages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.title || t('(no title)')}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className={LABEL} htmlFor="wb-page-posts">
                {t('Posts page')}
              </label>
              <select id="wb-page-posts" className={FIELD} value={draft.page_for_posts} onChange={(e) => c.edit({ page_for_posts: Number(e.target.value) })}>
                <option value={0}>{t('— Select —')}</option>
                {data.pages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.title || t('(no title)')}
                  </option>
                ))}
              </select>
            </div>
          </div>
        )}
      </section>

      <section className={SECTION} aria-labelledby="wb-site-css">
        <h3 id="wb-site-css" className="text-[13px] font-semibold uppercase tracking-wide text-ink-400">
          {t('Additional CSS')}
        </h3>
        <div>
          <label className="sr-only" htmlFor="wb-site-css-text">
            {t('Additional CSS')}
          </label>
          <textarea
            id="wb-site-css-text"
            spellCheck={false}
            rows={8}
            value={draft.custom_css}
            onChange={(e) => c.edit({ custom_css: e.target.value })}
            className="w-full resize-y rounded-lg border border-white/12 bg-ink-900 p-3 font-mono text-[12.5px] leading-relaxed text-ink-100 outline-none focus:border-accent focus-visible:ring-2 focus-visible:ring-accent/40"
          />
          <p className={HINT}>{t('CSS added here applies to your whole site.')}</p>
        </div>
      </section>
    </div>
  )
}

export function SiteSettingsDialog({ controller: c }: { controller: SiteSettingsController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (s.open) box.current?.focus()
  }, [s.open])
  if (!s.open) return null
  const dirty = c.dirty
  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={() => c.close()}>
      <div
        ref={box}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={t('Site settings')}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === 'Escape') c.close()
        }}
        className="flex max-h-[min(88vh,760px)] w-[min(640px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_30px_80px_-12px_rgba(0,0,0,.8)] outline-none"
      >
        <header className="flex shrink-0 items-center gap-3 border-b border-white/10 px-5 py-3.5">
          <Settings2 size={17} className="text-accent-soft" />
          <h2 className="flex-1 text-[15px] font-semibold">{t('Site settings')}</h2>
          <button type="button" aria-label={t('Close')} disabled={s.saving} onClick={c.close} className="grid size-7 place-items-center rounded-md text-ink-400 hover:bg-white/5 hover:text-white disabled:opacity-40">
            <X size={16} />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <Form c={c} />
        </div>
        <footer className="flex shrink-0 items-center gap-3 border-t border-white/10 px-5 py-3">
          <p role={s.error ? 'alert' : 'status'} className={'min-w-0 flex-1 truncate text-[12.5px] ' + (s.error ? 'text-red-300' : 'text-emerald-300')}>
            {s.data && s.error ? s.error : s.saved ? t('Saved. The changes are live.') : ''}
          </p>
          <button type="button" className={SECONDARY} onClick={c.close} disabled={s.saving}>
            {t('Close')}
          </button>
          <button type="button" className={PRIMARY} onClick={() => void c.save()} disabled={!dirty || s.saving}>
            {s.saving ? <LoaderCircle size={15} className="animate-spin" /> : s.saved && !dirty ? <Check size={15} /> : null}
            {t('Save changes')}
          </button>
        </footer>
      </div>
    </div>
  )
}
