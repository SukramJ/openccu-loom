import { test, expect, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

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

const EDIT_TOKEN = 'e2e-lock-token';

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
  // EditSessionResponse — token / key / expires. The token is what the
  // editor puts in X-Edit-Token on the MASTER write; a lock body without
  // it leaves the header off and the daemon answers 423, so a
  // wrong-shaped mock makes the save test pass against a rejected
  // request.
  await page.route('**/api/v1/sessions/edit', (route) =>
    route.fulfill({
      json: {
        token: EDIT_TOKEN,
        key: `channel:${DEVICE_ADDRESS}:1:MASTER`,
        subject: 'admin',
        expires: new Date(Date.now() + 300000).toISOString(),
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
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false }),
      );
    });
  });

  test('editing a FLOAT MASTER field and saving writes the paramset and shows a success toast', async ({
    page,
  }) => {
    let putBody: unknown = null;
    let putEditToken: string | undefined;
    // The channel address (…:1) is percent-encoded in the request URL, so
    // match the address segment with a wildcard rather than a literal colon.
    await page.route('**/api/v1/devices/*/paramsets/MASTER', async (route) => {
      putBody = route.request().postDataJSON();
      putEditToken = route.request().headers()['x-edit-token'];
      await route.fulfill({ json: { status: 'ok' } });
    });

    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    await page.waitForSelector('#main');
    await page.getByRole('tab', { name: 'Configure' }).click();
    await page.waitForSelector('text=Temperature offset');

    // Locate the FLOAT input rendered for TEMPERATURE_OFFSET and change it.
    // The single MASTER group in this fixture carries exactly one
    // parameter, so the page's only number input is unambiguous.
    const input = page.locator('section[data-channel="1"]').locator('input[type="number"]').first();
    await input.fill('1.5');
    await input.blur();

    // The parameter page's one save bar appears with the edit.
    const saveButton = page.getByRole('button', { name: 'Apply', exact: true });
    await expect(saveButton).toBeEnabled();
    await saveButton.click();

    // A MASTER save goes through the write preview (the preference is on by
    // default), which names the request and shows the from/to before the
    // write leaves. Nothing may reach the CCU until it is confirmed.
    const dialog = page.getByRole('dialog', { name: 'Review this write' });
    await expect(dialog).toBeVisible();
    // The parameter appears twice in the dialog — once in the from/to table,
    // once in the JSON body — so assert on the dialog's text rather than
    // locating an element.
    await expect(dialog).toContainText('TEMPERATURE_OFFSET');
    await expect(dialog).toContainText(
      `PUT /api/v1/devices/${DEVICE_ADDRESS}:1/paramsets/MASTER`,
    );
    expect(putBody).toBeNull();
    await dialog.getByRole('button', { name: 'Write', exact: true }).click();

    // The write reaches the MASTER paramset PUT endpoint with the edited value...
    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody).toMatchObject({ TEMPERATURE_OFFSET: 1.5 });

    // ...carrying the edit lock the daemon requires on MASTER — without
    // the header the real write comes back 423 Locked.
    expect(putEditToken).toBe(EDIT_TOKEN);

    // ...and a success toast confirms the write to the operator.
    await expect(page.getByText('Parameters saved.')).toBeVisible();
  });

  test('with the write preview turned off, saving writes straight through', async ({
    page,
  }) => {
    let putBody: unknown = null;
    await page.route('**/api/v1/devices/*/paramsets/MASTER', async (route) => {
      putBody = route.request().postDataJSON();
      await route.fulfill({ json: { status: 'ok' } });
    });

    // The preference is read from localStorage at module load, so it has to
    // be seeded before the SPA boots rather than toggled afterwards.
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ locale: 'en', theme: 'light', writePreview: false }),
      );
    });

    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    await page.waitForSelector('#main');
    await page.getByRole('tab', { name: 'Configure' }).click();
    await page.waitForSelector('text=Temperature offset');

    const input = page.locator('section[data-channel="1"]').locator('input[type="number"]').first();
    await input.fill('2.5');
    await input.blur();
    await page.getByRole('button', { name: 'Apply', exact: true }).click();

    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody).toMatchObject({ TEMPERATURE_OFFSET: 2.5 });
    await expect(page.getByRole('dialog', { name: 'Review this write' })).toHaveCount(0);
  });
});

function seedPrefs(page: Page, theme: 'light' | 'dark') {
  return page.addInitScript((th) => {
    localStorage.setItem(
      'openccu-loom.prefs.v1',
      JSON.stringify({ theme: th, locale: 'en', navCollapsed: false, expertMode: false }),
    );
  }, theme);
}

// Configuration repair (mock-api configRepairOutcomes): the dry run reports
// a clean :0, a drifted :1 with two corrections and a :2 carrying an entry
// its description does not know.
async function openRepair(page: Page) {
  await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
  await page.waitForSelector('#main');
  await page.getByRole('button', { name: 'Repair config' }).click();
  const dialog = page.getByRole('dialog', { name: /Repair configuration of/ });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('above maximum 3.5')).toBeVisible();
  return dialog;
}

