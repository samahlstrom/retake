import { describe, expect, it } from 'vitest';
import { extFor, pickMime, recorderMime } from './mic';

/** isTypeSupported of a browser that records exactly these types. */
const supports = (types: string[]) => (m: string) => types.includes(m);

describe('recorder type', () => {
  it('picks Opus in webm, then ogg, then webm, then mp4', () => {
    const chrome = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4'];
    expect(pickMime(supports(chrome))).toBe('audio/webm;codecs=opus');
    expect(pickMime(supports(['audio/ogg;codecs=opus', 'audio/webm']))).toBe('audio/ogg;codecs=opus'); // Firefox
    expect(pickMime(supports(['audio/webm']))).toBe('audio/webm');
  });

  it('picks AAC in mp4 on Safari (iOS records nothing else)', () => {
    expect(pickMime(supports(['audio/mp4;codecs=mp4a.40.2', 'audio/mp4']))).toBe('audio/mp4;codecs=mp4a.40.2');
    expect(pickMime(supports(['audio/mp4']))).toBe('audio/mp4');
  });

  it('leaves the choice to the browser when it supports none of them', () => {
    expect(pickMime(supports([]))).toBe('');
  });

  it("uses the recorder's own type, else the one asked for, else webm", () => {
    expect(recorderMime('audio/mp4; codecs=mp4a.40.2', 'audio/mp4')).toBe('audio/mp4; codecs=mp4a.40.2');
    expect(recorderMime('', 'audio/mp4')).toBe('audio/mp4'); // not started yet
    expect(recorderMime('', '')).toBe('audio/webm');
  });
});

describe('segment file extension', () => {
  it('saves mp4 audio as .m4a, ogg as .ogg, anything else as .webm', () => {
    expect(extFor('audio/mp4')).toBe('m4a');
    expect(extFor('audio/mp4;codecs=mp4a.40.2')).toBe('m4a');
    expect(extFor('audio/mp4; codecs="mp4a.40.2"')).toBe('m4a');
    expect(extFor('audio/ogg;codecs=opus')).toBe('ogg');
    expect(extFor('audio/webm;codecs=opus')).toBe('webm');
    expect(extFor('audio/webm')).toBe('webm');
    expect(extFor('')).toBe('webm');
  });

  it('only ever names files the server accepts', () => {
    const name = /^seg_\d{2,3}\.(webm|ogg|m4a)$/;
    for (const m of ['audio/mp4', 'audio/ogg', 'audio/webm', 'video/webm', 'audio/wav', ''])
      expect(`seg_01.${extFor(m)}`).toMatch(name);
  });
});
