import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// #/licenses renders the CycloneDX SBOM (fixtures/sbom.json: 6 components —
// svelte + type-fest + tailwindcss as npm packages, go-fabric + modernc.org/
// sqlite + golang.org/x/crypto as Go modules) as a searchable table, plus
// the product line, the MIT disclaimer and the download link.

test.describe('Licenses', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false }),
      );
    });
  });

  test('renders the component table from the fixture', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/licenses');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByRole('heading', { name: 'Licenses', level: 1 })).toBeVisible();

    // Product line + license/author summary.
    await expect(page.getByText('OpenCCU-Loom v0.2.0')).toBeVisible();
    await expect(page.getByText('SukramJ', { exact: true })).toBeVisible();

    // Disclaimer panel — localized title, verbatim MIT warranty text.
    await expect(page.getByRole('heading', { name: 'Disclaimer of Warranty' })).toBeVisible();
    await expect(page.getByText(/THE SOFTWARE IS PROVIDED "AS IS"/)).toBeVisible();

    // Table rows from the fixture: one npm package, one Go module.
    await expect(page.getByText('svelte', { exact: true })).toBeVisible();
    await expect(page.getByText('5.16.0', { exact: true })).toBeVisible();
    await expect(page.getByText('github.com/SukramJ/go-fabric', { exact: true })).toBeVisible();

    // Download link carries ?download=1.
    const downloadLink = page.getByRole('link', { name: 'Download SBOM' });
    await expect(downloadLink).toHaveAttribute('href', /\/api\/v1\/sbom\?download=1$/);
  });

  test('the search box narrows the table', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/licenses');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByText('svelte', { exact: true })).toBeVisible();
    await expect(page.getByText('tailwindcss', { exact: true })).toBeVisible();

    await page.getByPlaceholder('Search name, version, license, author').fill('svelte');
    await page.waitForTimeout(200);

    await expect(page.getByText('svelte', { exact: true })).toBeVisible();
    await expect(page.getByText('tailwindcss', { exact: true })).toHaveCount(0);
  });

  test('renders a friendly empty state when the build carries no SBOM', async ({ page }) => {
    // Overrides the sbom.json fixture registered by mockAllApis — Playwright
    // resolves the most recently registered matching route first.
    await page.route('**/api/v1/sbom*', (route) =>
      route.fulfill({
        status: 404,
        contentType: 'application/problem+json',
        body: JSON.stringify({
          type: 'about:blank',
          title: 'No SBOM in this build',
          status: 404,
          detail: 'this binary was built without `make sbom`',
        }),
      }),
    );

    await page.goto('http://localhost:5173/app/#/licenses');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByRole('heading', { name: 'Licenses', level: 1 })).toBeVisible();
    await expect(page.getByText('This build carries no SBOM')).toBeVisible();
    await expect(page.getByText('svelte', { exact: true })).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Licenses - visual light', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false }),
      );
    });
  });

  test('licenses light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/licenses');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'Licenses', level: 1 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('licenses-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Licenses - visual dark', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'dark', locale: 'en', navCollapsed: false, expertMode: false }),
      );
    });
  });

  test('licenses dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/licenses');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'Licenses', level: 1 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('licenses-dark.png');
  });
});
