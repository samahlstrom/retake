// The microphone: opened on the first R (that is when the browser asks for permission), closed on Done.

export interface Mic {
  stream: MediaStream;
  ac: AudioContext;
  an: AnalyserNode;
  buf: Float32Array<ArrayBuffer>;
}

export const MIC_BLOCKED = 'Microphone blocked. Allow it in the address bar, then press R again.';
/** The same, for the phone layout (no R key there). */
export const MIC_BLOCKED_TOUCH = 'Microphone blocked. Allow it in the address bar, then tap Record again.';

/** Ask for the mic and hang an analyser on it (for the VAD and the waveform). Throws when it is blocked. */
export async function openMic(): Promise<Mic> {
  // The AudioContext comes first, while the R press or the tap is still the current user gesture: iOS Safari only lets
  // a context start (resume) inside one, and the analyser would otherwise read silence.
  const ac = new AudioContext();
  void ac.resume();
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
    });
  } catch (err) {
    void ac.close();
    throw err;
  }
  const an = ac.createAnalyser();
  an.fftSize = 1024;
  ac.createMediaStreamSource(stream).connect(an);
  void ac.resume();
  return { stream, ac, an, buf: new Float32Array(an.fftSize) };
}

export function closeMic(mic: Mic): void {
  mic.stream.getTracks().forEach((tr) => tr.stop());
  void mic.ac.close();
}

/**
 * Opus first: webm, then ogg, then whatever webm the browser has. Then AAC in mp4, the only thing Safari (iOS
 * included) records. '' = let MediaRecorder pick.
 */
export const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/ogg;codecs=opus',
  'audio/webm',
  'audio/mp4;codecs=mp4a.40.2',
  'audio/mp4',
];

export const pickMime = (isTypeSupported: (m: string) => boolean): string =>
  MIME_CANDIDATES.find((m) => isTypeSupported(m)) || '';

/** The segment file extension for a recorder mime type (mp4 audio is saved as .m4a). */
export const extFor = (mime: string): 'ogg' | 'm4a' | 'webm' =>
  /ogg/.test(mime) ? 'ogg' : /mp4/.test(mime) ? 'm4a' : 'webm';

/**
 * A segment's mime type: what the recorder reports, else what was asked for, else webm. A recorder made without a
 * type may report '' until it has started (the browser picks then), so this is asked again once it runs.
 */
export const recorderMime = (reported: string, picked: string): string => reported || picked || 'audio/webm';
