/**
 * The host's template controls in template mode: the "Display Conditions" and "Preview as" rows of
 * the page switcher's dropdown (BuilderHost.switcherExtra, patch 0012). The dialogs themselves are
 * drawn by HostLayer (TemplateDialogs), outside the top bar.
 */
import { useSyncExternalStore } from 'react'
import { Eye, ListFilter } from 'lucide-react'
import type { CmsHost } from './cms-host'
import { ConditionsDialog } from './ConditionsDialog'
import { ruleLabel } from './conditions'
import { t } from './i18n'
import { PreviewPicker } from './PreviewPicker'
import { previewLabel } from './preview'
import type { TemplateController } from './template'

const ROW = 'flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[12.5px] text-ink-100 outline-none transition-colors hover:bg-white/[0.07] focus-visible:bg-white/[0.07]'

/** Short summary of the rules for the button ("Entire Site", "Posts +2", "None"). */
export function conditionsSummary(host: Pick<CmsHost, 'boot'>, rules: string[]): string {
  const include = rules.filter((r) => r.startsWith('include/'))
  if (include.length === 0) return t('Nowhere')
  const first = ruleLabel(host.boot.site, include[0])
  const more = rules.length - 1
  return more > 0 ? t('{first} +{n}', { first, n: more }) : first
}

/** The two template rows of the page switcher's dropdown (BuilderHost.switcherExtra): display conditions and "Preview as". */
export function TemplateMenuRows({ host, controller, close }: { host: CmsHost; controller: TemplateController; close: () => void }) {
  const s = useSyncExternalStore(controller.subscribe, controller.getState, controller.getState)
  const summary = conditionsSummary(host, s.conditions)
  const preview = previewLabel(s.preview?.label || '') || s.preview?.object || ''
  return (
    <div data-wb-template-type={controller.type}>
      <button type="button" className={ROW} onClick={() => { close(); void controller.openConditions('edit') }}>
        <ListFilter size={14} className="shrink-0 text-ink-300" />
        <span className="font-medium">{t('Display Conditions')}</span>
        <span className="ml-auto max-w-[130px] truncate text-[11px] text-ink-500">{summary}</span>
      </button>
      <button type="button" className={ROW} onClick={() => { close(); controller.openPreview() }}>
        <Eye size={14} className="shrink-0 text-ink-300" />
        <span className="font-medium">{t('Preview as')}</span>
        <span className="ml-auto max-w-[130px] truncate text-[11px] text-ink-500">{preview}</span>
      </button>
    </div>
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
  host.switcherExtra = (close) => <TemplateMenuRows host={host} controller={controller} close={close} />
  return true
}
