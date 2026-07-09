import { test, expect, type Page } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

// A minimal HmIP switch actuator. Fixture shape mirrors
// device-detail.spec.ts's deviceDetailFixture()/uiSchemaFixture(), trimmed
// to a single writable FLOAT MASTER parameter with no min/max (so it
// renders as a plain number input rather than the slider variant) — the
// undo/redo and edit-lock flows under test do not need the slider path.
const DEVICE_ADDRESS = '0002E4A17B93C1';

function deviceDetailFixture() {
  return {
    address: DEVICE_ADDRESS,
    central: 'ccu1',
    interface: 'HmIP-RF',
    interface_id: 'ccu1-HmIP-RF',
    model: 'HmIP-PSM',
    model_label: 'Switch Actuator',
    name: 'Bookshelf Switch',
    manufacturer: 'eQ-3',
    available: true,
    channels_count: 2,
    updatable: false,
    update_available: false,
    rooms: ['Living Room'],
    functions: ['Lighting'],
    master_pushes_config_pending: false,
    has_sub_devices: false,
    firmware: {},
    availability: { IsReachable: true },
    channels: [
      {
        address: `${DEVICE_ADDRESS}:0`,
        number: 0,
        name: '',
        type: 'MAINTENANCE',
        type_label: 'Maintenance',
        paramset_key: 'VALUES',
        paramset_keys: ['VALUES', 'MASTER'],
        data_points_count: 6,
      },
      {
        address: `${DEVICE_ADDRESS}:4`,
        number: 4,
        name: 'Bookshelf Switch',
        type: 'SWITCH_VIRTUAL_RECEIVER',
        type_label: 'Switch',
        paramset_key: 'VALUES',
        paramset_keys: ['VALUES', 'MASTER'],
        data_points_count: 1,
      },
    ],
  };
}

function uiSchemaFixture(channel: number) {
  return {
    channel: {
      address: `${DEVICE_ADDRESS}:${channel}`,
      number: channel,
      type: 'SWITCH_VIRTUAL_RECEIVER',
      label: 'Switch',
      device_address: DEVICE_ADDRESS,
    },
    groups: [
      {
        id: 'timing',
        label: 'Timing',
        parameters: ['ON_TIME'],
      },
    ],
    parameters: [
      {
        name: 'ON_TIME',
        label: 'On time',
        type: 'FLOAT',
        unit: 's',
        default: 0,
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        value: 0.5,
        observed: true,
        group_id: 'timing',
      },
    ],
  };
}

async function applyDeviceMocks(page: Page): Promise<void> {
  await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}`, (route) =>
    route.fulfill({ json: deviceDetailFixture() }),
  );
  await page.route('**/api/v1/**/channels/*/ui-schema*', (route) => {
    const m = route.request().url().match(/channels\/(\d+)\/ui-schema/);
    const ch = m ? Number(m[1]) : 4;
    return route.fulfill({ json: uiSchemaFixture(ch) });
  });
  await page.route('**/api/v1/**/channels/0/data-points', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/**/channels/*/data-points', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}/cdps`, (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/devices/*/schedule', (route) =>
    route.fulfill({ status: 404, body: JSON.stringify({ detail: 'no schedule' }) }),
  );
}

async function gotoConfigureTab(page: Page): Promise<void> {
  await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
  await page.waitForSelector('#main');
  await page.getByRole('tab', { name: 'Configure' }).click();
  await page.waitForSelector('text=On time');
}

test.describe('Channel editor — undo/redo', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await applyDeviceMocks(page);
    await page.route('**/api/v1/sessions/edit', (route) =>
      route.fulfill({
        json: {
          token: 'e2e-lock-token',
          key: `channel:${DEVICE_ADDRESS}:4:MASTER`,
          expires: new Date(Date.now() + 300000).toISOString(),
        },
      }),
    );
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('undo then redo restore the prior working value, and the redo-applied value is what gets saved', async ({
    page,
  }) => {
    let putBody: unknown = null;
    await page.route('**/api/v1/devices/*/paramsets/MASTER', async (route) => {
      putBody = route.request().postDataJSON();
      await route.fulfill({ json: { status: 'ok' } });
    });

    await gotoConfigureTab(page);

    const input = page.locator('input[type="number"]').first();
    await expect(input).toHaveValue('0.5');

    // Two sequential edits create two undo-stack entries.
    await input.fill('1.5');
    await input.blur();
    await expect(input).toHaveValue('1.5');
    await input.fill('2.5');
    await input.blur();
    await expect(input).toHaveValue('2.5');

    // The undo/redo buttons render as a bare "↶"/"↷" glyph with the
    // human-readable label only on `title`, so target them by title
    // rather than accessible name (which would resolve to the glyph).
    const undoButton = page.getByTitle('Undo (Ctrl+Z)');
    const redoButton = page.getByTitle('Redo (Ctrl+Y)');

    // Undo rolls back exactly one step (2.5 -> 1.5), not all the way to
    // the server value.
    await undoButton.click();
    await expect(input).toHaveValue('1.5');

    // Redo replays the same step forward again (1.5 -> 2.5).
    await redoButton.click();
    await expect(input).toHaveValue('2.5');

    const saveButton = page.getByRole('button', { name: /Save \(\d+\)/ }).first();
    await expect(saveButton).toBeEnabled();
    await saveButton.click();

    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody).toMatchObject({ ON_TIME: 2.5 });
    await expect(page.getByText('Saved.')).toBeVisible();
  });

  test('undoing back to the server value disables Save and clears the dirty indicator', async ({
    page,
  }) => {
    await gotoConfigureTab(page);

    const input = page.locator('input[type="number"]').first();
    await input.fill('1.5');
    await input.blur();

    const saveButton = page.getByRole('button', { name: /Save \(\d+\)/ }).first();
    await expect(saveButton).toBeEnabled();

    await page.getByTitle('Undo (Ctrl+Z)').click();
    await expect(input).toHaveValue('0.5');

    // Back at the server value: nothing left to save, and the sticky
    // "unsaved changes" bar disappears.
    await expect(page.getByText('Unsaved changes')).toHaveCount(0);
  });
});

