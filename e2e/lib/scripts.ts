// Scripts' end-to-end helpers (spec/scripts.md): a config folder with Scripts turned on (through `retake ext
// enable`), a fake transcriber (a command engine that prints fixed words), and a fake agent that shapes the one
// document (v0/<video>_v0.md), asks about a heading and builds a first cut. Nothing runs a real agent or a real speech model.
import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { RETAKE_BIN, WORK } from './env.ts';

/** The words the fake transcriber hears in every talk-out. */
export const WORDS = 'So the idea is the jump to secure souls only works if you time it';

/** The fake transcriber: prints WORDS as {"words": [{w, s, e, p}]} whatever the audio. */
const TRANSCRIBER = `const words = ${JSON.stringify(WORDS)}.split(' ');
process.stdout.write(JSON.stringify({ words: words.map((w, i) => ({ w: ' ' + w, s: i * 0.3, e: i * 0.3 + 0.3, p: 0.9 })) }));
`;

/**
 * The fake agent. In a chat turn (no $RETAKE_CONTEXT: spec/chat.md) it reads the prompt on stdin and prints Claude
 * Code's stream-json: for a message that picks an opening ("Start on the jump") it writes it into the cold open; for
 * one that says "slowly" it says a few words, runs two commands with an 8 s pause between them, and edits nothing; for
 * one with a link it puts the link alone on a line at the end; otherwise it adds `## Outline` and `## Script` (when
 * there's none yet), makes two pictures and asks "Which opening?" with them as choices (`retake ask --option`).
 * In an editing task it reads the task's context ($RETAKE_CONTEXT) and edits the one document, v0/<video>_v0.md:
 * - for a comment that wants a version (Start v1): writes a 2 s video to $RETAKE_OUTPUT and v0/cuts/v1.json;
 * - otherwise, on its first attempt: keeps the person's words and adds `## Outline` and `## Script` (with `### Cold
 *   open` and `### The trick`) when the document has no script yet, asks about "Cold open" with `retake ask --file v0/<video>_v0.md --heading "Cold open"`
 *   and two options with a picture each (v0/images/jump.png and shop.png, made with ffmpeg), and reports; once the
 *   answer is in its context (a later attempt, or a follow-up round of this one): writes it into the script's cold
 *   open and reports.
 */
