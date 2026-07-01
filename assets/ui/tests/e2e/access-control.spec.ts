import { test, expect } from '@playwright/test';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

test.describe('Access Control', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    // Ensure the SPA treats the session as admin so the route renders.
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('renders user and token sections', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);
    await expect(page.getByRole('heading', { name: 'Users', level: 2 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'API tokens', level: 2 })).toBeVisible();
  });

  test('create user flow shows success toast', async ({ page }) => {
    // Override POST /users specifically (GET already mocked by mockAllApis).
    await page.route('**/api/v1/users', (route) => {
      if (route.request().method() === 'POST') {
        return route.fulfill({ json: { subject: 'new-user', role: 'viewer' } });
      }
      return route.fulfill({ json: [{ subject: 'admin', role: 'admin' }] });
    });

    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByRole('button', { name: 'Add user' }).click();

    // The username input has no explicit `type` attribute (Input.svelte omits
    // it when unset), so `input[type="text"]` would not match — target it by
    // its wrapping <label> text ("Username") via the textbox role instead.
    await page.getByRole('textbox', { name: /Username/i }).fill('new-user');
    // Password input carries an explicit type=password.
    await page.locator('input[type="password"]').first().fill('password123');

    // Submit button is labelled "Add" (exact) — the toggle now reads "Cancel".
    await page.getByRole('button', { name: 'Add', exact: true }).click();

    // Toast region is attached (Toaster is always in the DOM).
    const toastRegion = page.locator('[role="region"][aria-live="polite"]');
    await expect(toastRegion).toBeAttached({ timeout: 5000 });
  });

  test('delete user opens confirm dialog', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Click the first Delete button (user row action).
    await page.getByRole('button', { name: 'Delete' }).first().click();

    // The shared ConfirmDialog renders a destructive confirm button labelled "Delete".
    await expect(
      page.getByRole('button', { name: 'Delete', exact: true }).last(),
    ).toBeVisible({ timeout: 5000 });
  });

  test('create token shows one-time plaintext token', async ({ page }) => {
    await page.route('**/api/v1/auth/tokens/v2', (route) => {
      if (route.request().method() === 'POST') {
        return route.fulfill({
          json: { token: 'secret-token-abc123', fingerprint: '…abc123' },
        });
      }
      return route.fulfill({
        json: [{ fingerprint: '…abc123', subject: 'admin', role: 'admin' }],
      });
    });

    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByRole('button', { name: 'Create token' }).first().click();

    // Subject input has no explicit `type` — target it via its <label> text.
    await page.getByRole('textbox', { name: /Subject/i }).fill('myservice');

    // Once the form is open the toggle reads "Cancel", so the only remaining
    // "Create token" button is the submit.
    await page.getByRole('button', { name: 'Create token', exact: true }).last().click();

    await expect(page.locator('[data-testid="token-value"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="token-value"]')).toContainText('secret-token-abc123');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Access Control - visual light', () => {
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

  test('access control light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('access-control-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Access Control - visual dark', () => {
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

  test('access control dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('access-control-dark.png');
  });
});
