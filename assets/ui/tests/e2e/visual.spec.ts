import { test, expect } from '@playwright/test';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

test.describe('Visual regression - light mode', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
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

  test('DeviceList light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/devices');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('device-list-light.png');
  });

  test('Settings light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-light.png');
  });

  test('Diagnostics light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/diagnostics');
    await page.waitForSelector('#main');
    await page.waitForTimeout(2000);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('diagnostics-light.png');
  });

  test('Sysvars empty state light', async ({ page }) => {
    await page.route('**/api/v1/sysvars', async (route) => {
      await route.fulfill({ json: [] });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-empty-light.png');
  });

  test('Sysvars error state light', async ({ page }) => {
    await page.route('**/api/v1/sysvars', async (route) => {
      await route.fulfill({ status: 500, body: JSON.stringify({ detail: 'server error' }) });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-error-light.png');
  });

  test('Alarm light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('alarm-light.png');
  });
});

test.describe('Visual regression - dark mode', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
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

  test('DeviceList dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/devices');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('device-list-dark.png');
  });

  test('Settings dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-dark.png');
  });

  test('Diagnostics dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/diagnostics');
    await page.waitForSelector('#main');
    await page.waitForTimeout(2000);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('diagnostics-dark.png');
  });

  test('Sysvars empty state dark', async ({ page }) => {
    await page.route('**/api/v1/sysvars', async (route) => {
      await route.fulfill({ json: [] });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-empty-dark.png');
  });

  test('Alarm dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('alarm-dark.png');
  });
});
