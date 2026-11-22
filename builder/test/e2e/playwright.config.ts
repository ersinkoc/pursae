/**
 * wp-admin browser smoke test (`make e2e`, CI job e2e): a Pursae binary on an empty database is
 * installed through the setup-token link, then an administrator logs in, publishes a post in the block
 * editor, uploads an image, opens Dashboard › Updates and logs out (admin-smoke.spec.ts).
 *
 * Environment:
 *   E2E_BIN           the binary to serve (default ../bin/pursae, what `make build` writes)
 *   E2E_DATABASE_URL  an EMPTY database the test installs a site into (`make e2e` recreates wpgo_e2e
 *                     on the compose PostgreSQL; never point it at a site you want to keep)
 *   E2E_PORT          listen port (default 18495)
 * Output (server log, traces and screenshots of a failure): builder/.work/e2e/.
 */
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from '@playwright/test'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..', '..', '..')
export const work = path.resolve(here, '..', '..', '.work', 'e2e')
export const bin = path.resolve(process.env.E2E_BIN || path.join(root, 'bin', 'pursae'))
export const port = Number(process.env.E2E_PORT || 18495)
export const site = `http://127.0.0.1:${port}`
const databaseURL = process.env.E2E_DATABASE_URL
if (!databaseURL) {
  throw new Error('[e2e] set E2E_DATABASE_URL to an empty database (make e2e recreates wpgo_e2e on the compose PostgreSQL)')
}

/** The environment of the server and of the CLI commands the test runs (setup-token). */
export const serverEnv: Record<string, string> = {
  DATABASE_URL: databaseURL,
  PURSAE_ADDR: `127.0.0.1:${port}`,
  PURSAE_SITE_URL: site,
  PURSAE_HUB: 'off',
  PURSAE_UPLOADS_DIR: path.join(work, 'data', 'uploads'),
}

export default defineConfig({
  testDir: here,
  testMatch: /.*\.spec\.ts$/,
  outputDir: path.join(work, 'test-results'),
  forbidOnly: !!process.env.CI,
  workers: 1,
  retries: 0, // one ordered scenario on one site: a retry would find it already installed
  timeout: 5 * 60_000,
  expect: { timeout: 20_000 },
  reporter: [['list']],
  use: {
    baseURL: site,
    browserName: 'chromium',
    headless: true,
    locale: 'en-US',
    timezoneId: 'UTC',
    actionTimeout: 20_000,
    navigationTimeout: 30_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    // A clean data directory on every run; the server log goes to builder/.work/e2e/server.log.
    command: `rm -rf "${path.join(work, 'data')}" && mkdir -p "${work}" && exec "${bin}" serve >"${path.join(work, 'server.log')}" 2>&1`,
    url: `${site}/healthz`,
    env: serverEnv,
    timeout: 120_000,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
  },
})
