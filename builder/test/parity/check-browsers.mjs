#!/usr/bin/env node
/**
 * Exit 0 when Playwright's Chromium can start, 3 (with a hint) when its browsers are not installed,
 * so `make builder-parity` can skip cleanly instead of failing on a machine without them.
 */
let chromium
try {
  ;({ chromium } = await import('@playwright/test'))
} catch {
  console.log('[parity] skipped: @playwright/test is not installed (run npm ci in builder/).')
  process.exit(3)
}
try {
  const b = await chromium.launch()
  await b.close()
} catch (e) {
  const msg = String(e?.message ?? e).split('\n')[0]
  console.log(`[parity] skipped: Playwright's Chromium is not available (${msg}).\n[parity] install it with: cd builder && npx playwright install chromium`)
  process.exit(3)
}
