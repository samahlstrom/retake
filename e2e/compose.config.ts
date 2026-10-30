import { defineConfig } from '@playwright/test';
import { chromiumPath } from './lib/env.ts';

// The Docker Compose smoke test of the team server (deploy/smoke.sh runs it against a running stack). It is not part
// of `npm test`: it needs the stack, which deploy/smoke.sh starts and tears down.
export default defineConfig({
  testDir: './compose',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 600_000,
  expect: { timeout: 15_000 },
  outputDir: './test-results/compose',
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: { width: 1920, height: 1080 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: {
      executablePath: chromiumPath(),
      // Chromium's fake mic plays a tone: enough for a take with audio.
      args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream',
        '--autoplay-policy=no-user-gesture-required', '--mute-audio', '--hide-scrollbars'],
    },
  },
});
