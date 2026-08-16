import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

test.describe('Visual regression - light mode', () => {
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

  test('DeviceList light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/devices');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('device-list-light.png');
  });

  test('Settings light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-light.png');
  });

  test('Diagnostics light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/diagnostics');
    await page.waitForSelector('#main');
    await page.waitForTimeout(2000);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('diagnostics-light.png');
  });

  test('Groups light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/groups');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('groups-light.png');
  });

  test('Links light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/links');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('links-light.png');
  });

  test('Sysvars empty state light', async ({ page }) => {
    await page.route('**/api/v1/sysvars*', async (route) => {
      await route.fulfill({ json: [] });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-empty-light.png');
  });

  test('Sysvars error state light', async ({ page }) => {
    await page.route('**/api/v1/sysvars*', async (route) => {
      await route.fulfill({ status: 500, body: JSON.stringify({ detail: 'server error' }) });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-error-light.png');
  });

  test('Alarm light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('alarm-light.png');
  });

  // The Matter diagnostics tab is dense with severity colour: finding
  // badges, a reachability column, an idle-age column that turns amber.
  // Those are exactly the pixels a token change silently inverts, which
  // is why this view carries a baseline in both modes.
  test('MatterDiagnostics light', async ({ page }) => {
    // Matter is off in the shared fixture, and the surface registry
    // hides the whole view when it is — flipping the fixture globally
    // would add a sidebar entry to every other baseline. Override the
    // status document for this test alone; a route registered after
    // mockAllApis takes precedence.
    await page.route('**/api/v1/matter/status', (route) =>
      route.fulfill({
        json: {
          enabled: true,
          listening: true,
          endpoint_count: 4,
          fabric_count: 2,
          enabled_count: 4,
          advertising: true,
          commissioning_window_open: false,
          commissioning_window_duration_seconds: 0,
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/matter/diagnostics');
    await page.waitForSelector('#main');
    // Wait for the last section to have content rather than for a fixed
    // interval: this view fans out to five endpoints, and a slower run
    // screenshotted it mid-load and wrote a blank baseline that the
    // tolerance check then accepted.
    await page.getByText('connected but receiving nothing').waitFor();
    await page.waitForTimeout(300);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('matter-diagnostics-light.png');
  });

  // The fabrics tab pairs a table of paired controllers with the two
  // maintenance actions. One of those is irreversible, so its weight on
  // the page — an outline-danger button, never a filled one competing
  // with the everyday actions above it — is worth a baseline in both
  // modes: the danger token is exactly what a theme change inverts.
  test('MatterFabrics light', async ({ page }) => {
    // Matter is off in the shared fixture and the surface registry hides
    // the view; the status document is overridden for this test alone.
    await page.route('**/api/v1/matter/status', (route) =>
      route.fulfill({
        json: {
          enabled: true,
          listening: true,
          endpoint_count: 4,
          fabric_count: 2,
          enabled_count: 4,
          advertising: true,
          commissioning_window_open: false,
          commissioning_window_duration_seconds: 0,
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/matter/fabrics');
    await page.waitForSelector('#main');
    // Wait for the content, not for a clock: a fixed timeout that the
    // container overruns yields a blank screenshot, and a blank baseline
    // passes the tolerance check against the next blank one.
    await page.getByText('0x0000011F743AAD34').waitFor();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('matter-fabrics-light.png');
  });
});

test.describe('Visual regression - dark mode', () => {
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

  test('DeviceList dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/devices');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('device-list-dark.png');
  });

  test('Settings dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('settings-dark.png');
  });

  test('Diagnostics dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/diagnostics');
    await page.waitForSelector('#main');
    await page.waitForTimeout(2000);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('diagnostics-dark.png');
  });

  test('Groups dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/groups');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('groups-dark.png');
  });

  test('Links dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/links');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('links-dark.png');
  });

  test('Sysvars empty state dark', async ({ page }) => {
    await page.route('**/api/v1/sysvars*', async (route) => {
      await route.fulfill({ json: [] });
    });
    await page.goto('http://localhost:5173/app/#/sysvars');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('sysvars-empty-dark.png');
  });

  test('Alarm dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('alarm-dark.png');
  });

  test('MatterDiagnostics dark', async ({ page }) => {
    // Matter is off in the shared fixture, and the surface registry
    // hides the whole view when it is — flipping the fixture globally
    // would add a sidebar entry to every other baseline. Override the
    // status document for this test alone; a route registered after
    // mockAllApis takes precedence.
    await page.route('**/api/v1/matter/status', (route) =>
      route.fulfill({
        json: {
          enabled: true,
          listening: true,
          endpoint_count: 4,
          fabric_count: 2,
          enabled_count: 4,
          advertising: true,
          commissioning_window_open: false,
          commissioning_window_duration_seconds: 0,
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/matter/diagnostics');
    await page.waitForSelector('#main');
    // Wait for the last section to have content rather than for a fixed
    // interval: this view fans out to five endpoints, and a slower run
    // screenshotted it mid-load and wrote a blank baseline that the
    // tolerance check then accepted.
    await page.getByText('connected but receiving nothing').waitFor();
    await page.waitForTimeout(300);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('matter-diagnostics-dark.png');
  });

  // The fabrics tab pairs a table of paired controllers with the two
  // maintenance actions. One of those is irreversible, so its weight on
  // the page — an outline-danger button, never a filled one competing
  // with the everyday actions above it — is worth a baseline in both
  // modes: the danger token is exactly what a theme change inverts.
  test('MatterFabrics dark', async ({ page }) => {
    // Matter is off in the shared fixture and the surface registry hides
    // the view; the status document is overridden for this test alone.
    await page.route('**/api/v1/matter/status', (route) =>
      route.fulfill({
        json: {
          enabled: true,
          listening: true,
          endpoint_count: 4,
          fabric_count: 2,
          enabled_count: 4,
          advertising: true,
          commissioning_window_open: false,
          commissioning_window_duration_seconds: 0,
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/matter/fabrics');
    await page.waitForSelector('#main');
    // Wait for the content, not for a clock: a fixed timeout that the
    // container overruns yields a blank screenshot, and a blank baseline
    // passes the tolerance check against the next blank one.
    await page.getByText('0x0000011F743AAD34').waitFor();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('matter-fabrics-dark.png');
  });
});
