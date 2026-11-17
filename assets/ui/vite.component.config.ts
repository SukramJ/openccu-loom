import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import routes from './src/component/editor-routes.json';

// The device editor as a web component (ADR 0088 decision 3, ADR 0089): a
// second build of the SPA's own sources into spa_dist/component/, after the
// SPA build has emptied spa_dist. One ES module, no chunks: the integration
// fetches it from the daemon and serves it from Home Assistant's origin
// under the content hash the daemon reports, so a second file would have
// no URL. Component styles are injected by the compiled components; Svelte
// appends them to the shadow root a component renders into, where a
// separate CSS file would never reach.
export default defineConfig({
  plugins: [
    svelte({ compilerOptions: { css: 'injected' } }),
    tailwindcss(),
    {
      // The route list next to the module: the daemon reads it from the
      // embedded bundle and publishes it in its manifest, so the list the
      // component enforces and the list the relay enforces are one file.
      name: 'editor-routes',
      generateBundle() {
        this.emitFile({ type: 'asset', fileName: 'routes.json', source: JSON.stringify(routes, null, 2) + '\n' });
      },
    },
  ],
  resolve: {
    alias: { $lib: '/src/lib' },
  },
  publicDir: false,
  build: {
    outDir: '../../internal/north/ui/spa_dist/component',
    emptyOutDir: true,
    sourcemap: false,
    lib: {
      entry: 'src/component/main.ts',
      formats: ['es'],
      fileName: () => 'editor.js',
    },
    rollupOptions: {
      output: { inlineDynamicImports: true },
    },
  },
});
