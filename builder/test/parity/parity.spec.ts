/**
 * Pixel parity: the editor's live preview (harness/live.tsx) and the static snapshot (render.html +
 * site css + runtime) of every fixture, at three widths. Target: at most 0.1 % differing pixels.
 */
import fs from 'node:fs'
import path from 'node:path'
import { expect, test } from '@playwright/test'
import { WORK } from './build-harness.mjs'
import { ORIGIN, compare, fixtureNames, serve, settle, shoot } from './serve'

export const WIDTHS = [1280, 820, 400] as const
const HEIGHT = 900
const TARGET = 0.001

for (const name of fixtureNames()) {
  for (const width of WIDTHS) {
    test(`${name} @ ${width}px`, async ({ browser }, info) => {
      const ctx = await browser.newContext({ viewport: { width, height: HEIGHT } })
      await serve(ctx)
      const errors: string[] = []
      const open = async (url: string, ready: string) => {
        const p = await ctx.newPage()
        p.on('pageerror', (e) => errors.push(`${url}: ${e.message}`))
        await p.goto(ORIGIN + url)
        await p.waitForFunction(ready, null, { timeout: 30_000 })
        await settle(p)
        return p
      }
      const live = await open(`/live.html?f=${name}`, '!!window.__parityReady || !!window.__parityError')
      const harnessError = await live.evaluate(() => (window as { __parityError?: string }).__parityError)
      expect(harnessError, 'live harness').toBeUndefined()
      const a = await shoot(live, '#root [data-preview]')
      const stat = await open(`/static.html?f=${name}`, "document.readyState === 'complete'")
      const b = await shoot(stat, '.wb-page[data-wb-root]')

      const cmp = await ctx.newPage()
      const d = await compare(cmp, a, b)
      await ctx.close()

      const ok = d.ratio <= TARGET
      const row = { fixture: name, width, ratio: d.ratio, diff: d.diff, ok, target: TARGET, sizeLive: d.sizeA, sizeStatic: d.sizeB, bands: d.bands.slice(0, 40), errors }
      fs.writeFileSync(path.join(WORK, 'parity', 'results', `${name}-${width}.json`), JSON.stringify(row) + '\n')
      if (d.diff || errors.length) {
        // Kept on disk for a look: builder/.work/parity/images/<fixture>-<width>.{live,static,diff}.png
        const img = path.join(WORK, 'parity', 'images')
        fs.mkdirSync(img, { recursive: true })
        fs.writeFileSync(path.join(img, `${name}-${width}.live.png`), a)
        fs.writeFileSync(path.join(img, `${name}-${width}.static.png`), b)
        if (d.png) fs.writeFileSync(path.join(img, `${name}-${width}.diff.png`), d.png)
        await info.attach('diff', { path: path.join(img, `${name}-${width}.diff.png`), contentType: 'image/png' }).catch(() => undefined)
      }
      expect.soft(errors, 'page errors').toEqual([])
      expect.soft(d.sizeA, 'page size (live vs static)').toEqual(d.sizeB)
      expect(d.ratio, `differing pixels ${(d.ratio * 100).toFixed(3)} % (${d.diff} px)`).toBeLessThanOrEqual(TARGET)
    })
  }
}
