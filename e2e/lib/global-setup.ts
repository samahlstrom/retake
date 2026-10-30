// Runs once before the suite: a fresh scratch root with all generated media and the servers' config.
import { generateMedia } from './fixtures.ts';
import { LEGACY_REVIEW, LEGACY, MODE, RETAKE_BIN, WORK } from './env.ts';
import { killLeftovers } from './server.ts';
import { resetWork } from './work.ts';

export default async function globalSetup(): Promise<void> {
  killLeftovers();
  resetWork();
  const t = Date.now();
  const media = await generateMedia(WORK);
  console.log(`e2e: server ${MODE} (${LEGACY ? LEGACY_REVIEW : RETAKE_BIN}); media in ${media.dir} `
    + `(${((Date.now() - t) / 1000).toFixed(1)} s${media.labels ? '' : ', no drawtext labels'})`);
}
