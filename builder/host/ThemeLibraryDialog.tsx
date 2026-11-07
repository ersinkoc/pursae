/**
 * The theme library dialog (theme-library.ts): a gallery of the ready-made themes, a confirmation
 * that says what the import will do (and asks replace / keep as drafts when the site already has
 * templates of the same types), the progress, and the result.
 */
import { useEffect, useRef, useSyncExternalStore } from 'react'
import { ArrowLeft, Check, Layers, LoaderCircle, X } from 'lucide-react'
import { t } from './i18n'
import type { ThemeCard, ThemeLibraryController } from './theme-library'

const BTN = 'inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-4 text-[13px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-accent/70 disabled:opacity-50'
const PRIMARY = BTN + ' bg-accent text-white hover:brightness-110'
const SECONDARY = BTN + ' border border-white/15 text-ink-100 hover:bg-white/5'

function counts(card: ThemeCard): string {
  const parts = [t('{n} templates', { n: card.templates.length })]
  if (card.pages.length) parts.push(t('{n} pages', { n: card.pages.length }))
  if (card.menus) parts.push(t('{n} menus', { n: card.menus }))
  return parts.join(' · ')
}

function Shot({ card, className }: { card: ThemeCard; className: string }) {
  return card.screenshot ? (
    <img src={card.screenshot} alt="" loading="lazy" className={className + ' object-cover object-top'} />
  ) : (
    <div className={className + ' grid place-items-center bg-ink-800 text-ink-500'}>
      <Layers size={28} />
    </div>
  )
}

