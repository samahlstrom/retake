// Ignition's page code (spec/ignition.md): one function per slot it fills. Each mounts its own Svelte (bundled in, never
// the page's) into the slot's element and returns the unmount. The stylesheet is built to ui/ignition.css.
import './ignition.css';
import { mount, unmount, type Component } from 'svelte';
import type { Ctx } from './types';
import Page from './page/Page.svelte';
import Editor from './editor/Editor.svelte';
import ScriptTab from './sidebar/ScriptTab.svelte';

function slot(view: Component<{ ctx: Ctx }>, el: HTMLElement, ctx: Ctx): () => void {
  const app = mount(view, { target: el, props: { ctx } });
  return () => void unmount(app);
}

/** The home's main area at #ext/ignition/…: the ideas, and New idea. */
export function page(el: HTMLElement, ctx: Ctx): () => void {
  return slot(Page, el, ctx);
}

/** v0, where the video would be: the editor. */
export function version(el: HTMLElement, ctx: Ctx): () => void {
  return slot(Editor, el, ctx);
}

/** The Script tab next to the comments, on a video that has v0. */
export function sidebarTab(el: HTMLElement, ctx: Ctx): () => void {
  return slot(ScriptTab, el, ctx);
}
