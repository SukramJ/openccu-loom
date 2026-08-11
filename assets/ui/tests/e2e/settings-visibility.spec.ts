import { test, expect } from './helpers/fixtures';
import {
  mockAllApis,
  addStylesForStableScreenshots,
} from './helpers/mock-api';

// Settings → Hidden parameters: the un-ignore picker.
//
// What a browser test adds over the unit tests on
// `$lib/visibility/candidates`: those pin the filter algebra on plain
// objects, this pins that the daemon's grouped payload reaches the
// screen as rows, that the noise categories really start collapsed,
// and that a tick travels from a checkbox into the saved pattern list.
// The regression it exists to prevent is the one the flat list had —
// a screen that renders every pattern of every model and is unusable
// on a real fleet (~2800 rows out of 45 parameters).

const TAB = 'http://localhost:5173/app/#/settings?tab=visibility';

const PREFS = (theme: 'light' | 'dark') =>
  JSON.stringify({
    theme,
    locale: 'en',
    navCollapsed: false,
    expertMode: false,
    deviceView: 'grid',
  });

test.describe('Settings — hidden parameters', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript((prefs) => {
      localStorage.setItem('openccu-loom.prefs.v1', prefs);
    }, PREFS('light'));
  });

  test('renders one row per parameter, not one per pattern', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // The fixture's 18 groups expand to 60+ patterns. A row count in
    // that range would mean the grouping collapsed back to the flat
    // list.
    const rows = page.getByTestId('unignore-group');
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);
    expect(count).toBeLessThanOrEqual(18);

    // A row shows the wire name plus its translated label, so the
    // operator does not have to decode ACTIVITY_STATE by itself.
    await expect(page.getByText('ACTIVITY_STATE', { exact: true })).toBeVisible();
    await expect(page.getByText('Fahrtrichtung')).toBeVisible();
  });

  test('noise categories start collapsed and can be revealed', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Diagnostic bits (internal_flag) are the largest bucket on a real
    // fleet and the reason the flat list was unusable. They must not be
    // on screen before the operator asks for them...
    await expect(
      page.locator('[data-parameter="STICKY_SABOTAGE"]'),
    ).toHaveCount(0);
    await expect(
      page.locator('[data-parameter="SMOKE_DETECTOR_ALARM_STATUS"]'),
    ).toHaveCount(0);
    // Week-profile cells are the other bulk family: a single climate
    // device contributes hundreds of them, and they already have a
    // schedule editor.
    await expect(
      page.locator('[data-parameter="P1_ENDTIME_MONDAY_1"]'),
    ).toHaveCount(0);
    await expect(page.locator('[data-parameter="01_WP_LEVEL"]')).toHaveCount(0);
    // ...while the categories worth a decision are.
    await expect(
      page.locator('[data-parameter="ACTIVITY_STATE"]'),
    ).toHaveCount(1);

    // Nothing may vanish silently: the count of what is hidden is on
    // screen with a way to show it.
    const notice = page.getByTestId('unignore-show-all');
    await expect(notice).toBeVisible();
    await notice.click();

    await expect(
      page.locator('[data-parameter="STICKY_SABOTAGE"]'),
    ).toHaveCount(1);
    await expect(
      page.locator('[data-parameter="SMOKE_DETECTOR_ALARM_STATUS"]'),
    ).toHaveCount(1);
    await expect(
      page.locator('[data-parameter="P1_ENDTIME_MONDAY_1"]'),
    ).toHaveCount(1);
    await expect(page.locator('[data-parameter="01_WP_LEVEL"]')).toHaveCount(1);
  });

  test('a category chip filters the list', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // Start from "everything", then narrow to one category.
    await page.getByTestId('unignore-show-all').click();
    await page.getByTestId('reason-chip-master_gate').click();

    await expect(
      page.locator('[data-parameter="TEMPERATURE_OFFSET"]'),
    ).toHaveCount(1);
    await expect(
      page.locator('[data-parameter="ACTIVITY_STATE"]'),
    ).toHaveCount(0);
  });

  test('search finds a parameter by its device model', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByTestId('unignore-search').fill('FROLL');

    await expect(
      page.locator('[data-parameter="ACTIVITY_STATE"]'),
    ).toHaveCount(1);
    await expect(page.getByTestId('unignore-group')).toHaveCount(1);
  });

  test('expanding a row reveals its models and channels', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByTestId('group-expand-ACTIVITY_STATE').click();

    const scopes = page.getByTestId('group-scopes-ACTIVITY_STATE');
    await expect(scopes).toBeVisible();
    await expect(scopes.getByText('HmIP-BROLL')).toBeVisible();
    await expect(scopes.getByText('HmIP-FROLL')).toBeVisible();
    await expect(scopes.getByText('Channel 3').first()).toBeVisible();
  });

  test('a saved pattern shows as enabled and toggles off', async ({ page }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // ACTIVITY_STATE is saved fleet-wide in the un-ignore fixture.
    const toggle = page.getByTestId('group-toggle-ACTIVITY_STATE');
    await expect(toggle).toBeChecked();

    // Nothing is pending before an edit.
    const save = page.getByTestId('unignore-save');
    await expect(save).toBeDisabled();

    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await expect(save).toBeEnabled();
  });

  test('a partially enabled parameter reads as indeterminate', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // WORKING is saved for one channel of one model only. Showing that
    // as plain "on" would tell the operator it is enabled everywhere.
    const toggle = page.getByTestId('group-toggle-WORKING');
    await expect(toggle).not.toBeChecked();
    await expect(
      toggle.evaluate((el: HTMLInputElement) => el.indeterminate),
    ).resolves.toBe(true);
  });

  test('saving reports the result and clears the pending state', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    await page.getByTestId('group-toggle-SECTION').click();
    const save = page.getByTestId('unignore-save');
    await expect(save).toBeEnabled();
    await save.click();

    // A save that reports nothing is indistinguishable from one that
    // silently failed.
    await expect(page.getByText(/Un-ignore list updated/)).toBeVisible();
    await expect(save).toBeDisabled();
  });

  test('a saved pattern with no matching parameter stays visible', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // RSSI_PEER on HmIP-GONE is in the saved list but in no candidate
    // group. Dropping it from the screen would let the next save
    // delete it without the operator ever seeing it.
    const orphans = page.getByTestId('unignore-orphans');
    await expect(orphans).toBeVisible();
    await expect(orphans.getByText('RSSI_PEER:VALUES@HmIP-GONE:0')).toBeVisible();
  });

  test('the header counts hidden, enabled and changed parameters', async ({
    page,
  }) => {
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    const stats = page.getByTestId('unignore-stats');
    // 18 groups in the fixture; ACTIVITY_STATE and WORKING are saved.
    await expect(stats).toHaveText(/18 hidden parameters/);
    await expect(stats).toHaveText(/2 enabled/);
    await expect(stats).toHaveText(/0 changed/);
  });
});

