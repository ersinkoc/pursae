/**
 * "Display Conditions" dialog of template mode (contract §11.3): Include / Exclude rows with the
 * scope cascade of conditions.ts, ids searched in core wp/v2 collections, the priority, and a
 * warning about other published templates that can match the same views.
 *
 * Saving sends PUT templates/{id}/conditions through the TemplateController; opened by the
 * conditions-first publish step its primary button saves and then publishes.
 */
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { CONDITION_LIMITS, type SiteBoot, type TemplateConflict } from './contract'
import {
  emptyRow,
  groupOptions,
  idSource,
  needsId,
  parseRule,
  ruleLabel,
  rowsToRules,
  subOptions,
  targetOptions,
  updateRow,
  clampPriority,
  hasInclude,
  type ConditionGroup,
  type ConditionMode,
  type ConditionRow,
  type Option,
} from './conditions'
import { FIELD, IdPicker } from './IdPicker'
import { t } from './i18n'
import { itemTitle, type RestClient } from './rest'
import { typeLabel, type DialogMode, type SaveResult } from './template'
import type { TemplateType } from './contract'

export interface ConditionsDialogProps {
  rest: RestClient
  site: Pick<SiteBoot, 'postTypes' | 'taxonomies'>
  type: TemplateType
  mode: DialogMode
  conditions: string[]
  priority: number
  conflicts: TemplateConflict[] | null
  save(conditions: string[], priority: number): Promise<SaveResult>
  onClose(saved: boolean): void
  /** Template title for the heading. */
  title?: string
}

const rowsOf = (rules: string[]) => rules.map(parseRule).filter((r): r is ConditionRow => r !== null)

