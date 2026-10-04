import { test, expect } from './helpers/fixtures';
import type { Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

/**
 * The "Add device" dialog: the one place pairing starts.
 *
 * It is opened from the device list and from the new-devices view, and it
 * must behave the same from both: the pairing controls for the chosen
 * interface, an accept with name, rooms and functions for every device the
 * daemon holds back — "accept and release" finishing onboarding in one
 * step — a release for every device already accepted, and the devices that
 * joined while it was open. Two centrals can expose the same
 * interface name, so starting pairing has to say which central it means —
 * without that the daemon picks the first match and the operator pairs on
 * the wrong box.
 *
 * Every test here overrides the install-mode list and the inbox on top of
 * the shared mock layer, so the state under test is spelled out locally.
 */

/** Fixed so any rendered timestamp does not move between runs. */
const SEEN = Math.floor(new Date('2025-12-28T08:00:00Z').getTime() / 1000);

const PENDING = {
  address: '0002PEND',
  model: 'HmIP-STH',
  interface: 'HmIP-RF',
  serial: '0002PEND',
  manufacturer: 'eQ-3',
  central: 'ccu1',
  first_seen: SEEN,
  pending_creation: true,
};

type InterfaceEntry = {
  interface: string;
  central?: string;
  active: boolean;
  seconds: number;
  observed?: boolean;
};

/**
 * Serves the install-mode list and records every POST body sent to it, so a
 * test can assert what starting pairing actually asked the daemon for.
 */
async function mockInstallMode(page: Page, entries: InterfaceEntry[]): Promise<unknown[]> {
  const posts: unknown[] = [];
  await page.route('**/api/v1/install-mode/interfaces', (route) => {
    const req = route.request();
    if (req.method() === 'POST') {
      posts.push(req.postDataJSON());
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({ json: entries });
  });
  return posts;
}

async function mockInbox(page: Page, devices: unknown[]) {
  await page.route('**/api/v1/inbox', (route) => route.fulfill({ json: devices }));
}

/**
 * Records every accept and release in the order the SPA sent them. The
 * accept-and-release action is two calls, and only this order publishes a
 * device with the name and assignments the operator entered.
 */
async function recordOnboarding(page: Page): Promise<{ url: string; body: unknown }[]> {
  const sent: { url: string; body: unknown }[] = [];
  await page.route('**/api/v1/devices/*/accept*', (route) => {
    const req = route.request();
    sent.push({ url: req.url(), body: req.postDataJSON() });
    return route.fulfill({ status: 202 });
  });
  await page.route('**/api/v1/devices/*/release*', (route) => {
    sent.push({ url: route.request().url(), body: null });
    return route.fulfill({ status: 204 });
  });
  return sent;
}

async function setTheme(page: Page, theme: 'light' | 'dark') {
  await page.addInitScript(
    (t) => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({
          theme: t,
          locale: 'en',
          navCollapsed: false,
          expertMode: false,
        }),
      );
    },
    theme,
  );
}

