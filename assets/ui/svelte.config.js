import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// Minimal Svelte 5 config. Runes are default in Svelte 5; no further
// opt-in required.
export default {
  preprocess: vitePreprocess(),
};
