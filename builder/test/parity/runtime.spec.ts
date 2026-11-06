/**
 * The static snapshot on its own: content without JavaScript, and the site runtime's behaviours
 * (menu, accordion, tabs, dismiss, form) in a real browser.
 */
import { expect, test, type Page } from '@playwright/test'
import { ORIGIN, fixtureNames, serve, settle } from './serve'

/** Paths of the text-bearing elements a visitor can see (checkVisibility incl. opacity and ancestors). */
async function visibleText(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const root = document.querySelector('.wb-page')!
    const path = (el: Element) => {
      const parts: string[] = []
      for (let e: Element | null = el; e && e !== root; e = e.parentElement) parts.unshift(`${e.tagName}:${Array.prototype.indexOf.call(e.parentElement!.children, e)}`)
      return parts.join('/')
    }
    return Array.from(root.querySelectorAll('h1,h2,h3,h4,p,a,button,summary,label,li,img'))
      .filter((el) => (el.tagName === 'IMG' || (el.textContent ?? '').trim() !== '') && el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }))
      .map(path)
  })
}

test.describe('without JavaScript', () => {
  for (const name of fixtureNames()) {
    for (const width of [1280, 400]) {
      test(`${name} @ ${width}px shows its content`, async ({ browser }) => {
        // Reference: the page with the runtime (reduced motion: every entrance revealed at once).
        const on = await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: 'reduce' })
        await serve(on)
        const p1 = await on.newPage()
        await p1.goto(`${ORIGIN}/static.html?f=${name}`)
        await settle(p1)
        const withJs = await visibleText(p1)
        await on.close()

        // No JavaScript, motion allowed: nothing may wait hidden for a runtime that never runs.
        const off = await browser.newContext({ viewport: { width, height: 900 }, javaScriptEnabled: false, reducedMotion: 'no-preference' })
        await serve(off)
        const p2 = await off.newPage()
        await p2.goto(`${ORIGIN}/static.html?f=${name}`)
        await p2.waitForLoadState('load')
        expect(await p2.evaluate(() => document.documentElement.classList.contains('wb-js'))).toBe(false)
        const withoutJs = new Set(await visibleText(p2))
        await off.close()

        expect(withJs.length).toBeGreaterThan(0)
        const missing = withJs.filter((p) => !withoutJs.has(p))
        expect(missing, 'elements visible with the runtime but hidden without JavaScript').toEqual([])
      })
    }
  }

  test('tabs show every panel', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, javaScriptEnabled: false })
    await serve(ctx)
    const page = await ctx.newPage()
    await page.goto(`${ORIGIN}/static.html?f=behaviours&x=tabs`)
    for (const t of ['Starter panel', 'Team panel', 'Business panel']) await expect(page.getByText(t)).toBeVisible()
    await ctx.close()
  })
})

