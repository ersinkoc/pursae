/**
 * The live frame's modules for live-frame.mjs: bundled with rolldown and evaluated in the editor's
 * page (which has the host: window.__WEB_BUILDER_HOST__ and Boot).
 */
import * as live from '../../host/elements/live'

declare global {
  interface Window {
    __wbLive: typeof live
  }
}

window.__wbLive = live
