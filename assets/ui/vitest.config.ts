import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "src/lib"),
    },
    // Svelte 5 ships browser vs. server builds under separate export conditions.
    // Without the "browser" condition Vitest picks the server build (SSR) which
    // throws "lifecycle_function_unavailable" when `render()` is called.
    conditions: ["browser"],
  },
  test: {
    include: ["src/**/*.test.ts"],
    // Default environment for pure-TS logic tests. DOM-requiring tests
    // (stores, components) override with // @vitest-environment happy-dom.
    environment: "node",
    globals: false,
    reporters: ["default"],
    // Extend vitest's expect with jest-dom matchers (toBeInTheDocument etc.)
    // for component and store tests that run in the happy-dom environment.
    setupFiles: ["src/test-setup.ts"],
  },
});
