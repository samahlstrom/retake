// The one toast at the top of the video. A new message replaces the old one and restarts its timer; the text stays
// while it fades out, as in the legacy page.

export class Toast {
  msg = $state('');
  show = $state(false);
  #timer: ReturnType<typeof setTimeout> | undefined;

  say = (msg: string, ms = 2200): void => {
    this.msg = msg;
    this.show = true;
    clearTimeout(this.#timer);
    this.#timer = setTimeout(() => (this.show = false), ms);
  };
}
