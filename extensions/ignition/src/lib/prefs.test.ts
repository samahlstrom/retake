import { describe, expect, it } from 'vitest';
import { docName, loadPrefs, savePrefs } from './prefs';

class Mem {
  m = new Map<string, string>();
  getItem(k: string) {
    return this.m.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
}

describe('what Ignition keeps in the browser', () => {
  it('keeps the open document and the carets per video', () => {
    const s = new Mem();
    savePrefs(s, 'jump', { tab: 'script', ref: null, carets: { 'v0/script.md': 42 } });
    expect(loadPrefs(s, 'jump')).toEqual({ tab: 'script', ref: null, carets: { 'v0/script.md': 42 } });
    expect(loadPrefs(s, 'other').tab).toBe('idea');
  });

  it('ignores what it does not know', () => {
    const s = new Mem();
    s.setItem('ignition.editor:x', JSON.stringify({ tab: 'nope', ref: '../etc', carets: { a: -1, b: 'x', c: 3.7 } }));
    expect(loadPrefs(s, 'x')).toEqual({ tab: 'idea', ref: null, carets: { c: 3 } });
    s.setItem('ignition.editor:y', '{');
    expect(loadPrefs(s, 'y').tab).toBe('idea');
    expect(loadPrefs(null, 'z').tab).toBe('idea');
  });

  it('names documents for the ask box', () => {
    expect(docName('v0/script.md')).toBe('the script');
    expect(docName('v0/refs/links.md')).toBe('links.md');
  });
});
