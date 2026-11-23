// The hero scene: a module resolved through the page's import map ("mini3"), an asset loaded
// relative to this module (import.meta.url, like three.js loaders with a kit path), sized to its
// container and to devicePixelRatio, drawn again on resize and on a theme change.
import { WebGLRenderer, rng } from 'mini3'

const html = document.documentElement
html.setAttribute('data-module', 'ok')
const stage = document.getElementById('stage')
const canvas = document.getElementById('scene')
const renderer = new WebGLRenderer({ canvas })
html.setAttribute('data-gl', renderer.kind)

let cfg = null
let scene = { tris: [] }
let draws = 0

function build() {
  const r = rng(cfg.seed)
  const tris = []
  for (let i = 0; i < cfg.count; i++) {
    const x = r() * 3.2 - 1.6
    const y = r() * 1.6 - 0.4
    const s = 0.08 + r() * 0.22
    const a = r() * Math.PI * 2
    const p = [0, 1, 2].map((k) => [x + Math.cos(a + (k * 2 * Math.PI) / 3) * s, y + Math.sin(a + (k * 2 * Math.PI) / 3) * s])
    tris.push({ p, c: cfg.palette[i % cfg.palette.length] })
  }
  scene = { tris }
}

function draw() {
  if (!cfg) return
  renderer.setPixelRatio(window.devicePixelRatio || 1)
  renderer.setSize(stage.clientWidth, stage.clientHeight, false)
  renderer.setClearColor(html.classList.contains('dark') ? cfg.background.dark : cfg.background.light)
  renderer.render(scene)
  draws++
  html.setAttribute('data-scene', `${canvas.width}x${canvas.height}@${window.devicePixelRatio || 1}#${draws}`)
}

fetch(new URL('../data/scene.json', import.meta.url))
  .then((r) => {
    if (!r.ok) throw new Error('HTTP ' + r.status)
    return r.json()
  })
  .then((j) => {
    cfg = j
    html.setAttribute('data-asset', 'ok')
    build()
    draw()
  })
  .catch((e) => html.setAttribute('data-asset', 'fail: ' + e.message))

window.addEventListener('resize', draw)
new MutationObserver(draw).observe(html, { attributes: true, attributeFilter: ['class'] })
