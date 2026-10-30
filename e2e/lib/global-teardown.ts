// Safety net after the suite: no server outlives the run, whatever happened.
import { killLeftovers } from './server.ts';

export default function globalTeardown(): void {
  killLeftovers();
}
