import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

/**
 * The session-expiry banner. A session is an absolute 12h window that
 * activity does not extend, and the daemon closes the WebSocket the moment
 * it lapses — so without a warning the first sign of an expired session is
 * a bounce to the login screen mid-task.
 *
 * The banner is driven entirely by the `expires_at` field on `/auth/me`,
 * which the shared fixture deliberately omits: absent means "no
 * server-side expiry", which is what a Basic-auth or HA Ingress deployment
 * looks like. Each test here overrides that one route.
 */

/**
 * The suite freezes the page clock (mock-api.ts) so pixel baselines stay
 * deterministic, and `Date.now()` inside the SPA never leaves that instant.
 * A deadline computed from the *test process* clock would therefore land
 * months away from the browser's "now" — the banner's whole input is a
 * difference against a clock the test does not share by default.
 */
const FROZEN_NOW = new Date('2026-01-01T09:00:00Z').getTime();

async function mockIdentityExpiringIn(page: import('@playwright/test').Page, ms: number) {
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({
      json: {
        subject: 'admin',
        role: 'admin',
        scheme: 'session',
        expires_at: new Date(FROZEN_NOW + ms).toISOString(),
      },
    }),
  );
}

async function setTheme(
  page: import('@playwright/test').Page,
  theme: 'light' | 'dark',
  locale: 'en' | 'de' = 'en',
) {
  await page.addInitScript(
    ([t, l]) => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: t,
          locale: l,
          navCollapsed: false,
          expertMode: false,
          deviceView: 'grid',
        }),
      );
    },
    [theme, locale] as const,
  );
}

test.describe('Session expiry banner', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('warns when the session is inside the warning window', async ({ page }) => {
    await mockIdentityExpiringIn(page, 8 * 60 * 1000);
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');

    const banner = page.getByRole('status').filter({ hasText: 'session ends in' });
    await expect(banner).toBeVisible({ timeout: 10000 });
    await expect(banner.getByRole('button', { name: 'Sign in again' })).toBeVisible();
  });

  // The negative control. Same page, same shell, one field removed from the
  // identity — if the banner still rendered, the test above would be
  // measuring the app shell rather than the deadline.
  test('stays away when the credential has no expiry', async ({ page }) => {
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByRole('status').filter({ hasText: 'session ends in' })).toHaveCount(0);
  });

  test('stays away while the deadline is still far off', async ({ page }) => {
    await mockIdentityExpiringIn(page, 6 * 60 * 60 * 1000);
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByRole('status').filter({ hasText: 'session ends in' })).toHaveCount(0);
  });

  test('renders in dark mode', async ({ page }) => {
    await setTheme(page, 'dark');
    await mockIdentityExpiringIn(page, 8 * 60 * 1000);
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');

    const banner = page.getByRole('status').filter({ hasText: 'session ends in' });
    await expect(banner).toBeVisible({ timeout: 10000 });
    // The dark palette must be carried by the element itself, not inherited
    // from the shell: a banner styled for light mode alone is unreadable
    // here and no assertion on visibility would notice.
    await expect(banner).toHaveClass(/dark:bg-amber-950/);
  });

  test('is localized', async ({ page }) => {
    await setTheme(page, 'light', 'de');
    await mockIdentityExpiringIn(page, 8 * 60 * 1000);
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');

    const banner = page.getByRole('status').filter({ hasText: 'Sitzung endet in' });
    await expect(banner).toBeVisible({ timeout: 10000 });
    await expect(banner.getByRole('button', { name: 'Neu anmelden' })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — the banner element itself
//
// Scoped to the element rather than the page: the shell around it is already
// covered by visual.spec.ts, and a full-page baseline here would fail for
// every unrelated change to the navigation or the device list.
// ---------------------------------------------------------------------------

test.describe('Session expiry banner - visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`banner ${theme}`, async ({ page }) => {
      await mockAllApis(page);
      await setTheme(page, theme);
      await mockIdentityExpiringIn(page, 8 * 60 * 1000);
      await page.goto('http://localhost:5173/app/');
      await page.waitForSelector('#main');

      const banner = page.getByRole('status').filter({ hasText: 'session ends in' });
      await expect(banner).toBeVisible({ timeout: 10000 });
      await addStylesForStableScreenshots(page);
      await expect(banner).toHaveScreenshot(`session-expiry-banner-${theme}.png`);
    });
  }
});
