/**
 * The host's controls in the builder's top bar in template mode (BuilderHost.topBarExtra, patch
 * 0009): the template type badge, the "Display Conditions" button and the "Preview as" button.
 * The dialogs themselves are drawn by HostLayer (TemplateDialogs), outside the top bar.
 */
import { useSyncExternalStore } from 'react'
import { Eye, ListFilter } from 'lucide-react'
import type { CmsHost } from './cms-host'
import { ConditionsDialog } from './ConditionsDialog'
import { ruleLabel } from './conditions'
import { t } from './i18n'
import { PreviewPicker } from './PreviewPicker'
import { previewLabel } from './preview'
import { typeLabel, type TemplateController } from './template'

const BTN = 'flex h-8 min-w-0 items-center gap-1.5 rounded-lg px-2.5 text-[12px] text-ink-300 ring-1 ring-white/[0.06] transition-colors hover:bg-white/[0.04] hover:text-ink-100'

/** Short summary of the rules for the button ("Entire Site", "Posts +2", "None"). */
export function conditionsSummary(host: Pick<CmsHost, 'boot'>, rules: string[]): string {
  const include = rules.filter((r) => r.startsWith('include/'))
  if (include.length === 0) return t('Nowhere')
  const first = ruleLabel(host.boot.site, include[0])
  const more = rules.length - 1
  return more > 0 ? t('{first} +{n}', { first, n: more }) : first
}

export function TemplateBar({ host, controller }: { host: CmsHost; controller: TemplateController }) {
  const s = useSyncExternalStore(controller.subscribe, controller.getState, controller.getState)
  const summary = conditionsSummary(host, s.conditions)
  return (
    <>
      <span data-wb-template-type={controller.type} title={t('Template type')} className="hidden shrink-0 rounded-full bg-accent/15 px-2 py-0.5 text-[11px] font-semibold text-accent md:inline">
        {typeLabel(controller.type)}
      </span>
      <button type="button" onClick={() => void controller.openConditions('edit')} aria-label={t('Display Conditions')} title={`${t('Display Conditions')}: ${summary}`} className={BTN + ' shrink-0'}>
        <ListFilter size={13} className="shrink-0" />
        <span className="hidden lg:inline 2xl:hidden">{t('Conditions')}</span>
        <span className="hidden max-w-[150px] truncate 2xl:inline">{summary}</span>
      </button>
      <button type="button" onClick={() => controller.openPreview()} aria-label={t('Preview as')} title={t('Preview as: {label}', { label: previewLabel(s.preview?.label || '') || s.preview?.object || '' })} className={BTN}>
        <Eye size={13} className="shrink-0" />
        <span className="hidden max-w-[160px] truncate xl:inline">{previewLabel(s.preview?.label || '') || t('Preview as')}</span>
      </button>
    </>
  )
}

/** The conditions dialog and the preview picker while the controller has them open. */
export function TemplateDialogs({ host, controller }: { host: CmsHost; controller: TemplateController }) {
  const s = useSyncExternalStore(controller.subscribe, controller.getState, controller.getState)
  return (
    <>
      {s.dialog && (
        <ConditionsDialog
          key={s.dialog.id}
          rest={host.rest}
          site={host.boot.site}
          type={controller.type}
          mode={s.dialog.mode}
          title={host.initial.page?.title}
          conditions={s.conditions}
          priority={s.priority}
          conflicts={s.conflicts}
          save={(c, p) => controller.saveConditions(c, p)}
          onClose={(saved) => controller.closeConditions(saved)}
        />
      )}
      {s.picker && <PreviewPicker rest={host.rest} site={host.boot.site} type={controller.type} preview={s.preview} apply={(o) => controller.setPreview(o)} onClose={() => controller.closePreview()} />}
    </>
  )
}

/** Wires the top bar controls into the host (template mode only); call before the builder renders. */
export function installTemplateUi(host: CmsHost): boolean {
  const controller = host.templates
  if (!controller) return false
  host.topBarExtra = () => <TemplateBar host={host} controller={controller} />
  return true
}
