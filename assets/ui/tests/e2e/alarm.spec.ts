import { test, expect } from './helpers/fixtures';
import { mockAllApis, mockAlarmTriggered, mockAlarmWizardDevices } from './helpers/mock-api';

// Alarm section (#/alarm, notes/concepts/alarm-concept.md §12). mockAllApis now wires
// sane defaults for every /api/v1/alarm/* route: two zones — "Erdgeschoss"
// (armed, full protection, steady state) and "Dachgeschoss" (disarmed) —
// three sensors, two outputs (one acoustic siren, one smoke-detector
// sounder) and a five-entry journal.

test.describe('Alarm', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('nav entry exists and routes to the overview', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/overview');
    await page.waitForSelector('#main');

    await page.getByRole('link', { name: 'Alarm system' }).click();

    await expect(page).toHaveURL(/#\/alarm$/);
    await expect(page.getByRole('heading', { name: 'Alarm system', level: 1 })).toBeVisible();
    // Overview is the default sub-route: its tab reads selected.
    await expect(page.getByRole('tab', { name: 'Overview', selected: true })).toBeVisible();
  });

  test('document title is localized', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await expect(page).toHaveTitle('Alarm system — OpenCCU-Loom');
  });

  test('overview shows both zone cards with localized state badges', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await expect(page.getByRole('heading', { name: 'Erdgeschoss', level: 3 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Dachgeschoss', level: 3 })).toBeVisible();

    // The state badge is the sole direct <span> child of each card's header
    // row (the icon+name block is the other child) — scoping this way avoids
    // the ambiguity of "Disarmed" also appearing on that zone's mode button.
    const egHeader = page
      .locator('div.flex.items-start.justify-between.gap-3')
      .filter({ hasText: 'Erdgeschoss' });
    await expect(egHeader.locator('> span')).toHaveText('Armed · Full protection');

    const ogHeader = page
      .locator('div.flex.items-start.justify-between.gap-3')
      .filter({ hasText: 'Dachgeschoss' });
    await expect(ogHeader.locator('> span')).toHaveText('Disarmed');
  });

  test('a latched motion detector offers a reset, per zone and fleet-wide', async ({
    page,
  }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(600);

    // The fixture latches one detector in zone-eg. Both the toolbar
    // control and that zone's own control appear; the other zone's does
    // not, because a reset must never reach into a zone the operator
    // did not ask about.
    await expect(page.getByTestId('reset-motion-all')).toBeVisible();
    await expect(page.getByTestId('reset-motion-zone-eg')).toBeVisible();
    await expect(page.getByTestId('reset-motion-zone-og')).toHaveCount(0);

    await page.getByTestId('reset-motion-zone-eg').click();
    // A reset that reports nothing is indistinguishable from one that
    // silently did nothing.
    await expect(page.getByText(/Motion detectors reset/)).toBeVisible();
  });

  test('no reset control appears while nothing is latched', async ({ page }) => {
    await page.route('**/api/v1/alarm/triggered-motion*', (route) =>
      route.fulfill({ json: [] }),
    );
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');
    await page.waitForTimeout(600);

    await expect(page.getByTestId('reset-motion-all')).toHaveCount(0);
    await expect(page.getByTestId('reset-motion-zone-eg')).toHaveCount(0);
  });

  test('sensors tab renders the sensor picker surface', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Sensors' }).click();
    await expect(page).toHaveURL(/#\/alarm\/picker$/);

    await expect(page.getByRole('button', { name: 'Add sensor' })).toBeVisible();
    await expect(page.getByText('Haustür')).toBeVisible();
  });

  test('outputs tab renders the output picker surface, incl. the smoke-sounder caveat', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Outputs' }).click();
    await expect(page).toHaveURL(/#\/alarm\/outputs$/);

    await expect(page.getByRole('button', { name: 'Add output' })).toBeVisible();
    await expect(page.getByText('Außensirene')).toBeVisible();
    await expect(page.getByText('Smoke-detector sounder')).toBeVisible();
    await expect(
      page.getByText('Smoke detectors double as sounders', { exact: false }),
    ).toBeVisible();
  });

  test('acoustic-siren card offers the device tone list even when the stored central is empty (address fallback)', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Outputs' }).click();
    await expect(page.getByText('Außensirene')).toBeVisible();

    // output-1 (Außensirene, VEQ0000010:1) deliberately carries an empty
    // `central` in alarm-outputs.json while the candidate row carries
    // "ccu1" — the address-only fallback must still bind the device's
    // ENUM extras, turning the tone field into a Select fed by the
    // candidate's available_tones, displayed via the localised
    // available_tone_labels.
    await page.locator('label:has-text("Tone") button').first().click();
    await expect(page.getByRole('option', { name: 'Device default' })).toBeVisible();
    await expect(page.getByRole('option', { name: 'Frequency rising', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');

    // The acoustic card also exposes the optical pattern (the acoustic
    // activation writes it in the same atomic paramset).
    await page.locator('label:has-text("Optical pattern") button').first().click();
    await expect(page.getByRole('option', { name: 'Blinking alternately' })).toBeVisible();
  });

  test('journal tab renders the five-entry journal table', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Journal' }).click();
    await expect(page).toHaveURL(/#\/alarm\/journal$/);

    await expect(page.getByRole('button', { name: 'Export CSV' })).toBeVisible();
    await expect(page.locator('table tbody tr')).toHaveCount(5);
    await expect(page.getByText('Markus').first()).toBeVisible();
  });

  test('codes tab lists codes from the fixture without leaking any hash or PIN', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await page.getByRole('tab', { name: 'Codes' }).click();
    await expect(page).toHaveURL(/#\/alarm\/codes$/);
    await expect(page.getByRole('button', { name: 'Add code' })).toBeVisible();

    await expect(page.getByText('Markus')).toBeVisible();
    await expect(page.getByText('Gast-Code')).toBeVisible();
    await expect(page.getByText('Haustür-Keypad Slot 1')).toBeVisible();
    await expect(page.getByText('Notfall')).toBeVisible();
    // The fixture carries exactly one duress-marked code ("Notfall"), so the
    // "Duress" badge renders exactly once on the list view.
    await expect(page.getByText('Duress', { exact: true })).toBeVisible();

    // Hash/PIN never round-trip onto this surface (notes/concepts/alarm-concept.md
    // §11/§16) — the fixture does not carry one, so this also guards
    // against a future fixture regression accidentally leaking one.
    await expect(page.getByText(/argon2/)).toHaveCount(0);
  });

  test('codes tab: create dialog shows the duress warning only once toggled, and posts the pin + duress flag on save', async ({ page }) => {
    let created: Record<string, unknown> | null = null;
    await page.route('**/api/v1/alarm/codes', (route) => {
      // Only the POST (create) is overridden here; GET falls back to the
      // default fixture-backed list route registered by mockAllApis so
      // the toolbar stays the sole "Add" affordance (an empty list would
      // also render the EmptyState's own "Add" action, breaking the
      // single-match assumption below).
      if (route.request().method() !== 'POST') return route.fallback();
      created = route.request().postDataJSON();
      return route.fulfill({ json: { id: 'code-new', ...created, zones: [], enabled: true } });
    });

    await page.goto('http://localhost:5173/app/#/alarm/codes');
    await page.waitForSelector('#main');

    await page.getByRole('button', { name: 'Add code' }).click();
    const dialog = page.getByRole('dialog');
    // Match on a distinguishing substring of the (long) duress-warning
    // copy rather than the full catalogue string, mirroring the
    // exact:false style used elsewhere in this spec for long strings.
    await expect(dialog.getByText('nothing changes on the panel', { exact: false })).toHaveCount(0);

    // Scoped to the dialog: "Name" is a common accessible name reused by
    // several other fields across the SPA (zone name, sensor name, …), so
    // an unscoped getByLabel would be a strict-mode trap the moment any of
    // those mount alongside this drawer.
    await dialog.getByLabel('Name', { exact: true }).fill('Notfall-Test');
    await page.locator('input[type="password"]').fill('4321');
    // The duress toggle is the first switch in the pin-kind drawer (PIN
    // field + duress row, ahead of the arm/disarm/silence permission
    // switches and the trailing enabled switch).
    await page.getByRole('switch').first().click();
    await expect(dialog.getByText('nothing changes on the panel', { exact: false })).toBeVisible();

    await page.getByRole('button', { name: 'Save' }).click();

    await expect.poll(() => created).not.toBeNull();
    expect(created?.name).toBe('Notfall-Test');
    expect(created?.kind).toBe('pin');
    expect(created?.pin).toBe('4321');
    expect(created?.duress).toBe(true);
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });

  test('policies tab: adding a schedule row and saving PUTs it back through the zone config', async ({ page }) => {
    let putBody: { config?: { schedules?: unknown[] } } | null = null;
    await page.route('**/api/v1/alarm/zones/zone-eg', (route) => {
      if (route.request().method() === 'PUT') {
        putBody = route.request().postDataJSON();
        return route.fulfill({ status: 200 });
      }
      return route.fulfill({ json: { id: 'zone-eg', name: 'Erdgeschoss', position: 1, config: {} } });
    });

    await page.goto('http://localhost:5173/app/#/alarm/policies');
    await page.waitForSelector('#main');

    await expect(page.getByText('No schedules yet')).toBeVisible();

    await page.getByRole('button', { name: 'Add schedule' }).click();
    await expect(page.getByText('No schedules yet')).toHaveCount(0);
    await expect(page.locator('input[type="time"]')).toHaveCount(1);

    await page.getByRole('button', { name: 'Save' }).click();

    await expect.poll(() => putBody).not.toBeNull();
    expect(putBody?.config?.schedules).toHaveLength(1);
  });

  test('silence acts on the first tap for a triggered zone, with no confirm dialog', async ({ page }) => {
    await mockAlarmTriggered(page);

    let silenceCalls = 0;
    await page.route('**/api/v1/alarm/zones/zone-eg/silence', (route) => {
      silenceCalls += 1;
      return route.fulfill({ status: 200 });
    });

    await page.goto('http://localhost:5173/app/#/alarm');
    await page.waitForSelector('#main');

    await expect(page.getByText('ALARM — Intrusion')).toBeVisible();

    // Safety invariants S3/S6 (notes/concepts/alarm-concept.md §2): silence acts on
    // the first tap, no confirm dialog is allowed to intercept it.
    await page.getByRole('button', { name: 'Silence sirens' }).click();

    await expect.poll(() => silenceCalls).toBe(1);
    await expect(page.getByRole('dialog')).not.toBeVisible();
  });
});

// Setup wizard (notes/concepts/alarm-concept.md §12.3). Steps ②/③ used to be bare
// links out into the zone-less sensor/output picker tabs — a dead end,
// since those tabs need an existing zone and the zone itself is only
// created on Finish. Both steps now embed a simplified inline picker
// instead; this suite pins the fix by asserting the candidates render and
// are selectable directly on the wizard step, not just linked to.
test.describe('Alarm — setup wizard', () => {
  test.beforeEach(async ({ page }) => {
    await mockAllApis(page);
    await mockAlarmWizardDevices(page);
    await page.addInitScript(() => {
      localStorage.setItem(
        'openccu-loom.prefs.v1',
        JSON.stringify({ theme: 'light', locale: 'en', navCollapsed: false, expertMode: false, deviceView: 'grid' }),
      );
    });
  });

  test('steps through zone, sensors and outputs inline, then finishes by posting the zone followed by the sensors and outputs bulk PUTs', async ({ page }) => {
    let postedZone: { name?: string; config?: unknown } | null = null;
    let putSensors: Array<Record<string, unknown>> | null = null;
    let putOutputs: Array<Record<string, unknown>> | null = null;
    // Records the three writes in call order — the finish() handler must
    // create the zone before either bulk PUT, and sensors before outputs.
    const calls: string[] = [];

    // The default fixture-backed POST /alarm/zones route always returns
    // id "zone-eg" regardless of the posted body, so the PUT targets below
    // can be pinned to that literal id and still exercise the real
    // create-then-PUT sequence finish() performs.
    await page.route('**/api/v1/alarm/zones', (route) => {
      if (route.request().method() !== 'POST') return route.fallback();
      postedZone = route.request().postDataJSON();
      calls.push('zone');
      return route.fulfill({
        json: { id: 'zone-eg', name: postedZone?.name, position: 1, config: postedZone?.config ?? {} },
      });
    });
    await page.route('**/api/v1/alarm/zones/zone-eg/sensors', (route) => {
      if (route.request().method() !== 'PUT') return route.fallback();
      putSensors = route.request().postDataJSON();
      calls.push('sensors');
      return route.fulfill({ status: 200 });
    });
    await page.route('**/api/v1/alarm/zones/zone-eg/outputs', (route) => {
      if (route.request().method() !== 'PUT') return route.fallback();
      putOutputs = route.request().postDataJSON();
      calls.push('outputs');
      return route.fulfill({ status: 200 });
    });

    await page.goto('http://localhost:5173/app/#/alarm/wizard');
    await page.waitForSelector('#main');

    // Step 1 — zone name.
    await expect(page.getByRole('heading', { name: 'Zones', level: 2 })).toBeVisible();
    await page.getByLabel('Name', { exact: true }).fill('Keller');
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 2 — sensors: the mocked device candidate renders inline and is
    // directly selectable, with no link out to the (zone-less) picker tab.
    await expect(page.getByRole('heading', { name: 'Sensors', level: 2 })).toBeVisible();
    await expect(page.locator('a[href="#/alarm/picker"]')).toHaveCount(0);
    const sensorRow = page.locator('label').filter({ hasText: 'Eingangstür' });
    await expect(sensorRow).toBeVisible();
    await sensorRow.getByRole('checkbox').check();
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 3 — outputs: pick the acoustic-siren candidate from the mocked
    // enrollment candidates (alarm-output-candidates.json's first row).
    await expect(page.getByRole('heading', { name: 'Outputs', level: 2 })).toBeVisible();
    await expect(page.locator('a[href="#/alarm/outputs"]')).toHaveCount(0);
    const outputRow = page.locator('label').filter({ hasText: 'Alarmierung' });
    await expect(outputRow).toBeVisible();
    await outputRow.getByRole('checkbox').check();
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 4 — delays: defaults are fine for this flow, just advance.
    await expect(page.getByRole('heading', { name: 'Delays & chirps', level: 2 })).toBeVisible();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 5 — codes: informational pointer only, advance.
    await expect(page.getByRole('heading', { name: 'Codes & users', level: 2 })).toBeVisible();
    await page.getByRole('button', { name: 'Next' }).click();

    // Step 6 — summary reflects both inline selections, then finish.
    await expect(page.getByRole('heading', { name: 'Done', level: 2 })).toBeVisible();
    const summary = page.locator('dl');
    await expect(summary.locator('dt', { hasText: 'Sensors' }).locator('xpath=following-sibling::dd[1]')).toHaveText('1');
    await expect(summary.locator('dt', { hasText: 'Outputs' }).locator('xpath=following-sibling::dd[1]')).toHaveText('1');
    await page.getByRole('button', { name: 'Finish' }).click();

    await expect.poll(() => calls).toEqual(['zone', 'sensors', 'outputs']);

    expect(postedZone?.name).toBe('Keller');

    expect(putSensors).toHaveLength(1);
    expect(putSensors?.[0]).toMatchObject({
      channel_address: 'VEQ0000030:1',
      parameter: 'STATE',
      type: 'window',
    });

    expect(putOutputs).toHaveLength(1);
    expect(putOutputs?.[0]).toMatchObject({
      channel_address: 'VEQ0000010:1',
      class: 'acoustic_siren',
    });

    await expect(page).toHaveURL(/#\/alarm$/);
  });

  test('preserves the entered zone name and sensor selection across navigating away from the wizard and back', async ({ page }) => {
    await page.goto('http://localhost:5173/app/#/alarm/wizard');
    await page.waitForSelector('#main');

    await page.getByLabel('Name', { exact: true }).fill('Keller');
    await page.getByRole('button', { name: 'Next' }).click();

    const sensorRow = page.locator('label').filter({ hasText: 'Eingangstür' });
    await expect(sensorRow).toBeVisible();
    await sensorRow.getByRole('checkbox').check();
    await expect(page.getByText('1 selected')).toBeVisible();

    // Navigate away mid-wizard (e.g. to double-check the overview) and
    // back in via the sidebar + the wizard-launch action — a full,
    // same-document SPA navigation, not a page reload. The wizard's
    // collected state lives in a module-singleton store precisely so this
    // survives the component unmounting along the way.
    await page.getByRole('link', { name: 'Alarm system' }).click();
    await expect(page).toHaveURL(/#\/alarm$/);
    await expect(page.getByRole('heading', { name: 'Alarm system', level: 1 })).toBeVisible();

    await page.locator('a[href="#/alarm/wizard"]').first().click();
    await expect(page).toHaveURL(/#\/alarm\/wizard$/);

    // Still on the sensors step, with the previous selection intact.
    await expect(page.getByRole('heading', { name: 'Sensors', level: 2 })).toBeVisible();
    await expect(page.getByText('1 selected')).toBeVisible();
    await expect(sensorRow.getByRole('checkbox')).toBeChecked();

    // Back to step 1 confirms the zone name itself also survived.
    await page.getByRole('button', { name: 'Back' }).click();
    await expect(page.getByRole('heading', { name: 'Zones', level: 2 })).toBeVisible();
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Keller');
  });
});
