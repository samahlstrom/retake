import { defineConfig } from '@playwright/test';
import { chromiumPath, MIC_WAV } from './lib/env.ts';

// One worker, one browser, files in order: the scenario is timed against the fake mic, so nothing runs in parallel.
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  timeout: 180_000,
  expect: { timeout: 10_000 },
  globalSetup: './lib/global-setup.ts',
  globalTeardown: './lib/global-teardown.ts',
  outputDir: './test-results',
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: './playwright-report' }],
    ['./lib/cleanup-reporter.ts'],
  ],
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: { width: 1920, height: 1080 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: {
      executablePath: chromiumPath(),
      args: [
        '--use-fake-ui-for-media-stream',
        '--use-fake-device-for-media-stream',
        // The fake mic plays from the moment the page opens it (the first R), once.
        `--use-file-for-fake-audio-capture=${MIC_WAV}%noloop`,
        '--autoplay-policy=no-user-gesture-required',
        '--mute-audio',
        '--hide-scrollbars',
      ],
    },
  },
});
