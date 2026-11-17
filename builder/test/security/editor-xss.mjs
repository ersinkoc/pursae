#!/usr/bin/env node
/**
 * Browser regression for the element editor's static frame (security review 2026-11-17, findings
 * 1–2): every payload of test/security/payloads.ts is put in a page, drawn in the editor's frame
 * by the editor's own modules (frame.ts, neutral.ts, bundled with rolldown) and then edited through
 * the reconciler; a payload fails when it reads the editor's REST nonce (window.__WEB_BUILDER__).
 *
 * Runs in Playwright's Chromium, under the editor's two CSP modes (report: the admin's
 * 'unsafe-inline' policy, nonce policy report-only; enforce: nonce + 'strict-dynamic'), for:
 *   static            the canvas: sandbox="allow-same-origin" (no scripts) + neutralised copy
 *   static-no-sandbox the neutraliser alone (old sandbox with allow-scripts): proves layer B
 *   control           the old behaviour (markup as stored, allow-scripts, createContextualFragment
 *                     edit): must be hit, else the harness proves nothing
 * and for a MIXED page (code sections around a builder hero section; patch 0021, CodeSection.tsx):
 *   mixed-edit        each run's frame in edit mode: guard's static copy, no allow-scripts; toolbar
 *                     attribute writes (javascript: link, clicked) and a structural reload
 *   mixed-preview-no-consent   Preview before the author confirmed running the scripts: static too
 *   mixed-control     the old per-run frame (markup as stored, allow-scripts): must be hit
 *
 * Not part of `make builder-test` (it needs a browser). Run:
 *   cd builder && npm ci && npx playwright install chromium   # once
 *   node test/security/editor-xss.mjs                         # exit 0: every payload blocked
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { rolldown } from 'rolldown'
import { chromium } from 'playwright'

const here = path.dirname(fileURLToPath(import.meta.url))
const builder = path.resolve(here, '../..')
const work = path.join(builder, '.work', 'upstream', 'src')
const upstream = fs.existsSync(work) ? work : path.join(builder, 'upstream', 'src')

const bundle = await rolldown({ input: path.join(here, 'harness.ts'), resolve: { alias: { '@': upstream } }, logLevel: 'silent' })
const { output } = await bundle.generate({ format: 'esm' })
const js = output[0].code

const ORIGIN = 'https://pursae.test'
const NONCE_POLICY = "script-src 'nonce-abc' 'strict-dynamic' 'self' 'unsafe-eval'; object-src 'none'; base-uri 'self'"
const MODES = {
  report: { 'Content-Security-Policy': "script-src 'self' 'unsafe-inline' 'unsafe-eval'", 'Content-Security-Policy-Report-Only': NONCE_POLICY },
  enforce: { 'Content-Security-Policy': NONCE_POLICY },
}
const SCENARIOS = [
  ['static', { static: true, sandbox: 'allow-same-origin' }],
  ['static-no-sandbox', { static: true, sandbox: 'allow-scripts allow-same-origin allow-forms' }],
  ['control', { static: false, sandbox: 'allow-scripts allow-same-origin allow-forms' }],
]
// Mixed pages (patch 0021): two runs of code sections around a builder hero section, each run a
// CodeSection.tsx frame (codeFrameDoc + the host's frame guard).
const MIXED = [
  ['mixed-edit', { guard: true, edit: true, sandbox: 'allow-same-origin' }],
  ['mixed-preview-no-consent', { guard: true, edit: false, sandbox: 'allow-same-origin' }],
  ['mixed-control', { guard: false, edit: true, sandbox: 'allow-scripts allow-same-origin allow-forms' }],
]

const browser = await chromium.launch()
let failed = false
const rows = []
for (const [mode, headers] of Object.entries(MODES)) {
  const page = await browser.newPage()
  await page.route(`${ORIGIN}/**`, (r) => {
    const u = new URL(r.request().url())
    if (u.pathname === '/harness.js') return r.fulfill({ contentType: 'text/javascript', body: js })
    if (u.pathname === '/wp-admin/post') {
      return r.fulfill({
        contentType: 'text/html',
        headers,
        body: `<!doctype html><html><body><script nonce="abc">window.__WEB_BUILDER__={rest:{nonce:'SECRET-${mode}'}};window.hits=[];window.hit=(n)=>window.hits.push(String(n))</script><script nonce="abc" type="module" src="/harness.js"></script></body></html>`,
      })
    }
    return r.fulfill({ status: 404, contentType: 'text/plain', body: 'no' })
  })
  await page.goto(`${ORIGIN}/wp-admin/post`)
  await page.waitForFunction(() => window.WB)
  for (const [name, opts] of SCENARIOS) {
    const res = await page.evaluate(([n, o]) => window.WB.run(n, o), [`${name}-${mode}`, opts])
    const leaked = [...res.loadHits, ...res.editHits].filter((h) => h.includes('SECRET'))
    const isControl = name === 'control'
    const ok = isControl ? leaked.length > 0 : leaked.length === 0 && res.reconciled !== false
    if (!ok) failed = true
    rows.push({ mode, scenario: name, load: res.loadHits.length, edit: res.editHits.length, reconciled: res.reconciled, ok })
    if (!isControl && leaked.length) console.log(`[${mode}/${name}] LEAKED:`, leaked.join(' | '))
    if (isControl) console.log(`[${mode}/${name}] control hits (expected): ${leaked.length} — ${[...new Set(leaked.map((h) => h.split('@')[0]))].join(', ')}`)
  }
  for (const [name, opts] of MIXED) {
    const res = await page.evaluate(([n, o]) => window.WB.runMixed(n, o), [`${name}-${mode}`, opts])
    const leaked = [...res.loadHits, ...res.editHits].filter((h) => h.includes('SECRET'))
    const isControl = name === 'mixed-control'
    // The old per-run frame has no reconciler: under the enforced nonce policy its srcdoc (which
    // inherits the policy) blocks the stored markup's scripts, so the control must hit in report
    // mode only (there it proves the harness reaches the nonce).
    const ok = isControl ? leaked.length > 0 || mode === 'enforce' : leaked.length === 0
    if (!ok) failed = true
    rows.push({ mode, scenario: name, load: res.loadHits.length, edit: res.editHits.length, reconciled: '-', ok })
    if (!isControl && leaked.length) console.log(`[${mode}/${name}] LEAKED:`, leaked.join(' | '))
    if (isControl) console.log(`[${mode}/${name}] control hits (expected): ${leaked.length}`)
  }
  await page.close()
}
await browser.close()
console.log('\nmode     scenario                  load-hits  edit-hits  reconciled  result')
for (const r of rows) console.log(`${r.mode.padEnd(8)} ${r.scenario.padEnd(25)} ${String(r.load).padStart(9)}  ${String(r.edit).padStart(9)}  ${String(r.reconciled).padStart(10)}  ${r.ok ? 'PASS' : 'FAIL'}`)
console.log(failed ? '\nFAIL' : '\nPASS: no payload read the nonce in static mode (element editor and mixed-page frames; report and enforce, before and after an edit)')
process.exit(failed ? 1 : 0)
