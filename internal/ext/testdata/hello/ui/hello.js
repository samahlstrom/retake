export function page(el, ctx) {
  const list = document.createElement('ul');
  el.append(list);
  ctx.home().then((home) => {
    for (const v of home.projects) {
      const li = document.createElement('li');
      li.textContent = `${v.title} · ${v.version || 'no cut yet'}`;
      list.append(li);
    }
  });
}

export function sidebarTab(el, ctx) {
  const p = document.createElement('p');
  p.dataset.part = 'hello-time';
  el.append(p);
  const show = (t) => (p.textContent = `Hello from ${ctx.video.title} ${ctx.version.label}, at ${t.toFixed(1)} s`);
  show(ctx.player.time());
  return ctx.player.onTime(show); // unsubscribes when the tab closes
}
