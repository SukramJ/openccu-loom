import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  timeout: 30000,
  // Retry once in CI: the GitHub runners are noticeably slower and more
  // contended than local/containerised runs, which occasionally turns a
  // reactivity-settle race into a one-off failure. Local runs keep retries
  // off so genuine flakes surface immediately.
  retries: process.env.CI ? 1 : 0,
  expect: {
    // No budget at all, and deliberately not a ratio. The previous
    // `maxDiffPixelRatio: 0.02` meant 20 480 pixels of a 1280x800 viewport —
    // more than a shifted navigation sidebar costs — so every committed
    // baseline had drifted away from the code while the suite stayed green,
    // and `npm run e2e:update` would not rewrite them because the
    // comparisons never failed.
    //
    // Zero is not zeal, it is the only value that separates signal from
    // noise here: this container renders bit-identically (two runs of all
    // 37 screenshot tests, 0 differing pixels each), while renaming one
    // table header costs 90 pixels. Any budget large enough to feel
    // comfortable is already large enough to hide a changed label.
    // Pinned by TestScreenshotComparisonBudgetIsTightEnoughToSeeDrift.
    toHaveScreenshot: { maxDiffPixels: 0 },
  },
  use: {
    headless: true,
    baseURL: 'http://localhost:5173/app/',
    viewport: { width: 1280, height: 800 },
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173/app/',
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
  },
});
