import { test, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// Curated documentation screenshots for docs/user/. These are NOT
// visual-regression baselines — they are hand-framed PNGs embedded in
// the MkDocs user guide and live under docs/user/img/, deliberately
// outside the e2e snapshot dirs so the toHaveScreenshot baseline
// machinery never touches them. Run with:
//   cd assets/ui && npx playwright test doc-screenshots
//
// Every view reuses the same hermetic mock-api + light-theme setup as
// visual.spec.ts; per-test route overrides (registered AFTER
// mockAllApis, so they win in Playwright's last-match-first order)
// supply the richer per-device / per-channel / Matter shapes the doc
// shots need.

const IMG_DIR = '../../docs/user/img';

// A representative HmIP wall thermostat. The address is referenced by
// the per-device + per-channel route overrides below.
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
    updatable: true,
    update_available: false,
    rooms: ['Living Room'],
    functions: ['Climate'],
    master_pushes_config_pending: true,
    has_sub_devices: false,
    firmware: { Current: '1.8.10', Updatable: true, UpdateState: 'UP_TO_DATE' },
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
      {
        address: `${DEVICE_ADDRESS}:2`,
        number: 2,
        name: 'Temperature Sensor',
        type: 'CLIMATE_TRANSCEIVER',
        type_label: 'Temperature Sensor',
        paramset_key: 'VALUES',
        paramset_keys: ['VALUES'],
        data_points_count: 2,
      },
    ],
  };
}

// A MASTER ui-schema for channel 1 with a couple of grouped, labelled
// config parameters so the channel-configuration form renders with real
// controls (a number input, an enum dropdown, a switch).
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
        parameters: ['TEMPERATURE_OFFSET', 'TEMPERATURE_MAXIMUM'],
      },
      {
        id: 'display',
        label: 'Display',
        parameters: ['TEMPERATURE_DISPLAY_UNIT', 'BUTTON_LOCK'],
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
      {
        name: 'TEMPERATURE_MAXIMUM',
        label: 'Maximum temperature',
        help: 'Upper bound the setpoint may reach.',
        type: 'FLOAT',
        unit: '°C',
        min: 5,
        max: 30,
        default: 30,
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        value: 28,
        observed: true,
        group_id: 'temperature',
      },
      {
        name: 'TEMPERATURE_DISPLAY_UNIT',
        label: 'Display unit',
        type: 'ENUM',
        default: 0,
        value_list: [
          { value: 0, key: 'CELSIUS', label: 'Celsius' },
          { value: 1, key: 'FAHRENHEIT', label: 'Fahrenheit' },
        ],
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        value: 0,
        observed: true,
        group_id: 'display',
      },
      {
        name: 'BUTTON_LOCK',
        label: 'Button lock (child lock)',
        help: 'Disables local button input on the device.',
        type: 'BOOL',
        default: false,
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        value: false,
        observed: true,
        group_id: 'display',
      },
    ],
  };
}

function maintenanceDataPoints() {
  return [
    {
      parameter: 'RSSI_DEVICE',
      parameter_label: 'Signal strength (device)',
      type: 'INTEGER',
      unit: 'dBm',
      value: -58,
    },
    {
      parameter: 'LOW_BAT',
      parameter_label: 'Low battery',
      type: 'BOOL',
      value: false,
    },
    {
      parameter: 'OPERATING_VOLTAGE',
      parameter_label: 'Operating voltage',
      type: 'FLOAT',
      unit: 'V',
      value: 2.9,
    },
  ];
}

async function applyDeviceMocks(page: Page): Promise<void> {
  // Per-device detail (single DeviceDetail with channels).
  await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}`, (route) =>
    route.fulfill({ json: deviceDetailFixture() }),
  );
  // MASTER ui-schema for the channel-config form. Scope every override
  // to the /api/v1/ prefix so the broad globs never intercept Vite's
  // own source-module requests (e.g. .../components/schedule/...).
  await page.route('**/api/v1/**/channels/*/ui-schema*', (route) => {
    const m = route.request().url().match(/channels\/(\d+)\/ui-schema/);
    const ch = m ? Number(m[1]) : 1;
    return route.fulfill({ json: uiSchemaFixture(ch) });
  });
  // Channel-0 data points feed the maintenance grid on the overview tab.
  await page.route('**/api/v1/**/channels/0/data-points', (route) =>
    route.fulfill({ json: maintenanceDataPoints() }),
  );
  await page.route('**/api/v1/**/channels/*/data-points', (route) =>
    route.fulfill({ json: [] }),
  );
  // No CDP tiles — keeps the overview deterministic.
  await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}/cdps`, (route) =>
    route.fulfill({ json: [] }),
  );
  // Schedule probe: report "no schedule" so the schedule sub-tab is hidden.
  await page.route('**/api/v1/devices/*/schedule', (route) =>
    route.fulfill({ status: 404, body: JSON.stringify({ detail: 'no schedule' }) }),
  );
  // Edit session: grant the lock so the form is not flagged "locked by other".
  await page.route('**/api/v1/sessions/edit', (route) =>
    route.fulfill({
      json: {
        session_id: 'doc-session',
        owner: 'admin',
        expires_at: new Date(Date.now() + 300000).toISOString(),
      },
    }),
  );
}

