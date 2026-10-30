// The microphone: opened on the first R (that is when the browser asks for permission), closed on Done.

export interface Mic {
  stream: MediaStream;
  ac: AudioContext;
  an: AnalyserNode;
  buf: Float32Array<ArrayBuffer>;
}

export const MIC_BLOCKED = 'Microphone blocked. Allow it in the address bar, then press R again.';

/** Ask for the mic and hang an analyser on it (for the VAD and the waveform). Throws when it is blocked. */
export async function openMic(): Promise<Mic> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
  });
  const ac = new AudioContext(),
    an = ac.createAnalyser();
  an.fftSize = 1024;
  ac.createMediaStreamSource(stream).connect(an);
  void ac.resume();
  return { stream, ac, an, buf: new Float32Array(an.fftSize) };
}

export function closeMic(mic: Mic): void {
  mic.stream.getTracks().forEach((tr) => tr.stop());
  void mic.ac.close();
}

/** Opus first: webm, then ogg, then whatever webm the browser has. '' = let MediaRecorder pick. */
export const MIME_CANDIDATES = ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/webm'];

export const pickMime = (isTypeSupported: (m: string) => boolean): string =>
  MIME_CANDIDATES.find((m) => isTypeSupported(m)) || '';

/** The segment file extension for a recorder mime type. */
export const extFor = (mime: string): 'ogg' | 'm4a' | 'webm' =>
  /ogg/.test(mime) ? 'ogg' : /mp4/.test(mime) ? 'm4a' : 'webm';
