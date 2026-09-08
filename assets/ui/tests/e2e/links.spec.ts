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

// ---------------------------------------------------------------------------
// The device's own link editor, driven end to end in a browser.
// ---------------------------------------------------------------------------

const DEVICE = 'ABC123';
const SENDER = 'ABC123:1';
const RECEIVER = 'PEER999:2';
const DEVICE_URL = `http://localhost:5173/app/#/devices/${DEVICE}?tab=links`;

// These routes live in this spec rather than mock-api.ts on purpose: a
// device-links fixture in the shared helper would put rows on every other
// spec's links tab, which several baselines render as empty.
async function mockDeviceLinkEditor(page: import('@playwright/test').Page, putDelayMs: number) {
  // Routes the device detail needs before its links tab renders at all;
  // mirrors device-detail.spec.ts's applyDeviceMocks. The channels matter:
  // a device the detail view believes has none offers no Configure
  // sub-tabs, so the `?tab=links` deep link falls back to the channel
  // strip and the editor is never reachable.
  await page.route(`**/api/v1/devices/${DEVICE}`, (route) =>
    route.fulfill({
      json: {
        address: DEVICE,
        name: 'Test Switch',
        model: 'HmIP-PSM',
        interface: 'HmIP-RF',
        central: 'ccu',
        channels_count: 2,
        updatable: false,
        update_available: false,
        rooms: [],
        functions: [],
        master_pushes_config_pending: false,
        has_sub_devices: false,
        firmware: {},
        availability: { IsReachable: true, LowBattery: false, SignalStrength: -58 },
        channels: [
          {
            address: `${DEVICE}:0`,
            number: 0,
            name: '',
            type: 'MAINTENANCE',
            type_label: 'Maintenance',
            paramset_key: 'VALUES',
            paramset_keys: ['VALUES', 'MASTER'],
            data_points_count: 6,
          },
          {
            address: SENDER,
            number: 1,
            name: 'Push-button',
            type: 'KEY_TRANSCEIVER',
            type_label: 'Push-button',
            paramset_key: 'VALUES',
            paramset_keys: ['VALUES', 'MASTER', 'LINK'],
            data_points_count: 3,
          },
        ],
      },
    }),
  );
  await page.route('**/api/v1/devices/*/central-links*', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/devices/*/schedule', (route) =>
    route.fulfill({ status: 404, body: JSON.stringify({ detail: 'no schedule' }) }),
  );
  await page.route('**/api/v1/**/channels/*/data-points', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/sessions/edit', (route) =>
    route.fulfill({
      json: {
        token: 'e2e-link-token',
        key: `channel:${SENDER}:LINK`,
        subject: 'admin',
        expires: new Date(Date.now() + 300000).toISOString(),
      },
    }),
  );
  await page.route('**/api/v1/sessions/edit/heartbeat', (route) =>
    route.fulfill({ status: 204, body: '' }),
  );
  await page.route('**/api/v1/devices/*/links*', (route) =>
    route.fulfill({
      json: [
        {
          sender_address: SENDER,
          receiver_address: RECEIVER,
          name: 'Stairwell light',
          description: '',
          direction: 'outgoing',
          sender_channel_type: 'KEY_TRANSCEIVER',
          sender_channel_type_label: 'Push-button',
          sender_device_name: 'Test Switch',
          receiver_channel_type: 'SWITCH_VIRTUAL_RECEIVER',
          receiver_channel_type_label: 'Switch',
          receiver_device_name: 'Hall lamp',
        },
      ],
    }),
  );
  // Parsed with a regex, not `new URL`: this file's own `URL` constant
  // (the overview route above) shadows the global constructor.
  await page.route('**/api/v1/**/channels/*/ui-schema*', (route) => {
    const m = route
      .request()
      .url()
      .match(/devices\/([^/]+)\/channels\/(\d+)\/ui-schema/);
    const address = decodeURIComponent(m?.[1] ?? '');
    const channel = Number(m?.[2] ?? 0);
    return route.fulfill({
      json: {
        channel: {
          address: `${address}:${channel}`,
          number: channel,
          type: 'SWITCH_VIRTUAL_RECEIVER',
          device_address: address,
        },
        parameters: [
          {
            name: 'SHORT_ON_TIME',
            label: 'Short on time',
            type: 'FLOAT',
            operations: { read: true, write: true, event: true },
            flags: { visible: true, internal: false, service: false },
            observed: true,
            value: 50,
          },
        ],
      },
    });
  });
  await page.route('**/api/v1/devices/*/link-ps/**', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, putDelayMs));
    await route.fulfill({ status: 204, body: '' });
  });
}

test.describe('Device link editor', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
  });

  // The editor renders from primitives the list snapshots when it opens,
  // not from the Link object — so the header parties and the save path
  // are what a browser has to confirm still line up. The race that made
  // that refactor necessary (leaving the editor while the PUT is in
  // flight) is pinned in the vitest reproducer
  // src/lib/components/links/LinkConfigPanel.null-link.test.ts: after a
  // browser teardown has fully settled Svelte answers a destroyed
  // derived from its cache, so the same gesture here cannot reach the
  // defect and a test asserting it would be green either way.
  test('opens a link from the list and writes its LINK paramset', async ({ page }) => {
    await mockDeviceLinkEditor(page, 0);

    await page.goto(DEVICE_URL);
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: /Configure/i }).first().click();

    // Every party label comes through the snapshot, each with its own prop.
    await expect(page.getByRole('heading', { name: 'Stairwell light' })).toBeVisible();
    await expect(
      page.getByText('Test Switch · Push-button → Hall lamp · Switch'),
    ).toBeVisible();

    const input = page.locator('input[type="number"]').first();
    await expect(input).toBeVisible();
    await input.fill('75');

    const putBody = page.waitForRequest(
      (r) => r.method() === 'PUT' && r.url().includes('/link-ps/'),
    );
    await page.getByRole('button', { name: /Save/i }).first().click();
    // A LINK save is previewed before it leaves; its request line names the
    // per-peer endpoint the write actually uses.
    const dialog = page.getByRole('dialog', { name: 'Review this write' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(`/link-ps/${SENDER}`)).toBeVisible();
    await dialog.getByRole('button', { name: 'Write', exact: true }).click();
    const request = await putBody;
    // The peer in the path is the sender address the snapshot carries.
    expect(decodeURIComponent(request.url())).toContain(`/link-ps/${SENDER}`);
    await expect(page.getByText('Saved.')).toBeVisible();
  });
});
