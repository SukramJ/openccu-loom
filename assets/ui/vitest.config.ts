import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "src/lib"),
    },
  },
  test: {
    include: ["src/**/*.test.ts"],
    environment: "node",
    globals: false,
    reporters: ["default"],
  },
});
