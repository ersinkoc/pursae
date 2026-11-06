/**
 * Media library dialog of the CMS host (BuilderHost.media.pick): images from wp/v2/media with
 * search, pagination and upload. Plain React + the editor's Tailwind theme (wp.media is not used:
 * the editor's Tailwind preflight breaks its styles).
 */
import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type KeyboardEvent } from 'react'
import type { MediaAsset } from '@/builder/host'
import type { MediaItem } from './contract'
import { listMedia, type RestClient } from './rest'
import { thumbUrl, toAsset } from './cms-host'

export interface MediaPickerProps {
  rest: RestClient
  /** null hides the upload button (no upload_files). */
  upload: ((file: File) => Promise<MediaAsset>) | null
  /** Accepted MIME types for the file input (Boot.media.types filtered to images). */
  accept: string
  onClose(asset: MediaAsset | null): void
  /** Search debounce (ms). */
  searchDelay?: number
}

type Load = { state: 'idle' } | { state: 'loading' } | { state: 'error'; message: string }

export function MediaPicker({ rest, upload, accept, onClose, searchDelay = 300 }: MediaPickerProps) {
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const [items, setItems] = useState<MediaItem[]>([])
  const [page, setPage] = useState(1)
  const [totalPages, setTotalPages] = useState(1)
  const [total, setTotal] = useState(0)
  const [load, setLoad] = useState<Load>({ state: 'loading' })
  const [selected, setSelected] = useState<MediaItem | null>(null)
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const seq = useRef(0)

  // Debounced search.
  useEffect(() => {
    const t = setTimeout(() => setSearch(query.trim()), searchDelay)
    return () => clearTimeout(t)
  }, [query, searchDelay])

  const fetchPage = useCallback(
    (p: number, append: boolean) => {
      const id = ++seq.current
      const ctl = new AbortController()
      setLoad({ state: 'loading' })
      listMedia(rest, { search, page: p, signal: ctl.signal })
        .then((r) => {
          if (id !== seq.current) return
          setItems((prev) => (append ? [...prev, ...r.items.filter((m) => !prev.some((x) => x.id === m.id))] : r.items))
          setPage(r.page)
          setTotalPages(r.totalPages)
          setTotal(r.total)
          setLoad({ state: 'idle' })
        })
        .catch((e: unknown) => {
          if (id !== seq.current || (e instanceof DOMException && e.name === 'AbortError')) return
          setLoad({ state: 'error', message: e instanceof Error && e.message ? e.message : 'The media library could not be loaded.' })
        })
      return () => ctl.abort()
    },
    [rest, search],
  )

  useEffect(() => fetchPage(1, false), [fetchPage])
  useEffect(() => searchRef.current?.focus(), [])

  const onFile = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f || !upload) return
    setUploading(true)
    setUploadError('')
    upload(f)
      .then((asset) => {
        const item: MediaItem = { id: asset.id ?? -Date.now(), source_url: asset.url, alt_text: asset.alt ?? '', mime_type: f.type || 'image/*', media_details: { width: asset.width, height: asset.height } }
        setItems((prev) => [item, ...prev.filter((x) => x.id !== item.id)])
        setTotal((n) => n + 1)
        setSelected(item)
      })
      .catch((err: unknown) => setUploadError(err instanceof Error && err.message ? err.message : `${f.name} could not be uploaded.`))
      .finally(() => setUploading(false))
  }

  const choose = (m: MediaItem | null) => onClose(m ? toAsset(m) : null)

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onClose(null)
    }
  }

  const empty = load.state === 'idle' && items.length === 0
  const status = useMemo(() => (load.state === 'loading' ? 'Loading…' : load.state === 'error' ? load.message : `${total} image${total === 1 ? '' : 's'}`), [load, total])

  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={() => onClose(null)}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="wb-media-title"
        onKeyDown={onKey}
        onMouseDown={(e) => e.stopPropagation()}
        className="flex h-[min(640px,85vh)] w-[min(880px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_40px_120px_-20px_rgba(0,0,0,.8)]"
      >
        <div className="flex items-center gap-3 border-b border-white/[0.07] px-5 py-3.5">
          <h2 id="wb-media-title" className="text-[15px] font-semibold">
            Media Library
          </h2>
          <input
            ref={searchRef}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search images"
            aria-label="Search images"
            className="ml-auto h-9 w-56 rounded-[10px] border border-white/[0.08] bg-ink-800 px-3 text-[13.5px] outline-none placeholder:text-ink-500 focus:border-accent"
          />
          {upload && (
            <label className={'flex h-9 cursor-pointer items-center rounded-[10px] border border-white/15 px-3 text-[13px] font-medium text-ink-200 transition hover:border-accent/60 hover:text-white' + (uploading ? ' pointer-events-none opacity-60' : '')}>
              {uploading ? 'Uploading…' : 'Upload'}
              <input type="file" accept={accept} className="sr-only" disabled={uploading} onChange={onFile} data-testid="wb-media-upload" />
            </label>
          )}
          <button type="button" onClick={() => onClose(null)} aria-label="Close" className="grid size-9 place-items-center rounded-[10px] text-ink-300 hover:bg-white/5 hover:text-white">
            ×
          </button>
        </div>

        {uploadError && (
          <p role="alert" className="border-b border-white/[0.07] bg-red-500/10 px-5 py-2 text-[13px] text-red-300">
            {uploadError}
          </p>
        )}

        <div className="scroll-thin min-h-0 flex-1 overflow-y-auto p-4">
          {empty ? (
            <p className="py-16 text-center text-[13.5px] text-ink-400">{search ? `No images match “${search}”.` : 'No images yet. Upload one to get started.'}</p>
          ) : (
            <ul className="grid grid-cols-[repeat(auto-fill,minmax(132px,1fr))] gap-3" role="listbox" aria-label="Images">
              {items.map((m) => {
                const on = selected?.id === m.id
                return (
                  <li key={m.id} role="option" aria-selected={on}>
                    <button
                      type="button"
                      onClick={() => setSelected(m)}
                      onDoubleClick={() => choose(m)}
                      title={m.alt_text || m.source_url.split('/').pop()}
                      className={'block aspect-square w-full overflow-hidden rounded-[10px] bg-ink-800 ring-2 transition ' + (on ? 'ring-accent' : 'ring-transparent hover:ring-white/20')}
                    >
                      <img src={thumbUrl(m)} alt={m.alt_text} loading="lazy" className="size-full object-cover" />
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
          {page < totalPages && load.state !== 'loading' && (
            <div className="mt-4 flex justify-center">
              <button type="button" onClick={() => fetchPage(page + 1, true)} className="h-9 rounded-[10px] border border-white/15 px-4 text-[13px] font-medium text-ink-200 hover:border-accent/60 hover:text-white">
                Load more
              </button>
            </div>
          )}
          {load.state === 'error' && (
            <div className="mt-4 flex justify-center">
              <button type="button" onClick={() => fetchPage(page, page > 1)} className="h-9 rounded-[10px] border border-white/15 px-4 text-[13px] font-medium text-ink-200 hover:border-accent/60 hover:text-white">
                Retry
              </button>
            </div>
          )}
        </div>

        <div className="flex items-center gap-3 border-t border-white/[0.07] px-5 py-3">
          <span className="text-[12.5px] text-ink-400" aria-live="polite">
            {status}
          </span>
          <button type="button" onClick={() => onClose(null)} className="ml-auto h-9 rounded-[10px] px-4 text-[13px] font-medium text-ink-300 hover:text-white">
            Cancel
          </button>
          <button type="button" disabled={!selected} onClick={() => choose(selected)} className="h-9 rounded-[10px] bg-accent px-4 text-[13px] font-semibold text-white disabled:opacity-50">
            Use image
          </button>
        </div>
      </div>
    </div>
  )
}
