// mini3: a few dozen lines that use WebGL the way three.js does (a renderer bound to a canvas,
// setPixelRatio + setSize from the container, render on resize). Not three.js; no network.
const VS = 'attribute vec2 p;attribute vec3 c;varying vec3 v;void main(){v=c;gl_Position=vec4(p,0.,1.);}'
const FS = 'precision mediump float;varying vec3 v;void main(){gl_FragColor=vec4(v,1.);}'

export function rng(seed) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export class WebGLRenderer {
  constructor({ canvas, preserveDrawingBuffer = true } = {}) {
    this.domElement = canvas || document.createElement('canvas')
    this.pixelRatio = 1
    this.clear = [0, 0, 0]
    const gl = this.domElement.getContext('webgl', { antialias: false, preserveDrawingBuffer })
    this.gl = gl
    this.kind = gl ? 'webgl' : '2d'
    if (!gl) {
      this.ctx = this.domElement.getContext('2d')
      return
    }
    const sh = (type, src) => {
      const s = gl.createShader(type)
      gl.shaderSource(s, src)
      gl.compileShader(s)
      return s
    }
    const prog = gl.createProgram()
    gl.attachShader(prog, sh(gl.VERTEX_SHADER, VS))
    gl.attachShader(prog, sh(gl.FRAGMENT_SHADER, FS))
    gl.linkProgram(prog)
    gl.useProgram(prog)
    this.prog = prog
    this.buf = gl.createBuffer()
  }
  setPixelRatio(r) {
    this.pixelRatio = r
  }
  setSize(w, h, updateStyle = true) {
    this.width = w
    this.height = h
    this.domElement.width = Math.floor(w * this.pixelRatio)
    this.domElement.height = Math.floor(h * this.pixelRatio)
    if (updateStyle) {
      this.domElement.style.width = w + 'px'
      this.domElement.style.height = h + 'px'
    }
  }
  setClearColor(rgb) {
    this.clear = rgb
  }
  render(scene) {
    const W = this.domElement.width
    const H = this.domElement.height
    // Triangles keep their shape on any aspect ratio: x is scaled by H / W.
    const ax = H / Math.max(1, W)
    if (!this.gl) {
      const g = this.ctx
      g.fillStyle = `rgb(${this.clear.map((v) => Math.round(v * 255)).join(',')})`
      g.fillRect(0, 0, W, H)
      for (const t of scene.tris) {
        g.fillStyle = `rgb(${t.c.map((v) => Math.round(v * 255)).join(',')})`
        g.beginPath()
        t.p.forEach(([x, y], i) => (i ? g.lineTo : g.moveTo).call(g, ((x * ax + 1) / 2) * W, ((1 - y) / 2) * H))
        g.fill()
      }
      return
    }
    const gl = this.gl
    gl.viewport(0, 0, W, H)
    gl.clearColor(this.clear[0], this.clear[1], this.clear[2], 1)
    gl.clear(gl.COLOR_BUFFER_BIT)
    const data = []
    for (const t of scene.tris) for (const [x, y] of t.p) data.push(x * ax, y, ...t.c)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buf)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(data), gl.STATIC_DRAW)
    const p = gl.getAttribLocation(this.prog, 'p')
    const c = gl.getAttribLocation(this.prog, 'c')
    gl.enableVertexAttribArray(p)
    gl.enableVertexAttribArray(c)
    gl.vertexAttribPointer(p, 2, gl.FLOAT, false, 20, 0)
    gl.vertexAttribPointer(c, 3, gl.FLOAT, false, 20, 8)
    gl.drawArrays(gl.TRIANGLES, 0, data.length / 5)
  }
}
