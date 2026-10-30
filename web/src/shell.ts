// Prerendered at build time (and per request by the dev server) into index.html: the page's markup is static HTML,
// so it shows and the <video> element exists before any script runs, as in the legacy single-file page. main.ts then
// hydrates it.
import { render } from 'svelte/server';
import App from './App.svelte';
import { Review } from './lib/review.svelte';

export const shell = (): string => render(App, { props: { review: new Review() } }).body;
