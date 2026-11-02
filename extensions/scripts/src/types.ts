// The page API Scripts is written against: retake-ext.d.ts, the types `retake ext init` ships to extension authors
// (spec/extensions.md, "The page API"). In this repo it is the page's own copy, so Scripts and the page can't drift.
export type {
  Anchor,
  Comments,
  Ctx,
  Edit,
  ExtError,
  FileInfo,
  Files,
  Home,
  HomeFolder as TreeNode,
  HomeVideo as Card,
  Item,
  Player,
  Recorded,
  Role,
  Word,
} from '../../../web/src/ext/retake-ext';
import type { Files, Item } from '../../../web/src/ext/retake-ext';

/** What `files().read` gives. */
export type ReadFile = Awaited<ReturnType<Files['read']>>;

/** The agent's open question under a comment. */
export type Ask = NonNullable<Item['ask']>;
