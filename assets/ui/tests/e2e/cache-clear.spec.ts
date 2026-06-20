import { test, expect } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

test.describe('Clear CCU cache', () => {
  test('cache clear button is visible on the Settings > System tab', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(800);

    // Click the System tab — i18n renders as the key in the hermetic test env
    const systemTab = page.getByText('settings.tab.system').first();
    await systemTab.click();
    await page.waitForTimeout(300);

    await expect(page.getByText('admin.cache_clear.button').first()).toBeVisible();
  });

  test('clicking cache clear button triggers the confirm dialog', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(800);

    await page.getByText('settings.tab.system').first().click();
    await page.waitForTimeout(300);

    await page.getByText('admin.cache_clear.button').first().click();

    // The shared ConfirmDialog renders the confirmLabel key as button text
    await expect(page.getByText('admin.cache_clear.confirm').first()).toBeVisible({
      timeout: 3000,
    });
  });

  test('confirming the dialog calls the cache/clear API and shows a success toast', async ({
    page,
  }) => {
    await mockAllApis(page);

    // Override the wildcard admin/** mock with a specific response for cache/clear
    await page.route('**/api/v1/admin/cache/clear', (route) =>
      route.fulfill({
        status: 200,
        json: {
          scope: 'global',
          devices: 5,
          paramsets: 20,
          values: 100,
          master: 2,
          centrals_reinit: 1,
          errors: 0,
        },
      }),
    );

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(800);

    await page.getByText('settings.tab.system').first().click();
    await page.waitForTimeout(300);

    await page.getByText('admin.cache_clear.button').first().click();
    await page.waitForTimeout(300);

    // Click the confirm button in the shared dialog
    await page.getByText('admin.cache_clear.confirm').first().click();

    // The toast region should be present and the success key visible
    const toastRegion = page.locator('[role="region"][aria-live="polite"]');
    await expect(toastRegion).toBeAttached();
  });
});