function Gallery({ c }: { c: ThemeLibraryController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  if (s.loading && !s.data) return <p className="p-8 text-center text-[13px] text-ink-400">{t('Loading themes…')}</p>
  if (s.error && !s.data?.themes.length) return <p role="alert" className="p-8 text-center text-[13px] text-ink-300">{s.error}</p>
  const themes = s.data?.themes ?? []
  if (!themes.length) return <p className="p-8 text-center text-[13px] text-ink-400">{t('The library has no themes for Pursae Web Builder yet.')}</p>
  return (
    <ul className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3">
      {themes.map((card) => (
        <li key={card.slug} className="flex flex-col overflow-hidden rounded-xl border border-white/10 bg-ink-900">
          <Shot card={card} className="aspect-[4/3] w-full" />
          <div className="flex flex-1 flex-col gap-1.5 p-3.5">
            <div className="flex items-center gap-2">
              <h3 className="text-[14px] font-semibold text-ink-100">{card.name}</h3>
              {card.imported && <span className="rounded-full bg-emerald-400/15 px-2 py-0.5 text-[10.5px] font-semibold text-emerald-300">{t('Imported')}</span>}
            </div>
            <p className="line-clamp-3 text-[12px] leading-snug text-ink-400" title={card.description}>
              {card.style || card.description}
            </p>
            <p className="text-[11.5px] text-ink-500">{counts(card)}</p>
            {card.error ? (
              <p className="text-[11.5px] text-red-300">{card.error}</p>
            ) : (
              <button type="button" className={PRIMARY + ' mt-auto'} onClick={() => c.select(card)} aria-label={t('Import {name}', { name: card.name })}>
                {t('Import')}
              </button>
            )}
          </div>
        </li>
      ))}
    </ul>
  )
}

function Confirm({ c, card }: { c: ThemeLibraryController; card: ThemeCard }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const conflicts = c.conflicts(card)
  return (
    <div className="grid gap-5 p-5 md:grid-cols-[260px_1fr]">
      <Shot card={card} className="aspect-[4/3] w-full rounded-xl border border-white/10" />
      <div className="min-w-0 space-y-4 text-[13px] text-ink-200">
        <div>
          <h3 className="text-[16px] font-semibold text-ink-100">{card.name}</h3>
          <p className="mt-1 leading-snug text-ink-400">{card.description}</p>
        </div>
        <div>
          <p className="mb-1 text-[11.5px] font-semibold uppercase tracking-wide text-ink-500">{t('What you get')}</p>
          <ul className="list-disc space-y-0.5 pl-5">
            <li>{t('Templates: {list}', { list: card.templates.map((p) => p.label).join(', ') })}</li>
            {card.pages.length > 0 && (
              <li>
                <label className="inline-flex items-center gap-2">
                  <input type="checkbox" checked={s.pages} onChange={(e) => c.setPages(e.target.checked)} /> {t('Demo pages: {list}', { list: card.pages.map((p) => p.label).join(', ') })}
                </label>
              </li>
            )}
            {card.menus > 0 && <li>{t('The theme’s menus, in the Primary and Footer menu locations')}</li>}
            {card.images > 0 && <li>{t('{n} demo images, added to your Media Library', { n: card.images })}</li>}
          </ul>
        </div>
        {conflicts.length > 0 ? (
          <fieldset className="space-y-2 rounded-lg border border-amber-400/40 bg-amber-400/5 p-3">
            <legend className="px-1 text-[12.5px] font-semibold text-amber-200">{t('Your site already has templates')}</legend>
            <p className="text-[12.5px] text-ink-300">
              {t('Published today: {list}.', { list: conflicts.map((x) => (x.count > 1 ? `${x.label} (${x.count})` : x.label)).join(', ') })}
            </p>
            <label className="flex items-start gap-2">
              <input type="radio" name="wb-theme-mode" className="mt-1" checked={s.mode === 'replace'} onChange={() => c.setMode('replace')} />
              <span>
                <b>{t('Replace them')}</b> — {t('the theme’s templates go live; yours are disabled (not deleted) and stay in the Theme Builder.')}
              </span>
            </label>
            <label className="flex items-start gap-2">
              <input type="radio" name="wb-theme-mode" className="mt-1" checked={s.mode === 'keep'} onChange={() => c.setMode('keep')} />
              <span>
                <b>{t('Keep both')}</b> — {t('the theme’s templates are saved as drafts; nothing changes on your site until you publish them.')}
              </span>
            </label>
          </fieldset>
        ) : (
          <p className="text-ink-400">{t('Your site has no templates yet, so nothing is replaced. Your posts, pages and settings stay as they are.')}</p>
        )}
        {card.store && <p className="text-[12px] text-ink-500">{t('Shop pages (products, cart, checkout) keep using Pursae Commerce; the theme only designs the header, footer and the pages around them.')}</p>}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" className={SECONDARY} onClick={c.back}>
            <ArrowLeft size={14} /> {t('Back')}
          </button>
          <button type="button" className={PRIMARY} onClick={c.run}>
            {s.mode === 'keep' && conflicts.length > 0 ? t('Import as drafts') : t('Import theme')}
          </button>
        </div>
      </div>
    </div>
  )
}

function Working({ c }: { c: ThemeLibraryController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const pct = s.total ? Math.round((s.done / s.total) * 100) : 0
  return (
    <div className="space-y-4 p-10 text-center" role="status" aria-live="polite">
      <LoaderCircle size={26} className="mx-auto animate-spin text-accent-soft" />
      <p className="text-[13.5px] text-ink-100">{s.step}</p>
      <div className="mx-auto h-1.5 w-72 max-w-full overflow-hidden rounded-full bg-white/10">
        <div className="h-full rounded-full bg-accent transition-[width]" style={{ width: pct + '%' }} />
      </div>
      <p className="text-[12px] text-ink-500">{t('Keep this window open until it is done.')}</p>
    </div>
  )
}

function Done({ c }: { c: ThemeLibraryController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const r = s.result
  if (!r || !s.selected) return null
  return (
    <div className="space-y-4 p-8 text-[13px] text-ink-200">
      <div className="flex items-center gap-2 text-[15px] font-semibold text-emerald-300">
        <Check size={18} /> {r.mode === 'keep' ? t('Saved as drafts') : t('{name} is live', { name: s.selected.name })}
      </div>
      <ul className="list-disc space-y-0.5 pl-5">
        {r.published > 0 && <li>{t('{n} templates published', { n: r.published })}</li>}
        {r.kept > 0 && <li>{t('{n} templates saved as drafts in the Theme Builder', { n: r.kept })}</li>}
        {r.pages > 0 && <li>{t('{n} demo pages published', { n: r.pages })}</li>}
        {r.menus > 0 && <li>{t('{n} menus created and assigned', { n: r.menus })}</li>}
        {r.media > 0 && <li>{t('{n} images added to the Media Library', { n: r.media })}</li>}
        {r.disabled.length > 0 && <li>{t('Disabled (you can enable them again): {list}', { list: r.disabled.join(', ') })}</li>}
      </ul>
      <div className="flex flex-wrap justify-end gap-2">
        <button type="button" className={SECONDARY} onClick={c.back}>
          {t('Back to the library')}
        </button>
        <a className={SECONDARY} href={c.siteUrl()} target="_blank" rel="noopener noreferrer">
          {t('View the site')}
        </a>
        <button type="button" className={PRIMARY} onClick={c.close}>
          {t('Close')}
        </button>
      </div>
    </div>
  )
}

function Failed({ c }: { c: ThemeLibraryController }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  return (
    <div className="space-y-4 p-8 text-[13px] text-ink-200">
      <p role="alert" className="font-semibold text-red-300">
        {t('The import stopped.')}
      </p>
      <p>{s.message}</p>
      <p className="text-ink-400">{t('Nothing of the theme is live yet. Importing again picks up where it stopped; your own content was not changed.')}</p>
      <div className="flex justify-end gap-2">
        <button type="button" className={SECONDARY} onClick={c.back}>
          {t('Back to the library')}
        </button>
        <button type="button" className={PRIMARY} onClick={c.run}>
          {t('Try again')}
        </button>
      </div>
    </div>
  )
}

export function ThemeLibraryDialog({ controller: c, page = false }: { controller: ThemeLibraryController; page?: boolean }) {
  const s = useSyncExternalStore(c.subscribe, c.getState, c.getState)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (s.open && !page) box.current?.focus()
  }, [s.open, s.phase, page])
  if (!s.open) return null
  const title = s.selected && s.phase !== 'list' ? s.selected.name : t('Theme library')
  const header = (
    <header className="flex shrink-0 items-center gap-3 border-b border-white/10 px-5 py-3.5">
      <Layers size={17} className="text-accent-soft" />
      <h2 className="flex-1 text-[15px] font-semibold">{title}</h2>
      {s.phase === 'list' && <p className="hidden text-[12px] text-ink-500 sm:block">{t('Ready-made full-site themes: header, footer and every page template')}</p>}
      {!page && (
        <button type="button" aria-label={t('Close')} disabled={c.busy} onClick={c.close} className="grid size-7 place-items-center rounded-md text-ink-400 hover:bg-white/5 hover:text-white disabled:opacity-40">
          <X size={16} />
        </button>
      )}
    </header>
  )
  const body = (
    <div className="min-h-0 flex-1 overflow-y-auto">
      {s.phase === 'list' && <Gallery c={c} />}
      {s.phase === 'confirm' && s.selected && <Confirm c={c} card={s.selected} />}
      {s.phase === 'working' && <Working c={c} />}
      {s.phase === 'done' && <Done c={c} />}
      {s.phase === 'error' && <Failed c={c} />}
    </div>
  )
  // On the admin screen (library frame) the gallery fills the document instead of floating over it.
  if (page) {
    return (
      <div role="region" aria-label={title} className="flex h-screen flex-col bg-ink-850 text-ink-100">
        {header}
        {body}
      </div>
    )
  }
  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={() => c.close()}>
      <div
        ref={box}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === 'Escape') c.close()
        }}
        className="flex max-h-[min(88vh,820px)] w-[min(960px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_30px_80px_-12px_rgba(0,0,0,.8)] outline-none"
      >
        {header}
        {body}
      </div>
    </div>
  )
}
