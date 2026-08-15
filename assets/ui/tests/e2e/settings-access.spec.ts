import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// User and API-token administration live in Settings, as the tabs
// "Users" and "API Tokens". The standalone #/access view that used to
// carry a second copy of both is gone; the route still resolves and is
// rewritten here, which the first test pins.

const USERS_TAB = 'http://localhost:5173/app/#/settings?tab=users';
const TOKENS_TAB = 'http://localhost:5173/app/#/settings?tab=tokens';

test.describe('Settings — access administration', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    // The tabs are admin-gated; the mocked identity is an admin.
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('the retired #/access route lands on the users tab', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/access');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page).toHaveURL(/#\/settings\?tab=users$/);
    await expect(page.getByRole('heading', { name: 'Users', level: 3 })).toBeVisible();
  });

  test('the retired #/visibility route lands on the hidden-parameters tab', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/visibility');
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page).toHaveURL(/#\/settings\?tab=visibility$/);
    await expect(
      page.getByRole('heading', { name: 'Hidden parameters', level: 3 }),
    ).toBeVisible();
  });

  test('renders the user and token tabs', async ({ page }) => {
    await page.goto(USERS_TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);
    await expect(page.getByRole('heading', { name: 'Users', level: 3 })).toBeVisible();

    await page.goto(TOKENS_TAB);
    await page.waitForTimeout(500);
    await expect(page.getByRole('heading', { name: 'API tokens', level: 3 })).toBeVisible();
  });

  test('create user flow shows success toast', async ({ page }) => {
    // Override POST /users specifically (GET already mocked by mockAllApis).
    await page.route('**/api/v1/users', (route) => {
      if (route.request().method() === 'POST') {
        return route.fulfill({ json: { subject: 'new-user', role: 'viewer' } });
      }
      return route.fulfill({ json: [{ subject: 'admin', role: 'admin' }] });
    });

    await page.goto(USERS_TAB);
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

    // The Toaster's live region is in the DOM on every page, so asserting
    // on it proves nothing — it is attached before any toast is raised.
    // The toast itself is the role="alert" child that exists only once
    // toastStore.success has run.
    await expect(
      page.getByRole('alert').filter({ hasText: 'User created.' }),
    ).toBeVisible({ timeout: 5000 });
  });

  test('delete user opens confirm dialog', async ({ page }) => {
    await page.goto(USERS_TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Click the first Delete button (user row action).
    await page.getByRole('button', { name: 'Delete' }).first().click();

    // Assert on the dialog, not on a "Delete" button: the row action
    // carries that exact label too, so a button query stays visible when
    // no dialog opened at all. The shared ConfirmDialog is labelled with
    // its title and holds the destructive confirm button.
    const dialog = page.getByRole('dialog', { name: 'Remove user?' });
    await expect(dialog).toBeVisible({ timeout: 5000 });
    await expect(dialog.getByRole('button', { name: 'Delete', exact: true })).toBeVisible();
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

    await page.goto(TOKENS_TAB);
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

test.describe('Settings access - visual light', () => {
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

  test('settings users light', async ({ page }) => {
    await page.goto(USERS_TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-users-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Settings access - visual dark', () => {
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

  test('settings users dark', async ({ page }) => {
    await page.goto(USERS_TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-users-dark.png');
  });
});
