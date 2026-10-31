/// <reference types="vitest/config" />
import { createServer, defineConfig, type Plugin, type ProxyOptions, type ViteDevServer } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';
import { mockNotes } from './dev/mock-notes';

// `npm run dev` serves the page and proxies the API and the videos to a running review server (the Go `retake open
// ... --no-open`, or the legacy player). RETAKE_SERVER picks it (default: the default port).
// The server only accepts POSTs whose Origin is its own, so the proxy rewrites the Origin header to the server's.
// RETAKE_MOCK_NOTES=1 (or =fail) stands in for the milestone-2 notes routes the server doesn't have yet (dev/).
const server = (process.env.RETAKE_SERVER ?? 'http://127.0.0.1:8777').replace(/\/+$/, '');
const mock = process.env.RETAKE_MOCK_NOTES;

const proxy: ProxyOptions = {
  target: server,
  changeOrigin: true,
  configure: (p) => {
    p.on('proxyReq', (req) => {
      if (req.getHeader('origin')) req.setHeader('origin', server);
    });
  },
};

/**
 * Render src/shell.ts (the whole page, server-side) into index.html's #app; main.ts hydrates it. The render runs in
 * its own middleware-mode Vite server (one per build; one per dev server, kept for later requests).
 */
function prerenderShell(): Plugin {
  let root = process.cwd();
  let ssr: Promise<ViteDevServer> | undefined;
  const ssrServer = () =>
    createServer({
      configFile: false,
      root,
      logLevel: 'error',
      plugins: [svelte()],
      server: { middlewareMode: true, hmr: false, ws: false, watch: null },
      appType: 'custom',
    });
  const render = async (srv: ViteDevServer) => {
    for (const m of srv.moduleGraph.idToModuleMap.values()) srv.moduleGraph.invalidateModule(m); // pick up edits
    return ((await srv.ssrLoadModule('/src/shell.ts')) as { shell: () => string }).shell();
  };
  return {
    name: 'retake:prerender-shell',
    configResolved(c) {
      root = c.root;
    },
    configureServer(dev) {
      dev.httpServer?.once('close', () => void ssr?.then((s) => s.close()));
    },
    async transformIndexHtml(html, ctx) {
      if (!html.includes('<div id="app"></div>')) return html; // home.html: the home screen mounts on its own
      let body: string;
      if (ctx.server) body = await render(await (ssr ??= ssrServer()));
      else {
        const srv = await ssrServer();
        try {
          body = await render(srv);
        } finally {
          await srv.close();
        }
      }
      return html.replace('<div id="app"></div>', `<div id="app">${body}</div>`);
    },
  };
}

export default defineConfig({
  plugins: [
    svelte(),
    prerenderShell(),
    ...(mock ? [mockNotes({ server, transcribe: mock === 'fail' ? 'fail' : 'ok' })] : []),
  ],
  // Relative asset and API URLs: the same build works at / (retake open) and at /p/<name>/ (retake serve).
  base: './',
  // hls.js (about 580 kB) is its own chunk, loaded only for .m3u8 versions in browsers without native HLS.
  // Two pages: the review page (index.html) and the home screen of retake open <folder> (home.html, spec/home.md).
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL('index.html', import.meta.url)),
        home: fileURLToPath(new URL('home.html', import.meta.url)),
      },
    },
  },
  server: { proxy: { '/api': proxy, '/video': proxy } },
  // phone.css is read as text by a unit test (its media query must match lib/layout.ts).
  test: { include: ['src/**/*.test.ts'], environment: 'node', css: { include: [/phone\.css/] } },
});
