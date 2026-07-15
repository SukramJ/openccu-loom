import { test, expect } from '@playwright/test';
import { mockAllApis, mockAlarmTriggered } from './helpers/mock-api';

// Alarm section (#/alarm, docs/alarm-concept.md §12). mockAllApis now wires
// sane defaults for every /api/v1/alarm/* route: two areas — "Erdgeschoss"
// (armed, full protection, steady state) and "Dachgeschoss" (disarmed) —
// three sensors, two outputs (one acoustic siren, one smoke-detector
// sounder) and a five-entry journal.

test.describe('Alarm', () => {
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

    await page.getByRole('link', { name: 'Alarm system' }).click();

    await expect(page).toHaveURL(/#\/alarm$/);
    await expect(page.getByRole('heading', { name: 'Alarm system', level: 1 })).toBeVisible();
    // Overview is the default sub-route: its tab reads selected.
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible();
  });

  test('document title is localized', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await expect(page).toHaveTitle('Alarm system — OpenCCU-Loom');
  });

  test('overview shows both area cards with localized state badges', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await expect(page.getByRole('heading', { name: 'Erdgeschoss', level: 3 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Dachgeschoss', level: 3 })).toBeVisible();

    // The state badge is the sole direct <span> child of each card's header
    // row (the icon+name block is the other child) — scoping this way avoids
    // the ambiguity of "Disarmed" also appearing on that area's mode button.
    const egHeader = page
      .locator('div.flex.items-start.justify-between.gap-3')
      .filter({ hasText: 'Erdgeschoss' });
    await expect(egHeader.locator('> span')).toHaveText('Armed · Full protection');

    const ogHeader = page
      .locator('div.flex.items-start.justify-between.gap-3')
      .filter({ hasText: 'Dachgeschoss' });
    await expect(ogHeader.locator('> span')).toHaveText('Disarmed');
  });

  test('sensors tab renders the sensor picker surface', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Sensors' }).click();
    await expect(page).toHaveURL(/#\/alarm\/picker$/);

    await expect(page.getByRole('button', { name: 'Add sensor' })).toBeVisible();
    await expect(page.getByText('Haustür')).toBeVisible();
  });

  test('outputs tab renders the output picker surface, incl. the smoke-sounder caveat', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Outputs' }).click();
    await expect(page).toHaveURL(/#\/alarm\/outputs$/);

    await expect(page.getByRole('button', { name: 'Add output' })).toBeVisible();
    await expect(page.getByText('Außensirene')).toBeVisible();
    await expect(page.getByText('Smoke-detector sounder')).toBeVisible();
    await expect(
      page.getByText('Smoke detectors double as sounders', { exact: false }),
    ).toBeVisible();
  });

  test('journal tab renders the five-entry journal table', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Journal' }).click();
    await expect(page).toHaveURL(/#\/alarm\/journal$/);

    await expect(page.getByRole('button', { name: 'Export CSV' })).toBeVisible();
    await expect(page.locator('table tbody tr')).toHaveCount(5);
    await expect(page.getByText('Markus')).toBeVisible();
  });

  test('silence acts on the first tap for a triggered area, with no confirm dialog', async ({ page }) => {
    await mockAlarmTriggered(page);

    let silenceCalls = 0;
    await page.route('**/api/v1/alarm/areas/area-eg/silence', (route) => {
      silenceCalls += 1;
      return route.fulfill({ status: 200 });
    });

    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await expect(page.getByText('ALARM — Intrusion')).toBeVisible();

    // Safety invariants S3/S6 (docs/alarm-concept.md §2): silence acts on
    // the first tap, no confirm dialog is allowed to intercept it.
    await page.getByRole('button', { name: 'Silence sirens' }).click();

    await expect.poll(() => silenceCalls).toBe(1);
    await expect(page.getByRole('dialog')).not.toBeVisible();
  });
});
