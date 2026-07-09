import { test, expect, type Page } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

// Client-side snapshot export/import on the channel MASTER editor
// (ChannelPanel.svelte's exportSnapshot/onImportFile). Fixture shape
// mirrors channel-editor.spec.ts, trimmed to the same single FLOAT
// MASTER parameter — export/import round-trips the whole `values` map
// regardless of parameter count, so one parameter is enough to prove
// the wiring.
const DEVICE_ADDRESS = '0002E4A17B93C1';
const CHANNEL_ADDRESS = `${DEVICE_ADDRESS}:4`;

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
        address: CHANNEL_ADDRESS,
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

function uiSchemaFixture() {
  return {
    channel: {
      address: CHANNEL_ADDRESS,
      number: 4,
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
  await page.route('**/api/v1/**/channels/*/ui-schema*', (route) =>
    route.fulfill({ json: uiSchemaFixture() }),
  );
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
  await page.route('**/api/v1/sessions/edit', (route) =>
    route.fulfill({
      json: {
        token: 'e2e-lock-token',
        key: `channel:${CHANNEL_ADDRESS}:MASTER`,
        expires: new Date(Date.now() + 300000).toISOString(),
      },
    }),
  );
}

async function gotoConfigureTab(page: Page): Promise<void> {
  await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
  await page.waitForSelector('#main');
  await page.getByRole('tab', { name: 'Configure' }).click();
  await page.waitForSelector('text=On time');
}

test.describe('Channel editor — export / import snapshot', () => {
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

  test('export downloads a JSON snapshot carrying the channel id and current values', async ({ page }) => {
    await gotoConfigureTab(page);

    const downloadPromise = page.waitForEvent('download');
    await page.getByRole('button', { name: 'Export', exact: true }).click();
    const download = await downloadPromise;

    // Chrome's download manager sanitises ":" out of suggested filenames
    // (the browser, not our code — the app sets `a.download` to the raw
    // "<address>:<channel>-MASTER-<timestamp>.json").
    expect(download.suggestedFilename()).toMatch(/^0002E4A17B93C1.4-MASTER-.*\.json$/);

    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const chunk of stream) chunks.push(chunk as Buffer);
    const snapshot = JSON.parse(Buffer.concat(chunks).toString('utf-8'));

    expect(snapshot).toMatchObject({
      openccu_loom_export: 1,
      paramset: 'MASTER',
      peer: null,
      channel: { address: CHANNEL_ADDRESS },
      values: { ON_TIME: 0.5 },
    });

    await expect(page.getByText('Snapshot downloaded.')).toBeVisible();
  });

  test('import stages a matching snapshot into the working copy and enables Save', async ({ page }) => {
    await gotoConfigureTab(page);

    const input = page.locator('input[type="number"]').first();
    await expect(input).toHaveValue('0.5');

    const snapshot = {
      openccu_loom_export: 1,
      exported_at: new Date().toISOString(),
      channel: { address: CHANNEL_ADDRESS },
      paramset: 'MASTER',
      peer: null,
      values: { ON_TIME: 9.5 },
    };

    const fileChooserPromise = page.waitForEvent('filechooser');
    await page.getByRole('button', { name: 'Import', exact: true }).click();
    const fileChooser = await fileChooserPromise;
    await fileChooser.setFiles({
      name: 'snapshot.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(snapshot)),
    });

    await expect(page.getByText('Import staged — press Save to apply.')).toBeVisible();
    await expect(input).toHaveValue('9.5');

    const saveButton = page.getByRole('button', { name: /Save \(\d+\)/ }).first();
    await expect(saveButton).toBeEnabled();
  });

  test('importing a snapshot from a different channel prompts for confirmation before staging', async ({ page }) => {
    await gotoConfigureTab(page);

    const snapshot = {
      openccu_loom_export: 1,
      exported_at: new Date().toISOString(),
      channel: { address: '0002E4A17B93C1:9' },
      paramset: 'MASTER',
      peer: null,
      values: { ON_TIME: 3 },
    };

    const fileChooserPromise = page.waitForEvent('filechooser');
    await page.getByRole('button', { name: 'Import', exact: true }).click();
    const fileChooser = await fileChooserPromise;
    await fileChooser.setFiles({
      name: 'snapshot.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(snapshot)),
    });

    // Cross-channel snapshot: the shared confirm dialog gates the import
    // rather than staging it silently.
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Import', exact: true }).click();

    await expect(page.getByText('Import staged — press Save to apply.')).toBeVisible();
  });
});
