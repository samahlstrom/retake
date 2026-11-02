import { describe, expect, it } from 'vitest';
import { hintOf, isChanged, isMissing, isOffline, messageOf, OFFLINE, sayError, shaOf, statusOf } from './errors';

describe('failed calls', () => {
  it('reads the status from the error, its response or its message', () => {
    expect(statusOf(Object.assign(new Error('x'), { status: 404 }))).toBe(404);
    expect(statusOf({ response: { status: 503 } })).toBe(503);
    expect(statusOf(new Error('PUT failed: 409 Conflict'))).toBe(409);
    expect(statusOf(new Error('no'))).toBe(0);
  });

  it('knows a changed file and its sha', () => {
    const e = { changed: true, sha256: 'abc' };
    expect(isChanged(e)).toBe(true);
    expect(shaOf(e)).toBe('abc');
    expect(isChanged({ error: 'changed' })).toBe(true);
    expect(isChanged(new Error('boom'))).toBe(false);
  });

  it('knows a missing file and an offline computer', () => {
    expect(isMissing({ status: 404 })).toBe(true);
    expect(isMissing(new Error('not found'))).toBe(true);
    expect(isOffline({ status: 503, message: 'x' })).toBe(true);
    expect(isOffline(new Error('editing machine is offline'))).toBe(true);
    expect(sayError({ status: 503 })).toBe(OFFLINE);
    expect(sayError(new Error('disk full'))).toBe('disk full');
  });

  it("prefers a job's hint", () => {
    expect(hintOf({ hint: 'Set up [transcribe] in config.toml', message: 'failed' })).toBe(
      'Set up [transcribe] in config.toml',
    );
    expect(hintOf({ job: { hint: 'h' } })).toBe('h');
    expect(hintOf(new Error('no words'))).toBe('no words');
    expect(messageOf('plain')).toBe('plain');
  });
});