test.describe('Channel editor — edit-lock lifecycle', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await applyDeviceMocks(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('a heartbeat failure blocks Save and tells the operator their lock was lost', async ({
    page,
  }) => {
    // The clock must be installed before navigation so ChannelPanel's
    // 90s heartbeat setInterval (see ChannelPanel.svelte's edit-lock
    // effect) is registered against the fake timers from the start.
    await page.clock.install();

    let heartbeatShouldFail = false;
    await page.route('**/api/v1/sessions/edit/heartbeat', (route) => {
      if (heartbeatShouldFail) {
        return route.fulfill({ status: 423, json: { detail: 'lock taken over' } });
      }
      return route.fulfill({
        json: {
          token: 'e2e-lock-token',
          key: `channel:${DEVICE_ADDRESS}:4:MASTER`,
          expires: new Date(Date.now() + 300000).toISOString(),
        },
      });
    });
    await page.route('**/api/v1/sessions/edit', (route) => {
      if (route.request().method() === 'DELETE') return route.fulfill({ status: 200 });
      return route.fulfill({
        json: {
          token: 'e2e-lock-token',
          key: `channel:${DEVICE_ADDRESS}:4:MASTER`,
          expires: new Date(Date.now() + 300000).toISOString(),
        },
      });
    });

    let putCount = 0;
    await page.route('**/api/v1/devices/*/paramsets/MASTER', async (route) => {
      putCount += 1;
      await route.fulfill({ json: { status: 'ok' } });
    });

    await gotoConfigureTab(page);

    // Acquire the lock (the open call already fired on mount) and make
    // an edit so Save has something to write.
    const input = page.locator('input[type="number"]').first();
    await input.fill('1.5');
    await input.blur();
    const saveButton = page.getByRole('button', { name: /Save \(\d+\)/ }).first();
    await expect(saveButton).toBeEnabled();

    // Simulate the lock being taken over / expiring: the next heartbeat
    // fails, and jumping the fake clock past the 90s interval fires it.
    heartbeatShouldFail = true;
    await page.clock.fastForward('01:31');

    // The panel surfaces the lost-lock banner without any further user
    // action — the operator is told before they even try to save.
    await expect(
      page.getByText(
        "Another session took over the edit lock, or your lock expired. Re-open this editor before saving so you don't overwrite concurrent changes.",
      ),
    ).toBeVisible();

    // Save is still clickable (the field is still dirty) but must
    // refuse to write and must not clobber whoever holds the lock now.
    await saveButton.click();
    await expect(page.getByText('Edit lock lost')).toBeVisible();
    expect(putCount).toBe(0);
  });
});
