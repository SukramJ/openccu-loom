import { test as base, expect } from '@playwright/test';

/**
 * Every `/api/v1` request a test does not mock explicitly.
 *
 * The suite is meant to be hermetic: no daemon, no CCU. A request that
 * escapes the mock layer does not fail loudly — it reaches the Vite dev
 * server, which proxies to a daemon that is not running, and the SPA
 * renders its error surface instead of the state under test. Both sysvars
 * visual tests spent months in that state after the list endpoint gained
 * `page`/`per_page` query parameters that the `**\/api/v1/sysvars` glob no
 * longer matched: the screenshots showed "API 502 Bad Gateway" while the
 * baselines still froze the pre-pagination "no variables" surface, and the
 * screenshot tolerance was wide enough to call that a match.
 *
 * The catch-all is registered before any test-specific route so the
 * specific ones win — Playwright matches routes in reverse registration
 * order — and it answers with a status the SPA cannot mistake for data.
 */
type HermeticApiFixtures = {
  unmockedApiCalls: string[];
};

export const test = base.extend<HermeticApiFixtures>({
  unmockedApiCalls: [
    async ({ page }, use) => {
      const unmocked: string[] = [];
      await page.route('**/api/v1/**', (route) => {
        unmocked.push(new URL(route.request().url()).pathname);
        return route.fulfill({
          status: 599,
          json: { detail: 'unmocked API call — see tests/e2e/helpers/fixtures.ts' },
        });
      });

      await use(unmocked);

      const distinct = [...new Set(unmocked)].sort();
      expect(
        distinct,
        'requests escaped the mock layer and were served by the dev-server proxy, ' +
          'so this test exercised the SPA error surface instead of the state under test',
      ).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };
export type { Page } from '@playwright/test';
