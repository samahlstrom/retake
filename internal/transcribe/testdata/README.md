# Test speech

The audio here is synthetic. No person's voice or recording is in it.

| File | Says | Voice |
|---|---|---|
| `seg_01.webm` | "Throw out everything I said about this clip." | `en-us+m3` |
| `seg_02.ogg` | "Actually, add that back." | `en-us+klatt4` |
| `gaps.webm` | "Cut the intro and zoom in on the map.", 3 s of silence, then "Make the caption bigger here." | `en-us+m3` |

`speech.json` gives each phrase's start and end in seconds. Each file is the browser's format: Opus at 32 kb/s, in
WebM or Ogg.

## How it was made

- **Tool.** [eSpeak NG](https://github.com/espeak-ng/espeak-ng) 1.52.0, built from source. It's a formant
  synthesizer: the voice is computed from rules. It isn't a model trained on a person's recordings, and no MBROLA
  voices were used.
- **Settings.** 145 words per minute, with a 20 ms gap between words (`-s 145 -g 2`). The voice variants
  (`en-us+m3`, `en-us+klatt4`) were picked so that whisper's tiny and base models both get every word right after Opus
  encoding.
- **Regenerating.** Run `gen-speech.sh [path/to/espeak-ng]`. It needs ffmpeg with libopus, and python3. The output
  is byte for byte the same for the same eSpeak NG and ffmpeg versions.

## License

eSpeak NG is GPL-3.0-or-later. The GPL covers the program, not its output: the generated audio contains none of the
program's code or data, only sound computed from the text above. These recordings are dedicated to the public
domain under [CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/).

## Other fixtures

- `whisper-cli-gaps.json` is whisper-cli's output (`-ojf -ml 1 -sow --vad`, model tiny) for `gaps.webm`.
  `whisper-cli-gaps.log` holds the VAD table lines from its log. The machine-specific system info and model path were
  replaced.
