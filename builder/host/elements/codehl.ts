/**
 * Small HTML / CSS / JS colouring for the code editor (CodeEditor.tsx): one linear pass that
 * returns flat, ordered, non-overlapping tokens [s, e) with a class. It is a colouring aid, not a
 * parser: unknown input is left plain and nothing here can throw or loop forever.
 */
export type Cls = 'tag' | 'attr' | 'str' | 'com' | 'kw' | 'num' | 'ent' | 'meta'
export interface Tok {
  s: number
  e: number
  c: Cls
}
export type Lang = 'html' | 'css' | 'js' | 'text'

const KW = new Set('async await break case catch class const continue default delete do else export extends false finally for from function if import in instanceof let new null of return static super switch this throw true try typeof undefined var void while with yield'.split(' '))

const isWord = (c: string) => /[A-Za-z0-9_$-]/.test(c)

/** CSS between [i, end). */
function css(src: string, i: number, end: number, out: Tok[]) {
  let depth = 0
  while (i < end) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '*') {
      const e = src.indexOf('*/', i + 2)
      const stop = e < 0 || e + 2 > end ? end : e + 2
      out.push({ s: i, e: stop, c: 'com' })
      i = stop
    } else if (c === '"' || c === "'") {
      let j = i + 1
      while (j < end && src[j] !== c && src[j] !== '\n') j += src[j] === '\\' ? 2 : 1
      const stop = Math.min(end, j + 1)
      out.push({ s: i, e: stop, c: 'str' })
      i = stop
    } else if (c === '{') {
      depth++
      i++
    } else if (c === '}') {
      depth = Math.max(0, depth - 1)
      i++
    } else if (c === '@') {
      let j = i + 1
      while (j < end && /[\w-]/.test(src[j])) j++
      out.push({ s: i, e: j, c: 'kw' })
      i = Math.max(j, i + 1)
    } else if (c === '#' && /[0-9a-fA-F]/.test(src[i + 1] ?? '') && depth > 0) {
      let j = i + 1
      while (j < end && /[0-9a-fA-F]/.test(src[j])) j++
      out.push({ s: i, e: j, c: 'num' })
      i = j
    } else if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(src[i + 1] ?? '') && !isWord(src[i - 1] ?? ' '))) {
      if (isWord(src[i - 1] ?? ' ')) {
        i++
        continue
      }
      let j = i + 1
      while (j < end && /[0-9.]/.test(src[j])) j++
      while (j < end && /[a-zA-Z%]/.test(src[j])) j++
      out.push({ s: i, e: j, c: 'num' })
      i = j
    } else if (c === '!' && src.startsWith('!important', i)) {
      out.push({ s: i, e: i + 10, c: 'kw' })
      i += 10
    } else if (/[A-Za-z_-]/.test(c)) {
      let j = i + 1
      while (j < end && isWord(src[j])) j++
      // A property when a colon follows and the declaration ends with ; or } before any {.
      let k = j
      while (k < end && /\s/.test(src[k])) k++
      let prop = false
      if (depth > 0 && src[k] === ':') {
        let m = k + 1
        while (m < end && src[m] !== ';' && src[m] !== '{' && src[m] !== '}') m++
        prop = src[m] !== '{'
      }
      if (prop) out.push({ s: i, e: j, c: 'attr' })
      else if (depth === 0 || (src[k] === '{' || src[k] === ',' || src[k] === ':')) out.push({ s: i, e: j, c: 'tag' })
      i = j
    } else if ((c === '.' || c === '#') && depth === 0) {
      let j = i + 1
      while (j < end && isWord(src[j])) j++
      out.push({ s: i, e: j, c: 'tag' })
      i = Math.max(j, i + 1)
    } else i++
  }
}

/** JavaScript between [i, end): comments, strings, numbers, keywords. */
function js(src: string, i: number, end: number, out: Tok[]) {
  while (i < end) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '/') {
      let e = src.indexOf('\n', i)
      if (e < 0 || e > end) e = end
      out.push({ s: i, e, c: 'com' })
      i = e
    } else if (c === '/' && src[i + 1] === '*') {
      const e = src.indexOf('*/', i + 2)
      const stop = e < 0 || e + 2 > end ? end : e + 2
      out.push({ s: i, e: stop, c: 'com' })
      i = stop
    } else if (c === '"' || c === "'" || c === '`') {
      let j = i + 1
      while (j < end && src[j] !== c && (c === '`' || src[j] !== '\n')) j += src[j] === '\\' ? 2 : 1
      const stop = Math.min(end, j + 1)
      out.push({ s: i, e: stop, c: 'str' })
      i = stop
    } else if (/[0-9]/.test(c) && !isWord(src[i - 1] ?? ' ')) {
      let j = i + 1
      while (j < end && /[0-9a-fA-FxX._]/.test(src[j])) j++
      out.push({ s: i, e: j, c: 'num' })
      i = j
    } else if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1
      while (j < end && /[\w$]/.test(src[j])) j++
      if (KW.has(src.slice(i, j))) out.push({ s: i, e: j, c: 'kw' })
      i = j
    } else i++
  }
}

