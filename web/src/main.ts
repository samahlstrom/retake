import './theme/tokens.css';
import './app.css';
import './ui.css';
import './phone.css';
import { flushSync, hydrate, mount } from 'svelte';
import App from './App.svelte';
import { Review, type ReviewHook } from './lib/review.svelte';

declare global {
  interface Window {
    /** The end-to-end tests read the page's state here (spec/api.md, "The page's test hook"). */
    __review: ReviewHook;
  }
}

const review = new Review();
window.__review = review.hook;
const target = document.getElementById('app');
if (!target) throw new Error('#app missing');
// index.html carries the prerendered markup (vite.config.ts), so the page is there before this script runs.
(target.firstChild ? hydrate : mount)(App, { target, props: { review } });
review.watchLayout(); // the prerendered page is the desktop layout; a phone switches to its own here
flushSync(); // run the mount effects now: the elements are bound before start()
void review.start();
