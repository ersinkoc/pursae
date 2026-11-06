/**
 * Editor entry of the Pursae Web Builder inside the CMS (replaces upstream src/main.tsx in the
 * build, see builder/vite.config.ts editorEntry()).
 *
 * Reads window.__WEB_BUILDER__ (written by the editor screen), installs the CMS host with setHost()
 * before the builder first renders, then mounts upstream's App and the host layer (notices, media
 * dialog) into #root.
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from '@/App'
import { setHost } from '@/builder/host'
import '@/index.css'
import { createCmsHost, fontsBase, readBoot, type CmsHost } from './cms-host'
import { HostLayer } from './HostLayer'

function mountPoint(): HTMLElement {
  let el = document.getElementById('root')
  if (!el) {
    el = document.createElement('div')
    el.id = 'root'
    document.body.appendChild(el)
  }
  return el
}

/**
 * The fonts css2 endpoint needs the wp_rest nonce in `_wpnonce` (a stylesheet link cannot send
 * headers), but upstream builds links as `${host.fontsCss}?family=…`. Add the current nonce to every
 * stylesheet link that points at the endpoint, before the browser requests it.
 */
function installFontNonce(host: CmsHost) {
  const base = fontsBase(host.boot)
  const desc = Object.getOwnPropertyDescriptor(HTMLLinkElement.prototype, 'href')
  if (!base || !desc?.set || !desc.get) return
  const withNonce = (v: string) => {
    if (typeof v !== 'string' || !v.startsWith(base + '?') || /[?&]_wpnonce=/.test(v)) return v
    return `${v}&_wpnonce=${encodeURIComponent(host.rest.nonce())}`
  }
  Object.defineProperty(HTMLLinkElement.prototype, 'href', {
    configurable: true,
    enumerable: desc.enumerable,
    get: desc.get,
    set(this: HTMLLinkElement, v: string) {
      desc.set!.call(this, withNonce(v))
    },
  })
}

function fail(message: string) {
  const el = mountPoint()
  el.innerHTML = ''
  const box = document.createElement('div')
  box.setAttribute('role', 'alert')
  box.style.cssText = 'max-width:520px;margin:15vh auto;padding:24px;font:14px/1.5 system-ui,sans-serif;color:#eef0f3;background:#17181c;border:1px solid #393c45;border-radius:12px'
  const h = document.createElement('strong')
  h.textContent = 'Pursae Web Builder could not start'
  const p = document.createElement('p')
  p.textContent = message
  box.append(h, p)
  el.append(box)
}

async function boot() {
  let host: CmsHost
  try {
    host = createCmsHost(readBoot())
  } catch (e) {
    fail(e instanceof Error ? e.message : String(e))
    return
  }
  setHost(host)
  window.__WEB_BUILDER_HOST__ = host
  installFontNonce(host)
  // Prefs and the library seed the builder's initial state; never block the editor on them for long.
  await Promise.race([host.start().catch(() => undefined), new Promise((r) => setTimeout(r, 4000))])

  window.addEventListener('beforeunload', (e) => {
    if (!host.autosaver.pending || host.session.stopped) return
    void host.autosaver.flush()
    e.preventDefault()
    e.returnValue = ''
  })
  window.addEventListener('pagehide', () => {
    void host.storage.flushPrefs()
    host.dispose()
  })

  const root = mountPoint()
  const layer = document.createElement('div')
  layer.id = 'wb-host-layer'
  document.body.appendChild(layer)
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
  createRoot(layer).render(<HostLayer host={host} />)
}

void boot()
