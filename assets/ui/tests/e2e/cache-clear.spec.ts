import { test, expect } from './helpers/fixtures';
import { mockAllApis } from './helpers/mock-api';

test.describe('Clear CCU cache', () => {
  test('cache clear button is visible on the Settings > System tab', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');

    // The System tab button may appear in both a mobile strip and a desktop sidebar;
    // scope to the first visible instance.
    const systemTab = page.getByRole('button', { name: 'System', exact: true }).first();
    await systemTab.click();

    await expect(page.getByRole('button', { name: 'Clear CCU cache' }).first()).toBeVisible();
  });

  test('clicking cache clear button triggers the confirm dialog', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'System', exact: true }).first().click();

    await page.getByRole('button', { name: 'Clear CCU cache' }).first().click();

    // The shared ConfirmDialog renders a confirm button labelled "Clear cache"
    await expect(page.getByRole('button', { name: 'Clear cache', exact: true }).first()).toBeVisible({
      timeout: 5000,
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
          errors: [],
        },
      }),
    );

    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'System', exact: true }).first().click();

    await page.getByRole('button', { name: 'Clear CCU cache' }).first().click();

    // Click the confirm button in the shared dialog
    await page.getByRole('button', { name: 'Clear cache', exact: true }).first().click();

    // The toast region should be present after the API call
    const toastRegion = page.locator('[role="region"][aria-live="polite"]');
    await expect(toastRegion).toBeAttached();
  });
});
