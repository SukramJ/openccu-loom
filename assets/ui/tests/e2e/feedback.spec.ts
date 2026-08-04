import { test, expect } from './helpers/fixtures';
import { mockAllApis } from './helpers/mock-api';

test.describe('Feedback', () => {
  test('toast region is present in DOM', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1000);

    const toastRegion = page.locator('[role="region"][aria-live="polite"]');
    await expect(toastRegion).toBeAttached();
  });

  test('page loads correctly on sysvars route', async ({ page }) => {
    await mockAllApis(page);

    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1000);

    await expect(page.locator('#main')).toBeVisible();
  });
});
