#!/usr/bin/env python3
"""A [transcribe] command engine for retake that runs faster-whisper (https://github.com/SYSTRAN/faster-whisper).

retake runs the command with the path of a 16 kHz mono WAV as its last argument, and reads the words it prints:
    {"model": "...", "words": [{"w": " word", "s": 0.12, "e": 0.4, "p": 0.98}, ...]}
with times in seconds from the start of the file, and each word keeping whisper's leading space. retake sets
RETAKE_PROMPT (the initial prompt) and RETAKE_LANGUAGE ("" = detect) in the environment.

Setup, in a virtualenv of your choice:
    python3 -m venv /path/to/fw-venv
    /path/to/fw-venv/bin/pip install faster-whisper
and in ~/.config/retake/config.toml:
    [transcribe]
    engine = "command"
    command = "/path/to/fw-venv/bin/python /path/to/retake/scripts/faster-whisper-words.py --model large-v3"

Models are downloaded by faster-whisper on first use (into the Hugging Face cache). For CUDA, faster-whisper needs
cuBLAS and cuDNN; see its README.
"""

import argparse
import json
import os
import sys
import wave


def load_audio(path: str):
    """retake passes a 16 kHz mono 16-bit WAV: read it with the standard library, so decoding doesn't depend on
    PyAV. Anything else is left to faster-whisper."""
    import numpy as np  # a faster-whisper dependency

    try:
        with wave.open(path, "rb") as w:
            if w.getframerate() == 16000 and w.getnchannels() == 1 and w.getsampwidth() == 2:
                return np.frombuffer(w.readframes(w.getnframes()), np.int16).astype(np.float32) / 32768.0
    except (wave.Error, EOFError):
        pass
    return path


def main() -> int:
    ap = argparse.ArgumentParser(description="Transcribe one audio file to timed words (retake's command engine).")
    ap.add_argument("audio", help="the audio file (retake passes a 16 kHz mono WAV)")
    ap.add_argument("--model", default=None, help="model name or path (default: large-v3 on CUDA, small on the CPU)")
    ap.add_argument("--device", default="auto", choices=["auto", "cuda", "cpu"])
    ap.add_argument("--compute-type", default=None, help="default: float16 on CUDA, int8 on the CPU")
    ap.add_argument("--beam-size", type=int, default=5)
    ap.add_argument("--threads", type=int, default=0, help="CPU threads (0 = faster-whisper's default)")
    a = ap.parse_args()

    try:
        from faster_whisper import WhisperModel
    except ImportError:
        print("faster-whisper-words: the faster_whisper package isn't installed (pip install faster-whisper)",
              file=sys.stderr)
        return 2

    device = a.device
    if device == "auto":
        try:
            import ctranslate2
            device = "cuda" if ctranslate2.get_cuda_device_count() > 0 else "cpu"
        except Exception:  # noqa: BLE001 - no usable CUDA build: the CPU it is
            device = "cpu"
    name = a.model or ("large-v3" if device == "cuda" else "small")
    compute = a.compute_type or ("float16" if device == "cuda" else "int8")
    model = WhisperModel(name, device=device, compute_type=compute, cpu_threads=a.threads)

    # faster-whisper's own Silero VAD keeps silence from turning into words; each remark is decoded on its own.
    segments, _ = model.transcribe(
        load_audio(a.audio),
        language=os.environ.get("RETAKE_LANGUAGE") or None,
        initial_prompt=os.environ.get("RETAKE_PROMPT") or None,
        beam_size=a.beam_size,
        word_timestamps=True,
        vad_filter=True,
        condition_on_previous_text=False,
    )
    words = []
    for seg in segments:
        for w in seg.words or []:
            words.append({"w": w.word, "s": round(w.start, 3), "e": round(w.end, 3), "p": round(w.probability, 3)})
    json.dump({"model": f"faster-whisper {name} ({device})", "words": words}, sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
