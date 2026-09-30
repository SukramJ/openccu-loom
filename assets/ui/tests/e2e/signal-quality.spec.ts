import { test, expect, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// #/signal with the BidCos-RF radio management: the pairwise reception
// matrix (mock-api RSSI_MATRIX — central "ccu", two BidCos devices, the
// CCU's own radio and one LAN gateway) and the best-receiver dialog
// (mock-api RECEIVER_PROPOSAL — one switch, one keep, one marginal, one
// roaming device).

function seedPrefs(page: Page, theme: 'light' | 'dark') {
  return page.addInitScript((th) => {
    localStorage.setItem(
      'openccu-loom.prefs.v1',
      JSON.stringify({ theme: th, locale: 'en', navCollapsed: false, expertMode: false }),
    );
  }, theme);
}

async function openSignal(page: Page) {
  await page.goto('http://localhost:5173/app/#/signal');
  await page.waitForSelector('#main');
  await expect(page.getByRole('heading', { name: 'BidCos-RF reception matrix' })).toBeVisible();
}

async function openProposal(page: Page) {
  await page.getByRole('button', { name: 'Best receiver…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Best receiver per device' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Living Room Switch')).toBeVisible();
  return dialog;
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(`Signal quality — radio management (${theme})`, () => {
    test.beforeEach(async ({ page }) => {
      await mockAllApis(page);
      await seedPrefs(page, theme);
    });

    test(`matrix section ${theme}`, async ({ page }) => {
      await openSignal(page);
      const matrix = page.getByTestId('rssi-matrix');
      await expect(matrix.getByText('LAN gateway Garage').first()).toBeVisible();
      await expect(matrix.getByText('-64 dBm').first()).toBeVisible();
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`signal-matrix-${theme}.png`, { fullPage: true });
    });

    test(`best-receiver dialog ${theme}`, async ({ page }) => {
      await openSignal(page);
      await openProposal(page);
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`receiver-proposal-${theme}.png`);
    });
  });
}

test.describe('Signal quality — applying the best receiver', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await seedPrefs(page, 'light');
  });

  test('only the switch row is applicable, and applying assigns its best interface', async ({
    page,
  }) => {
    const assigned: { path: string; body: unknown }[] = [];
    await page.route('**/api/v1/devices/*/rf-interface', (route) => {
      assigned.push({
        path: new URL(route.request().url()).pathname,
        body: route.request().postDataJSON(),
      });
      return route.fulfill({ status: 204, body: '' });
    });

    await openSignal(page);
    const dialog = await openProposal(page);

    const ticks = dialog.getByRole('checkbox');
    await expect(ticks).toHaveCount(1);
    await expect(ticks.first()).toBeChecked();
    await expect(ticks.first()).toHaveAccessibleName('Switch Living Room Switch');

    await dialog.getByRole('button', { name: 'Switch 1 devices' }).click();
    await expect.poll(() => assigned.length).toBe(1);
    expect(assigned[0]).toEqual({
      path: '/api/v1/devices/OEQ0123456/rf-interface',
      body: { interface_address: 'NEQ1000002', roaming: false },
    });
    await expect(dialog.getByTestId('proposal-result')).toHaveText('Assigned');
    await expect(page.getByText('1 devices re-assigned.')).toBeVisible();
  });
});
