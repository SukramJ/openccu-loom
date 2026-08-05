import { test, expect } from './helpers/fixtures';
import { readFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { mockAllApis, mockEnergy, mockEnergyDisabled, addStylesForStableScreenshots } from './helpers/mock-api';

const __dirname = dirname(fileURLToPath(import.meta.url));
const energyFixture = JSON.parse(readFileSync(join(__dirname, 'fixtures/energy.json'), 'utf-8'));

test.describe('Energy', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockEnergy(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('renders totals, the per-device table and the consumption chart', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/energy');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Range totals: total_consumed_wh 9218 / 1000, total_feed_in_wh 2300 / 1000.
    // Scope to the big totals value elements (`.text-2xl.font-bold`) — the
    // per-device table also renders "2.30 kWh" (Balkonkraftwerk's feed-in),
    // so an unscoped getByText would hit two elements.
    const totals = page.locator('.text-2xl.font-bold');
    await expect(totals.filter({ hasText: '9.22 kWh' })).toBeVisible();
    await expect(totals.filter({ hasText: '2.30 kWh' })).toBeVisible();

    // Per-device breakdown table.
    await expect(page.getByRole('heading', { name: 'Per-device breakdown' })).toBeVisible();
    await expect(page.getByText('Bücherregal')).toBeVisible();
    await expect(page.getByText('Balkonkraftwerk')).toBeVisible();

    // The reset badge/footnote only appears for "Bücherregal" (one bucket has reset: true).
    await expect(page.getByText('reset', { exact: true })).toBeVisible();

    // The consumption chart renders an SVG line chart.
    await expect(page.locator('svg[aria-label="Consumption over time"]')).toBeVisible();
  });

  test('changing the group selector re-queries the energy endpoint', async ({ page }) => {
    const requestedGroups: string[] = [];
    await page.route('**/api/v1/energy**', (route) => {
      const url = new URL(route.request().url());
      requestedGroups.push(url.searchParams.get('group') ?? 'day');
      return route.fulfill({ json: energyFixture });
    });

    await page.goto('http://localhost:5173/app/#/energy');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // The shared Select.svelte (bits-ui) renders a custom dropdown: the
    // trigger is a plain <button> (aria-haspopup="listbox", no combobox
    // role), and options are role="option" in a portalled listbox. Drive
    // it by clicking the trigger button inside the "Group by" label, then
    // clicking the "Hour" option.
    await page.locator('label:has-text("Group by") button').click();
    await page.getByRole('option', { name: 'Hour' }).click();

    await page.waitForTimeout(500);
    expect(requestedGroups).toContain('hour');
  });

  test('shows the feature-off state when the daemon returns 404', async ({ page }) => {
    await mockEnergyDisabled(page);

    await page.goto('http://localhost:5173/app/#/energy');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByText('History recording is off')).toBeVisible();
    await expect(page.getByText('Open settings')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Energy - visual light', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockEnergy(page);
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

  test('energy light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/energy');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('energy-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Energy - visual dark', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockEnergy(page);
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

  test('energy dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/energy');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('energy-dark.png');
  });
});
