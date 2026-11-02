# Extensions: your own tools in Retake

Retake does review, versions and the agent loop. An extension adds a tool around them: a section in the home's left
column, a page, a tab next to the comments, a new kind of version (Scripts' v0 script), files it keeps in a
video's folder, and instructions for the agent. Extensions live outside the install, so updating `retake` keeps them.

The contract is [spec/extensions.md](../spec/extensions.md); this page is the how-to.

**An extension is a program.** Its UI runs inside the Retake page with your session, and its tools and server run as
you. Install one only as you'd install any program. On a hosted server, only the extensions the admin installs and
allows run.

## Turn one on

Scripts ([spec/scripts.md](../spec/scripts.md)) comes with Retake, off:

```bash
retake ext list                 # what's found, where, and whether it's on
retake ext enable scripts       # adds it to [extensions] enabled in ~/.config/retake/config.toml
retake open ~/Videos            # the Scripts section is under the folder tree
```

On a server, allow it for everyone (each person can still turn it off for themselves in the account menu):

```bash
retake serve --extensions scripts         # or RETAKE_EXTENSIONS=scripts, or [server] extensions = ["scripts"]
```

and turn it on on the editing computer too (`retake ext enable scripts`), since its instructions for the agent run
there.

Scripts was called Ignition. A config, server flag or command that still says `ignition` keeps working.

## Where they live

| | Folder |
|---|---|
| Yours | `~/.config/retake/extensions/<id>/` (copy the folder there) |
| A server's | `<data dir>/extensions/<id>/` (`/data/extensions/` in Docker) |
| A projects folder's | `<folder>/.retake/extensions/<id>/`: enabling one pins its files; a change turns it off until you enable it again |
| Built in | Scripts |
| While you write one | `--ext DIR` on `retake open`, or `RETAKE_EXT_PATH` |

## Write one: hello

Three small files. It adds a "Hello" section to the home that lists your videos, and a "Hello" tab next to the comments
that follows the playhead.

```
hello/
  extension.toml
  ui/hello.js
  ui/hello.css
```

`extension.toml`:

```toml
api = 1
id = "hello"
name = "Hello"
version = "0.1.0"
description = "Says hello on the home and next to the comments."

[ui]
module = "ui/hello.js"
style = "ui/hello.css"

[nav]
title = "Hello"

[[slots]]
slot = "page"

[[slots]]
slot = "sidebar-tab"
title = "Hello"
```

`ui/hello.js` exports one function per slot. Each gets an empty element to fill and `ctx`, the extension API, and
may return a function that cleans up:

```js
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
```

`ui/hello.css` uses the theme's tokens, so it matches any theme, and stays inside its own element:

```css
[data-ext='hello'] li,
[data-ext='hello'] p {
  color: var(--text-3);
  font-family: var(--font-ui);
  padding: 8px 16px;
}
```

Try it, then install it:

```bash
retake ext check ./hello                 # the manifest, the exports, the CSS scope and tokens
retake open ~/Videos --ext ./hello       # on for this run only; reload the page after each edit
cp -r ./hello ~/.config/retake/extensions/ && retake ext enable hello
```

The `ctx` types for your editor are `web/src/ext/retake-ext.d.ts` in Retake's source. (`retake ext init`, `install`,
`remove` and `tool` are in the spec but not built yet.)

## What else an extension can do

- **A kind of version**: a `v0/` folder with its marker file, listed first in the version menus and opened in your
  `version` slot instead of the video. Comments on it are typed, and the agent works them like any other.
- **Files it owns** (`[files] owns = ["v0/**"]`): `ctx.files()` reads, writes and watches them. They're plain files in
  the video's folder, local or on the editing computer when hosted.
- **Recording and transcription**: `ctx.onRecorded` gets the audio of R on your version, and `ctx.transcribe` turns
  a file into words.
- **Instructions for the agent** (`[agent]`): Markdown added to the prompt of a task on its version or files, and a
  short context for every task of a video that has its files. Retake's own rules come last and win.
- **Agent tools** (`[[agent.tools]]`, not built yet): small programs the agent runs with `retake ext tool`, in the
  asset plug-in protocol ([plugins.md](plugins.md)).
- **A server** (`[server]`, not built yet), when files aren't enough. Retake starts it when needed and stops it when
  idle. It signs people in for you and passes their role in a header.
- **Undo an agent edit**: before each agent attempt Retake keeps a copy of the files you own in `.retake/history/`;
  `ctx.files().history(path)` lists the agent's edits and `undo(id, path)` puts one back.

## What it can't do

Change the review: the pen, the comment box and R to record are core's. Your code gets no pen, takes or
review keys, and nothing over the video. It also can't change how agents run or who may send, answer or switch them,
or write a video version. A broken extension shows one line in its own place, and the rest of Retake keeps working.

## After an update

```bash
retake ext check                         # every enabled extension: what this version of Retake won't take
```

Retake 2.x still loads extensions written for API 1, and the release notes say what to change. See
[spec/extensions.md](../spec/extensions.md), "Versioning".
