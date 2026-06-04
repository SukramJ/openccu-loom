import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
// @tailwindcss/node (4.3.0, the current latest) registers its ESM loader
// hook via the deprecated `module.register()`, which makes Node emit a
// DEP0205 warning every time Vite loads this config. The warning is
// suppressed with `--disable-warning=DEP0205` in the package.json vite
// scripts; remove that flag once Tailwind upstream switches to
// `module.registerHooks()`.
import tailwindcss from '@tailwindcss/vite';

// The SPA is mounted under /app/ so the existing HTMX UI on / stays
// functional while the migration is in flight. Asset paths must be
// prefixed accordingly (see `base`), and the Go handler strips /app
// before hitting the embedded FS.
export default defineConfig({
  base: '/app/',
  plugins: [svelte(), tailwindcss()],
  build: {
    // Ship the build output directly into the Go package so the
    // daemon's go:embed directive picks it up without an additional
    // copy step. The outDir is relative to the Vite project root.
    outDir: '../../internal/north/ui/spa_dist',
    emptyOutDir: true,
    sourcemap: true,
    // Every chunk stays below 500 kB — the daemon embeds the whole
    // bundle, so silent 5 MB regressions would be invisible.
    chunkSizeWarningLimit: 500,
    rollupOptions: {
      output: {
        // Split third-party code into its own long-lived chunk so the
        // app chunk stays small and browser caches survive app-only
        // redeploys. bits-ui + lucide icons dominate vendor weight.
        manualChunks(id) {
          if (id.includes('node_modules')) {
            return 'vendor';
          }
          return undefined;
        },
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    // REST + WS proxied to the daemon so `npm run dev` works without
    // CORS gymnastics. Start the daemon on :8080 before dev:
    //   ./bin/openccu-loom run --config config.yaml
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  resolve: {
    alias: {
      $lib: '/src/lib',
    },
  },
});
