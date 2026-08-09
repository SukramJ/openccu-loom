import { test, expect } from './helpers/fixtures';
import {
  mockAllApis,
  mockHiddenSurfaces,
  addStylesForStableScreenshots,
} from './helpers/mock-api';

// The fleet-wide schedule overview. Its value is the question it answers
// — "which devices have a week schedule at all" — so the tests check
// that both kinds are listed and that a row leads to the editor.

const URL = 'http://localhost:5173/app/#/schedules';

test.describe('Schedules overview', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('lists both schedule kinds with their device identity', async ({ page }) => {
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByText('Bad Heizung')).toBeVisible();
    await expect(page.getByText('Küche Licht')).toBeVisible();
    // The kind badge is what separates a thermostat (profile in MASTER)
    // from a device with a dedicated week-profile channel.
    await expect(page.getByText('Thermostat')).toBeVisible();
    await expect(page.getByText('Week profile')).toBeVisible();
  });

  test('a row links to the device schedule editor', async ({ page }) => {
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    const row = page.getByRole('link', { name: /Bad Heizung/ });
    await expect(row).toHaveAttribute('href', /#\/devices\/0001D3C99C1234\?tab=schedule/);
  });

  test('search narrows the list', async ({ page }) => {
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByRole('searchbox').fill('eTRV');
    await expect(page.getByText('Bad Heizung')).toBeVisible();
    await expect(page.getByText('Küche Licht')).not.toBeVisible();
  });

  // With the device's schedule tab hidden by the surface profile, the
  // rows must stop linking. The state is only reachable through a real
  // payload, and it is the one that shipped broken: every row still
  // offered the jump and landed on a device without that tab.
  test('drops the link when the device schedule editor is hidden', async ({ page }) => {
    await mockHiddenSurfaces(page, ['device.configure.schedule']);

    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // The catalogue keeps its rows — it answers a question the device
    // detail cannot answer at all.
    await expect(page.getByText('Bad Heizung')).toBeVisible();
    await expect(page.getByRole('link', { name: /Bad Heizung/ })).toHaveCount(0);
    await expect(page.getByText(/schedule editor is hidden in this profile/i)).toBeVisible();
  });

  test('schedules light', async ({ page }) => {
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('schedules-light.png');
  });

  test('schedules dark', async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: 'dark',
          locale: 'en',
          navCollapsed: false,
          expertMode: false,
          deviceView: 'grid',
        }),
      );
    });
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('schedules-dark.png');
  });
});
