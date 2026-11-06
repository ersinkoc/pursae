/**
 * Parity suite of the Pursae Web Builder (`make builder-parity`): every renderStatic fixture
 * (internal/webbuilder/testdata/render) drawn by the editor's live React preview and as the published
 * static snapshot, compared pixel by pixel at 1280, 820 and 400 px; plus the snapshot without
 * JavaScript and the site runtime's behaviours. Needs `make builder` (patched upstream copy, dist,
 * fixtures) and Playwright's Chromium (`npx playwright install chromium`).
 * Output: builder/.work/parity/ (report.json, diff images under test-results/).
 */
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from '@playwright/test'

const here = path.dirname(fileURLToPath(import.meta.url))
const work = path.resolve(here, '..', '..', '.work', 'parity')

export default defineConfig({
  testDir: here,
  testMatch: /.*\.spec\.ts$/,
  outputDir: path.join(work, 'test-results'),
  globalSetup: path.join(here, 'global-setup.ts'),
  globalTeardown: path.join(here, 'global-teardown.ts'),
  fullyParallel: true,
  forbidOnly: true,
  retries: 0,
  timeout: 120_000,
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    headless: true,
    deviceScaleFactor: 1,
    colorScheme: 'light',
    reducedMotion: 'reduce',
    locale: 'en-US',
    timezoneId: 'UTC',

  },
})
