import { type Page } from '@playwright/test';
import { readFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const fixturesDir = join(__dirname, '../fixtures');

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(join(fixturesDir, name), 'utf-8'));
}

export async function mockAllApis(page: Page): Promise<void> {
  // Auth
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({ json: fixture('auth-me.json') }),
  );

  // First-run onboarding probe — the SPA calls this on boot. Report "not
  // required" so the e2e suite exercises the normal login/app flow rather
  // than the setup wizard.
  await page.route('**/api/v1/setup/status', (route) =>
    route.fulfill({ json: { required: false } }),
  );

  // System
  await page.route('**/api/v1/system/restart-pending', (route) =>
    route.fulfill({ json: fixture('restart-pending.json') }),
  );
  await page.route('**/api/v1/system/startup-capture', (route) =>
    route.fulfill({ json: fixture('startup-capture.json') }),
  );
  await page.route('**/api/v1/system/config-changes', (route) =>
    route.fulfill({ json: fixture('config-changes.json') }),
  );
  await page.route('**/api/v1/system/update', (route) =>
    route.fulfill({ json: fixture('system-update.json') }),
  );
  await page.route('**/api/v1/system/restart', (route) =>
    route.fulfill({ status: 200, json: { status: 'ok', at: new Date().toISOString() } }),
  );

  // Install mode (per-interface only; no CCU-wide endpoint)
  await page.route('**/api/v1/install-mode/interfaces', (route) =>
    route.fulfill({ json: fixture('install-mode.json') }),
  );

  // Info
  await page.route('**/api/v1/info', (route) =>
    route.fulfill({ json: fixture('info.json') }),
  );

  // Devices
  await page.route('**/api/v1/devices*', (route) =>
    route.fulfill({ json: fixture('devices.json') }),
  );

  // Sysvars
  await page.route('**/api/v1/sysvars', (route) =>
    route.fulfill({ json: fixture('sysvars.json') }),
  );
  await page.route('**/api/v1/sysvars/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Programs
  await page.route('**/api/v1/programs', (route) =>
    route.fulfill({ json: fixture('programs.json') }),
  );
  await page.route('**/api/v1/programs/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Health
  await page.route('**/api/v1/health', (route) =>
    route.fulfill({ json: fixture('health.json') }),
  );

  // Interfaces
  await page.route('**/api/v1/interfaces', (route) =>
    route.fulfill({ json: fixture('interfaces.json') }),
  );
  await page.route('**/api/v1/interfaces/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Incidents
  await page.route('**/api/v1/incidents', (route) =>
    route.fulfill({ json: fixture('incidents.json') }),
  );

  // Diagnostics - more specific routes FIRST
  await page.route('**/api/v1/diagnostics/log-levels/**', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/diagnostics/log-levels', (route) =>
    route.fulfill({ json: fixture('log-levels.json') }),
  );
  await page.route('**/api/v1/diagnostics/log-level', (route) =>
    route.fulfill({ json: { default: 'info' } }),
  );
  await page.route('**/api/v1/diagnostics/capture/**', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/diagnostics/capture', (route) =>
    route.fulfill({ json: fixture('captures.json') }),
  );
  await page.route('**/api/v1/diagnostics/rpc-recording/**', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/diagnostics/rpc-recording', (route) =>
    route.fulfill({ json: fixture('rpc-recordings.json') }),
  );
  await page.route('**/api/v1/diagnostics/logs', (route) =>
    route.fulfill({ json: { last_seq: 0, records: [] } }),
  );
  await page.route('**/api/v1/diagnostics/rssi', (route) =>
    route.fulfill({
      json: {
        devices: [
          {
            address: 'OEQ0123456',
            name: 'Living Room Switch',
            interface_id: 'BidCos-RF',
            central: 'ccu',
            rssi_device: -72,
            rssi_peer: -68,
            battery_level: null,
            low_battery: null,
            reachable: true,
          },
          {
            address: 'MEQ0987654',
            name: 'Hallway Sensor',
            interface_id: 'HmIP-RF',
            central: 'ccu',
            rssi_device: -80,
            rssi_peer: null,
            battery_level: 35,
            low_battery: false,
            reachable: false,
          },
        ],
      },
    }),
  );
  await page.route(/\/api\/v1\/diagnostics(\?.*)?$/, (route) =>
    route.fulfill({ json: fixture('diagnostics.json') }),
  );

  // Config - more specific FIRST
  await page.route('**/api/v1/config/schema', (route) =>
    route.fulfill({ json: fixture('config-schema.json') }),
  );
  await page.route('**/api/v1/config/effective', (route) =>
    route.fulfill({ json: fixture('config-effective.json') }),
  );
  await page.route('**/api/v1/config/sections/**', (route) =>
    route.fulfill({ json: {} }),
  );
  await page.route('**/api/v1/config/fields/**', (route) =>
    route.fulfill({ status: 204 }),
  );
  await page.route('**/api/v1/config/**', (route) =>
    route.fulfill({ json: {} }),
  );

  // Rooms and functions
  await page.route('**/api/v1/rooms', (route) =>
    route.fulfill({ json: fixture('rooms.json') }),
  );
  await page.route('**/api/v1/functions', (route) =>
    route.fulfill({ json: fixture('functions.json') }),
  );

  // Alarm and service messages
  await page.route('**/api/v1/alarm-messages', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/alarm-messages/**', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/service-messages', (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route('**/api/v1/service-messages/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Alarm panel (native intrusion-alarm engine, docs/alarm-concept.md §13) —
  // distinct from the legacy CCU alarm-messages/service-messages surface
  // above. Defaults: two areas — "Erdgeschoss" (armed, full protection,
  // steady state with no running countdown) and "Dachgeschoss" (disarmed) —
  // three sensors, two outputs (one acoustic siren, one smoke-detector
  // sounder) and a five-entry journal. alarm.spec.ts overrides individual
  // routes (e.g. a triggered area) where a test needs a different state.
  await page.route('**/api/v1/alarm/state', (route) =>
    route.fulfill({ json: fixture('alarm-state.json') }),
  );
  await page.route('**/api/v1/alarm/areas/*/sensors', (route) => {
    if (route.request().method() === 'PUT') return route.fulfill({ status: 200 });
    return route.fulfill({ json: fixture('alarm-sensors.json') });
  });
  await page.route('**/api/v1/alarm/areas/*/outputs', (route) => {
    if (route.request().method() === 'PUT') return route.fulfill({ status: 200 });
    return route.fulfill({ json: fixture('alarm-outputs.json') });
  });
  await page.route('**/api/v1/alarm/areas/*/readiness', (route) =>
    route.fulfill({ json: fixture('alarm-readiness.json') }),
  );
  await page.route('**/api/v1/alarm/areas/*/walktest/start', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/areas/*/walktest/stop', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/areas/*/walktest', (route) =>
    route.fulfill({ json: fixture('alarm-walktest.json') }),
  );
  await page.route('**/api/v1/alarm/areas/*/arm', (route) =>
    route.fulfill({ json: { state: 'armed', bypassed: [], exit_delay_s: 0 } }),
  );
  await page.route('**/api/v1/alarm/areas/*/disarm', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/areas/*/silence', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/areas/*/acknowledge', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/areas/*', (route) => {
    if (route.request().method() === 'DELETE') return route.fulfill({ status: 200 });
    if (route.request().method() === 'PUT') return route.fulfill({ status: 200 });
    return route.fulfill({ json: { id: 'area-eg', name: 'Erdgeschoss', position: 1, config: {} } });
  });
  await page.route('**/api/v1/alarm/areas', (route) => {
    if (route.request().method() === 'POST') {
      return route.fulfill({ json: { id: 'area-eg', name: 'Erdgeschoss', position: 1, config: {} } });
    }
    return route.fulfill({ json: fixture('alarm-areas.json') });
  });
  await page.route('**/api/v1/alarm/silence-all', (route) =>
    route.fulfill({ status: 200 }),
  );
  await page.route('**/api/v1/alarm/journal**', (route) =>
    route.fulfill({ json: fixture('alarm-journal.json') }),
  );
  await page.route('**/api/v1/alarm/outputs/*/test', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Alarm codes (docs/alarm-concept.md §11) — operator-gated, hash/PIN
  // never round-tripped. Default fixture: three enabled pin/keypad_slot
  // codes plus one duress-marked pin. Single-code route (GET/PUT/DELETE)
  // is registered before the bare collection route, mirroring the
  // areas/*-before-areas ordering above.
  await page.route('**/api/v1/alarm/codes/*', (route) => {
    if (route.request().method() === 'DELETE') return route.fulfill({ status: 200 });
    if (route.request().method() === 'PUT') return route.fulfill({ status: 200 });
    const codes = fixture('alarm-codes.json') as Record<string, unknown>[];
    return route.fulfill({ json: codes[0] });
  });
  await page.route('**/api/v1/alarm/codes', (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      return route.fulfill({
        json: {
          id: 'code-new',
          name: body.name,
          kind: body.kind,
          duress: body.duress,
          perms: body.perms,
          areas: body.areas ?? [],
          valid_from_ms: body.valid_from_ms,
          valid_until_ms: body.valid_until_ms,
          enabled: body.enabled,
        },
      });
    }
    return route.fulfill({ json: fixture('alarm-codes.json') });
  });

  // Auth users and tokens
  await page.route('**/api/v1/auth/users', (route) =>
    route.fulfill({ json: fixture('users.json') }),
  );
  await page.route('**/api/v1/auth/tokens', (route) =>
    route.fulfill({ json: fixture('tokens.json') }),
  );
  await page.route('**/api/v1/auth/tokens/v2', (route) =>
    route.fulfill({ json: fixture('tokens.json') }),
  );
  await page.route('**/api/v1/auth/tokens/v2/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Users
  await page.route('**/api/v1/users', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: fixture('users.json') });
    }
    return route.fulfill({ json: { subject: 'new', role: 'viewer' } });
  });
  await page.route('**/api/v1/users/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // System CCU fleet metadata (GET /api/v1/system/ccu) — a single online
  // CCU matching devices.json/centrals.json's "ccu1" by default. The
  // Fleet route's own tests override this with mockFleet() for a
  // multi-CCU, mixed-availability fixture.
  await page.route('**/api/v1/system/ccu', (route) =>
    route.fulfill({
      json: {
        entries: [
          {
            name: 'ccu1',
            host: '172.18.4.29',
            available: true,
            model: 'CCU3',
            version: '3.75.7',
            is_ha_app: false,
            configured_interfaces: ['HmIP-RF'],
            readiness: { phase: 'ready', ready: true, interfaces_loaded: 1, interfaces_total: 1 },
          },
        ],
      },
    }),
  );

  // Centrals
  await page.route('**/api/v1/centrals', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: fixture('centrals.json') });
    }
    return route.fulfill({ json: { name: 'new-ccu' } });
  });
  await page.route('**/api/v1/centrals/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Inbox
  await page.route('**/api/v1/inbox', (route) =>
    route.fulfill({ json: fixture('inbox.json') }),
  );

  // Audit
  await page.route('**/api/v1/audit**', (route) =>
    route.fulfill({ json: fixture('audit.json') }),
  );

  // Backups
  await page.route('**/api/v1/backups', (route) =>
    route.fulfill({ json: fixture('backups.json') }),
  );
  await page.route('**/api/v1/backups/**', (route) =>
    route.fulfill({ status: 200 }),
  );

  // Matter
  await page.route('**/api/v1/matter/**', (route) =>
    route.fulfill({ json: fixture('matter-status.json') }),
  );

  // Admin
  await page.route('**/api/v1/admin/**', (route) =>
    route.fulfill({ json: { reloaded: true, took_ms: 1 } }),
  );

  // Visibility
  await page.route('**/api/v1/visibility/**', (route) =>
    route.fulfill({ json: fixture('visibility-unignore.json') }),
  );

  // Sessions
  await page.route('**/api/v1/sessions/**', (route) => {
    const url = route.request().url();
    if (url.includes('/edit')) {
      return route.fulfill({ json: { session_id: 'test-session', owner: 'admin', expires_at: new Date(Date.now() + 300000).toISOString() } });
    }
    return route.fulfill({ status: 200 });
  });

  // History
  await page.route('**/api/v1/history**', (route) =>
    route.fulfill({ json: [] }),
  );

  // Energy — default empty breakdown; the Energy route's own tests
  // override this with mockEnergy()/mockEnergyDisabled() for real data
  // and the feature-off (404) path.
  await page.route('**/api/v1/energy**', (route) =>
    route.fulfill({
      json: { group: 'day', from: '', to: '', devices: [], total_consumed_wh: 0, total_feed_in_wh: 0 },
    }),
  );

  // WebSocket — abort to prevent hanging
  await page.route('**/api/v1/ws', (route) => route.abort());
  await page.route('**/api/v1/ws**', (route) => route.abort());
}

