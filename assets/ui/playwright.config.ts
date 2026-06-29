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
    toHaveScreenshot: { maxDiffPixelRatio: 0.02 },
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
