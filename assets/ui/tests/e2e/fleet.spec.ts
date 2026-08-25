import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockFleet, addStylesForStableScreenshots } from './helpers/mock-api';

// Read-only cross-CCU fleet overview (#/fleet). mockFleet serves two CCUs:
// ccu1 (online, CCU3, HmIP-RF + BidCos-RF, one device) and ccu2 (offline,
// HmIP-RF, no devices).

test.describe('Fleet', () => {
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

  test('renders a card per CCU with status, interfaces and device counts', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await expect(page.getByRole('heading', { name: 'ccu2', level: 2 })).toBeVisible();

    // Readiness badges: ccu1 has completed southbound bring-up (Ready),
    // ccu2 is unreachable (Offline) — see CentralStatusBadge.svelte.
    await expect(page.getByText('Ready', { exact: true })).toBeVisible();
    await expect(page.getByText('Offline', { exact: true })).toBeVisible();

    // Interface chips (ccu1 has both; ccu2 has HmIP-RF).
    await expect(page.getByText('BidCos-RF', { exact: true })).toBeVisible();

    // ccu1 host + model surface.
    await expect(page.getByText('192.0.2.29')).toBeVisible();
    await expect(page.getByText('CCU3')).toBeVisible();

    // CCU-reported security posture: ccu1 requires auth, neither CCU
    // redirects to HTTPS. The flags are independent labels, not one badge.
    await expect(page.getByText('Authentication required', { exact: true })).toBeVisible();
    await expect(page.getByText('No authentication', { exact: true })).toBeVisible();
    await expect(page.getByText('HTTPS redirect off', { exact: true }).first()).toBeVisible();

    // The CCU's own interface list carries ports, and the CUxD interface the
    // daemon is not configured for is flagged as unmanaged.
    await expect(page.getByText('HmIP-RF:2010', { exact: true })).toBeVisible();
    await expect(page.getByText('CUxD:8701', { exact: true })).toBeVisible();
    await expect(page.getByText('CUxD:8701', { exact: true })).toHaveAttribute(
      'title',
      'The CCU offers this interface, but this daemon is not configured for it.',
    );
  });
});

// ---------------------------------------------------------------------------
// Readiness-gated southbound bring-up: a reachable-but-not-yet-ready CCU
// must read as "still initializing", distinct from both Ready and Offline.
// ---------------------------------------------------------------------------

test.describe('Fleet - readiness initializing state', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.route('**/api/v1/system/ccu', (route) =>
      route.fulfill({
        json: {
          entries: [
            {
              name: 'ccu1',
              host: '192.0.2.29',
              available: true,
              model: 'CCU3',
              version: '3.75.7',
              is_ha_app: false,
              configured_interfaces: ['HmIP-RF', 'BidCos-RF'],
              // Reachable, but southbound bring-up has only wired 2 of 5
              // configured interfaces so far.
              readiness: { phase: 'loading_devices', ready: false, interfaces_loaded: 2, interfaces_total: 5 },
            },
          ],
        },
      }),
    );
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('shows the device-loading progress label for a not-ready CCU', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });

    // Reachable + mid-bring-up reads as "Initializing (devices 2/5)", never
    // "Ready" or "Offline" — see CentralStatusBadge.svelte's phase switch.
    await expect(page.getByText('Initializing (devices 2/5)', { exact: true })).toBeVisible();
    await expect(page.getByText('Ready', { exact: true })).not.toBeVisible();
    await expect(page.getByText('Offline', { exact: true })).not.toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Fleet - visual light', () => {
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

  test('fleet light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('fleet-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Fleet - visual dark', () => {
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

  test('fleet dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/fleet');
    await page.waitForSelector('#main');
    await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('fleet-dark.png');
  });
});