export async function mockEmptyDevices(page: Page): Promise<void> {
  await page.route('**/api/v1/devices*', (route) =>
    route.fulfill({ json: { items: [], total: 0, page: 1, per_page: 50 } }),
  );
}

/**
 * Multi-device, multi-central fleet for the Overview route (roadmap
 * B8). Covers both tile-dispatch paths: `AAA0000001` carries a
 * registered CDP (kind "switch") on channel 4, `AAA0000002` carries an
 * orphan sensor channel with no CDP so it falls through to the
 * AutoTile composer. `BBB0000001` lives on a second central so the
 * central filter + the "never merge rooms across CCUs" rule are
 * exercised (its "Office" room/central pair is distinct from any
 * ccu1 room, even if the name happened to collide).
 */
export async function mockOverviewFleet(page: Page): Promise<void> {
  const devices = [
    {
      address: 'AAA0000001',
      central: 'ccu1',
      interface: 'HmIP-RF',
      interface_id: 'ccu1-HmIP-RF',
      model: 'HmIP-PSM',
      model_label: 'Switch Actuator',
      name: 'Living Room Switch',
      available: true,
      channels_count: 2,
      updatable: false,
      update_available: false,
      rooms: ['Living Room'],
      functions: ['Lighting'],
      master_pushes_config_pending: false,
      has_sub_devices: false,
    },
    {
      address: 'AAA0000002',
      central: 'ccu1',
      interface: 'HmIP-RF',
      interface_id: 'ccu1-HmIP-RF',
      model: 'HmIP-STHO',
      model_label: 'Temperature and Humidity Sensor',
      name: 'Kitchen Sensor',
      available: true,
      channels_count: 2,
      updatable: false,
      update_available: false,
      rooms: ['Kitchen'],
      functions: ['Climate'],
      master_pushes_config_pending: false,
      has_sub_devices: false,
    },
    {
      address: 'BBB0000001',
      central: 'ccu2',
      interface: 'HmIP-RF',
      interface_id: 'ccu2-HmIP-RF',
      model: 'HmIP-BSL',
      model_label: 'Switch and Dimming Actuator with Signal Lamp',
      name: 'Office Lamp',
      available: true,
      channels_count: 1,
      updatable: false,
      update_available: false,
      rooms: ['Office'],
      functions: ['Lighting'],
      master_pushes_config_pending: false,
      has_sub_devices: false,
    },
  ];

  const details: Record<string, unknown> = {
    AAA0000001: {
      ...devices[0],
      firmware: {},
      availability: { IsReachable: true },
      channels: [
        {
          address: 'AAA0000001:0',
          number: 0,
          name: '',
          type: 'MAINTENANCE',
          type_label: 'Maintenance',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 3,
        },
        {
          address: 'AAA0000001:4',
          number: 4,
          name: 'Switch',
          type: 'SWITCH_VIRTUAL_RECEIVER',
          type_label: 'Switch',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 1,
          custom_dp_name: 'STATE@4',
        },
      ],
    },
    AAA0000002: {
      ...devices[1],
      firmware: {},
      availability: { IsReachable: true },
      channels: [
        {
          address: 'AAA0000002:0',
          number: 0,
          name: '',
          type: 'MAINTENANCE',
          type_label: 'Maintenance',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 3,
        },
        {
          address: 'AAA0000002:1',
          number: 1,
          name: 'Temperature',
          type: 'CLIMATE_TRANSCEIVER',
          type_label: 'Temperature Sensor',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 1,
        },
      ],
    },
    BBB0000001: {
      ...devices[2],
      firmware: {},
      availability: { IsReachable: true },
      channels: [
        {
          address: 'BBB0000001:0',
          number: 0,
          name: '',
          type: 'MAINTENANCE',
          type_label: 'Maintenance',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 2,
        },
        {
          address: 'BBB0000001:1',
          number: 1,
          name: 'Lamp',
          type: 'SWITCH_VIRTUAL_RECEIVER',
          type_label: 'Switch',
          paramset_key: 'VALUES',
          paramset_keys: ['VALUES'],
          data_points_count: 1,
        },
      ],
    },
  };

  const cdpsByAddress: Record<string, unknown[]> = {
    AAA0000001: [
      {
        name: 'STATE@4',
        category: 'switch',
        channel_no: 4,
        supported_operations: ['turn_on', 'turn_off'],
        kind: 'switch',
        channels: [4],
      },
    ],
    AAA0000002: [],
    BBB0000001: [],
  };

  const dataPointsByChannel: Record<string, unknown[]> = {
    'AAA0000001:4': [
      {
        parameter: 'STATE',
        unique_id: 'ccu1/AAA0000001:4/STATE',
        observed: true,
        value: false,
        type: 'BOOL',
        operations: { read: true, write: true, event: true },
      },
    ],
    'AAA0000002:1': [
      {
        parameter: 'TEMPERATURE',
        parameter_label: 'Temperature',
        unique_id: 'ccu1/AAA0000002:1/TEMPERATURE',
        observed: true,
        value: 21.5,
        type: 'FLOAT',
        unit: '°C',
        operations: { read: true, write: false, event: true },
        ui_hint: { icon: 'mdi:thermometer', semantic: 'temperature' },
      },
    ],
    'BBB0000001:1': [
      {
        parameter: 'STATE',
        unique_id: 'ccu2/BBB0000001:1/STATE',
        observed: true,
        value: true,
        type: 'BOOL',
        operations: { read: true, write: true, event: true },
      },
    ],
  };

  await page.route('**/api/v1/devices*', (route) =>
    route.fulfill({ json: { items: devices, total: devices.length, page: 1, per_page: 50 } }),
  );

  // Per-device detail — registered as a regex so it only matches the
  // single-segment `/devices/{addr}` path, never `/devices/{addr}/cdps`
  // or the channels sub-path.
  await page.route(/\/api\/v1\/devices\/([^/?]+)(\?.*)?$/, (route) => {
    const m = route.request().url().match(/\/devices\/([^/?]+)/);
    const addr = m ? decodeURIComponent(m[1]) : '';
    const detail = details[addr];
    if (!detail) return route.fulfill({ status: 404, json: { detail: 'not found' } });
    return route.fulfill({ json: detail });
  });

  await page.route(/\/api\/v1\/devices\/([^/]+)\/cdps$/, (route) => {
    const m = route.request().url().match(/\/devices\/([^/]+)\/cdps/);
    const addr = m ? decodeURIComponent(m[1]) : '';
    return route.fulfill({ json: cdpsByAddress[addr] ?? [] });
  });

  await page.route(/\/api\/v1\/devices\/([^/]+)\/channels\/(\d+)\/data-points$/, (route) => {
    const m = route.request().url().match(/\/devices\/([^/]+)\/channels\/(\d+)\/data-points/);
    const addr = m ? decodeURIComponent(m[1]) : '';
    const ch = m ? m[2] : '';
    return route.fulfill({ json: dataPointsByChannel[`${addr}:${ch}`] ?? [] });
  });
}

