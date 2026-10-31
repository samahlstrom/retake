import './home.css';
import { mount } from 'svelte';
import Home from './Home.svelte';
import { Home as HomeState, type HomeHook } from './home.svelte';

declare global {
  interface Window {
    /** The end-to-end tests read the home's state here (spec/home.md). */
    __home: HomeHook;
  }
}

const home = new HomeState();
window.__home = home.hook;
const target = document.getElementById('home');
if (!target) throw new Error('#home missing');
mount(Home, { target, props: { home } });
home.start();
