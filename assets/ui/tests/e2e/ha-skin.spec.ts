import { test, expect } from './helpers/fixtures';
import {
  mockAllApis,
  mockFleet,
  mockOverviewFleet,
  addStylesForStableScreenshots,
} from './helpers/mock-api';

// Visual-regression coverage of the Home Assistant skin (data-skin="ha").
//
// Standalone (not embedded in HA) the HA skin resolves the static HA-default
// tokens — HA primary #009ac7, HA neutral surfaces, flat cards, the Roboto
// body font, and the Tailwind palette remapped to HA's ramps — so these
// baselines lock the native-HA look. When embedded via Ingress the same skin
// additionally mirrors the operator's live HA theme through the bridge; that
// path is not exercised here because the hermetic e2e has no HA parent
// document. See app.css html[data-skin="ha"] and docs/design/ha-theme-bridge.md.
//
// The skin is selected the same way the SPA persists it: prefs.skin = "ha" in
// localStorage. applyTheme() then stamps data-skin="ha" on <html> (standalone,
// resolveSkin returns the stored value).

function haPrefs(theme: 'light' | 'dark'): string {
  return JSON.stringify({
    theme,
    skin: 'ha',
    locale: 'en',
    navCollapsed: false,
    expertMode: false,
    deviceView: 'grid',
  });
}

test.describe('HA skin - Fleet visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`fleet ha ${theme}`, async ({ page }) => {
      await mockAllApis(page);
      await mockFleet(page);
      await page.addInitScript((prefs) => {
        localStorage.setItem('openccu-loom.prefs.v1', prefs as string);
      }, haPrefs(theme));

      await page.goto('http://localhost:5173/app/#/fleet');
      await page.waitForSelector('#main');
      await expect(page.getByRole('heading', { name: 'ccu1', level: 2 })).toBeVisible({ timeout: 10000 });
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`ha-skin-fleet-${theme}.png`);
    });
  }
});

test.describe('HA skin - Overview visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`overview ha ${theme}`, async ({ page }) => {
      await mockAllApis(page);
      await mockOverviewFleet(page);
      await page.addInitScript((prefs) => {
        localStorage.setItem('openccu-loom.prefs.v1', prefs as string);
      }, haPrefs(theme));

      await page.goto('http://localhost:5173/app/#/overview');
      await page.waitForSelector('#main');
      await expect(page.getByText('Kitchen · ccu1')).toBeVisible({ timeout: 10000 });
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`ha-skin-overview-${theme}.png`);
    });
  }
});
