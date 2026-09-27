import { test, expect, type Page } from './helpers/fixtures';
import { mockAllApis, addStylesForStableScreenshots } from './helpers/mock-api';

// An openccu-lite box nests its rooms: Settings → Rooms & functions edits
// them as a tree, with nodes created below others, renamed, moved and
// deleted by path.

function setTheme(page: Page, theme: 'light' | 'dark'): Promise<void> {
  return page.addInitScript((t) => {
    localStorage.setItem(
      'openccu-loom.prefs.v1',
      JSON.stringify({ theme: t, locale: 'en', navCollapsed: false, expertMode: false }),
    );
  }, theme);
}

const BOX = {
  central: 'box',
  revision: 7,
  writable: true,
  tree: true,
  enums: [
    {
      id: 'room',
      names: { en: 'Rooms', de: 'Räume' },
      nodes: [
        {
          id: 'eg',
          path: 'eg',
          name: 'Ground floor',
          children: [
            { id: 'kitchen', path: 'eg/kitchen', name: 'Kitchen' },
            { id: 'living', path: 'eg/living', name: 'Living room' },
          ],
        },
        { id: 'og', path: 'og', name: 'Upper floor', children: [{ id: 'kitchen', path: 'og/kitchen', name: 'Kitchen' }] },
      ],
    },
    { id: 'function', names: { en: 'Functions', de: 'Gewerke' }, nodes: [{ id: 'light', path: 'light', name: 'Lighting' }] },
  ],
};

async function mockTree(page: Page, onCreate?: (body: unknown, url: string) => void): Promise<void> {
  await mockAllApis(page);
  await page.route('**/api/v1/taxonomy', (route) => route.fulfill({ json: { centrals: [BOX] } }));
  await page.route('**/api/v1/taxonomy/box/room/nodes**', async (route, request) => {
    if (request.method() === 'POST') onCreate?.(request.postDataJSON(), request.url());
    await route.fulfill({ status: request.method() === 'POST' ? 201 : 204, json: { path: 'eg/bath' } });
  });
}

test.describe('Nested rooms', () => {
  test('a node is created below another by its path', async ({ page }) => {
    let created: unknown = null;
    await mockTree(page, (body) => (created = body));
    await page.goto('http://localhost:5173/app/#/settings?tab=groups');
    const tree = page.getByTestId('taxonomy-tree-box');
    await expect(tree.getByText('Upper floor', { exact: true }).first()).toBeVisible();
    await tree.getByLabel('Add below Ground floor').click();
    await tree.getByLabel('Name', { exact: true }).fill('Bath');
    await tree.getByRole('button', { name: 'Save' }).click();
    await expect.poll(() => created).toEqual({ name: 'Bath', parent_path: 'eg' });
  });
});

test.describe('Nested rooms - visual', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`tree editor ${theme}`, async ({ page }) => {
      await setTheme(page, theme);
      await mockTree(page);
      await page.goto('http://localhost:5173/app/#/settings?tab=groups');
      const tree = page.getByTestId('taxonomy-tree-box');
      await expect(tree.getByText('Living room')).toBeVisible();
      await addStylesForStableScreenshots(page);
      await expect(tree.locator('xpath=ancestor::div[contains(@class,"rounded-lg")][1]')).toHaveScreenshot(
        `taxonomy-tree-${theme}.png`,
      );
    });
  }
});
