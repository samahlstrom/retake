// What a video's ⋯ opened (spec/home.md, "A video's ⋯"): one share sheet and one Move to Trash confirm per page,
// whichever card or page asked, and the Undo after a move. VideoMenuHost.svelte shows them.
import type { VideoTarget } from '../../lib/video-menu';

export class VideoMenus {
  /** The video the share sheet is open for. */
  share = $state<VideoTarget | null>(null);
  /** The video Move to Trash asks about. */
  trash = $state<VideoTarget | null>(null);
  /** The last move, while Undo is offered. */
  undo = $state<{ id: string; title: string; home: string } | null>(null);
  /** A short message: the page's toast (set by the host). */
  say: (msg: string) => void = () => {};
}

/** The page's one. */
export const menus = new VideoMenus();
