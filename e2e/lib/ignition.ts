// Ignition's end-to-end helpers (spec/ignition.md): a config folder with Ignition turned on (through `retake ext
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
 * The fake agent. It reads the task's context ($RETAKE_CONTEXT) and edits the one document, v0/<video>_v0.md:
 * - for a comment that wants a version (Start v1): writes a 2 s video to $RETAKE_OUTPUT and v0/cuts/v1.json;
 * - otherwise, on its first attempt: keeps the person's words and adds `## Outline` and `## Script` (with `### Cold
 *   open` and `### The trick`) when the document has no script yet, asks about "Cold open" with `retake ask --file v0/<video>_v0.md --heading "Cold open"`
 *   and two options with a picture each (v0/images/jump.png and shop.png, made with ffmpeg), and reports; on a later
 *   attempt (the answer came back): writes the answer into the script's cold open and reports.
 */
const AGENT = `import { execFileSync } from 'node:child_process';
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
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
} else if (ctx.task.attempt === 1) {
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

export interface IgnitionConfig {
  /** XDG_CONFIG_HOME for the servers and retake connect. */
  home: string;
  /** Its config.toml. */
  file: string;
}

/**
 * A config folder for Ignition's tests under WORK/<name>: the author, no agents found on PATH, the fake agent, the
 * fake transcriber, and Ignition turned on with `retake ext enable ignition` (which edits this config.toml).
 */
export function ignitionConfig(name: string): IgnitionConfig {
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
  execFileSync(RETAKE_BIN, ['ext', 'enable', 'ignition'], { env: { ...process.env, XDG_CONFIG_HOME: home } });
  return { home, file };
}
