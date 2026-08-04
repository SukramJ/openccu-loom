import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockOverviewFleet, addStylesForStableScreenshots } from './helpers/mock-api';

// Fleet Overview route (roadmap B8). The mocked fleet (see
// mockOverviewFleet) spans two centrals: ccu1 has "Kitchen" (an
// orphan-sensor AutoTile device) and "Living Room" (a CDP switch-tile
// device); ccu2 has "Office". Default grouping is by room, and the
// first group (alphabetically "Kitchen · ccu1") auto-expands on load.

test.describe('Overview', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockOverviewFleet(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: 'light',
          locale: 'en',
          navCollapsed: false,
          expertMode: false,
          deviceView: 'grid',
        }),
      );
    });
  });

  test('renders grouped tiles, auto-expanding the first group', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // All three room groups render as headers (multi-CCU: every label
    // carries its central since more than one CCU is present).
    await expect(page.getByText('Kitchen · ccu1')).toBeVisible();
    await expect(page.getByText('Living Room · ccu1')).toBeVisible();
    await expect(page.getByText('Office · ccu2')).toBeVisible();

    // First group (Kitchen · ccu1) is auto-expanded and lazily loaded —
    // its orphan temperature channel renders through the AutoTile
    // fallback path.
    await expect(page.getByText('21.5', { exact: false })).toBeVisible({ timeout: 5000 });

    // Living Room is still collapsed — its CDP switch tile is not
    // rendered yet.
    await expect(page.getByText('Switch', { exact: true })).toHaveCount(0);

    // Expand it: the CDP dispatch path (SwitchTile) now renders.
    await page.getByText('Living Room · ccu1').click();
    await expect(page.getByText('Switch', { exact: true })).toBeVisible({ timeout: 5000 });
  });

  test('central filter narrows the group set', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByText('Office · ccu2')).toBeVisible();

    await page.getByTitle('CCU').selectOption('ccu1');

    await expect(page.getByText('Office · ccu2')).toHaveCount(0);
    await expect(page.getByText('Kitchen · ccu1')).toBeVisible();
    await expect(page.getByText('Living Room · ccu1')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Overview - visual light', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockOverviewFleet(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: 'light',
          locale: 'en',
          navCollapsed: false,
          expertMode: false,
          deviceView: 'grid',
        }),
      );
    });
  });

  test('overview light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');
    // Wait for the grouped skeleton to render (device list loaded) rather
    // than a fixed timeout, so the shot never catches the loading spinner.
    await expect(page.getByText('Kitchen · ccu1')).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('overview-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Overview - visual dark', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockOverviewFleet(page);
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
  });

  test('overview dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');
    // Wait for the grouped skeleton to render (device list loaded) rather
    // than a fixed timeout, so the shot never catches the loading spinner.
    await expect(page.getByText('Kitchen · ccu1')).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('overview-dark.png');
  });
});
