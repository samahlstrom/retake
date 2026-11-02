// Ignition's end-to-end helpers (spec/ignition.md): a config folder with Ignition turned on (through `retake ext
// enable`), a fake transcriber (a command engine that prints fixed words), and a fake agent that shapes the documents,
// asks about a heading and builds a first cut. Nothing runs a real agent or a real speech model.
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
 * The fake agent. It reads the task's context ($RETAKE_CONTEXT) and:
 * - for a comment that wants a version (Start v1): writes a 2 s video to $RETAKE_OUTPUT and v0/cuts/v1.json;
 * - otherwise, on its first attempt: writes v0/outline.md and v0/script.md with a "Cold open" section, asks about it
 *   with `retake ask --file v0/script.md --heading "Cold open"` and two options, and reports; on a later attempt
 *   (the answer came back): writes the answer into the script and reports.
 */
const AGENT = `import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
const report = (message) => writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message }));
const mine = (ctx.notes ?? []).filter((n) => ctx.task.notes.includes(n.id));
if (mine.some((n) => n.detail && n.detail.wants === 'version')) {
  execFileSync('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-threads', '2', '-f', 'lavfi', '-i',
    'testsrc2=s=320x180:r=25:d=2', '-f', 'lavfi', '-i', 'sine=f=330:d=2', '-c:v', 'libx264', '-preset', 'ultrafast',
    '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', '-movflags', '+faststart', process.env.RETAKE_OUTPUT]);
  mkdirSync('v0/cuts', { recursive: true });
  writeFileSync('v0/cuts/v1.json', JSON.stringify({ version: 'v1', sections: [
    { heading: 'Cold open', from: 0, to: 1 }, { heading: 'The trick', from: 1, to: 2 }] }));
  report('Built the first cut from script.md');
} else if (ctx.task.attempt === 1) {
  writeFileSync('v0/outline.md', '## Cold open\\nThe jump, cold.\\n\\n## The trick\\nWhy timing matters.\\n');
  writeFileSync('v0/script.md', '## Cold open\\n\\n[The jump.] So the idea is the jump.\\n\\n## The trick\\n\\nTime it.\\n');
  execFileSync(process.env.RETAKE_BIN_FOR_AGENT, ['ask', '--note', String(ctx.task.notes[0]), '--file', 'v0/script.md',
    '--line', '1', '--heading', 'Cold open', '--option', 'Start on the jump', '--option', 'Start on the shop',
    'Which opening?']);
  report('Wrote the outline and the script; asked which opening');
} else {
  const answer = (ctx.questions ?? []).map((q) => q.answer && q.answer.text).filter(Boolean).at(-1) ?? '';
  writeFileSync('v0/script.md', '## Cold open\\n\\n[' + answer + '.] So the idea is the jump.\\n\\n## The trick\\n\\nTime it.\\n');
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
