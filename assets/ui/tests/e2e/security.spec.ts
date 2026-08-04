import { test, expect } from '@playwright/test';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// Security & Safety domain (#/security, docs/security-safety-concept.md §7.8).
// Runs independently of the alarm engine — a smoke/water/gas-only install
// still gets the hazard classes and the fault ledger. mockAllApis wires sane
// defaults for GET /api/v1/security (severity "warning", an active "water"
// class with two named sources, an inactive "smoke" class, one zone, one
// open fault, and a last_alarm/last_fault pair each carrying a real subject +
// message), GET /api/v1/security/faults (the same standing fault) and
// GET /api/v1/security/sources (five classified rows, including one
// overridden and one not-relevant source). Individual tests override a route
// where they need a different state (an empty snapshot, a failing
// acknowledge).

test.describe('Security & Safety', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('nav entry exists and routes to the overview', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');

    await page.getByRole('link', { name: 'Security & Safety' }).click();

    await expect(page).toHaveURL(/#\/security$/);
    await expect(page.getByRole('heading', { name: 'Security & Safety', level: 1 })).toBeVisible();
    // Overview is the default sub-route: its tab reads selected.
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible();
  });

  test('document title is localized', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');
    await expect(page).toHaveTitle('Security & Safety — OpenCCU-Loom');
  });

  test('overview shows the severity badge and one tile per hazard/fault class', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');

    await expect(page.getByText('Warning', { exact: true })).toBeVisible();

    // The active-count/inactive badge is the sole direct <span> child of
    // each class tile's header row (icon+name block is the other child) —
    // scoping this way mirrors the zone-card pattern in alarm.spec.ts.
    const waterHeader = page
      .locator('div.flex.items-start.justify-between.gap-2')
      .filter({ hasText: 'Water' });
    await expect(waterHeader.locator('> span')).toHaveText('2 active');
    await expect(page.getByText('Küche Wasser, Keller Wasser')).toBeVisible();

    const smokeHeader = page
      .locator('div.flex.items-start.justify-between.gap-2')
      .filter({ hasText: 'Smoke' });
    await expect(smokeHeader.locator('> span')).toHaveText('No active sources');
  });

  test("overview shows the last alarm report's subject and message — the thing an operator reads first", async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');

    await expect(page.getByText('Last alarm report')).toBeVisible();
    await expect(page.getByText('Water detected in the kitchen')).toBeVisible();
    await expect(page.getByText('Küche Wasser reported water at 08:00 this morning.')).toBeVisible();
  });

  test('overview shows the shared loading state while the snapshot is in flight', async ({ page }) => {
    await page.route('**/api/v1/security', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 400));
      return route.fulfill({ json: { severity: 'ok', classes: [], engine_healthy: true } });
    });

    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');

    await expect(page.getByRole('status')).toBeVisible();
    await expect(page.getByRole('status')).toContainText('Loading');

    // Resolves once the delayed response lands.
    await expect(page.getByText('Nothing classified yet')).toBeVisible();
  });

  test('an empty snapshot renders the shared EmptyState, not a bare paragraph', async ({ page }) => {
    await page.route('**/api/v1/security', (route) =>
      route.fulfill({ json: { severity: 'ok', classes: [], engine_healthy: true } }),
    );

    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');

    // Both the message AND the muted description line are EmptyState's
    // signature — a bare <p> would carry only the former.
    await expect(page.getByText('Nothing classified yet')).toBeVisible();
    await expect(
      page.getByText(
        'Once a device with a smoke, water, gas, tamper or other security role comes online, it appears here automatically.',
      ),
    ).toBeVisible();
  });

  test('faults tab lists the fault, its reason, and states that acknowledging does not clear it', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');

    // The permanent hint banner, not a tooltip-only explanation.
    await expect(
      page.getByText('Acknowledging a fault only records that you have seen it', { exact: false }),
    ).toBeVisible();

    await expect(page.getByRole('table')).toBeVisible();
    await expect(page.getByText('Dachgeschoss Rauchmelder')).toBeVisible();
    await expect(page.getByText('Unreachable')).toBeVisible();
    await expect(page.getByText('Not yet acknowledged')).toBeVisible();
  });

  test('acknowledging a fault goes through the confirm dialog and raises a success toast', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');

    const row = page.locator('tbody tr').filter({ hasText: 'Dachgeschoss Rauchmelder' });
    await row.getByRole('button', { name: 'Acknowledge' }).click();

    // The shared confirm dialog — house rule: a destructive-style confirm
    // guards every acknowledge, no reflex-click can silence a fault.
    const dialog = page.getByRole('dialog', { name: 'Acknowledge this fault?' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Dachgeschoss Rauchmelder', { exact: false })).toBeVisible();

    await dialog.getByRole('button', { name: 'Acknowledge', exact: true }).click();

    await expect(page.getByRole('dialog')).toHaveCount(0);
    // House rule: a successful mutation always raises a toast, never a
    // silent inline update.
    await expect(
      page.getByRole('alert').filter({ hasText: 'Fault acknowledged — condition still stands' }),
    ).toBeVisible();
  });

  test('does not acknowledge when the operator declines the confirm dialog', async ({ page }) => {
    let ackCalls = 0;
    await page.route('**/api/v1/security/faults/*/acknowledge', (route) => {
      ackCalls += 1;
      return route.fulfill({ status: 204 });
    });

    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');

    const row = page.locator('tbody tr').filter({ hasText: 'Dachgeschoss Rauchmelder' });
    await row.getByRole('button', { name: 'Acknowledge' }).click();

    const dialog = page.getByRole('dialog', { name: 'Acknowledge this fault?' });
    await dialog.getByRole('button', { name: 'Cancel' }).click();

    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(ackCalls).toBe(0);
    await expect(page.getByText('Not yet acknowledged')).toBeVisible();
  });

  test('a failing acknowledge raises an error toast and does not silently abort', async ({ page }) => {
    await page.route('**/api/v1/security/faults/*/acknowledge', (route) =>
      route.fulfill({ status: 500 }),
    );

    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');

    const row = page.locator('tbody tr').filter({ hasText: 'Dachgeschoss Rauchmelder' });
    await row.getByRole('button', { name: 'Acknowledge' }).click();

    const dialog = page.getByRole('dialog', { name: 'Acknowledge this fault?' });
    await dialog.getByRole('button', { name: 'Acknowledge', exact: true }).click();

    await expect(page.getByRole('dialog')).toHaveCount(0);

    // House rule: a failure must surface — assert the error toast
    // explicitly, a silent abort here is a bug, not a test nuance.
    const errorToast = page.getByRole('alert').filter({ hasText: 'Acknowledge failed' });
    await expect(errorToast).toBeVisible();
    await expect(errorToast).toContainText('Server error (500)');

    // The fault is still open — no optimistic, unconfirmed state flip.
    await expect(row.getByText('Not yet acknowledged')).toBeVisible();
    await expect(row.getByRole('button', { name: 'Acknowledge' })).toBeVisible();
  });

  test('sources tab renders the classified inventory', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/sources');
    await page.waitForSelector('#main');

    await expect(page.getByRole('table')).toBeVisible();
    await expect(page.getByText('Küche Wasser')).toBeVisible();
    await expect(page.getByText('Keller Wasser')).toBeVisible();
    await expect(page.getByText('Garage Temperatur')).toBeVisible();
    await expect(page.getByText('Nebenraum Fenster')).toBeVisible();
    await expect(page.getByText('Overridden')).toBeVisible();
    await expect(page.getByText('Not relevant')).toBeVisible();
  });

  test('the relevant-only filter narrows the source inventory', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/sources');
    await page.waitForSelector('#main');

    await expect(page.getByText('Garage Temperatur')).toBeVisible();
    await expect(page.getByText('Küche Wasser')).toBeVisible();

    const relevantLabel = page.locator('label').filter({ hasText: 'Relevant only' });
    await relevantLabel.getByRole('switch').click();

    // "Garage Temperatur" is the fixture's only relevant:false row.
    await expect(page.getByText('Garage Temperatur')).toHaveCount(0);
    await expect(page.getByText('Küche Wasser')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Visual regression — light mode
// ---------------------------------------------------------------------------

test.describe('Security - visual light', () => {
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

  test('security overview light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('security-overview-light.png');
  });

  test('security faults light', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('security-faults-light.png');
  });
});

// ---------------------------------------------------------------------------
// Visual regression — dark mode
// ---------------------------------------------------------------------------

test.describe('Security - visual dark', () => {
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

  test('security overview dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('security-overview-dark.png');
  });

  test('security faults dark', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/security/faults');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('security-faults-dark.png');
  });
});
