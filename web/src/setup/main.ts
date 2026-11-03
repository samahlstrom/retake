import '../theme/tokens.css';
import './setup.css';
import { mount } from 'svelte';
import Setup from './Setup.svelte';
import type { SetupHook } from './setup';

declare global {
  interface Window {
    /** The end-to-end tests read the setup page's state here (spec/setup.md). */
    __setup: SetupHook;
  }
}

const hook: SetupHook = { loaded: false, state: null, done: false };
window.__setup = hook;
const target = document.getElementById('setup');
if (!target) throw new Error('#setup missing');
mount(Setup, { target, props: { hook } });
