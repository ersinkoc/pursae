/**
 * The Theme Library screen (Web Builder › Theme Library), loaded by ./editor-entry instead of the
 * editor when the page sets window.__WEB_BUILDER_LIBRARY__. The server writes a bare document with a
 * small boot config (no document): the same gallery and import as the editor's "Browse theme
 * library" card (theme-library.ts), filling the frame the admin screen embeds.
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { API_VERSION } from './contract'
import { createRest } from './rest'
import { ThemeLibraryController } from './theme-library'
import { ThemeLibraryDialog } from './ThemeLibraryDialog'

interface LibraryBoot {
  apiVersion: number
  rendererVersion: number
  rest: { root: string; nonce: string }
  links: { site: string }
}

function fail(message: string) {
  const el = document.getElementById('root') ?? document.body
  el.innerHTML = ''
  const p = document.createElement('p')
  p.setAttribute('role', 'alert')
  p.style.cssText = 'margin:32px;font:14px/1.5 system-ui,sans-serif;color:#eef0f3'
  p.textContent = message
  el.append(p)
}

function readLibraryBoot(): LibraryBoot {
  const boot = (window as { __WEB_BUILDER__?: LibraryBoot }).__WEB_BUILDER__
  if (!boot || typeof boot !== 'object' || !boot.rest?.root || !boot.links?.site) throw new Error('The Web Builder boot configuration is missing.')
  if (boot.apiVersion !== API_VERSION) throw new Error(`The editor (API ${API_VERSION}) does not match the server (API ${boot.apiVersion}). Reload the page; clear the browser cache if this persists.`)
  return boot
}

function boot() {
  let b: LibraryBoot
  try {
    b = readLibraryBoot()
  } catch (e) {
    fail(e instanceof Error ? e.message : String(e))
    return
  }
  const rest = createRest({ root: b.rest.root, nonce: b.rest.nonce, nonceUrl: `${b.links.site.replace(/\/+$/, '')}/wp-admin/admin-ajax?action=rest-nonce` })
  const controller = new ThemeLibraryController({ rest, boot: b })
  const root = document.getElementById('root')
  if (!root) return
  createRoot(root).render(
    <StrictMode>
      <ThemeLibraryDialog controller={controller} page />
    </StrictMode>,
  )
  controller.open()
}

boot()
