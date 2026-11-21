/**
 * wp-admin smoke test: one ordered scenario on a fresh site (see playwright.config.ts). Every wait is
 * on a condition (an element, a response, a URL), never a fixed sleep; console errors and failed
 * same-origin requests on any admin page fail the test.
 */
import { execFileSync } from 'node:child_process'
import { expect, test, type Page } from '@playwright/test'
import { bin, serverEnv, site } from './playwright.config'

const user = 'e2e-admin'
const pass = 'E2e!Smoke-' + Math.random().toString(36).slice(2, 12)
const postTitle = 'E2E smoke post ' + Date.now()

// A 2×2 PNG.
const png = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGMwTpsJRAwQCgAf7gTJ/qZDUgAAAABJRU5ErkJggg==',
  'base64',
)

/** Records console errors, uncaught exceptions and failed same-origin requests of page. */
function watch(page: Page): string[] {
  const problems: string[] = []
  page.on('console', (m) => {
    if (m.type() === 'error') problems.push(`console: ${m.text()} (${page.url()})`)
  })
  page.on('pageerror', (e) => problems.push(`exception: ${e.message} (${page.url()})`))
  page.on('response', (r) => {
    if (r.url().startsWith(site) && r.status() >= 500) problems.push(`HTTP ${r.status()}: ${r.url()}`)
  })
  page.on('requestfailed', (r) => {
    const why = r.failure()?.errorText ?? ''
    // Navigations away from a page abort its pending requests; that is not a failure.
    if (r.url().startsWith(site) && !why.includes('ERR_ABORTED')) problems.push(`request failed: ${r.url()} ${why}`)
  })
  return problems
}

test('install, log in, write a post, upload media, open Updates, log out', async ({ page }) => {
  const problems = watch(page)

  await test.step('installer is locked without the setup token', async () => {
    const r = await page.request.get('/wp-admin/install', { maxRedirects: 0 })
    expect(r.status(), 'a fresh site locks its installer; is E2E_DATABASE_URL an empty database?').toBe(403)
  })

  await test.step('install through the setup-token link', async () => {
    const out = execFileSync(bin, ['setup-token'], { env: { ...process.env, ...serverEnv }, encoding: 'utf8' })
    const link = out.split('\n')[0].trim()
    expect(link).toMatch(/\/wp-admin\/install\?token=/)
    await page.goto(link)
    // Step 1: language.
    await page.locator('select#language').selectOption('en_US')
    await page.locator('#submit').click()
    // Step 2: site and administrator.
    await page.locator('#weblog_title').fill('E2E Smoke Site')
    await page.locator('#user_login').fill(user)
    await page.locator('#pass1').fill(pass)
    // pass2 is hidden while the password is shown in clear text; keep it in step for either mode.
    await page.locator('#pass2').evaluate((el, v) => ((el as HTMLInputElement).value = v), pass)
    await page.locator('#admin_email').fill('e2e@example.com')
    await page.locator('#submit').click()
    await expect(page.getByRole('link', { name: /log in/i }).first()).toBeVisible()
  })

  await test.step('log in', async () => {
    await page.goto('/wp-login')
    await page.locator('#user_login').fill(user)
    await page.locator('#user_pass').fill(pass)
    await page.locator('#wp-submit').click()
    await page.waitForURL(/\/wp-admin\/?$/)
    await expect(page.locator('#wpadminbar')).toBeVisible()
    await expect(page.locator('h1').first()).toContainText('Dashboard')
  })

  await test.step('publish a post in the block editor', async () => {
    await page.goto('/wp-admin/post-new')
    // First visit: the welcome guide.
    const guide = page.locator('.components-modal__frame')
    const canvas = page.frameLocator('iframe[name="editor-canvas"]')
    const title = canvas.getByRole('textbox', { name: 'Add title' })
    await expect(title.or(guide.first())).toBeVisible({ timeout: 60_000 })
    if (await guide.first().isVisible()) {
      await guide.getByRole('button', { name: 'Close' }).click()
      await expect(guide).toHaveCount(0)
    }
    await title.click()
    await title.fill(postTitle)
    await title.press('Enter') // a new paragraph block below the title
    await page.keyboard.type('Written by the wp-admin smoke test.')
    await page.locator('.editor-header, .edit-post-header').getByRole('button', { name: 'Publish', exact: true }).click()
    // The pre-publish panel asks to confirm.
    await page.locator('.editor-post-publish-panel').getByRole('button', { name: 'Publish', exact: true }).click()
    await expect(page.locator('.editor-post-publish-panel__header-published, .post-publish-panel__postpublish-header')).toBeVisible({ timeout: 30_000 })
    // The post is on the site.
    const front = await page.request.get('/')
    expect(front.status()).toBe(200)
    expect(await front.text()).toContain(postTitle)
  })

  await test.step('upload an image', async () => {
    await page.goto('/wp-admin/media-new')
    const uploaded = page.waitForResponse((r) => r.url().includes('/wp-admin/async-upload') && r.request().method() === 'POST')
    // plupload's file input (moxie), or the browser uploader's when JavaScript did not take over.
    const input = page.locator('#plupload-upload-ui input[type="file"], #html-upload-ui input[type="file"]').first()
    await input.setInputFiles({ name: 'e2e-smoke.png', mimeType: 'image/png', buffer: png })
    expect((await uploaded).status()).toBe(200)
    await expect(page.locator('#media-items .media-item').first()).toBeVisible()
    await page.goto('/wp-admin/upload?mode=list')
    await expect(page.locator('#the-list')).toContainText('e2e-smoke')
  })

  await test.step('open Dashboard › Updates', async () => {
    await page.goto('/wp-admin/update-core')
    await expect(page.locator('h1').first()).toContainText('Updates')
  })

  await test.step('log out', async () => {
    await page.locator('#wp-admin-bar-my-account').hover()
    await page.locator('#wp-admin-bar-logout a').click()
    await page.waitForURL(/wp-login/)
    await expect(page.locator('#loginform')).toBeVisible()
    await page.goto('/wp-admin/')
    await page.waitForURL(/wp-login/)
  })

  expect(problems, problems.join('\n')).toEqual([])
})
