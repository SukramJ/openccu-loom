import { test, expect, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// openccu-lite in the SPA: the setup wizard identifies a box, pins its
// certificate and pairs with it; the navigation drops views no configured
// system serves; the CCU form edits a box without username and password.

const FINGERPRINT = '3f'.repeat(32);

function setTheme(page: Page, theme: 'light' | 'dark'): Promise<void> {
  return page.addInitScript((t) => {
    localStorage.setItem(
      'openccu-loom.prefs.v1',
      JSON.stringify({ theme: t, locale: 'en', navCollapsed: false, expertMode: false }),
    );
  }, theme);
}

async function mockSetupRequired(page: Page): Promise<void> {
  await mockAllApis(page);
  await page.route('**/api/v1/setup/status', (route) => route.fulfill({ json: { required: true } }));
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({ status: 401, json: { code: 'unauthorized', detail: 'no session' } }),
  );
}

// A box that answers over plain HTTP and over HTTPS with a certificate no
// authority signed. `approve` decides whether the long poll ever answers:
// left pending, the wizard stays on the code.
async function mockLiteBox(page: Page, approve: boolean): Promise<void> {
  await page.route('**/api/v1/setup/probe', async (route, request) => {
    const body = request.postDataJSON() as { tls?: boolean };
    await route.fulfill({
      json: {
        system_type: 'openccu-lite',
        ready: true,
        ...(body.tls ? { tls_fingerprint: FINGERPRINT } : {}),
        lite: {
          implementation: 'occulited',
          api_majors: { rpc: 1, meta: 1, auth: 1 },
          pairing_available: true,
          hmip_key_mode: { keyserver_mode: 'local', device_keys: 12, offline_pairing: true },
        },
      },
    });
  });
  await page.route('**/api/v1/setup/pairing', (route) =>
    route.fulfill({
      status: 201,
      json: { pairing_id: 'p-e2e', code: '042317', fingerprint: FINGERPRINT, expires_in: 300 },
    }),
  );
  await page.route('**/api/v1/setup/pairing/p-e2e**', (route) => {
    if (route.request().method() === 'DELETE') return route.fulfill({ status: 204, body: '' });
    if (!approve) return; // never answered: the pairing stays pending
    return route.fulfill({ json: { state: 'approved', scopes: ['rpc:read', 'rpc:operate', 'meta:read'] } });
  });
}

async function toCCUStep(page: Page): Promise<void> {
  await page.goto('http://localhost:5173/app/');
  await expect(page.getByText('Administrator account')).toBeVisible();
  await page.locator('input[autocomplete="username"]').fill('admin');
  const pw = page.locator('input[autocomplete="new-password"]');
  await pw.nth(0).fill('supersecret');
  await pw.nth(1).fill('supersecret');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Connect a CCU' })).toBeVisible();
}

async function identifyAndConfirm(page: Page): Promise<void> {
  await page.getByLabel('Name', { exact: true }).fill('box');
  await page.getByLabel('Host', { exact: true }).fill('192.0.2.50');
  await page.getByRole('button', { name: 'Identify system' }).click();
  await expect(page.getByText(FINGERPRINT)).toBeVisible();
  await page.getByLabel('I compared this fingerprint with the one the box shows').check();
}

