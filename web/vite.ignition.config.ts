/// <reference types="vitest/config" />
// Ignition's page code (spec/ignition.md), built apart from the review page: one ES module with its own copy of Svelte
// (it must never share the page's), and one stylesheet, into extensions/ignition/ui/. `npm run build` runs it after the
// page's build; `npx vitest run -c vite.ignition.config.ts` runs its unit tests (extensions/ignition/src/**/*.test.ts).
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { svelte, vitePreprocess } from '@sveltejs/vite-plugin-svelte';

const src = fileURLToPath(new URL('../extensions/ignition/src/', import.meta.url));
const out = fileURLToPath(new URL('../extensions/ignition/ui/', import.meta.url));
const SLOTS = ['page', 'version', 'sidebarTab', 'reviewPanel'];

/**
 * `retake ext check` finds a module's slots by a static scan for `export function <slot>` (spec/extensions.md), but a
 * bundle ends in `export { a as page, … }`. Rewrite that statement so each slot is a plain exported function that calls
 * the bundled one.
 */
function plainExports(file: string): Plugin {
  return {
    name: 'ignition:plain-exports',
    apply: 'build',
    writeBundle(options) {
      const path = join(options.dir ?? out, file);
      const code = readFileSync(path, 'utf8');
      const m = /export\s*\{([^}]*)\}\s*;?\s*$/.exec(code);
      if (!m) throw new Error(`${file}: no export statement at the end`);
      const keep: string[] = [];
      const fns: string[] = [];
      for (const spec of m[1]!.split(',')) {
        const [local, exported = local] = spec.trim().split(/\s+as\s+/);
        if (!local) continue;
        if (SLOTS.includes(exported!))
          fns.push(`export function ${exported}(el, ctx) {\n  return ${local}(el, ctx);\n}`);
        else keep.push(local === exported ? local : `${local} as ${exported}`);
      }
      const tail = [...(keep.length ? [`export { ${keep.join(', ')} };`] : []), ...fns].join('\n');
      writeFileSync(path, `${code.slice(0, m.index)}${tail}\n`);
    },
  };
}

export default defineConfig({
  plugins: [
    svelte({ configFile: false, preprocess: vitePreprocess(), compilerOptions: { runes: true } }),
    plainExports('ignition.js'),
  ],
  // The sources live outside web/, so bare imports resolve from here (web/node_modules).
  resolve: { dedupe: ['svelte', 'vitest'] },
  build: {
    outDir: out,
    // ui/ also holds the extension's icon (spark.svg): only the two built files are replaced.
    emptyOutDir: false,
    target: 'es2022',
    // Not minified: `retake ext check` reads the selectors as written ([data-ext='ignition'] …).
    cssMinify: false,
    lib: {
      entry: join(src, 'main.ts'),
      formats: ['es'],
      fileName: () => 'ignition.js',
      cssFileName: 'ignition',
    },
    rolldownOptions: { output: { codeSplitting: false } },
  },
  test: {
    dir: src,
    include: ['**/*.test.ts'],
    environment: 'node',
  },
});
