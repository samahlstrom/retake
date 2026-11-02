// The editor around the live preview: CodeMirror 6 editing the document's raw text (what's on disk is byte for byte
// what was typed; nothing re-serializes the Markdown), Markdown's parse for the preview, undo, wrapping lines, and
// the keys (Esc leaves the text so R talks; Ctrl+S saves; Enter in a list continues it).
import { Annotation, EditorState, type Extension } from '@codemirror/state';
import { EditorView, keymap, type ViewUpdate } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { LanguageSupport } from '@codemirror/language';
import { markdownKeymap, markdownLanguage } from '@codemirror/lang-markdown';
import { livePreview, type LiveConfig } from './live';

/** A change that came from outside the editor (a reload, an agent's edit, a talk-out): not typing, and not in undo. */
export const outside = Annotation.define<boolean>();

/** Whether a text breaks its lines with CR LF only (then the editor keeps them; otherwise it uses LF). */
export const isCRLF = (text: string): boolean => text.includes('\r\n') && !/\r(?!\n)|(?<!\r)\n/.test(text);

/** An editor position as an offset in the file's text: with CR LF the editor counts each line break as one
 * character. */
export const offsetOf = (state: EditorState, pos: number): number =>
  state.lineBreak === '\r\n' ? pos + state.doc.lineAt(pos).number - 1 : pos;

/** An offset in the file's text `raw` as an editor position (the other way). */
export function posOf(raw: string, off: number): number {
  if (!isCRLF(raw)) return off;
  let breaks = 0;
  for (let i = raw.indexOf('\r\n'); i !== -1 && i < off; i = raw.indexOf('\r\n', i + 2)) breaks++;
  return off - breaks;
}

/**
 * CodeMirror's base theme is a stylesheet outside the page's layers, so it beats Scripts' layered CSS: here are the
 * few properties it sets, taken from the page instead (the font, the line height, no padding of its own, the caret in
 * a token). The padding of the text and its lines comes from custom properties scripts.css sets (--ig-tail, --ig-pad-*,
 * --ig-indent, --ig-list-pad); everything else that draws the preview is in scripts.css.
 */
const look = EditorView.theme(
  {
    '&': { color: 'inherit', fontSize: 'inherit', backgroundColor: 'transparent' },
    '&.cm-focused': { outline: 'none' },
    '.cm-scroller': { fontFamily: 'inherit', lineHeight: 'inherit' },
    '.cm-content': { padding: '0 0 var(--ig-tail, 0px)', caretColor: 'var(--text-strong)' },
    '.cm-line': {
      padding:
        'var(--ig-pad-top, 0px) var(--ig-pad-right, 0px) var(--ig-pad-bottom, 0px) ' +
        'calc(var(--ig-indent, 0px) + var(--ig-list-pad, 0px))',
    },
  },
  { dark: true },
);

export interface Setup extends LiveConfig {
  text: string;
  /** Where the caret starts. */
  caret: number;
  /** Ctrl+S (⌘S). */
  save(): void;
  /** Every update (typing, the caret, the focus). */
  update(u: ViewUpdate): void;
  /** The extensions for the paste and drop of images, and anything else the pane adds. */
  extra?: Extension;
}

/** The editor's state for a text. */
export function editorState(s: Setup): EditorState {
  const crlf = isCRLF(s.text);
  const caret = Math.min(Math.max(0, s.caret), s.text.length - (crlf ? s.text.split('\r\n').length - 1 : 0));
  return EditorState.create({
    doc: s.text,
    selection: { anchor: caret },
    extensions: [
      crlf ? EditorState.lineSeparator.of('\r\n') : [],
      history(),
      keymap.of([
        {
          key: 'Escape',
          run: (v) => {
            v.contentDOM.blur();
            return true;
          },
        },
        {
          key: 'Mod-s',
          preventDefault: true,
          run: () => {
            s.save();
            return true;
          },
        },
        ...markdownKeymap,
        ...defaultKeymap,
        ...historyKeymap,
      ]),
      new LanguageSupport(markdownLanguage),
      EditorView.lineWrapping,
      EditorState.readOnly.of(!!s.readonly),
      EditorView.editable.of(!s.readonly),
      EditorView.contentAttributes.of({
        'aria-label': 'The document',
        'data-part': 'scripts-text',
        spellcheck: 'true',
        autocapitalize: 'sentences',
        autocorrect: 'on',
      }),
      // The top fade (under the back link and ⋯) and a little room above the bottom bar.
      EditorView.scrollMargins.of(() => ({ top: 72, bottom: 48 })),
      look,
      livePreview(s),
      EditorView.updateListener.of(s.update),
      s.extra ?? [],
    ],
  });
}
