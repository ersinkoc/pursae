/**
 * Id field of the conditions dialog and the preview picker: a search box over a core wp/v2
 * collection (posts, pages, categories, tags, users) with a result list, or a plain number field
 * when the collection has no search route (custom post types).
 */
import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { t } from './i18n'
import { searchItems, type FoundItem, type RestClient, type SearchCollection } from './rest'

export interface IdPickerProps {
  rest: RestClient
  /** null = the id is typed in. */
  collection: SearchCollection | null
  id: string
  label?: string
  onChange(id: string, label?: string): void
  /** Accessible name of the field. */
  name: string
  invalid?: boolean
  searchDelay?: number
}

export const FIELD = 'h-8 rounded-lg border border-white/[0.08] bg-ink-800 px-2.5 text-[12.5px] text-ink-100 outline-none placeholder:text-ink-500 focus:border-accent'

export function IdPicker({ rest, collection, id, label, onChange, name, invalid, searchDelay = 250 }: IdPickerProps) {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<FoundItem[]>([])
  const [state, setState] = useState<'idle' | 'loading' | 'error'>('idle')
  const [active, setActive] = useState(0)
  const listId = useId()
  const seq = useRef(0)

  useEffect(() => {
    if (!collection || !open) return
    const n = ++seq.current
    const ctl = new AbortController()
    setState('loading') // not "Nothing found" while the debounce runs
    const timer = setTimeout(() => {
      searchItems(rest, collection, query, ctl.signal)
        .then((r) => {
          if (n !== seq.current) return
          setItems(r)
          setActive(0)
          setState('idle')
        })
        .catch((e: unknown) => {
          if (n !== seq.current || (e instanceof DOMException && e.name === 'AbortError')) return
          setItems([])
          setState('error')
        })
    }, searchDelay)
    return () => {
      clearTimeout(timer)
      ctl.abort()
    }
  }, [rest, collection, query, open, searchDelay])

  if (!collection) {
    return (
      <input
        type="text"
        inputMode="numeric"
        value={id}
        aria-label={name}
        aria-invalid={invalid || undefined}
        placeholder={t('ID')}
        onChange={(e) => onChange(e.target.value.replace(/[^0-9]/g, '').slice(0, 19))}
        className={FIELD + ' w-24' + (invalid ? ' border-red-400/70' : '')}
      />
    )
  }

  const choose = (it: FoundItem) => {
    onChange(String(it.id), it.label)
    setOpen(false)
    setQuery('')
  }

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setOpen(true)
      setActive((a) => Math.min(a + 1, Math.max(0, items.length - 1)))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => Math.max(0, a - 1))
    } else if (e.key === 'Enter' && open && items[active]) {
      e.preventDefault()
      choose(items[active])
    } else if (e.key === 'Escape' && open) {
      e.stopPropagation()
      setOpen(false)
    }
  }

  const shown = open ? query : id ? label || `#${id}` : ''
  return (
    <div className="relative min-w-[140px] flex-1">
      <input
        type="search"
        role="combobox"
        aria-label={name}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-invalid={invalid || undefined}
        value={shown}
        placeholder={t('Search…')}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onKeyDown={onKey}
        className={FIELD + ' w-full' + (invalid ? ' border-red-400/70' : '')}
      />
      {open && (
        <ul id={listId} role="listbox" aria-label={name} className="scroll-thin absolute right-0 top-[calc(100%+4px)] z-10 max-h-56 w-full min-w-[240px] overflow-y-auto rounded-lg border border-white/10 bg-ink-850 py-1 shadow-[0_20px_50px_-12px_rgba(0,0,0,.7)]">
          {state === 'loading' && items.length === 0 && <li className="px-3 py-2 text-[12.5px] text-ink-400">{t('Searching…')}</li>}
          {state === 'error' && <li className="px-3 py-2 text-[12.5px] text-red-300">{t('The search failed.')}</li>}
          {state === 'idle' && items.length === 0 && <li className="px-3 py-2 text-[12.5px] text-ink-400">{t('Nothing found.')}</li>}
          {items.map((it, i) => (
            <li
              key={it.id}
              role="option"
              aria-selected={String(it.id) === id}
              onMouseDown={(e) => {
                e.preventDefault()
                choose(it)
              }}
              onMouseEnter={() => setActive(i)}
              className={'flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[12.5px] ' + (i === active ? 'bg-white/[0.06] text-white' : 'text-ink-200')}
            >
              <span className="min-w-0 flex-1 truncate">{it.label}</span>
              <span className="shrink-0 font-mono text-[11px] text-ink-500">#{it.id}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