test.describe('runtime', () => {
  test.use({ viewport: { width: 400, height: 800 }, reducedMotion: 'reduce' })

  test.beforeEach(async ({ context }) => {
    await serve(context)
  })

  test('mobile menu: toggle, Escape, outside click, link click', async ({ page }) => {
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const menu = page.locator('details[data-wb-menu]:visible').first()
    const summary = menu.locator(':scope > summary')
    await expect(summary).toHaveAttribute('aria-expanded', 'false')
    const link = menu.locator('a[href^="#"]').first()
    await expect(link).toBeHidden()

    await summary.click()
    await expect(menu).toHaveJSProperty('open', true)
    await expect(summary).toHaveAttribute('aria-expanded', 'true')
    await expect(link).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(menu).toHaveJSProperty('open', false)
    await expect(summary).toBeFocused()
    await expect(summary).toHaveAttribute('aria-expanded', 'false')

    await summary.click()
    await expect(menu).toHaveJSProperty('open', true)
    await page.mouse.click(200, 780)
    await expect(menu).toHaveJSProperty('open', false)

    await summary.click()
    await link.click()
    await expect(menu).toHaveJSProperty('open', false)
  })

  test('mobile menu works without the runtime (native details)', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 400, height: 800 }, javaScriptEnabled: false })
    await serve(ctx)
    const page = await ctx.newPage()
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const menu = page.locator('details[data-wb-menu]:visible').first()
    await menu.locator(':scope > summary').click()
    await expect(menu.locator('a[href^="#"]').first()).toBeVisible()
    await ctx.close()
  })

  test('accordion keeps one item open per group', async ({ page }) => {
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const items = page.locator('[data-wb-acc-single] details[data-wb-acc]')
    expect(await items.count()).toBeGreaterThan(2)
    await expect(items.nth(0)).toHaveJSProperty('open', true)
    await items.nth(1).locator(':scope > summary').click()
    await expect(items.nth(1)).toHaveJSProperty('open', true)
    await expect(items.nth(0)).toHaveJSProperty('open', false)
    await items.nth(2).locator(':scope > summary').click()
    await expect(items.nth(2)).toHaveJSProperty('open', true)
    await expect(items.nth(1)).toHaveJSProperty('open', false)
    await items.nth(2).locator(':scope > summary').click()
    await expect(items.nth(2)).toHaveJSProperty('open', false)
  })

  test('tabs switch panels by click and keyboard', async ({ page }) => {
    await page.goto(`${ORIGIN}/static.html?f=behaviours&x=tabs`)
    const tab = (n: string) => page.getByRole('tab', { name: n })
    const panel = (t: string) => page.getByText(t)
    await expect(tab('Starter')).toHaveAttribute('aria-selected', 'true')
    await expect(panel('Starter panel')).toBeVisible()
    await expect(panel('Team panel')).toBeHidden()

    await tab('Team').click()
    await expect(tab('Team')).toHaveAttribute('aria-selected', 'true')
    await expect(tab('Starter')).toHaveAttribute('aria-selected', 'false')
    await expect(panel('Team panel')).toBeVisible()
    await expect(panel('Starter panel')).toBeHidden()

    await page.keyboard.press('ArrowRight')
    await expect(tab('Business')).toBeFocused()
    await expect(panel('Business panel')).toBeVisible()
    await page.keyboard.press('ArrowRight')
    await expect(tab('Starter')).toBeFocused()
    await page.keyboard.press('End')
    await expect(tab('Business')).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Home')
    await expect(tab('Starter')).toHaveAttribute('aria-selected', 'true')
    await expect(tab('Starter')).toHaveAttribute('tabindex', '0')
    await expect(tab('Team')).toHaveAttribute('tabindex', '-1')
  })

  test('dismissible banner stays closed for the session', async ({ page }) => {
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const host = page.locator('section:has([data-wb-dismiss])').first()
    await expect(host).toBeVisible()
    await host.locator('[data-wb-dismiss-btn]').click()
    await expect(host).toBeHidden()
    await page.reload()
    await expect(page.locator('section:has([data-wb-dismiss])').first()).toBeHidden()
  })

  test('form posts with fetch and shows the reply', async ({ page, context }) => {
    let body = ''
    await context.route('https://example.com/wp-json/web-builder/v2/forms/**', async (route) => {
      body = route.request().postData() ?? ''
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true, message: 'Thanks, we got it.' }) })
    })
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const form = page.locator('form[data-wb-form]').filter({ has: page.locator('input[name=email]') }).first()
    await form.scrollIntoViewIfNeeded()
    // Every visible field a visitor fills; never the honeypot (wb_hp, visually hidden) or the timing field.
    for (const input of await form.locator('input[name]:not([name^="wb_"]):visible, textarea[name]:visible').all()) {
      const name = await input.getAttribute('name')
      await input.fill(name === 'email' ? 'visitor@example.com' : name === 'phone' ? '+1 555 0100' : 'Parity test')
    }
    await form.locator('[type=submit], button:not([type=button])').first().click()
    await expect(form.locator('[data-wb-form-status]')).toHaveText('Thanks, we got it.')
    await expect(form).toHaveAttribute('data-wb-state', 'sent')
    const sent = new URLSearchParams(body)
    expect(sent.get('email')).toBe('visitor@example.com')
    expect(Number(sent.get('wb_t'))).toBeGreaterThan(0)
    expect(sent.get('wb_hp')).toBe('')
  })

  test('form marks the fields a 422 rejects and clears them on the next submit', async ({ page, context }) => {
    let n = 0
    await context.route('https://example.com/wp-json/web-builder/v2/forms/**', async (route) => {
      n++
      if (n === 1) {
        await route.fulfill({ status: 422, contentType: 'application/json', body: JSON.stringify({ code: 'wb_form_invalid', message: 'Please check the highlighted fields.', data: { status: 422, fields: { email: 'invalid' } } }) })
      } else {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true, message: 'Thanks, we got it.' }) })
      }
    })
    await page.goto(`${ORIGIN}/static.html?f=behaviours`)
    const form = page.locator('form[data-wb-form]').filter({ has: page.locator('input[name=email]') }).first()
    await form.scrollIntoViewIfNeeded()
    for (const input of await form.locator('input[name]:not([name^="wb_"]):visible, textarea[name]:visible').all()) {
      const name = await input.getAttribute('name')
      await input.fill(name === 'email' ? 'visitor@localhost' : name === 'phone' ? '+1 555 0100' : 'Parity test')
    }
    await form.locator('[type=submit], button:not([type=button])').first().click()
    const email = form.locator('input[name=email]')
    await expect(form.locator('[data-wb-form-status]')).toHaveText('Please check the highlighted fields.')
    await expect(email).toHaveAttribute('aria-invalid', 'true')
    await expect(email).toHaveAttribute('data-wb-invalid', '')
    await expect(email).toBeFocused()
    await expect(form.locator('[data-wb-invalid]')).toHaveCount(1)
    await form.locator('[type=submit], button:not([type=button])').first().click()
    await expect(form).toHaveAttribute('data-wb-state', 'sent')
    await expect(form.locator('[data-wb-invalid]')).toHaveCount(0)
    await expect(email).not.toHaveAttribute('aria-invalid', 'true')
  })
})
