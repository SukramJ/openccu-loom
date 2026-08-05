import { test, expect, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// The first-run onboarding wizard renders only when GET /setup/status reports
// required:true. mockAllApis defaults it to false (normal login/app flow), so
// these tests override it. auth/me is forced to 401 too: no operator is logged
// in while onboarding runs, so after the wizard finishes the SPA falls through
// to the login screen (which is what the real first-run flow does).
async function mockSetupRequired(page: Page): Promise<void> {
  await mockAllApis(page);
  await page.route('**/api/v1/setup/status', (route) =>
    route.fulfill({ json: { required: true } }),
  );
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({ status: 401, json: { code: 'unauthorized', detail: 'no session' } }),
  );
}

function setTheme(page: Page, theme: 'light' | 'dark'): Promise<void> {
  return page.addInitScript((t) => {
    localStorage.setItem(
      'openccu-loom.prefs.v1',
      JSON.stringify({
        theme: t,
        locale: 'en',
        navCollapsed: false,
        expertMode: false,
        deviceView: 'grid',
      }),
    );
  }, theme);
}

test.describe('Setup wizard', () => {
  test('renders the admin step on first run', async ({ page }) => {
    await mockSetupRequired(page);
    await page.goto('http://localhost:5173/app/');
    await expect(page.getByText('Administrator account')).toBeVisible();
    // The login form must NOT be shown — the wizard takes precedence.
    await expect(page.getByRole('button', { name: 'Next' })).toBeVisible();
  });

  test('completes the flow and posts an atomic payload, then shows login', async ({ page }) => {
    await mockSetupRequired(page);
    let postBody: unknown = null;
    await page.route('**/api/v1/setup', async (route, request) => {
      if (request.method() === 'POST') {
        postBody = request.postDataJSON();
        await route.fulfill({ status: 204, body: '' });
        return;
      }
      await route.fallback();
    });

    await page.goto('http://localhost:5173/app/');
    await expect(page.getByText('Administrator account')).toBeVisible();

    // Step 1 — admin
    await page.locator('input[autocomplete="username"]').fill('admin');
    const pw = page.locator('input[autocomplete="new-password"]');
    await pw.nth(0).fill('supersecret');
    await pw.nth(1).fill('supersecret');
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 2 — language & appearance (defaults are valid)
    await expect(page.getByRole('heading', { name: 'Language & appearance' })).toBeVisible();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 3 — CCU: switch the optional CCU step off (skip it)
    await expect(page.getByRole('heading', { name: 'Connect a CCU' })).toBeVisible();
    await page.getByRole('switch').click();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 4 — MQTT: disabled by default → finish straight away
    await expect(page.getByRole('heading', { name: 'MQTT broker' })).toBeVisible();
    await page.getByRole('button', { name: 'Finish setup' }).click();

    // The wizard posts exactly one atomic payload with admin + locale and no
    // ccu / mqtt (both were skipped).
    await expect.poll(() => postBody).not.toBeNull();
    expect(postBody).toMatchObject({
      admin: { username: 'admin', password: 'supersecret' },
      locale: { locale: 'en', theme: 'system' },
    });
    expect((postBody as Record<string, unknown>).ccu).toBeUndefined();
    expect((postBody as Record<string, unknown>).mqtt).toBeUndefined();

    // After finalizing, the SPA returns to the login screen.
    await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
  });

  test('an authenticated admin is never trapped in the wizard (HA Ingress)', async ({ page }) => {
    // ADR 0044 regression guard: under HA Ingress the request is already
    // authenticated as admin (auth/me 200, kept from mockAllApis) while the
    // daemon still has no persistent auth source (setup/status required:true).
    // The app shell must render — not the onboarding wizard.
    await mockAllApis(page);
    await page.route('**/api/v1/setup/status', (route) =>
      route.fulfill({ json: { required: true } }),
    );

    await page.goto('http://localhost:5173/app/');

    // <main id="main"> is unique to the logged-in branch of App.svelte, so its
    // presence proves the app shell rendered rather than the wizard or login.
    await expect(page.locator('#main')).toBeVisible();
    await expect(page.getByText('Administrator account')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Sign in' })).toHaveCount(0);
  });

  test('Next stays disabled until the admin step is valid', async ({ page }) => {
    await mockSetupRequired(page);
    await page.goto('http://localhost:5173/app/');
    await expect(page.getByText('Administrator account')).toBeVisible();

    const next = page.getByRole('button', { name: 'Next' });
    const pw = page.locator('input[autocomplete="new-password"]');
    await expect(next).toBeDisabled();

    // username + too-short password → still disabled
    await page.locator('input[autocomplete="username"]').fill('admin');
    await pw.nth(0).fill('short'); // < 8 chars
    await pw.nth(1).fill('short');
    await expect(next).toBeDisabled();

    // long but mismatched passwords → still disabled
    await pw.nth(0).fill('longenough');
    await pw.nth(1).fill('different');
    await expect(next).toBeDisabled();

    // matching long passwords → enabled. Re-fill both fields so the valid
    // state comes from a clean entry, and assert the DOM values settled
    // before checking the derived button state (avoids a reactivity-settle
    // race under CI load).
    await pw.nth(0).fill('longenough');
    await pw.nth(1).fill('longenough');
    await expect(pw.nth(0)).toHaveValue('longenough');
    await expect(pw.nth(1)).toHaveValue('longenough');
    await expect(next).toBeEnabled();
  });
});

test.describe('Setup wizard - visual', () => {
  test('admin step light', async ({ page }) => {
    await setTheme(page, 'light');
    await mockSetupRequired(page);
    await page.goto('http://localhost:5173/app/');
    await page.getByRole('heading', { name: 'Administrator account' }).waitFor();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('setup-wizard-light.png');
  });

  test('admin step dark', async ({ page }) => {
    await setTheme(page, 'dark');
    await mockSetupRequired(page);
    await page.goto('http://localhost:5173/app/');
    await page.getByRole('heading', { name: 'Administrator account' }).waitFor();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('setup-wizard-dark.png');
  });
});
