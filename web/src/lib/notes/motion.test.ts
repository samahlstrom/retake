import { describe, expect, it } from 'vitest';
import { arriveCss, arriving, leaveCss, leaving, scopeKey, slides } from './motion';

const todo = scopeKey({ filter: 'todo', version: 'v4', search: '' });
const done = scopeKey({ filter: 'done', version: 'v4', search: '' });

describe('the comments list moves only in place', () => {
  it('a comment that turns Done leaves To do with a move; nothing else does', () => {
    expect(leaving(todo, todo, 'todo', 'done')).toBe('done');
    // Deleted, or answered with a question: it just goes.
    expect(leaving(todo, todo, 'todo', 'open')).toBe('gone');
    expect(leaving(todo, todo, 'todo', 'question')).toBe('gone');
    // A switch of tab, version or search swaps the list at once.
    expect(leaving(todo, done, 'done', 'done')).toBe('gone');
    expect(leaving(todo, scopeKey({ filter: 'todo', version: 'v5', search: '' }), 'todo', 'done')).toBe('gone');
    expect(leaving(todo, scopeKey({ filter: 'todo', version: 'v4', search: 'logo' }), 'todo', 'done')).toBe('gone');
    expect(leaving(null, todo, 'todo', 'done')).toBe('gone'); // the first look
  });
  it('a comment that turns Done fades into Done when Done is on screen', () => {
    expect(arriving(done, done, 'done', 'done')).toBe('done');
    expect(arriving(todo, done, 'done', 'done')).toBe('none'); // switched to Done: no move
    expect(arriving(todo, todo, 'todo', 'working')).toBe('none'); // a new comment in To do
  });
  it('the cards that stay slide into place only within one scope', () => {
    expect(slides(todo, todo)).toBe(true);
    expect(slides(todo, done)).toBe(false);
    expect(slides(null, todo)).toBe(false);
    // Search is the same however it's typed.
    expect(scopeKey({ filter: 'todo', version: null, search: ' Logo ' })).toBe(
      scopeKey({ filter: 'todo', version: null, search: 'logo' }),
    );
  });
  it('moves with transform and opacity only', () => {
    for (const css of [leaveCss(0), leaveCss(0.5), leaveCss(1), arriveCss(0), arriveCss(1)]) {
      const props = css.split(';').map((p) => p.split(':')[0]!.trim());
      expect(props.every((p) => p === 'opacity' || p === 'transform')).toBe(true);
    }
    expect(leaveCss(1)).toBe('opacity: 1; transform: translateY(0.00px) scale(1.0000)');
    expect(leaveCss(0)).toContain('opacity: 0');
  });
});
