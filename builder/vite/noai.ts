/**
 * No-AI build: every upstream src/builder/ai/** module is replaced by a CMS-owned stub in
 * builder/host/ai-stub/, so the shipped editor contains no AI code, no provider URLs and makes no
 * AI network calls. The AI mode is also removed from the TopBar mode switch.
 *
 * The plugin fails the build loudly instead of silently shipping AI code:
 *  - an upstream ai/ module with no stub file is an error (add a stub, or extend an existing one);
 *  - a load of any real ai/ module (however it was reached) is an error;
 *  - when the TopBar transform target is gone but TopBar still offers an 'ai' mode, it is an error.
 *
 * AI is out of scope for now. When it comes back, the host seam decides (host.ai.enabled) and this
 * plugin is only used for an AI-off build.
 */
import fs from 'node:fs'
import path from 'node:path'
import type { Plugin } from 'vite'

const STUB_EXT = ['.tsx', '.ts']

export interface NoAiOptions {
  /** Absolute path of the (patched) upstream src directory. */
  upstreamSrc: string
  /** Absolute path of builder/host/ai-stub. */
  stubDir: string
}

const norm = (p: string) => p.split(path.sep).join('/')

export function noAi({ upstreamSrc, stubDir }: NoAiOptions): Plugin {
  const aiDir = norm(path.join(upstreamSrc, 'builder', 'ai')) + '/'
  const topBar = norm(path.join(upstreamSrc, 'builder', 'TopBar.tsx'))
  const stubbed = new Set<string>()

  /** Upstream ai/ module name for an absolute file path ("AiProvider" for .../ai/AiProvider.tsx). */
  const aiModule = (file: string): string | null => {
    const f = norm(file).split('?')[0]
    if (!f.startsWith(aiDir)) return null
    return f.slice(aiDir.length).replace(/\.(tsx?|jsx?)$/, '')
  }

  const stubFor = (name: string): string => {
    for (const ext of STUB_EXT) {
      const f = path.join(stubDir, name + ext)
      if (fs.existsSync(f)) return f
    }
    throw new Error(
      `[noai] upstream module src/builder/ai/${name} has no stub in builder/host/ai-stub/. ` +
        `Add builder/host/ai-stub/${name}.ts(x) exporting the names its importers use (inert).`,
    )
  }

  const resolveUpstream = (spec: string, importer: string | undefined): string | null => {
    let base: string | null = null
    if (spec.startsWith('@/')) base = path.join(upstreamSrc, spec.slice(2))
    else if ((spec.startsWith('./') || spec.startsWith('../')) && importer) base = path.resolve(path.dirname(importer.split('?')[0]), spec)
    else if (path.isAbsolute(spec)) base = spec
    return base && aiModule(base) !== null ? base : null
  }

  return {
    name: 'pursae-noai',
    enforce: 'pre',
    resolveId(spec, importer) {
      const target = resolveUpstream(spec, importer)
      if (!target) return null
      const name = aiModule(target)
      if (name === null) return null
      // Stubs import each other with relative paths inside ai-stub/, never upstream ai/.
      stubbed.add(name)
      return stubFor(name)
    },
    load(id) {
      const name = aiModule(id)
      if (name !== null) throw new Error(`[noai] real AI module loaded: src/builder/ai/${name} (${id}). The noai alias missed an import path.`)
      return null
    },
    transform(code, id) {
      if (norm(id).split('?')[0] !== topBar) return null
      // Drop the AI entry of the ModeSwitch options; the rest of TopBar's AI UI (AiChip) only
      // renders in 'ai' mode, which is then unreachable.
      const entry = /^[ \t]*\{\s*m:\s*'ai'\s*,[^\n]*\},?[ \t]*\r?\n/m
      if (entry.test(code)) return { code: code.replace(entry, ''), map: null }
      // With the host seam patch the entry is gated: ...(hostAi().enabled ? [{ m: 'ai' as const, … }] : []),
      const gated = /^[ \t]*\.\.\.\(\s*hostAi\(\)\.enabled\s*\?\s*\[\s*\{\s*m:\s*'ai'(?:\s+as\s+const)?\s*,[^\n]*\}\s*\]\s*:\s*\[\]\s*\),?[ \t]*\r?\n/m
      if (gated.test(code)) return { code: code.replace(gated, ''), map: null }
      if (/m:\s*'ai'/.test(code)) {
        throw new Error("[noai] TopBar.tsx still offers the 'ai' mode but the transform target moved; update builder/vite/noai.ts.")
      }
      return null
    },
    buildEnd(err) {
      if (err) return
      this.info?.(`[noai] stubbed upstream AI modules: ${[...stubbed].sort().join(', ') || '(none)'}`)
    },
  }
}