/**
 * Two-CCU fixture for the Fleet route (read-only cross-CCU overview):
 * "ccu1" is online with two configured interfaces and two devices;
 * "ccu2" is offline with one configured interface and one device. Covers
 * both availability states plus the per-central device-count derivation.
 */
export async function mockFleet(page: Page): Promise<void> {
  await page.route('**/api/v1/system/ccu', (route) =>
    route.fulfill({
      json: {
        entries: [
          {
            name: 'ccu1',
            host: '172.18.4.29',
            available: true,
            model: 'CCU3',
            version: '3.75.7',
            hostname: 'ccu1.local',
            serial: 'SERIAL0001',
            url: 'https://172.18.4.29',
            is_ha_app: false,
            configured_interfaces: ['HmIP-RF', 'BidCos-RF'],
            readiness: { phase: 'ready', ready: true, interfaces_loaded: 2, interfaces_total: 2 },
          },
          {
            name: 'ccu2',
            host: '172.18.4.30',
            available: false,
            is_ha_app: false,
            configured_interfaces: ['HmIP-RF'],
            readiness: { phase: 'waiting_for_ccu', ready: false, interfaces_loaded: 0, interfaces_total: 0 },
          },
        ],
      },
    }),
  );

  await page.route('**/api/v1/devices*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            address: 'AAA0000001',
            central: 'ccu1',
            interface: 'HmIP-RF',
            interface_id: 'ccu1-HmIP-RF',
            model: 'HmIP-PSM',
            model_label: 'Switch Actuator',
            name: 'Living Room Switch',
            available: true,
            channels_count: 2,
            updatable: false,
            update_available: false,
            rooms: ['Living Room'],
            functions: ['Lighting'],
            master_pushes_config_pending: false,
            has_sub_devices: false,
          },
          {
            address: 'AAA0000002',
            central: 'ccu1',
            interface: 'HmIP-RF',
            interface_id: 'ccu1-HmIP-RF',
            model: 'HmIP-STHO',
            model_label: 'Temperature and Humidity Sensor',
            name: 'Kitchen Sensor',
            available: true,
            channels_count: 2,
            updatable: false,
            update_available: false,
            rooms: ['Kitchen'],
            functions: ['Climate'],
            master_pushes_config_pending: false,
            has_sub_devices: false,
          },
          {
            address: 'BBB0000001',
            central: 'ccu2',
            interface: 'HmIP-RF',
            interface_id: 'ccu2-HmIP-RF',
            model: 'HmIP-BSL',
            model_label: 'Switch and Dimming Actuator with Signal Lamp',
            name: 'Office Lamp',
            available: false,
            channels_count: 1,
            updatable: false,
            update_available: false,
            rooms: ['Office'],
            functions: ['Lighting'],
            master_pushes_config_pending: false,
            has_sub_devices: false,
          },
        ],
        total: 3,
        page: 1,
        per_page: 50,
      },
    }),
  );
}

