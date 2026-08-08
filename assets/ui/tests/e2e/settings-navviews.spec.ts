import { test, expect } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// Settings → Navigation & views: the surface-profile editor.
//
// The value of driving it in a browser rather than in a component test
// is the wiring: that the tab is reachable, that the registry the daemon
// serves becomes rows, and that the profile switcher moves the editor
// without moving the live profile. See notes/concepts/ui-surface-profiles.md.

const TAB = 'http://localhost:5173/app/#/settings?tab=navviews';

test.describe('Settings — navigation & views', () => {
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

  test('renders the master toggle and the surface rows', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await expect(page.getByText('Embedded in Home Assistant')).toBeVisible();
    // The disclaimer is load-bearing copy, not decoration: "hidden" reads
    // as "forbidden" to almost everyone.
    await expect(page.getByText(/API tokens, Loom accounts and MQTT are unaffected/)).toBeVisible();

    // Rows come from the daemon's registry, so a row proves the payload
    // was fetched and mapped, not that a fixture was rendered.
    await expect(page.getByText('Alarm system', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('Arming, zones, sensors and sirens.')).toBeVisible();
  });

  test('a floor surface cannot be switched off', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // The device list is floor in both profiles; the reason is shown
    // rather than the row being silently dropped.
    await expect(page.getByText(/Cannot be hidden .* the device list is what this UI is for/)).toBeVisible();
    await expect(page.getByRole('switch', { name: 'Devices' })).toBeDisabled();
  });

  test('hiding a view marks it changed and offers a reset', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Favorites is an ordinary row: visible by default in standalone.
    await page.getByRole('switch', { name: 'Favorites' }).click();

    await expect(page.getByText(/Changed · default: visible/).first()).toBeVisible();
    await expect(page.getByText(/unsaved changes/)).toBeVisible();
  });

  test('the inactive profile is editable without changing the live one', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByRole('tab', { name: /Embedded/ }).click();
    await page.waitForTimeout(300);

    // The preview says which profile it is showing, so an operator
    // preparing the HA layout is never in doubt about what they see.
    await expect(page.getByText(/not currently live/)).toBeVisible();
    // The live profile is unchanged: the sidebar still carries Favorites.
    await expect(page.getByRole('link', { name: 'Favorites' })).toBeVisible();
  });
});

// Visual baselines. Both modes, because the editor carries several
// states that only colour distinguishes — the changed dot, the locked
// row, the write-gated badge — and a dark-mode regression in any of them
// would otherwise ship unnoticed.
test.describe('Settings — navigation & views, visual', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('navviews light', async ({ page }) => {
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
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('navviews-light.png');
  });

  test('navviews dark', async ({ page }) => {
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
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('navviews-dark.png');
  });
});
