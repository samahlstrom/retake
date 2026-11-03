// Typing on a phone (spec/notes.md, "Typing on a phone"): the one place that watches the visual viewport and the focus.
// It undoes any pan the browser makes to reveal a field, and writes what the layout needs on <html>: --vv-top, --kb
// and --vv-h, data-typing (which field has the focus) and data-kb (the keyboard is up). Only on a phone (`active`); on
// a desktop it writes nothing. The math is lib/viewport.ts.
import {
  barLift,
  GUESS_MS,
  KB_MIN,
  covered,
  kbSlot,
  layoutFor,
  panned,
  recall,
  remember,
  typingOf,
  type Reading,
  type Store,
  type Typing,
} from './viewport';

export interface ViewportOptions {
  /** The phone layout is on (the store does nothing otherwise). */
  active: () => boolean;
  /** Where the keyboard's height is remembered (localStorage), or null. */
  storage: () => Store | null;
}

export class Viewport {
  /** --vv-top: what's left of a pan of the visual viewport (normally 0). */
  top = $state(0);
  /** --kb: the on-screen keyboard's height, 0 when it isn't up. */
  kb = $state(0);
  /** --vv-h: the height above the keyboard (or above a toolbar over the page's bottom); 0 until read. */
  height = $state(0);
  /** The keyboard is up (measured, or remembered while it comes up). */
  up = $state(false);
  /** Which text field has the focus, or null. */
  typing = $state<Typing | null>(null);
  /** Record and Send's lift over a toolbar the browser lays over the page's bottom, held while typing. */
  lift = $state(0);

  readonly #o: ViewportOptions;
  #guess: number | null = null;
  #guessTimer: ReturnType<typeof setTimeout> | null = null;
  #written = false;

  constructor(o: ViewportOptions) {
    this.#o = o;
  }

  /** Starts watching; returns the stop. */
  start(): () => void {
    if (typeof window === 'undefined') return () => {};
    const vv = window.visualViewport;
    const update = () => this.update();
    vv?.addEventListener('resize', update);
    vv?.addEventListener('scroll', update);
    window.addEventListener('scroll', update, { passive: true });
    window.addEventListener('resize', update);
    document.addEventListener('focusin', this.#focusIn);
    document.addEventListener('focusout', this.#focusOut);
    this.#setTyping(typingOf(document.activeElement));
    this.update();
    return () => {
      vv?.removeEventListener('resize', update);
      vv?.removeEventListener('scroll', update);
      window.removeEventListener('scroll', update);
      window.removeEventListener('resize', update);
      document.removeEventListener('focusin', this.#focusIn);
      document.removeEventListener('focusout', this.#focusOut);
      this.#dropGuess();
      this.#clear();
    };
  }

  #read(): Reading {
    const vv = window.visualViewport;
    return {
      inner: window.innerHeight,
      top: vv?.offsetTop ?? 0,
      height: vv?.height ?? window.innerHeight,
      scale: vv?.scale ?? 1,
    };
  }

  #focusIn = (e: FocusEvent): void => {
    this.#setTyping(typingOf(e.target as Element | null));
  };
  /** The focus left a field: what has it now decides, once the next one (if any) has it. */
  #focusOut = (): void => {
    setTimeout(() => this.#setTyping(typingOf(document.activeElement)));
  };

  #dropGuess(): void {
    this.#guess = null;
    if (this.#guessTimer) clearTimeout(this.#guessTimer);
    this.#guessTimer = null;
  }

  #setTyping(t: Typing | null): void {
    if (t === this.typing) return;
    const was = this.typing;
    this.typing = t;
    if (t && !was && this.#o.active()) {
      // A field took the focus: its keyboard is on the way. Lay out for the one remembered for this window now, so the
      // field is never under it (and the browser has nothing to pan); drop the guess if none comes.
      this.#dropGuess();
      this.#guess = recall(this.#o.storage(), kbSlot(window.innerWidth, window.innerHeight));
      if (this.#guess !== null)
        this.#guessTimer = setTimeout(() => {
          this.#dropGuess();
          this.update();
        }, GUESS_MS);
    }
    if (!t) this.#dropGuess();
    this.update();
    // iOS pans a frame or two after the focus: look again then.
    if (t && typeof requestAnimationFrame === 'function') requestAnimationFrame(() => this.update());
  }

  /** Reads the browser, undoes a pan, and writes the layout. */
  update(): void {
    if (typeof window === 'undefined') return;
    if (!this.#o.active()) {
      this.#clear();
      return;
    }
    let r = this.#read();
    // The page never pans: whatever the browser scrolled to reveal a field goes back at once.
    if (panned(window.scrollX, window.scrollY, r)) {
      window.scrollTo(0, 0);
      r = this.#read();
    }
    const measured = covered(r);
    if (this.typing && measured >= KB_MIN) {
      // The keyboard is here: remember its height for this window; the guess is done.
      remember(this.#o.storage(), kbSlot(window.innerWidth, r.inner), measured);
      this.#dropGuess();
    }
    this.lift = barLift(this.lift, r, !!this.typing);
    const l = layoutFor(r, this.typing, this.#guess, this.lift);
    this.top = l.top;
    this.kb = l.kb;
    this.height = l.height;
    this.up = l.up;
    const root = document.documentElement;
    root.style.setProperty('--vv-top', `${l.top}px`);
    root.style.setProperty('--kb', `${l.kb}px`);
    root.style.setProperty('--vv-h', `${l.height}px`);
    if (this.typing) root.dataset.typing = this.typing;
    else delete root.dataset.typing;
    root.toggleAttribute('data-kb', l.up);
    this.#written = true;
  }

  #clear(): void {
    this.top = this.kb = this.height = 0;
    this.up = false;
    if (!this.#written || typeof document === 'undefined') return;
    const root = document.documentElement;
    for (const p of ['--vv-top', '--kb', '--vv-h']) root.style.removeProperty(p);
    delete root.dataset.typing;
    root.removeAttribute('data-kb');
    this.#written = false;
  }
}

/**
 * The stand-in field (spec/notes.md): iOS raises the keyboard only for a field focused in the tap itself, so a control
 * that opens a field that isn't there yet focuses this one in its tap, and the real field takes the focus over when it
 * shows. `as` says which field it stands in for (data-typing-as).
 */
let standIn: HTMLInputElement | null = null;
export function raiseKeyboard(as: Typing): void {
  if (typeof document === 'undefined') return;
  if (!standIn || !standIn.isConnected) {
    standIn = document.createElement('input');
    standIn.className = 'kb-proxy';
    standIn.setAttribute('aria-hidden', 'true');
    standIn.tabIndex = -1;
    document.body.append(standIn);
  }
  standIn.dataset.typingAs = as;
  standIn.focus({ preventScroll: true });
}
