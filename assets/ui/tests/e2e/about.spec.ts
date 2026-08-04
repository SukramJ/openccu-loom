import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockFleet, addStylesForStableScreenshots } from './helpers/mock-api';

// #/about surfaces daemon build info (fixtures/info.json: version "0.2.0",
// api_version "2.15.0", addon_build false, three capabilities), per-CCU
// identity via mockFleet (ccu1 online with model/firmware/serial, ccu2
// offline with none of those) and the license/link card. system-update.json
// is an empty array, so no "Update … available" badge is expected here.

test.describe('About', () => {
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

  test('renders daemon info, per-CCU identity and license links', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/about');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByRole('heading', { name: 'About', level: 1 })).toBeVisible();

    // Daemon card. Scope the version assertion to #main — the sidebar
    // footer also renders "v0.2.0" as a link to this same route.
    await expect(page.getByRole('heading', { name: 'Daemon', level: 2 })).toBeVisible();
    await expect(page.locator('#main').getByText('v0.2.0', { exact: true })).toBeVisible();
    await expect(page.getByText('2.15.0', { exact: true })).toBeVisible();
    await expect(page.getByText('Standalone (binary, Docker, or HA add-on)')).toBeVisible();
    await expect(page.getByText('rest.v1', { exact: true })).toBeVisible();
    await expect(page.getByText('ws.broadcasts.v1', { exact: true })).toBeVisible();
    await expect(page.getByText('errors.problem_details.v1', { exact: true })).toBeVisible();

    // Centrals card: ccu1 (online, CCU3, firmware 3.75.7, serial SERIAL0001)
    // and ccu2 (offline, no model/firmware/serial reported).
    await expect(page.getByRole('heading', { name: 'Centrals', level: 2 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'ccu1', level: 3 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'ccu2', level: 3 })).toBeVisible();
    await expect(page.getByText('CCU3', { exact: true })).toBeVisible();
    await expect(page.getByText('3.75.7', { exact: true })).toBeVisible();
    await expect(page.getByText('SERIAL0001', { exact: true })).toBeVisible();
    await expect(page.getByText('Online', { exact: true })).toBeVisible();
    await expect(page.getByText('Offline', { exact: true })).toBeVisible();
    // No pending firmware update in fixtures/system-update.json ([]).
    await expect(page.getByText(/Update .* available/)).toHaveCount(0);

    // License & links card.
    await expect(page.getByRole('heading', { name: 'License & links', level: 2 })).toBeVisible();
    await expect(page.getByRole('link', { name: 'GitHub' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Releases & changelog' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Third-party notices' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'User guide' })).toBeVisible();
  });

  test('sidebar exposes an About nav item and a footer version link to it', async ({ page }) => {
    await page.goto('http://localhost:5173/app/');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    const navLink = page.getByRole('link', { name: 'About', exact: true });
    await expect(navLink).toBeVisible();
    await expect(navLink).toHaveAttribute('href', '#/about');

    const footerLink = page.getByRole('link', { name: 'v0.2.0' });
    await expect(footerLink).toBeVisible();
    await expect(footerLink).toHaveAttribute('href', '#/about');

    await footerLink.click();
    await page.waitForTimeout(500);
    await expect(page.getByRole('heading', { name: 'About', level: 1 })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('About - visual light', () => {
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

  test('about light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/about');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'About', level: 1 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('about-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('About - visual dark', () => {
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

  test('about dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/about');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'About', level: 1 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('about-dark.png');
  });
});
