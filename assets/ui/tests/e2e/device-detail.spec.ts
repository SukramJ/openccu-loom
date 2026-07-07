import { test, expect, type Page } from '@playwright/test';
import { mockAllApis } from './helpers/mock-api';

// A representative HmIP wall thermostat. Fixture shape mirrors
// doc-screenshots.spec.ts's applyDeviceMocks() — a MASTER-paramset FLOAT
// field (TEMPERATURE_OFFSET) is the write target under test.
const DEVICE_ADDRESS = '0001D3C99B4E2F';

function deviceDetailFixture() {
  return {
    address: DEVICE_ADDRESS,
    central: 'ccu1',
    interface: 'HmIP-RF',
    interface_id: 'ccu1-HmIP-RF',
    model: 'HmIP-WTH-2',
    model_label: 'Wall Thermostat with Humidity Sensor',
    name: 'Living Room Thermostat',
    manufacturer: 'eQ-3',
    available: true,
    channels_count: 3,
    updatable: false,
    update_available: false,
    rooms: ['Living Room'],
    functions: ['Climate'],
    master_pushes_config_pending: false,
    has_sub_devices: false,
    firmware: {},
    availability: {
      IsReachable: true,
      LastUpdated: '2026-06-20T08:30:00Z',
      LowBattery: false,
      SignalStrength: -58,
    },
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
        address: `${DEVICE_ADDRESS}:1`,
        number: 1,
        name: 'Living Room Thermostat',
        type: 'HEATING_CLIMATECONTROL_TRANSCEIVER',
        type_label: 'Heating Thermostat',
        paramset_key: 'VALUES',
        paramset_keys: ['VALUES', 'MASTER'],
        data_points_count: 9,
      },
    ],
  };
}

function uiSchemaFixture(channel: number) {
  return {
    channel: {
      address: `${DEVICE_ADDRESS}:${channel}`,
      number: channel,
      type: 'HEATING_CLIMATECONTROL_TRANSCEIVER',
      label: 'Heating Thermostat',
      device_address: DEVICE_ADDRESS,
    },
    groups: [
      {
        id: 'temperature',
        label: 'Temperature',
        parameters: ['TEMPERATURE_OFFSET'],
      },
    ],
    parameters: [
      {
        name: 'TEMPERATURE_OFFSET',
        label: 'Temperature offset',
        help: 'Corrects the measured temperature by this amount.',
        type: 'FLOAT',
        unit: '°C',
        min: -3.5,
        max: 3.5,
        default: 0,
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        value: 0.5,
        observed: true,
        group_id: 'temperature',
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
    const ch = m ? Number(m[1]) : 1;
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
  await page.route('**/api/v1/sessions/edit', (route) =>
    route.fulfill({
      json: {
        session_id: 'e2e-session',
        owner: 'admin',
        expires_at: new Date(Date.now() + 300000).toISOString(),
      },
    }),
  );
}

test.describe('Device detail — MASTER parameter write', () => {
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

  test('editing a FLOAT MASTER field and saving writes the paramset and shows a success toast', async ({
    page,
  }) => {
    let putBody: unknown = null;
    await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}:1/paramsets/MASTER`, async (route) => {
      putBody = route.request().postDataJSON();
      await route.fulfill({ json: { status: 'ok' } });
    });

    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    await page.waitForSelector('#main');
    await page.getByRole('tab', { name: 'Configure' }).click();
    await page.waitForSelector('text=Temperature offset');

    // Locate the FLOAT input rendered for TEMPERATURE_OFFSET and change it.
    // The single MASTER group in this fixture carries exactly one
    // parameter, so the page's only number input is unambiguous.
    const input = page.locator('input[type="number"]').first();
    await input.fill('1.5');
    await input.blur();

    // The sticky save bar shows the dirty-field count and becomes enabled.
    const saveButton = page.getByRole('button', { name: /Save \(\d+\)/ });
    await expect(saveButton).toBeEnabled();
    await saveButton.click();

    // The write reaches the MASTER paramset PUT endpoint with the edited value...
    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody).toMatchObject({ TEMPERATURE_OFFSET: 1.5 });

    // ...and a success toast confirms the write to the operator.
    await expect(page.getByText('Saved.')).toBeVisible();
  });
});
