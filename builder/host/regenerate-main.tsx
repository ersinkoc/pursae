/**
 * The bulk regenerate screen (Settings › Web Builder), loaded by ./editor-entry instead of the
 * editor when the page sets window.__WEB_BUILDER_REGENERATE__. A small frame of plain DOM: a
 * progress bar and the pages that failed; the work is regenerate.ts.
 */
import { API_VERSION } from './contract'
import { createRest } from './rest'
import { regenerateAll, type RegenerateProgress } from './regenerate'

interface RegenBoot {
  apiVersion: number
  rendererVersion: number
  rest: { root: string; nonce: string }
  links: { site: string }
}

function line(root: HTMLElement, text: string, alert = false) {
  const p = document.createElement('p')
  if (alert) p.setAttribute('role', 'alert')
  p.style.cssText = 'margin:8px 16px;font:14px/1.5 system-ui,sans-serif;color:#eef0f3'
  p.textContent = text
  root.append(p)
}

async function main() {
  const root = document.getElementById('root') ?? document.body
  const b = (window as { __WEB_BUILDER__?: RegenBoot }).__WEB_BUILDER__
  if (!b?.rest?.root || !b.links?.site) return line(root, 'The Web Builder boot configuration is missing.', true)
  if (b.apiVersion !== API_VERSION) return line(root, `The editor (API ${API_VERSION}) does not match the server (API ${b.apiVersion}). Reload the page.`, true)
  const rest = createRest({ root: b.rest.root, nonce: b.rest.nonce, nonceUrl: `${b.links.site.replace(/\/+$/, '')}/wp-admin/admin-ajax?action=rest-nonce` })
  const bar = document.createElement('progress')
  bar.style.cssText = 'display:block;width:calc(100% - 32px);margin:16px'
  bar.max = 1
  bar.value = 0
  const status = document.createElement('p')
  status.setAttribute('role', 'status')
  status.style.cssText = 'margin:8px 16px;font:14px/1.5 system-ui,sans-serif;color:#eef0f3'
  root.append(bar, status)
  const show = (p: RegenerateProgress) => {
    bar.max = Math.max(p.total, 1)
    bar.value = p.done
    status.textContent = p.finished ? `Done: ${p.done - p.failed.length} of ${p.total} published again.` : `Publishing ${p.done + 1} of ${p.total}: ${p.current}`
  }
  try {
    const res = await regenerateAll({ rest, boot: b, onProgress: show })
    for (const f of res.failed) line(root, `${f.title} (#${f.id}): ${f.message}`, true)
  } catch (e) {
    line(root, e instanceof Error ? e.message : String(e), true)
  }
}

void main()
