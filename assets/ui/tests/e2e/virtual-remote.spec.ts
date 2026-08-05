import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockVirtualRemoteDevice, VIRTUAL_REMOTE_ADDRESS } from './helpers/mock-api';

// Functional coverage for the virtual-remote (HM-RCV-50) key-simulation
// grid — VirtualRemoteKeyGrid.svelte. Deliberately DOM/network assertions
// only, no `toHaveScreenshot()`: a visual baseline needs the Linux
// Playwright container (see assets/ui/tests/e2e/README-equivalent notes
// in CLAUDE.md's Testing Guidelines) and is left as a follow-up.
test.describe('Device detail — virtual-remote key simulation', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockVirtualRemoteDevice(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('renders one key cell per KEY channel and excludes the maintenance channel', async ({ page }) => {
    await page.goto(`http://localhost:5173/app/#/devices/${VIRTUAL_REMOTE_ADDRESS}`);
    await page.waitForSelector('#main');

    // Scope to the key-grid region so the labels don't collide with the
    // same channel names rendered elsewhere on the device-detail page.
    const grid = page.getByRole('region', { name: 'Key simulation' });
    await expect(grid).toBeVisible();
    await expect(grid.getByText('Key 1')).toBeVisible();
    await expect(grid.getByText('Key 3')).toBeVisible();
    // Channel 2 carries no CCU name — falls back to the "Key {n}" label.
    await expect(grid.getByText('Key 2')).toBeVisible();

    // Three keys x (short + long) = six press buttons; the maintenance
    // channel (0) must not contribute any.
    await expect(grid.getByRole('button', { name: /Press key \d short/ })).toHaveCount(3);
    await expect(grid.getByRole('button', { name: /Press key \d long/ })).toHaveCount(3);
  });

  test('a short press writes PRESS_SHORT=true for the clicked channel', async ({ page }) => {
    let putBody: unknown = null;
    let putUrl = '';
    await page.route(
      `**/api/v1/devices/${VIRTUAL_REMOTE_ADDRESS}/channels/1/data-points/PRESS_SHORT/value`,
      async (route) => {
        putUrl = route.request().url();
        putBody = route.request().postDataJSON();
        await route.fulfill({ status: 200 });
      },
    );

    await page.goto(`http://localhost:5173/app/#/devices/${VIRTUAL_REMOTE_ADDRESS}`);
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'Press key 1 short' }).click();

    await expect.poll(() => putBody).not.toBeNull();
    expect(putUrl).toContain('/channels/1/data-points/PRESS_SHORT/value');
    expect(putBody).toMatchObject({ value: true });
  });

  test('a long press writes PRESS_LONG=true for the clicked channel', async ({ page }) => {
    let putBody: unknown = null;
    await page.route(
      `**/api/v1/devices/${VIRTUAL_REMOTE_ADDRESS}/channels/3/data-points/PRESS_LONG/value`,
      async (route) => {
        putBody = route.request().postDataJSON();
        await route.fulfill({ status: 200 });
      },
    );

    await page.goto(`http://localhost:5173/app/#/devices/${VIRTUAL_REMOTE_ADDRESS}`);
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'Press key 3 long' }).click();

    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody).toMatchObject({ value: true });
  });

  test('a failed press shows an error toast instead of failing silently', async ({ page }) => {
    await page.route(
      `**/api/v1/devices/${VIRTUAL_REMOTE_ADDRESS}/channels/3/data-points/PRESS_LONG/value`,
      (route) => route.fulfill({ status: 500, json: { detail: 'ccu unreachable' } }),
    );

    await page.goto(`http://localhost:5173/app/#/devices/${VIRTUAL_REMOTE_ADDRESS}`);
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'Press key 3 long' }).click();

    await expect(page.getByText('Key press failed')).toBeVisible();
  });
});