function Select({ value, options, onChange, name }: { value: string; options: Option[]; onChange(v: string): void; name: string }) {
  return (
    <select value={value} aria-label={name} onChange={(e) => onChange(e.target.value)} className={FIELD + ' min-w-0 max-w-[190px] pr-6'}>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
}

export function ConditionsDialog({ rest, site, type, mode, conditions, priority, conflicts, save, onClose, title }: ConditionsDialogProps) {
  const [rows, setRows] = useState<ConditionRow[]>(() => rowsOf(conditions))
  const [prio, setPrio] = useState(String(priority))
  const [errors, setErrors] = useState<SaveResult['errors']>({})
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const first = useRef<HTMLButtonElement>(null)

  useEffect(() => first.current?.focus(), [])

  // Titles of the ids already in the rules (one request each, only where a search route exists).
  useEffect(() => {
    const ctl = new AbortController()
    rows.forEach((row, i) => {
      const col = row.id && !row.idLabel ? idSource(site, row) : null
      if (!col) return
      void itemTitle(rest, col, Number(row.id), ctl.signal).then((label) => {
        if (!label) return
        setRows((rs) => rs.map((r, j) => (j === i && r.id === row.id && !r.idLabel ? { ...r, idLabel: label } : r)))
      })
    })
    return () => ctl.abort()
    // Only for the rules the dialog opened with.
  }, [])

  const groups = useMemo(groupOptions, [])
  const modes: Option[] = useMemo(() => [{ value: 'include', label: t('Include') }, { value: 'exclude', label: t('Exclude') }], [])

  const change = (i: number, patch: Partial<ConditionRow>) => {
    setRows((rs) => rs.map((r, j) => (j === i ? updateRow(r, patch) : r)))
    setErrors((e) => {
      const { [i]: _, ...rest } = e
      void _
      return rest
    })
  }
  const add = () => setRows((rs) => (rs.length >= CONDITION_LIMITS.maxConditions ? rs : [...rs, emptyRow(rs.length ? 'exclude' : 'include')]))
  const remove = (i: number) => {
    setRows((rs) => rs.filter((_, j) => j !== i))
    setErrors({})
  }

  const submit = async () => {
    const { rules, errors: local } = rowsToRules(rows)
    const p = Number(prio)
    const bad: SaveResult['errors'] = { ...local }
    if (!/^-?\d+$/.test(prio.trim()) || clampPriority(p) !== p) bad.priority = t('Use a whole number from {min} to {max}.', { min: CONDITION_LIMITS.priorityMin, max: CONDITION_LIMITS.priorityMax })
    setErrors(bad)
    setMessage('')
    if (Object.keys(bad).length) return
    setBusy(true)
    const r = await save(rules, p)
    setBusy(false)
    if (r.ok) {
      onClose(true)
      return
    }
    // Server indexes count the saved (deduplicated) rules; map them back to rows.
    const mapped: SaveResult['errors'] = {}
    for (const [k, v] of Object.entries(r.errors)) {
      if (k === 'priority') mapped.priority = v
      else {
        const rule = rules[Number(k)]
        const row = rows.findIndex((x) => rowsToRules([x]).rules[0] === rule)
        mapped[row >= 0 ? row : Number(k)] = v
      }
    }
    setErrors(mapped)
    setMessage(r.message)
  }

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && !busy) {
      e.stopPropagation()
      onClose(false)
    }
  }

  const noInclude = !hasInclude(rowsToRules(rows).rules)
  const primary = mode === 'publish' ? t('Save and publish') : t('Save and close')

  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px]" onMouseDown={() => !busy && onClose(false)}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="wb-cond-title"
        aria-describedby="wb-cond-desc"
        onKeyDown={onKey}
        onMouseDown={(e) => e.stopPropagation()}
        className="flex max-h-[85vh] w-[min(760px,100%)] flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink-850 text-ink-100 shadow-[0_40px_120px_-20px_rgba(0,0,0,.8)]"
      >
        <div className="flex items-start gap-3 border-b border-white/[0.07] px-5 py-3.5">
          <div className="min-w-0 flex-1">
            <h2 id="wb-cond-title" className="text-[15px] font-semibold">
              {t('Display Conditions')}
              <span className="ml-2 rounded-full bg-accent/15 px-2 py-px align-middle text-[11px] font-medium text-accent">{typeLabel(type)}</span>
            </h2>
            <p id="wb-cond-desc" className="mt-1 text-[12.5px] leading-snug text-ink-400">
              {title ? t('Choose where “{title}” is used on the site.', { title }) : t('Choose where this template is used on the site.')}{' '}
              {t('Include rules add views, Exclude rules take them out again. Among templates that match a view, the most specific rule wins; on a tie, the higher priority.')}
            </p>
          </div>
          <button type="button" onClick={() => onClose(false)} disabled={busy} aria-label={t('Close')} className="grid size-8 shrink-0 place-items-center rounded-lg text-ink-300 hover:bg-white/5 hover:text-white">
            ×
          </button>
        </div>

        <div className="scroll-thin min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {rows.length === 0 ? (
            <p className="rounded-lg border border-dashed border-white/10 px-4 py-6 text-center text-[13px] text-ink-400">{t('No conditions yet: the template is not shown anywhere.')}</p>
          ) : (
            <ul className="flex flex-col gap-2" aria-label={t('Conditions')}>
              {rows.map((row, i) => {
                const subs = subOptions(site, row.group, row.sub)
                const targets = targetOptions(site, row, row.target)
                const err = errors[i]
                return (
                  <li key={i} className={'rounded-xl border bg-ink-900/60 p-2 ' + (err ? 'border-red-400/50' : 'border-white/[0.07]')}>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Select name={t('Include or exclude')} value={row.mode} options={modes} onChange={(v) => change(i, { mode: v as ConditionMode })} />
                      <Select name={t('Where')} value={row.group} options={groups} onChange={(v) => change(i, { group: v as ConditionGroup })} />
                      {subs.length > 0 && <Select name={t('Which')} value={row.sub} options={subs} onChange={(v) => change(i, { sub: v })} />}
                      {targets.length > 0 && <Select name={t('Narrow down')} value={row.target} options={targets} onChange={(v) => change(i, { target: v })} />}
                      {needsId(row) && targets.length > 0 && (
                        <IdPicker rest={rest} collection={idSource(site, row)} id={row.id} label={row.idLabel} name={t('Item')} invalid={!!err} onChange={(id, label) => change(i, { id, idLabel: label })} />
                      )}
                      <button type="button" onClick={() => remove(i)} aria-label={t('Remove condition')} className="ml-auto grid size-8 shrink-0 place-items-center rounded-lg text-ink-400 hover:bg-white/5 hover:text-red-300">
                        ×
                      </button>
                    </div>
                    {err && (
                      <p role="alert" className="mt-1.5 px-1 text-[12px] text-red-300">
                        {err}
                      </p>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
          <button
            type="button"
            ref={first}
            onClick={add}
            disabled={rows.length >= CONDITION_LIMITS.maxConditions}
            className="mt-3 inline-flex h-8 items-center rounded-lg border border-white/15 px-3 text-[12.5px] font-medium text-ink-200 hover:border-accent/60 hover:text-white disabled:opacity-50"
          >
            + {t('Add condition')}
          </button>

          <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-white/[0.07] pt-4">
            <label htmlFor="wb-cond-priority" className="text-[12.5px] font-medium text-ink-200">
              {t('Priority')}
            </label>
            <input
              id="wb-cond-priority"
              type="number"
              min={CONDITION_LIMITS.priorityMin}
              max={CONDITION_LIMITS.priorityMax}
              step={1}
              value={prio}
              aria-invalid={!!errors.priority || undefined}
              onChange={(e) => setPrio(e.target.value)}
              className={FIELD + ' w-20' + (errors.priority ? ' border-red-400/70' : '')}
            />
            <span className="text-[12px] text-ink-400">{t('Used only when two templates match a view equally well; the higher number wins.')}</span>
            {errors.priority && (
              <p role="alert" className="w-full text-[12px] text-red-300">
                {errors.priority}
              </p>
            )}
          </div>

          {noInclude && rows.length > 0 && <p className="mt-3 text-[12.5px] text-amber-300">{t('Without an Include rule the template is not shown anywhere.')}</p>}

          {conflicts && conflicts.length > 0 && (
            <div role="status" className="mt-4 rounded-xl border border-amber-400/40 bg-amber-400/[0.06] px-3.5 py-3 text-[12.5px]">
              <p className="font-semibold text-amber-200">{t('Other published templates can show on the same views:')}</p>
              <ul className="mt-1.5 flex flex-col gap-1 text-ink-200">
                {conflicts.map((c) => (
                  <li key={c.id} className="flex flex-wrap items-baseline gap-x-2">
                    <a href={c.editUrl} target="_blank" rel="noopener noreferrer" className="font-medium text-white underline decoration-white/30 underline-offset-2 hover:decoration-white">
                      {c.title || t('(no title)')}
                    </a>
                    <span className="text-ink-400">
                      {t('Priority {n}', { n: c.priority })} · {c.conditions.map((r) => ruleLabel(site, r)).join(', ')}
                    </span>
                  </li>
                ))}
              </ul>
              <p className="mt-1.5 text-ink-400">{t('On each view the most specific rule decides which template is used.')}</p>
            </div>
          )}
        </div>

        <div className="flex items-center gap-3 border-t border-white/[0.07] px-5 py-3">
          <span role="alert" className="min-w-0 flex-1 text-[12.5px] text-red-300">
            {message}
          </span>
          <button type="button" onClick={() => onClose(false)} disabled={busy} className="h-9 rounded-[10px] px-4 text-[13px] font-medium text-ink-300 hover:text-white">
            {t('Cancel')}
          </button>
          <button type="button" onClick={() => void submit()} disabled={busy} className="h-9 rounded-[10px] bg-accent px-4 text-[13px] font-semibold text-white disabled:opacity-50">
            {busy ? t('Saving…') : primary}
          </button>
        </div>
      </div>
    </div>
  )
}