const AGENT = `import { execFileSync } from 'node:child_process';
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
const doc0 = 'v0/' + readdirSync('v0').filter((f) => f.endsWith('_v0.md')).sort()[0];
if (!process.env.RETAKE_CONTEXT) {
  // A chat turn (spec/chat.md): the prompt on stdin, Claude Code's stream-json out.
  const prompt = readFileSync(0, 'utf8');
  const msg = prompt.slice(prompt.lastIndexOf("The person's message"));
  const sid = 'e2e-session';
  const out = (o) => process.stdout.write(JSON.stringify({ ...o, session_id: sid, parent_tool_use_id: null }) + '\\n');
  const say = (t) => {
    out({ type: 'stream_event', event: { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } } });
    for (const w of t.split(/(?<= )/)) out({ type: 'stream_event', event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: w } } });
  };
  let n = 0;
  const tool = (name, input) => out({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 't' + ++n, name, input }] } });
  const path = process.cwd() + '/' + doc0;
  const text = readFileSync(doc0, 'utf8');
  out({ type: 'system', subtype: 'init', cwd: process.cwd() });
  const picked = /Start on the (jump|shop)/.exec(msg);
  const link = /https?:\\/\\/\\S+/.exec(msg);
  if (picked) {
    tool('Edit', { file_path: path, old_string: '[The jump.]', new_string: '[x]' });
    writeFileSync(doc0, text.replace('[The jump.]', '[' + picked[0] + '.]'));
    say('Done: the cold open starts on the ' + picked[1] + '.');
  } else if (/slowly/.test(msg)) {
    // A slow turn, for the page's live activity line: words, a command, a pause, more words and another command.
    say('Checking the server first. ');
    tool('Bash', { command: 'ls -la v0' });
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 8000);
    say('It serves the script from the right place.');
    tool('Bash', { command: 'ls v0/images' });
  } else if (link) {
    tool('WebFetch', { url: link[0], prompt: 'title' });
    tool('Edit', { file_path: path, old_string: 'x', new_string: 'y' });
    writeFileSync(doc0, text.trimEnd() + '\\n\\n' + link[0] + '\\n');
    say('Added the link at the end, on its own line.');
  } else {
    say("I'll shape it into an outline and a script.");
    tool('Read', { file_path: path });
    tool('Edit', { file_path: path, old_string: 'x', new_string: 'y' });
    if (!text.includes('## Script')) writeFileSync(doc0, text.trimEnd() + '\\n\\n## Outline\\n\\n- Cold open: the jump, cold.\\n- The trick: why timing matters.\\n\\n'
      + '## Script\\n\\n### Cold open\\n\\n[The jump.] So the idea is the jump.\\n\\n### The trick\\n\\nTime it.\\n');
    mkdirSync('v0/images', { recursive: true });
    for (const [name, src] of [['jump', 'testsrc2'], ['shop', 'smptebars']]) {
      execFileSync('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-threads', '2', '-f', 'lavfi', '-i', src + '=s=320x180',
        '-frames:v', '1', 'v0/images/' + name + '.png']);
    }
    tool('Write', { file_path: process.cwd() + '/v0/images/jump.png', content: '' });
    tool('Bash', { command: 'retake ask --option "Start on the jump@v0/images/jump.png" "Which opening?"' });
    execFileSync(process.env.RETAKE_BIN_FOR_AGENT, ['ask', '--file', doc0, '--heading', 'Cold open',
      '--option', 'Start on the jump@v0/images/jump.png', '--option', 'Start on the shop@v0/images/shop.png', 'Which opening?']);
    say('Wrote the outline and the script. Which opening do you want?');
  }
  out({ type: 'result', subtype: 'success', is_error: false, result: 'ok' });
  process.exit(0);
}
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
const report = (message) => writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message }));
const mine = (ctx.notes ?? []).filter((n) => ctx.task.notes.includes(n.id));
const doc = 'v0/' + readdirSync('v0').filter((f) => f.endsWith('_v0.md')).sort()[0];
const text = readFileSync(doc, 'utf8');
if (mine.some((n) => n.detail && n.detail.wants === 'version')) {
  execFileSync('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-threads', '2', '-f', 'lavfi', '-i',
    'testsrc2=s=320x180:r=25:d=2', '-f', 'lavfi', '-i', 'sine=f=330:d=2', '-c:v', 'libx264', '-preset', 'ultrafast',
    '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', '-movflags', '+faststart', process.env.RETAKE_OUTPUT]);
  mkdirSync('v0/cuts', { recursive: true });
  writeFileSync('v0/cuts/v1.json', JSON.stringify({ version: 'v1', sections: [
    { heading: 'Cold open', from: 0, to: 1 }, { heading: 'The trick', from: 1, to: 2 }] }));
  report('Built the first cut from the script');
} else if (!(ctx.questions ?? []).some((q) => q.answer)) { // until the answer is in its context (a later attempt or round)
  if (!text.includes('## Script')) writeFileSync(doc, text.trimEnd() + '\\n\\n## Outline\\n\\n- Cold open: the jump, cold.\\n- The trick: why timing matters.\\n\\n'
    + '## Script\\n\\n### Cold open\\n\\n[The jump.] So the idea is the jump.\\n\\n### The trick\\n\\nTime it.\\n');
  // Two pictures to choose from, kept next to the document (v0/images/), as an agent asks an image decision.
  mkdirSync('v0/images', { recursive: true });
  for (const [name, src] of [['jump', 'testsrc2'], ['shop', 'smptebars']]) {
    execFileSync('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-threads', '2', '-f', 'lavfi', '-i', src + '=s=320x180',
      '-frames:v', '1', 'v0/images/' + name + '.png']);
  }
  execFileSync(process.env.RETAKE_BIN_FOR_AGENT, ['ask', '--note', String(ctx.task.notes[0]), '--file', doc,
    '--heading', 'Cold open', '--option', 'Start on the jump@v0/images/jump.png',
    '--option', 'Start on the shop@v0/images/shop.png', 'Which opening?']);
  report('Wrote the outline and the script; asked which opening');
} else {
  const answer = (ctx.questions ?? []).map((q) => q.answer && q.answer.text).filter(Boolean).at(-1) ?? '';
  writeFileSync(doc, text.replace('[The jump.]', '[' + answer + '.]'));
  report('Opened on: ' + answer);
}
`;

export interface ScriptsConfig {
  /** XDG_CONFIG_HOME for the servers and retake connect. */
  home: string;
  /** Its config.toml. */
  file: string;
}

/**
 * A config folder for Scripts' tests under WORK/<name>: the author, no agents found on PATH, the fake agent, the
 * fake transcriber, and Scripts turned on with `retake ext enable scripts` (which edits this config.toml).
 */
export function scriptsConfig(name: string): ScriptsConfig {
  const home = join(WORK, name, 'config');
  const dir = join(home, 'retake');
  mkdirSync(dir, { recursive: true });
  const transcriber = join(WORK, name, 'fake-words.mjs');
  const agent = join(WORK, name, 'fake-agent.mjs');
  writeFileSync(transcriber, TRANSCRIBER);
  writeFileSync(agent, AGENT);
  const file = join(dir, 'config.toml');
  writeFileSync(file, `name = "E2E Reviewer"
open_browser = false
find_agents = false

[transcribe]
engine = "command"
command = "node ${transcriber}"

[[agents]]
id = "fake"
name = "Fake agent"
command = ["node", "${agent}"]
env = { RETAKE_BIN_FOR_AGENT = "${RETAKE_BIN}" }
`);
  execFileSync(RETAKE_BIN, ['ext', 'enable', 'scripts'], { env: { ...process.env, XDG_CONFIG_HOME: home } });
  return { home, file };
}