// MASTER multi-apply (mock-api MASTER_APPLY_TARGETS): two sibling
// thermostats, the second of which the apply mock refuses.
async function openApply(page: Page) {
  await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
  await page.waitForSelector('#main');
  await page.getByRole('tab', { name: 'Configure' }).click();
  await page.waitForSelector('text=Temperature offset');
  const input = page.locator('section[data-channel="1"]').locator('input[type="number"]').first();
  await input.fill('1.5');
  await input.blur();
  await page.getByRole('button', { name: 'Apply to identical channels…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Apply to identical channels' });
  await expect(dialog.getByText('Bedroom Thermostat').first()).toBeVisible();
  return dialog;
}

async function checkAndApply(dialog: ReturnType<Page['getByRole']>) {
  await dialog.getByRole('checkbox', { name: 'Select Bedroom Thermostat' }).check();
  await dialog.getByRole('checkbox', { name: 'Select Office Thermostat' }).check();
  await dialog.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(dialog.getByText(/MASTER description differs/)).toBeVisible();
  await dialog.getByRole('button', { name: 'Apply to 1' }).click();
  await expect(dialog.getByText('Applied', { exact: true })).toBeVisible();
}

for (const theme of ['light', 'dark'] as const) {
  test.describe(`Device detail — device admin dialogs (${theme})`, () => {
    test.beforeEach(async ({ page }) => {
      await mockAllApis(page);
      await applyDeviceMocks(page);
      await seedPrefs(page, theme);
    });

    test(`repair dry-run report ${theme}`, async ({ page }) => {
      await openRepair(page);
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`config-repair-${theme}.png`);
    });

    test(`apply-to-channels outcomes ${theme}`, async ({ page }) => {
      const dialog = await openApply(page);
      await checkAndApply(dialog);
      await page.waitForTimeout(500);
      await addStylesForStableScreenshots(page);
      await expect(page).toHaveScreenshot(`apply-to-channels-${theme}.png`);
    });
  });
}

test.describe('Device detail — configuration repair and multi-apply', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await applyDeviceMocks(page);
    await seedPrefs(page, 'light');
  });

  test('repair runs the dry run first and writes only the channels it flagged', async ({
    page,
  }) => {
    const bodies: unknown[] = [];
    await page.route('**/api/v1/devices/*/config/repair', async (route) => {
      bodies.push(route.request().postDataJSON());
      await route.fallback();
    });
    const dialog = await openRepair(page);
    await expect(dialog.getByText('LEGACY_TEMP_MODE', { exact: false })).toBeVisible();
    expect(bodies).toEqual([{ dry_run: true }]);

    await dialog.getByRole('button', { name: 'Repair 2 channels' }).click();
    await expect(dialog.getByText('Repaired', { exact: true })).toBeVisible();
    expect(bodies[1]).toEqual({
      dry_run: false,
      channels: [`${DEVICE_ADDRESS}:1`, `${DEVICE_ADDRESS}:2`],
    });
    await expect(page.getByText('Configuration repaired.')).toBeVisible();
  });

  test('multi-apply checks first and writes only the targets the check cleared', async ({
    page,
  }) => {
    const calls: { body: { targets: string[]; dry_run: boolean }; token?: string }[] = [];
    await page.route('**/api/v1/devices/*/paramsets/MASTER/apply-to', async (route) => {
      calls.push({
        body: route.request().postDataJSON(),
        token: route.request().headers()['x-edit-token'],
      });
      await route.fallback();
    });
    const dialog = await openApply(page);
    await checkAndApply(dialog);
    expect(calls.map((c) => [c.body.dry_run, c.body.targets])).toEqual([
      [true, ['0001D3C99B4E30:1', '0001D3C99B4E31:1']],
      [false, ['0001D3C99B4E30:1']],
    ]);
    expect(calls.every((c) => c.token === EDIT_TOKEN)).toBe(true);
    await expect(dialog.getByText(/sent 1\.5, device kept 1/)).toBeVisible();
  });
});

// 1×1 transparent PNG — enough for the browser to decode an image.
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
);

test.describe('Device detail — device picture', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await applyDeviceMocks(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false }),
      );
    });
  });

  test('shows the picture the icon route serves', async ({ page }) => {
    let iconPath = '';
    await page.route('**/api/v1/devices/*/icon', (route) => {
      iconPath = new URL(route.request().url()).pathname;
      return route.fulfill({ status: 200, contentType: 'image/png', body: TINY_PNG });
    });

    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    const picture = page.getByTestId('device-image');
    await expect(picture).toHaveAttribute('data-state', 'image');
    const img = picture.getByRole('img', { name: 'Picture of HmIP-WTH-2' });
    await expect(img).toBeVisible();
    await expect.poll(() => img.evaluate((el) => (el as HTMLImageElement).naturalWidth)).toBe(1);
    expect(iconPath).toBe(`/api/v1/devices/${DEVICE_ADDRESS}/icon`);
  });

  test('falls back to the device-type glyph when the route has no picture', async ({ page }) => {
    // mockAllApis answers the icon route with 404.
    await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
    const picture = page.getByTestId('device-image');
    await expect(picture).toHaveAttribute('data-state', 'fallback');
    await expect(picture.locator('img')).toHaveCount(0);
    await expect(picture.locator('svg[aria-label="Picture of HmIP-WTH-2"]')).toBeVisible();
  });
});
