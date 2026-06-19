import { test, expect } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

test.describe('App Shell', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('renders authenticated app shell', async ({ page }) => {
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    await expect(page.locator('#main')).toBeVisible();
  });

  test('skip-to-content link exists', async ({ page }) => {
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    const skipLink = page.locator('a[href="#main"]');
    await expect(skipLink).toBeAttached();
  });

  test('navigates to Sysvars via hash', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);
    await expect(page.locator('#main')).toBeVisible();
  });

  test('navigates to Settings via hash', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await expect(page.locator('#main')).toBeVisible();
  });

  test('document title is set', async ({ page }) => {
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    const title = await page.title();
    expect(title.length).toBeGreaterThan(0);
  });
});
