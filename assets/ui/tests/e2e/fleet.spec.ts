import { test, expect } from '@playwright/test';
import { mockAllApis, mockFleet, addStylesForStableScreenshots } from './helpers/mock-api';

// Read-only cross-CCU fleet overview (#/fleet). mockFleet serves two CCUs:
// ccu1 (online, CCU3, HmIP-RF + BidCos-RF, one device) and ccu2 (offline,
// HmIP-RF, no devices).

test.describe('Fleet', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockFleet(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('renders a card per CCU with status, interfaces and device counts', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await expect(page.getByRole('heading', { name: 'ccu2', level: 2 })).toBeVisible();

    // Availability badges: ccu1 online, ccu2 offline.
    await expect(page.getByText('Online', { exact: true })).toBeVisible();
    await expect(page.getByText('Offline', { exact: true })).toBeVisible();

    // Interface chips (ccu1 has both; ccu2 has HmIP-RF).
    await expect(page.getByText('BidCos-RF', { exact: true })).toBeVisible();

    // ccu1 host + model surface.
    await expect(page.getByText('172.18.4.29')).toBeVisible();
    await expect(page.getByText('CCU3')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Fleet - visual light', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockFleet(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('fleet light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('fleet-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Fleet - visual dark', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockFleet(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'dark', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('fleet dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('fleet-dark.png');
  });
});
