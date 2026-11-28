import { test, expect, type Page } from './helpers/fixtures';
import { fixture, mockAllApis } from './helpers/mock-api';
import { DEVICE_ADDRESS, EDIT_TOKEN, applyDeviceMocks } from './helpers/thermostat-device';

// The device editor runs in two hosts: the SPA's device route and the web
// component an embedding page (Home Assistant) mounts (ADR 0088, ADR 0089).
// Both mount the same Svelte component, and this scenario runs against
// both, so a change that breaks the editor fails here twice and a change
// that breaks only the embedding fails here once.

// The fixture's schema carries TEMPERATURE_OFFSET on the device block and on
// channel 1; channel 1 is the one the scenario edits.
function offsetLabel(page: Page) {
  return page.locator('section[data-channel="1"]').getByText('Temperature offset');
}

const hosts: { name: string; open: (page: Page) => Promise<void> }[] = [
  {
    name: 'SPA route',
    open: async (page) => {
      await page.goto(`http://localhost:5173/app/#/devices/${DEVICE_ADDRESS}`);
      await page.waitForSelector('#main');
      await page.getByRole('tab', { name: 'Configure' }).click();
    },
  },
  {
    name: 'web component',
    open: async (page) => {
      await page.goto(
        `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
      );
      // The component shows the configuration editor and the change log
      // only, so the editor is the first tab and opens by itself.
      await expect(page.getByRole('tab', { name: 'Configure' })).toBeVisible();
      await expect(page.getByRole('tab', { name: 'Overview' })).toHaveCount(0);
    },
  },
];

for (const host of hosts) {
  test.describe(`Device editor in the ${host.name}`, () => {
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

    test('renders the MASTER parameters and saves an edit through the write preview', async ({
      page,
    }) => {
      const pageErrors: string[] = [];
      page.on('pageerror', (err) => pageErrors.push(err.message));
      let putBody: unknown = null;
      let putEditToken: string | undefined;
      await page.route('**/api/v1/devices/*/paramsets/MASTER', async (route) => {
        putBody = route.request().postDataJSON();
        putEditToken = route.request().headers()['x-edit-token'];
        await route.fulfill({ json: { status: 'ok' } });
      });

      await host.open(page);
      await expect(offsetLabel(page)).toBeVisible();

      const input = page.locator('section[data-channel="1"]').locator('input[type="number"]').first();
      await input.fill('1.5');
      await input.blur();
      const saveButton = page.getByRole('button', { name: 'Apply', exact: true });
      await expect(saveButton).toBeEnabled();
      await saveButton.click();

      const dialog = page.getByRole('dialog', { name: 'Review this write' });
      await expect(dialog).toBeVisible();
      expect(putBody).toBeNull();
      await dialog.getByRole('button', { name: 'Write', exact: true }).click();

      await expect.poll(() => putBody).not.toBeNull();
      expect(putBody).toMatchObject({ TEMPERATURE_OFFSET: 1.5 });
      expect(putEditToken).toBe(EDIT_TOKEN);
      await expect(page.getByText('Parameters saved.')).toBeVisible();
      expect(pageErrors).toEqual([]);
    });
  });
}

test.describe('Device editor web component — embedding contract', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await applyDeviceMocks(page);
  });

  test('every call goes through the transport and stays inside the published route list', async ({
    page,
  }) => {
    // The element makes no request of its own: every /api request on the
    // page carries the host transport's marker.
    const direct: string[] = [];
    page.on('request', (req) => {
      const url = new URL(req.url());
      if (url.pathname.startsWith('/api/') && req.headers()['x-test-host-transport'] !== '1') {
        direct.push(url.pathname);
      }
    });
    // A call outside the route list is refused by the element and logged.
    const refused: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() === 'error' && msg.text().includes("outside the editor's route list")) {
        refused.push(msg.text());
      }
    });
    await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}/central-links`, (route) =>
      route.fulfill({ json: [] }),
    );
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    // Walk every part the element shows, so each part's calls are made.
    await page.getByRole('tab', { name: 'Direct links' }).click();
    await page.getByRole('tab', { name: 'History' }).click();
    await expect(page.getByRole('tab', { name: 'History' })).toHaveAttribute('aria-selected', 'true');

    const calls = await page.evaluate(() => (window as unknown as { __transportCalls: string[] }).__transportCalls);
    expect(calls.length).toBeGreaterThan(0);
    expect(refused).toEqual([]);
    expect(direct).toEqual([]);
  });

  test('a select opens inside the component with its styles', async ({ page }) => {
    // The recording picker on the history tab is a select; it is offered
    // while the daemon forwards measurements.
    const info = fixture('info.json') as { capabilities: string[] };
    await page.route('**/api/v1/info', (route) =>
      route.fulfill({ json: { ...info, capabilities: [...info.capabilities, 'measurements.v1'] } }),
    );
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    await page.getByRole('tab', { name: 'History' }).click();

    const trigger = page.getByRole('combobox').filter({ hasText: 'Living Room Thermostat (1)' });
    await expect(trigger).toBeVisible();
    // Tailwind's border utilities read a variable whose value only an
    // `@property` rule on the document supplies (#1067 step 2a).
    await expect(trigger).toHaveCSS('border-top-width', '1px');
    await trigger.click();
    const listbox = page.getByRole('listbox');
    await expect(listbox).toBeVisible();
    // The portal target sits in the component's shadow root: a list in the
    // document body would be outside every style the component carries.
    const inDocumentBody = await page.evaluate(
      () => document.body.querySelector('[role="listbox"]') !== null,
    );
    expect(inDocumentBody).toBe(false);
  });

  test('the theme tokens reach the component', async ({ page }) => {
    // The `--ha-*` tokens of @thingfabric/fabric-ui carry a `:host` form beside
    // `:root` (thingfabric/go-fabric-ui#72), so they reach the component's
    // shadow root and the open list has a background.
    const info = fixture('info.json') as { capabilities: string[] };
    await page.route('**/api/v1/info', (route) =>
      route.fulfill({ json: { ...info, capabilities: [...info.capabilities, 'measurements.v1'] } }),
    );
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    await page.getByRole('tab', { name: 'History' }).click();
    await page.getByRole('combobox').filter({ hasText: 'Living Room Thermostat (1)' }).click();
    const listbox = page.getByRole('listbox');
    await expect(listbox).toBeVisible();
    await expect(listbox).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)', { timeout: 2000 });
  });

  test('a link to another editor page goes to the host, not to the page address', async ({ page }) => {
    await page.route(`**/api/v1/devices/${DEVICE_ADDRESS}/central-links`, (route) =>
      route.fulfill({ json: [] }),
    );
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    const before = page.url();
    await page.getByRole('tab', { name: 'Direct links' }).click();
    await page.getByRole('button', { name: 'New link', exact: true }).click();
    await expect
      .poll(() => page.evaluate(() => (window as unknown as { __navigations: string[] }).__navigations))
      .toEqual([expect.stringContaining('#/links/new')]);
    expect(page.url()).toBe(before);
  });

  test('the HA skin and its dark palette reach the theme tokens', async ({ page }) => {
    // The tokens select on the shadow host (`:host([data-skin="ha"])`,
    // `:host([data-skin="ha"].dark)`), so the element itself has to carry
    // `data-skin` and `dark`. The test host defines none of Home Assistant's
    // variables, so the HA skin resolves to its literal defaults; the
    // standalone palette would give #ffffff in both modes.
    const token = (p: Page) =>
      offsetLabel(p).evaluate((n) =>
        getComputedStyle(n).getPropertyValue('--ha-primary-background-color').trim(),
      );
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    expect(await token(page)).toBe('#fafafa');
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}&dark=1`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    expect(await token(page)).toBe('#111111');
  });

  test('dark mode reaches the editor inside the shadow root', async ({ page }) => {
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}&dark=1`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    // A `dark:` utility resolves only when `.dark` matches an ancestor
    // inside the shadow root; the same element in light mode is the control.
    const darkColor = await offsetLabel(page).evaluate((n) => getComputedStyle(n).color);
    await page.goto(
      `http://localhost:5173/app/tests/e2e/component-host/index.html?address=${DEVICE_ADDRESS}`,
    );
    await expect(offsetLabel(page)).toBeVisible();
    const lightColor = await offsetLabel(page).evaluate((n) => getComputedStyle(n).color);
    expect(darkColor).not.toBe(lightColor);
  });
});
