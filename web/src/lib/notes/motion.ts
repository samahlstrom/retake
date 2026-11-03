// How the comments list moves (spec/notes.md, "What a version shows"): a comment the agent finishes fades and lifts out
// of To do while the cards under it slide up into its place, and fades into Done. Only a change in place moves:
// switching the tab, the version or the search swaps the list at once. Transform and opacity only, never layout.
// Pure, so it is unit-tested; the list only plays what these return.
import type { CommentState } from '../status';

/** What the list shows: its tab, its version and its search. A move animates only within one scope. */
export interface ListScope {
  filter: 'todo' | 'done';
  version: string | null;
  search: string;
}

export const scopeKey = (s: ListScope): string => `${s.filter}|${s.version ?? ''}|${s.search.trim().toLowerCase()}`;

/** How long a card takes to leave, arrive or slide, in ms. */
export const LEAVE_MS = 260;
export const ARRIVE_MS = 220;
export const SLIDE_MS = 240;

/** A card leaving the list: `done` when it left To do because it's done now (it fades and lifts toward the Done tab),
 * else `gone` (deleted, filtered, another tab or version: no motion). `then` and `now` are the scope the list had and
 * has; `state` the card's state now. */
export function leaving(
  then: string | null,
  now: string,
  filter: ListScope['filter'],
  state: CommentState,
): 'done' | 'gone' {
  return then === now && filter === 'todo' && state === 'done' ? 'done' : 'gone';
}

/** A card arriving: `done` when it just finished and Done is on screen (it fades in), else `none`. */
export function arriving(
  then: string | null,
  now: string,
  filter: ListScope['filter'],
  state: CommentState,
): 'done' | 'none' {
  return then === now && filter === 'done' && state === 'done' ? 'done' : 'none';
}

/** Whether the cards still there slide from where they were to where they are: only within one scope. */
export const slides = (then: string | null, now: string): boolean => then === now;

/** The keyframes of a card leaving to Done, for a transition's `css` (t runs 1 → 0): it fades and lifts a little. */
export const leaveCss = (t: number): string =>
  `opacity: ${t}; transform: translateY(${((1 - t) * -10).toFixed(2)}px) scale(${(0.98 + 0.02 * t).toFixed(4)})`;

/** The keyframes of a card arriving in Done (t runs 0 → 1). */
export const arriveCss = (t: number): string => `opacity: ${t}; transform: translateY(${((1 - t) * 8).toFixed(2)}px)`;
