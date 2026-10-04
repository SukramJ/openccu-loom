import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

/**
 * The onboarding wizard as an operator meets it.
 *
 * The inbox surface carries three different asks, and they must not read
 * the same:
 *
 *   - a plain CCU entry — "take this into service"
 *   - `pending_creation` — "decide whether this device exists here at all"
 *   - `awaiting_release` — "it exists and is configured; publish it"
 *
 * The third is the one this suite did not cover before: offering Accept
 * for it would ask the operator to accept a device that is already
 * accepted, and offering Replace would offer to swap a device that is
 * already in service.
 *
 * The shared fixture serves an empty inbox, so every test here overrides
 * that one route.
 */

/** Fixed so the rendered timestamps do not move between runs. */
const SEEN = Math.floor(new Date('2025-12-28T08:00:00Z').getTime() / 1000);

const THREE_KINDS = [
  {
    address: '0001CCU0',
    model: 'HmIP-SWDO',
    interface: 'HmIP-RF',
    serial: '0001CCU0',
    manufacturer: 'eQ-3',
    first_seen: SEEN,
  },
  {
    address: '0002PEND',
    model: 'HmIP-STH',
    interface: 'HmIP-RF',
    serial: '0002PEND',
    manufacturer: 'eQ-3',
    first_seen: SEEN,
    pending_creation: true,
  },
  {
    address: '0003RELE',
    model: 'HmIP-PS',
    interface: 'HmIP-RF',
    serial: '0003RELE',
    manufacturer: 'eQ-3',
    first_seen: SEEN,
    awaiting_release: true,
  },
];

async function mockInbox(page: import('@playwright/test').Page, devices: unknown[]) {
  await page.route('**/api/v1/inbox', (route) => route.fulfill({ json: devices }));
}

async function setTheme(page: import('@playwright/test').Page, theme: 'light' | 'dark') {
  await page.addInitScript(
    (t) => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: t,
          locale: 'en',
          navCollapsed: false,
          expertMode: false,
        }),
      );
    },
    theme,
  );
}

test.describe('Inbox onboarding states', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('the view is named for what it holds', async ({ page }) => {
    await mockInbox(page, THREE_KINDS);
    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'New devices', level: 1 })).toBeVisible({
      timeout: 10000,
    });
    await expect(page.locator('aside nav').getByRole('link', { name: 'New devices' })).toBeVisible();
  });

  test('a held device is accepted and released from the view, in that order', async ({ page }) => {
    await mockInbox(page, THREE_KINDS);
    const sent: string[] = [];
    await page.route('**/api/v1/devices/*/accept*', (route) => {
      sent.push(new URL(route.request().url()).pathname);
      return route.fulfill({ status: 202 });
    });
    await page.route('**/api/v1/devices/*/release*', (route) => {
      sent.push(new URL(route.request().url()).pathname);
      return route.fulfill({ status: 204 });
    });
    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');

    const pending = page.getByRole('row').filter({ hasText: '0002PEND' });
    await pending.getByRole('button', { name: 'Accept' }).click();
    const dialog = page.getByRole('dialog', { name: 'Accept device' });
    await expect(dialog.getByRole('button', { name: 'Accept', exact: true })).toBeVisible();
    await dialog.getByRole('button', { name: 'Accept and release' }).click();

    await expect.poll(() => sent).toEqual([
      '/api/v1/devices/0002PEND/accept',
      '/api/v1/devices/0002PEND/release',
    ]);
  });

  test('an entry only a CCU inbox holds is accepted without a release', async ({ page }) => {
    await mockInbox(page, THREE_KINDS);
    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');

    const ccu = page.getByRole('row').filter({ hasText: '0001CCU0' });
    await ccu.getByRole('button', { name: 'Accept' }).click();
    const dialog = page.getByRole('dialog', { name: 'Accept device' });
    await expect(dialog.getByRole('button', { name: 'Accept', exact: true })).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Accept and release' })).toHaveCount(0);
  });

  test('shows a distinct badge and action per state', async ({ page }) => {
    await mockInbox(page, THREE_KINDS);
    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');

    const released = page.getByRole('row').filter({ hasText: '0003RELE' });
    await expect(released).toBeVisible({ timeout: 10000 });
    await expect(released.getByRole('button', { name: 'Release' })).toBeVisible();
    // The two things that must NOT be offered for it.
    await expect(released.getByRole('button', { name: 'Accept' })).toHaveCount(0);
    await expect(released.getByRole('button', { name: /Replace/ })).toHaveCount(0);

    // The negative control: the other two rows keep the accept path, so a
    // view that showed Release everywhere would fail here.
    for (const addr of ['0001CCU0', '0002PEND']) {
      const row = page.getByRole('row').filter({ hasText: addr });
      await expect(row.getByRole('button', { name: 'Accept' })).toBeVisible();
      await expect(row.getByRole('button', { name: 'Release' })).toHaveCount(0);
    }
  });

  test('the two waiting states do not read the same', async ({ page }) => {
    await mockInbox(page, THREE_KINDS);
    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');

    await expect(page.getByText('Awaiting acceptance')).toBeVisible({ timeout: 10000 });
    await expect(page.getByText('Awaiting release')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression
//
// Scoped to #main: the view is what changed, and a full-page baseline here
// would fail for every unrelated navigation or header change.
// ---------------------------------------------------------------------------

test.describe('Inbox onboarding states - visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`inbox onboarding ${theme}`, async ({ page }) => {
      await mockAllApis(page);
      await setTheme(page, theme);
      await mockInbox(page, THREE_KINDS);
      await page.goto('http://localhost:5173/app/#/inbox');
      await page.waitForSelector('#main');
      await expect(page.getByText('Awaiting release')).toBeVisible({ timeout: 10000 });
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page.locator('#main')).toHaveScreenshot(`inbox-onboarding-${theme}.png`);
    });
  }
});
