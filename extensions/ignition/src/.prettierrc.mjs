// web/.prettierrc.json for Ignition's sources, which live outside web/: the Svelte plugin is web's.
import { fileURLToPath } from 'node:url';

export default {
  printWidth: 120,
  singleQuote: true,
  trailingComma: 'all',
  plugins: [fileURLToPath(new URL('../../../web/node_modules/prettier-plugin-svelte/plugin.js', import.meta.url))],
  overrides: [{ files: '*.svelte', options: { parser: 'svelte' } }],
};
