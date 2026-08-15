import { test, expect } from './helpers/fixtures';
import { mockAllApis } from './helpers/mock-api';

// App-level routing behaviour: what happens around the router rather than
// inside a view. All three cases are only observable in a browser — a
// failing dynamic import, a hash navigation that has to survive a confirm
// dialog, and the boot sequence of a password login.

const PREFS = JSON.stringify({
  theme: 'light',
  locale: 'en',
  navCollapsed: false,
  expertMode: false,
  deviceView: 'grid',
});

test.describe('App — code-split route failure', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript((prefs) => {
      localStorage.setItem('openccu-loom.prefs.v1', prefs);
    }, PREFS);
  });

  test('a chunk that fails to load surfaces an error with a retry', async ({ page }) => {
    // A daemon update renames the content-hashed chunks, so the old URL a
    // long-open tab requests no longer resolves. Aborting the module
    // request reproduces that rejection.
    await page.route('**/routes/Diagnostics.svelte*', (route) => route.abort());

    await page.goto('http://localhost:5173/app/#/diagnostics');
    await page.waitForSelector('#main');

    await expect(
      page.getByText(/This view could not be loaded/),
    ).toBeVisible({ timeout: 10000 });
    await expect(page.getByRole('button', { name: 'Reload' })).toBeVisible();
  });
});

test.describe('App — leaving an editor with unsaved changes', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript((prefs) => {
      localStorage.setItem('openccu-loom.prefs.v1', prefs);
    }, PREFS);
  });

  test('confirming the leave dialog discards the draft instead of keeping it', async ({
    page,
  }) => {
    await page.goto('http://localhost:5173/app/#/settings?tab=navviews');
    await page.waitForSelector('#main');

    // The surface editor's draft lives in a module-level store, so it
    // outlives the view that edits it.
    await page.getByRole('switch', { name: 'Favorites' }).click();
    await expect(page.getByText(/unsaved changes/)).toBeVisible();

    await page.getByRole('link', { name: 'Devices', exact: true }).click();
    await page.getByRole('button', { name: 'Leave' }).click();
    await expect(page).toHaveURL(/#\/devices$/);

    // Second navigation: the draft was discarded, so nothing is unsaved
    // and the view must swap without asking again. Before the rollback
    // existed the dialog re-opened here — and on every navigation after
    // it — holding the router on the previous view. Asserting that the
    // target view rendered is what makes this fail then: the URL alone
    // already changes while the dialog waits, because hashchange fires
    // after the hash has moved.
    await page.getByRole('link', { name: 'Backups', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Backups', level: 1 })).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });
});

test.describe('App — start page after an interactive login', () => {
  test('applies the configured start route once the operator signs in', async ({
    page,
  }) => {
    await mockAllApis(page);

    // A tab opened without a session: everything behind AuthRequire
    // answers 401 until the password form succeeds, which is exactly the
    // boot in which the per-user preferences used to be lost for good.
    let signedIn = false;
    await page.route('**/api/v1/auth/me', (route) =>
      signedIn
        ? route.fulfill({ json: { subject: 'admin', role: 'admin' } })
        : route.fulfill({ status: 401, json: { title: 'unauthorized' } }),
    );
    await page.route('**/api/v1/auth/login', (route) => {
      signedIn = true;
      return route.fulfill({ json: { subject: 'admin', role: 'admin' } });
    });
    await page.route('**/api/v1/me/preferences/**', (route) => {
      if (route.request().method() !== 'GET') return route.fulfill({ status: 204, body: '' });
      return signedIn
        ? route.fulfill({ json: { key: 'start_route', value: '#/overview' } })
        : route.fulfill({ status: 401, json: { title: 'unauthorized' } });
    });

    await page.addInitScript((prefs) => {
      localStorage.setItem('openccu-loom.prefs.v1', prefs);
    }, PREFS);

    await page.goto('http://localhost:5173/app/');
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password').fill('secret');
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(page).toHaveURL(/#\/overview$/, { timeout: 10000 });
  });
});
