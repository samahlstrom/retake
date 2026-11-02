// v0/cuts/<label>.json (spec/ignition.md, "Later versions link back to the script"): which `##` sections of
// script.md a version covers, and where. The editor's chips and the Script tab's playhead read them.
import { clock } from './stamp';

export interface CutSection {
  heading: string;
  from: number;
  to: number;
}

export interface Cut {
  version: string;
  sections: CutSection[];
}

const key = (s: string): string => s.replace(/\s+/g, ' ').trim().toLowerCase();

/** A cuts file, made safe: sections without a heading or with bad times are left out. `label` names the version when
 * the file doesn't. null when it isn't one. */
export function parseCut(body: unknown, label = ''): Cut | null {
  if (!body || typeof body !== 'object') return null;
  const b = body as Record<string, unknown>;
  const version = typeof b.version === 'string' && b.version ? b.version : label;
  if (!version || !Array.isArray(b.sections)) return null;
  const sections: CutSection[] = [];
  for (const x of b.sections as unknown[]) {
    if (!x || typeof x !== 'object') continue;
    const s = x as Record<string, unknown>;
    const heading = typeof s.heading === 'string' ? s.heading.replace(/^#+\s*/, '').trim() : '';
    const from = Number(s.from),
      to = Number(s.to);
    if (!heading || !Number.isFinite(from) || !Number.isFinite(to) || to < from || from < 0) continue;
    sections.push({ heading, from, to });
  }
  return { version, sections: sections.sort((a, b) => a.from - b.from) };
}

/** A version label's order: v3 < v4-draft1 < v4-draft2 < v4. Unknown labels sort first. */
export function versionOrder(label: string): [number, number] {
  const m = /^v(\d+)(?:-draft(\d+))?$/.exec(label);
  if (!m) return [-1, 0];
  return [Number(m[1]), m[2] === undefined ? Number.MAX_SAFE_INTEGER : Number(m[2])];
}

export function newerFirst(a: string, b: string): number {
  const x = versionOrder(a),
    y = versionOrder(b);
  return y[0] - x[0] || y[1] - x[1] || a.localeCompare(b);
}

export interface Chip {
  version: string;
  /** Where that section starts in it, in seconds. */
  t: number;
  /** "v3 0:12". */
  text: string;
}

/** For each `##` heading of the script, the newest version that covers it and where; a heading no cut has is left
 * out (a heading renamed since the cut has no chip until the next version's file). Keys are the headings as given. */
export function chips(scriptHeadings: string[], cuts: Cut[]): Map<string, Chip> {
  const out = new Map<string, Chip>();
  const newest = [...cuts].sort((a, b) => newerFirst(a.version, b.version));
  for (const h of scriptHeadings) {
    for (const c of newest) {
      const s = c.sections.find((x) => key(x.heading) === key(h));
      if (s) {
        out.set(h, { version: c.version, t: s.from, text: `${c.version} ${clock(s.from)}` });
        break;
      }
    }
  }
  return out;
}

/** The section under the playhead: the one whose [from, to) holds t (the last one also holds its own end). */
export function sectionAt(cut: Cut | null, t: number): CutSection | null {
  if (!cut || !Number.isFinite(t)) return null;
  for (const s of cut.sections) if (t >= s.from && t < s.to) return s;
  const last = cut.sections[cut.sections.length - 1];
  return last && t === last.to ? last : null;
}

/** The cut's section for a heading, or null when the version doesn't cover it. */
export function sectionFor(cut: Cut | null, heading: string): CutSection | null {
  return cut?.sections.find((s) => key(s.heading) === key(heading)) ?? null;
}

/** The label a cuts file is for: v0/cuts/v3.json is "v3". */
export function labelOfCutFile(path: string): string | null {
  return /^v0\/cuts\/([^/]+)\.json$/.exec(path)?.[1] ?? null;
}
