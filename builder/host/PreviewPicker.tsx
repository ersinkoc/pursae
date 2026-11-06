/**
 * "Preview as" dialog of template mode (contract §11.6): which entry, archive or view the
 * template's dynamic widgets are drawn with. Applying sends PUT templates/{id}/preview; its
 * DynContext replaces the editor's sample data (DynStore), so the canvas redraws with it.
 */
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import type { SiteBoot, TemplatePreview, TemplateType } from './contract'
import { FIELD, IdPicker } from './IdPicker'
import { t } from './i18n'
import { kindIdSource, kindOptions, parsePreview, previewKinds, previewObject, type PreviewChoice, type PreviewKind } from './preview'
import type { RestClient } from './rest'

export interface PreviewPickerProps {
  rest: RestClient
  site: Pick<SiteBoot, 'postTypes'>
  type: TemplateType
  preview: TemplatePreview
  apply(object: string): Promise<unknown>
  onClose(): void
}

export function PreviewPicker({ rest, site, type, preview, apply, onClose }: PreviewPickerProps) {
  const kinds = useMemo(() => previewKinds(type, site), [type, site])
  const [choice, setChoice] = useState<PreviewChoice>(() => parsePreview(preview.object, kinds))
  const [idLabel, setIdLabel] = useState<string | undefined>(() => (choice.id ? preview.label : undefined))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const firstRef = useRef<HTMLSelectElement>(null)

  useEffect(() => firstRef.current?.focus(), [])

  const object = previewObject(choice)
  const source = kindIdSource(choice.kind)
  const archives = site.postTypes.filter((p) => !!p.archive)

  const setKind = (kind: PreviewKind) => {
    setChoice({ kind, id: '', text: kind === 'type' ? (archives[0]?.slug ?? '') : kind === 'search' ? choice.text : '' })
    setIdLabel(undefined)
    setError('')
  }

  const submit = async () => {
    if (!object) return
    if (object === preview.object) {
      onClose()
      return
    }
    setBusy(true)
    setError('')
    try {
      await apply(object)
      onClose()
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : t('The preview could not be changed.'))
    } finally {
      setBusy(false)
    }
  }

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && !busy) {
      e.stopPropagation()
      onClose()
    }
  }

  return (
    <div className="fixed inset-0 z-[90] flex items-start justify-center bg-black/40 p-6 pt-[12vh]" onMouseDown={() => !busy && onClose()}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="wb-preview-title"
        onKeyDown={onKey}
        onMouseDown={(e) => e.stopPropagation()}
        className="w-[min(460px,100%)] rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_40px_120px_-20px_rgba(0,0,0,.8)]"
      >
        <div className="flex items-start gap-3 border-b border-white/[0.07] px-5 py-3.5">
          <div className="min-w-0 flex-1">
            <h2 id="wb-preview-title" className="text-[15px] font-semibold">
              {t('Preview as')}
            </h2>
            <p className="mt-1 text-[12.5px] leading-snug text-ink-400">{t('The content the dynamic widgets show while you edit. It does not change where the template is used.')}</p>
          </div>
          <button type="button" onClick={onClose} disabled={busy} aria-label={t('Close')} className="grid size-8 shrink-0 place-items-center rounded-lg text-ink-300 hover:bg-white/5 hover:text-white">
            ×
          </button>
        </div>
        <form
          className="flex flex-col gap-3 px-5 py-4"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <p className="text-[12.5px] text-ink-400">
            {t('Now: {label}', { label: preview.label || preview.object })}
            {preview.url && (
              <>
                {' · '}
                <a href={preview.url} target="_blank" rel="noopener noreferrer" className="text-ink-200 underline decoration-white/30 underline-offset-2 hover:text-white">
                  {t('Open on the site')}
                </a>
              </>
            )}
          </p>
          <label className="flex flex-col gap-1 text-[12.5px] font-medium text-ink-200">
            {t('Show')}
            <select ref={firstRef} value={choice.kind} onChange={(e) => setKind(e.target.value as PreviewKind)} className={FIELD}>
              {kindOptions(kinds).map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
          {source !== undefined && (
            <div className="flex flex-col gap-1 text-[12.5px] font-medium text-ink-200">
              <span>{source === null ? t('Entry ID') : t('Item')}</span>
              <IdPicker
                rest={rest}
                collection={source}
                id={choice.id}
                label={idLabel}
                name={source === null ? t('Entry ID') : t('Item')}
                onChange={(id, label) => {
                  setChoice((c) => ({ ...c, id }))
                  setIdLabel(label)
                }}
              />
            </div>
          )}
          {choice.kind === 'type' && (
            <label className="flex flex-col gap-1 text-[12.5px] font-medium text-ink-200">
              {t('Post type')}
              <select value={choice.text} onChange={(e) => setChoice((c) => ({ ...c, text: e.target.value }))} className={FIELD}>
                {archives.map((p) => (
                  <option key={p.slug} value={p.slug}>
                    {p.label || p.slug}
                  </option>
                ))}
              </select>
            </label>
          )}
          {choice.kind === 'search' && (
            <label className="flex flex-col gap-1 text-[12.5px] font-medium text-ink-200">
              {t('Search for')}
              <input type="search" maxLength={100} value={choice.text} onChange={(e) => setChoice((c) => ({ ...c, text: e.target.value }))} className={FIELD} />
            </label>
          )}
          {error && (
            <p role="alert" className="text-[12.5px] text-red-300">
              {error}
            </p>
          )}
          <div className="mt-1 flex justify-end gap-2">
            <button type="button" onClick={onClose} disabled={busy} className="h-9 rounded-[10px] px-4 text-[13px] font-medium text-ink-300 hover:text-white">
              {t('Cancel')}
            </button>
            <button type="submit" disabled={busy || !object} className="h-9 rounded-[10px] bg-accent px-4 text-[13px] font-semibold text-white disabled:opacity-50">
              {busy ? t('Loading…') : t('Apply')}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
