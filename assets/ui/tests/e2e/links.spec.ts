import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockHiddenSurfaces } from './helpers/mock-api';

// The fleet-wide direct-links overview. It is a read-only catalogue that
// hands off to the owning device's link editor, so the two things worth
// driving in a browser are that the hand-off exists and that it
// disappears when the profile removes the editor it points at.

const URL = 'http://localhost:5173/app/#/links';

test.describe('Direct links overview', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('offers the hand-off to the device link editor', async ({ page }) => {
    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    const edit = page.getByRole('link', { name: /Edit on device/ }).first();
    await expect(edit).toHaveAttribute('href', /#\/devices\/[^?]+\?tab=links/);
  });

  // The state that shipped broken: with the device's link tab hidden the
  // row still offered "Edit on device" and landed on a device where that
  // tab was gone.
  test('drops the hand-off when the device link editor is hidden', async ({ page }) => {
    await mockHiddenSurfaces(page, ['device.configure.links']);

    await page.goto(URL);
    await page.waitForSelector('#main');
    await page.waitForTimeout(500);

    // The cross-device listing keeps its rows — the device detail has no
    // fleet-wide link view to fall back on.
    await expect(page.getByRole('link', { name: /Edit on device/ })).toHaveCount(0);
    await expect(page.getByText(/link editor is hidden in this profile/i)).toBeVisible();
  });
});