test.describe('openccu-lite onboarding', () => {
  test('the wizard pairs with a box and posts the pairing, never a token', async ({ page }) => {
    await mockSetupRequired(page);
    await mockLiteBox(page, true);
    let postBody: Record<string, unknown> | null = null;
    await page.route('**/api/v1/setup', async (route, request) => {
      if (request.method() === 'POST') {
        postBody = request.postDataJSON();
        await route.fulfill({ status: 204, body: '' });
        return;
      }
      await route.fallback();
    });

    await toCCUStep(page);
    await identifyAndConfirm(page);
    // A box has no username, password or CUxD.
    await expect(page.getByText('Username')).toHaveCount(0);
    await expect(page.getByText('CUxD')).toHaveCount(0);

    await page.getByRole('button', { name: 'Start pairing' }).click();
    await expect(page.getByText('Approved — the token stays in the daemon')).toBeVisible();
    await page.getByLabel('HmIP-RF').check();
    await page.getByRole('button', { name: 'Next' }).click();
    await page.getByRole('button', { name: 'Finish setup' }).click();

    await expect.poll(() => postBody).not.toBeNull();
    const ccu = (postBody as unknown as { ccu: Record<string, unknown> }).ccu;
    expect(ccu).toMatchObject({
      name: 'box',
      host: '192.0.2.50',
      system_type: 'openccu-lite',
      tls: true,
      tls_fingerprint: FINGERPRINT,
      pairing_id: 'p-e2e',
      interfaces: ['HmIP-RF'],
    });
    expect(ccu.api_token).toBeUndefined();
    expect(ccu.username).toBeUndefined();
    expect(ccu.password).toBeUndefined();
  });

  test('the navigation drops views no configured system serves', async ({ page }) => {
    await mockAllApis(page);
    await page.route('**/api/v1/system/ccu', (route) =>
      route.fulfill({
        json: {
          entries: [
            {
              name: 'box',
              host: '192.0.2.50',
              available: true,
              is_ha_app: false,
              configured_interfaces: ['HmIP-RF'],
              system_type: 'openccu-lite',
              features: {
                'hub.programs': { available: false, reason: 'not_supported_by_system' },
                'hub.sysvars': { available: false, reason: 'not_supported_by_system' },
                'hub.inbox': { available: false, reason: 'not_supported_by_system' },
              },
              readiness: { phase: 'ready', ready: true, interfaces_loaded: 1, interfaces_total: 1 },
            },
          ],
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/devices');
    const nav = page.locator('aside nav');
    await expect(nav.getByRole('link', { name: 'Devices', exact: true })).toBeVisible();
    await expect(nav.getByRole('link', { name: 'Programs' })).toHaveCount(0);
    await expect(nav.getByRole('link', { name: 'Variables' })).toHaveCount(0);
    // The new-devices view lists the daemon's own hold, which every system
    // has; it does not depend on the CCU inbox.
    await expect(nav.getByRole('link', { name: 'New devices' })).toBeVisible();

    // A bookmark still opens the view, which says why it is empty.
    await page.route('**/api/v1/programs*', (route) => route.fulfill({ json: [] }));
    await page.goto('http://localhost:5173/app/#/programs');
    await expect(page.getByText('Programs: not available on any configured system')).toBeVisible();
    await expect(page.getByText('box: openccu-lite does not offer it')).toBeVisible();
  });

  test('the CCU form edits a box without username and password', async ({ page }) => {
    await mockAllApis(page);
    await page.route('**/api/v1/centrals', (route) =>
      route.fulfill({
        json: [
          {
            name: 'box',
            host: '192.0.2.50',
            enabled: true,
            system_type: 'openccu-lite',
            api_token_plain: '***',
            tls: true,
            tls_fingerprint: FINGERPRINT,
            interfaces: [{ name: 'HmIP-RF' }],
          },
        ],
      }),
    );
    await page.goto('http://localhost:5173/app/#/settings?tab=ccus');
    await page.getByRole('button', { name: 'Edit' }).first().click();
    await expect(page.getByRole('heading', { name: 'Edit CCU' })).toBeVisible();
    await expect(page.getByText('Username')).toHaveCount(0);
    await expect(page.getByRole('tab', { name: 'Pair' })).toBeVisible();
  });
});

test.describe('openccu-lite onboarding - visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`wizard pairing code ${theme}`, async ({ page }) => {
      await setTheme(page, theme);
      await mockSetupRequired(page);
      await mockLiteBox(page, false);
      await toCCUStep(page);
      await identifyAndConfirm(page);
      await page.getByRole('button', { name: 'Start pairing' }).click();
      await expect(page.getByTestId('pairing-code')).toHaveText('042317');
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`lite-wizard-pairing-${theme}.png`, { fullPage: true });
    });
  }
});