/**
 * Two-device GET /api/v1/energy breakdown for the Energy route (roadmap
 * A2 step 5): "Bücherregal" carries a bucket with `reset: true` (exercises
 * the reset badge/footnote), "Balkonkraftwerk" is a net feed-in device
 * (exercises the feed-in column + totals).
 */
export async function mockEnergy(page: Page): Promise<void> {
  await page.route('**/api/v1/energy**', (route) =>
    route.fulfill({ json: fixture('energy.json') }),
  );
}

/** Simulates the history/energy feature being disabled on the daemon
 *  (GET /api/v1/energy returns 404 — the same gate as /history). */
export async function mockEnergyDisabled(page: Page): Promise<void> {
  await page.route('**/api/v1/energy**', (route) =>
    route.fulfill({ status: 404, json: { detail: 'history recording is not enabled' } }),
  );
}

export async function mockEmptySysvars(page: Page): Promise<void> {
  await page.route('**/api/v1/sysvars', (route) =>
    route.fulfill({ json: [] }),
  );
}

/**
 * Overrides GET /api/v1/alarm/state with a variant where "Erdgeschoss" is
 * `triggered` (open incident, unsilenced) while "Dachgeschoss" stays
 * disarmed — for exercising the Overview's high-contrast triggered card
 * (giant SILENCE/DISARM, single tap, no confirm — safety invariants S3/S6)
 * without disturbing the steady-state default used elsewhere.
 */
export async function mockAlarmTriggered(page: Page): Promise<void> {
  await page.route('**/api/v1/alarm/state', (route) =>
    route.fulfill({ json: fixture('alarm-state-triggered.json') }),
  );
}

export async function addStylesForStableScreenshots(page: Page): Promise<void> {
  await page.addStyleTag({
    content: `
      *, *::before, *::after {
        animation-duration: 0s !important;
        animation-delay: 0s !important;
        transition-duration: 0s !important;
        transition-delay: 0s !important;
      }
    `,
  });
}
