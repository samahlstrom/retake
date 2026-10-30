// Deletes the run's scratch data when everything passed; keeps it (and says where) when something failed.
import type { FullResult, Reporter } from '@playwright/test/reporter';
import { KEEP, WORK } from './env.ts';
import { removeWork } from './work.ts';

export default class CleanupReporter implements Reporter {
  printsToStdio(): boolean {
    return false;
  }

  onEnd(result: FullResult): void {
    if (result.status === 'passed' && !KEEP) {
      removeWork();
    } else {
      console.log(`e2e: scratch data kept in ${WORK} (takes, server logs, media)`);
    }
  }
}
