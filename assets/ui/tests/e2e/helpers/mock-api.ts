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

  // WebSocket — abort to prevent hanging
  await page.route('**/api/v1/ws', (route) => route.abort());
  await page.route('**/api/v1/ws**', (route) => route.abort());
}

export async function mockEmptyDevices(page: Page): Promise<void> {
  await page.route('**/api/v1/devices*', (route) =>
    route.fulfill({ json: { items: [], total: 0, page: 1, per_page: 50 } }),
  );
}

export async function mockEmptySysvars(page: Page): Promise<void> {
  await page.route('**/api/v1/sysvars', (route) =>
    route.fulfill({ json: [] }),
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