// Visual baselines in both modes. The screen leans on colour for the
// category badges and the three-state row toggle, so a dark-mode
// regression in any of them would otherwise ship unnoticed.
test.describe('Settings — hidden parameters, visual', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  for (const theme of ['light', 'dark'] as const) {
    test(`hidden parameters ${theme}`, async ({ page }) => {
      await page.addInitScript((prefs) => {
        localStorage.setItem('openccu-loom.prefs.v1', prefs);
      }, PREFS(theme));
      await page.goto(TAB);
      await page.waitForSelector('#main');
      await page.waitForTimeout(1500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`visibility-${theme}.png`);
    });
  }

  // The collapsed list above never shows the scope drill-down, which is
  // the part of the screen that replaced the flat pattern list. Pin it
  // separately, scrolled into view.
  test('hidden parameters expanded scopes', async ({ page }) => {
    await page.addInitScript((prefs) => {
      localStorage.setItem('openccu-loom.prefs.v1', prefs);
    }, PREFS('light'));
    await page.goto(TAB);
    await page.waitForSelector('#main');
    await page.waitForTimeout(1000);

    await page.getByTestId('group-expand-ACTIVITY_STATE').click();
    const scopes = page.getByTestId('group-scopes-ACTIVITY_STATE');
    await scopes.scrollIntoViewIfNeeded();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await expect(page).toHaveScreenshot('visibility-scopes-light.png');
  });
});