const RAW_CODE = new Set(['script', 'style'])
const RAW_TEXT = new Set(['textarea', 'title', 'xmp', 'noembed', 'noframes', 'iframe', 'noscript', 'template'])

/** HTML: tags, attributes, strings, comments, entities; script and style content by language. */
function html(src: string, out: Tok[]) {
  const n = src.length
  let i = 0
  while (i < n) {
    const lt = src.indexOf('<', i)
    const amp = src.indexOf('&', i)
    const next = lt < 0 ? amp : amp < 0 ? lt : Math.min(lt, amp)
    if (next < 0) break
    i = next
    if (src[i] === '&') {
      const m = /^&(#x[0-9a-fA-F]+|#\d+|[A-Za-z][A-Za-z0-9]*);/.exec(src.slice(i, i + 40))
      if (m) {
        out.push({ s: i, e: i + m[0].length, c: 'ent' })
        i += m[0].length
      } else i++
      continue
    }
    if (src.startsWith('<!--', i)) {
      const e = src.indexOf('-->', i + 4)
      const stop = e < 0 ? n : e + 3
      out.push({ s: i, e: stop, c: 'com' })
      i = stop
      continue
    }
    const c1 = src[i + 1]
    if (c1 === '!' || c1 === '?') {
      const e = src.indexOf('>', i)
      const stop = e < 0 ? n : e + 1
      out.push({ s: i, e: stop, c: 'meta' })
      i = stop
      continue
    }
    if (!(c1 && /[A-Za-z/]/.test(c1))) {
      i++
      continue
    }
    // A tag: <name or </name
    let j = i + (c1 === '/' ? 2 : 1)
    const nameStart = j
    while (j < n && !/[\s/>]/.test(src[j])) j++
    const name = src.slice(nameStart, j).toLowerCase()
    out.push({ s: i, e: j, c: 'tag' })
    // Attributes up to '>'
    for (;;) {
      while (j < n && /\s/.test(src[j])) j++
      if (j >= n) break
      const c = src[j]
      if (c === '>') {
        out.push({ s: j, e: j + 1, c: 'tag' })
        j++
        break
      }
      if (c === '/' && src[j + 1] === '>') {
        out.push({ s: j, e: j + 2, c: 'tag' })
        j += 2
        break
      }
      if (c === '/' || c === '=') {
        j++
        continue
      }
      if (c === '"' || c === "'") {
        // A stray quoted value.
        const e = src.indexOf(c, j + 1)
        const stop = e < 0 ? n : e + 1
        out.push({ s: j, e: stop, c: 'str' })
        j = stop
        continue
      }
      let k = j
      while (k < n && !/[\s=/>]/.test(src[k])) k++
      if (k === j) k = j + 1
      out.push({ s: j, e: k, c: 'attr' })
      j = k
      let m = j
      while (m < n && /\s/.test(src[m])) m++
      if (src[m] === '=') {
        m++
        while (m < n && /\s/.test(src[m])) m++
        const q = src[m]
        if (q === '"' || q === "'") {
          const e = src.indexOf(q, m + 1)
          const stop = e < 0 ? n : e + 1
          out.push({ s: m, e: stop, c: 'str' })
          j = stop
        } else {
          let e = m
          while (e < n && !/[\s>]/.test(src[e])) e++
          if (e > m) out.push({ s: m, e, c: 'str' })
          j = e
        }
      }
    }
    i = j
    if (c1 === '/') continue
    if (RAW_CODE.has(name) || RAW_TEXT.has(name)) {
      const re = new RegExp(`</${name}[\\s/>]`, 'ig')
      re.lastIndex = i
      const m = re.exec(src)
      const end = m ? m.index : n
      if (RAW_CODE.has(name)) (name === 'style' ? css : js)(src, i, end, out)
      i = end
    }
  }
}

/** Tokens of `src` (flat, ordered, non-overlapping). */
export function highlight(src: string, lang: Lang): Tok[] {
  const out: Tok[] = []
  if (lang === 'html') html(src, out)
  else if (lang === 'css') css(src, 0, src.length, out)
  else if (lang === 'js') js(src, 0, src.length, out)
  return out
}