test.describe('Documentation screenshots (light mode)', () => {
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

  test('device list / main navigation', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/devices');
    await page.waitForSelector('#main');
    await page.waitForTimeout(1200);
    await addStylesForStableScreenshots(page);
    await page.screenshot({ path: `${IMG_DIR}/web-ui-device-list.png` });
  });

  test('device detail with channels', async ({ page }) => {
    await applyDeviceMocks(page);
    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    await page.waitForSelector('#main');
    // Land on the Configure tab so the channel strip + config form are
    // both visible — the clearest single shot of "channels & parameters".
    await page.getByRole('tab', { name: 'Configure' }).click();
    await page.waitForSelector('text=Temperature offset');
    await page.waitForTimeout(800);
    await addStylesForStableScreenshots(page);
    await page.screenshot({ path: `${IMG_DIR}/web-ui-device-detail.png` });
  });

  test('channel configuration form', async ({ page }) => {
    await applyDeviceMocks(page);
    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}/channels/1`);
    await page.waitForSelector('#main');
    await page.getByRole('tab', { name: 'Configure' }).click();
    await page.waitForSelector('text=Temperature offset');
    await page.waitForTimeout(800);
    await addStylesForStableScreenshots(page);
    // Frame just the configuration form card so it is unambiguously the
    // subject (the full-page device-detail shot already shows the
    // surrounding header + channel strip). The form lives in the Card
    // that carries the "Heating Thermostat" channel heading.
    const formCard = page
      .locator('main')
      .getByRole('heading', { name: 'Heating Thermostat' })
      .locator('xpath=ancestor::div[contains(@class,"rounded")][1]');
    await formCard.screenshot({ path: `${IMG_DIR}/web-ui-channel-config.png` });
  });

  test('hidden parameters with a row expanded', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/settings?tab=visibility');
    await page.waitForSelector('#main');
    await page.waitForTimeout(800);
    // Expand one row: the scope list is the part of the screen the
    // user guide has to explain, and it is closed on arrival.
    await page.getByTestId('group-expand-ACTIVITY_STATE').click();
    await page.getByTestId('group-scopes-ACTIVITY_STATE').scrollIntoViewIfNeeded();
    await page.waitForTimeout(500);
    await addStylesForStableScreenshots(page);
    await page.screenshot({ path: `${IMG_DIR}/web-ui-hidden-parameters.png` });
  });

  test('Matter view with QR + manual pairing code', async ({ page }) => {
    // Matter bridge enabled with an open commissioning window.
    await page.route('**/api/v1/matter/status', (route) =>
      route.fulfill({
        json: {
          enabled: true,
          listening: true,
          endpoint_count: 4,
          fabric_count: 1,
          enabled_count: 4,
          advertising: true,
          commissioning_window_open: true,
          commissioning_window_duration_seconds: 300,
        },
      }),
    );
    // Setup payload carries the real QR (MT:…) + 11-digit manual code,
    // which MatterPair renders into the SVG QR + the code line.
    await page.route('**/api/v1/matter/setup-payload', (route) =>
      route.fulfill({
        json: {
          discriminator: 3840,
          passcode: 20202021,
          vendor_id: 65521,
          product_id: 32768,
          qr_code: 'MT:Y.K9042C00KA0648G00',
          manual_code: '34970112332',
        },
      }),
    );
    await page.goto('http://localhost:5173/app/#/matter/pair');
    await page.waitForSelector('#main');
    await page.waitForSelector('text=Manual code');
    await page.waitForTimeout(1000);
    await addStylesForStableScreenshots(page);
    await page.screenshot({ path: `${IMG_DIR}/matter-pairing.png` });
  });
});