async function openFromDeviceList(page: Page) {
  await page.goto('http://localhost:5173/app/#/devices');
  await page.waitForSelector('#main');
  await page.getByRole('button', { name: 'Add device', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add device' });
  await expect(dialog).toBeVisible({ timeout: 10000 });
  return dialog;
}

test.describe('Add device dialog', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  test('opens from the device list with the pairing controls', async ({ page }) => {
    await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
    await mockInbox(page, []);

    const dialog = await openFromDeviceList(page);
    await expect(dialog.getByRole('button', { name: 'Start pairing' })).toBeVisible();
  });

  test('opens from the new-devices view, whose header has no pairing form of its own', async ({ page }) => {
    await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
    await mockInbox(page, []);

    await page.goto('http://localhost:5173/app/#/inbox');
    await page.waitForSelector('#main');
    const open = page.getByRole('button', { name: 'Add device', exact: true });
    await expect(open).toBeVisible({ timeout: 10000 });

    // Pairing starts in the dialog only: before it opens, nothing on the
    // page offers a pairing window or a targeted teach-in.
    await expect(page.getByRole('button', { name: 'Start pairing' })).toHaveCount(0);
    await expect(page.getByText('Pair one specific device')).toHaveCount(0);

    await open.click();
    const dialog = page.getByRole('dialog', { name: 'Add device' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Start pairing' })).toBeVisible();
    // The one control on the page is the dialog's.
    await expect(page.getByRole('button', { name: 'Start pairing' })).toHaveCount(1);
  });

  test('starting pairing names the chosen central', async ({ page }) => {
    const posts = await mockInstallMode(page, [
      { central: 'ccu1', interface: 'HmIP-RF', active: false, seconds: 0 },
      { central: 'box', interface: 'HmIP-RF', active: false, seconds: 0 },
    ]);
    await mockInbox(page, []);

    const dialog = await openFromDeviceList(page);
    // The second of two same-named interfaces is only reachable when the
    // request carries its central.
    await dialog.getByRole('button', { name: 'Interface' }).click();
    await page.getByRole('option', { name: 'HmIP-RF · box' }).click();
    await dialog.getByRole('button', { name: 'Start pairing' }).click();

    await expect.poll(() => posts.length).toBe(1);
    expect(posts[0]).toMatchObject({ central: 'box', interface: 'HmIP-RF', active: true });
  });

  test('a held device is accepted in the dialog with its name, rooms and functions, then released', async ({ page }) => {
    await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
    await mockInbox(page, [PENDING]);
    await page.route('**/api/v1/rooms', (route) =>
      route.fulfill({ json: [{ id: 1, name: 'Bathroom' }, { id: 2, name: 'Kitchen' }] }),
    );
    await page.route('**/api/v1/functions', (route) =>
      route.fulfill({ json: [{ id: 3, name: 'Climate' }, { id: 4, name: 'Lights' }] }),
    );
    const sent = await recordOnboarding(page);

    const dialog = await openFromDeviceList(page);
    const held = dialog.getByTestId('add-device-acceptable');
    await expect(held.getByText('0002PEND')).toBeVisible();
    await held.getByRole('textbox', { name: 'Name' }).fill('Bathroom climate');
    await held.getByLabel('Rooms').fill('Bath');
    await held.getByRole('option', { name: 'Bathroom' }).click();
    await held.getByLabel('Functions').fill('Clim');
    await held.getByRole('option', { name: 'Climate' }).click();
    await held.getByRole('button', { name: 'Accept and release', exact: true }).click();

    await expect.poll(() => sent.length).toBe(2);
    const accept = new URL(sent[0].url);
    expect(accept.pathname).toBe('/api/v1/devices/0002PEND/accept');
    expect(accept.searchParams.get('central')).toBe('ccu1');
    expect(sent[0].body).toEqual({
      name: 'Bathroom climate',
      rooms: ['Bathroom'],
      functions: ['Climate'],
    });
    const release = new URL(sent[1].url);
    expect(release.pathname).toBe('/api/v1/devices/0002PEND/release');
    expect(release.searchParams.get('central')).toBe('ccu1');
  });

  test('a plain accept keeps the device withheld', async ({ page }) => {
    await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
    const sent = await recordOnboarding(page);
    // What the daemon lists: held until the accept, then built and withheld.
    await page.route('**/api/v1/inbox', (route) =>
      route.fulfill({
        json: [
          sent.length === 0
            ? PENDING
            : { ...PENDING, pending_creation: false, awaiting_release: true },
        ],
      }),
    );

    const dialog = await openFromDeviceList(page);
    const held = dialog.getByTestId('add-device-acceptable');
    await held.getByRole('button', { name: 'Accept', exact: true }).click();

    await expect.poll(() => sent.length).toBe(1);
    expect(new URL(sent[0].url).pathname).toBe('/api/v1/devices/0002PEND/accept');
    // The device moved to the release list instead of being published.
    const waiting = dialog.getByTestId('add-device-awaiting-release');
    await expect(waiting.getByRole('button', { name: 'Release' })).toBeVisible();
    await page.waitForTimeout(300);
    expect(sent).toHaveLength(1);
  });

  test('a device waiting to be released is released in the dialog', async ({ page }) => {
    await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
    await mockInbox(page, [{ ...PENDING, pending_creation: false, awaiting_release: true }]);
    const sent = await recordOnboarding(page);

    const dialog = await openFromDeviceList(page);
    const waiting = dialog.getByTestId('add-device-awaiting-release');
    await expect(waiting.getByText('Waiting to be released (1)')).toBeVisible();
    await expect(waiting.getByRole('link', { name: 'Configure' })).toHaveAttribute(
      'href',
      '#/devices/0002PEND',
    );
    await waiting.getByRole('button', { name: 'Release' }).click();

    await expect.poll(() => sent.length).toBe(1);
    expect(new URL(sent[0].url).pathname).toBe('/api/v1/devices/0002PEND/release');
  });

  test('offers the controls that apply to the chosen interface', async ({ page }) => {
    await mockInstallMode(page, [
      { interface: 'HmIP-RF', active: false, seconds: 0 },
      { interface: 'BidCos-Wired', active: false, seconds: 0 },
    ]);
    await mockInbox(page, []);

    const dialog = await openFromDeviceList(page);

    // HmIP radio: a pairing window, and SGTIN + key as the targeted way;
    // the HmIP radio has no pairing by serial.
    await expect(dialog.getByRole('button', { name: 'Start pairing' })).toBeVisible();
    await dialog.getByText('Pair one specific device').click();
    await expect(dialog.getByRole('textbox', { name: 'SGTIN' })).toBeVisible();
    await expect(dialog.getByText('Pair by serial:')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Search wired bus' })).toHaveCount(0);

    // Wired bus: a bus search instead of a pairing window.
    await dialog.getByRole('button', { name: 'Interface' }).click();
    await page.getByRole('option', { name: 'BidCos-Wired' }).click();
    await expect(dialog.getByRole('button', { name: 'Search wired bus' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Start pairing' })).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------
// Visual regression
//
// Scoped to the dialog panel: the dialog is what is under test, and a
// full-page baseline would fail for every unrelated change to the device
// list behind the overlay.
// ---------------------------------------------------------------------------

test.describe('Add device dialog - visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`add device dialog ${theme}`, async ({ page }) => {
      await mockAllApis(page);
      await setTheme(page, theme);
      await mockInstallMode(page, [{ interface: 'HmIP-RF', active: false, seconds: 0 }]);
      await mockInbox(page, [PENDING]);

      const dialog = await openFromDeviceList(page);
      await expect(dialog.getByTestId('add-device-acceptable')).toBeVisible();
      await expect(dialog.getByText('No new device has joined yet.')).toBeVisible();
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(dialog.locator(':scope > div')).toHaveScreenshot(`add-device-dialog-${theme}.png`);
    });
  }
});
