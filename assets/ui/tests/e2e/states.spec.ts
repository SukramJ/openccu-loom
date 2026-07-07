import { test, expect } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

test.describe('Shared States', () => {
  test('shows empty state when sysvars returns empty array', async ({ page }) => {
    await mockAllApis(page);
    await page.route('**/api/v1/sysvars*', async (route) => {
      await route.fulfill({ json: [] });
    });

    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1000);

    const emptyEl = page.locator('.text-slate-500, .text-slate-400').first();
    await expect(emptyEl).toBeVisible();
  });

  test('shows error state when sysvars returns 500', async ({ page }) => {
    await mockAllApis(page);
    await page.route('**/api/v1/sysvars*', async (route) => {
      await route.fulfill({ status: 500, body: JSON.stringify({ detail: 'internal error' }) });
    });

    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1000);

    const errorEl = page.locator('.text-red-600, .text-red-400').first();
    await expect(errorEl).toBeVisible();
  });

  test('shows loading state initially when response is delayed', async ({ page }) => {
    await mockAllApis(page);
    let resolveDelay!: () => void;
    const delay = new Promise<void>((res) => { resolveDelay = res; });

    await page.route('**/api/v1/sysvars*', async (route) => {
      await delay;
      await route.fulfill({ json: [] });
    });

    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');

    const loadingEl = page.locator('[role="status"]').first();
    await expect(loadingEl).toBeVisible();

    resolveDelay();
  });
});
