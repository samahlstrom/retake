// Safety net after the suite: no server (nor the WebKit container) outlives the run, whatever happened.
import { killLeftovers } from './server.ts';
import { stopWebkit } from './webkit.ts';

export default function globalTeardown(): void {
  killLeftovers();
  stopWebkit();
}
