#!/usr/bin/env bash
# Regenerates the sample's synthetic speech with eSpeak NG (formant synthesis: no recording of any person's voice) and
# transcribes it with retake's own transcriber. The outputs are committed, so using the sample needs neither tool;
# run this only to change the script. See README.md.
#
# usage: examples/sample/make-voice.sh [path/to/espeak-ng] [path/to/retake]
# needs: espeak-ng (1.52.0 was used), ffmpeg with libopus, a retake with transcription set up (whisper.cpp)
# env:   VOICE_MODEL  the whisper model for the word times (default large-v3)
#
# Writes voice/vo.ogg, voice/vo.words.json and voice/phrases.json (the voice-over), and take/seg_01.webm and
# take/transcript.json (the pre-made review take's speech).
set -euo pipefail
cd "$(dirname "$0")"
ESPEAK="${1:-${ESPEAK_NG:-espeak-ng}}"
RETAKE="${2:-retake}"
MODEL="${VOICE_MODEL:-large-v3}"
command -v "$ESPEAK" >/dev/null 2>&1 || { echo "make-voice: espeak-ng not found (pass its path)" >&2; exit 1; }
command -v "$RETAKE" >/dev/null 2>&1 || { echo "make-voice: retake not found (pass its path)" >&2; exit 1; }
RATE=150 # words per minute
GAP=2    # extra pause between words, in 10 ms units
SR=48000
work="$(mktemp -d "${TMPDIR:-/tmp}/retake-sample-voice.XXXXXX")"
trap 'rm -rf "$work"' EXIT

# silence SECONDS: raw s16le mono at $SR.
silence() { head -c "$(awk -v s="$1" -v r="$SR" 'BEGIN { printf "%d", 2 * int(s * r + 0.5) }')" /dev/zero; }
# speak VOICE TEXT: raw s16le mono at $SR.
speak() {
  "$ESPEAK" -v "$1" -s "$RATE" -g "$GAP" -w "$work/p.wav" "$2"
  ffmpeg -nostdin -v error -y -i "$work/p.wav" -ac 1 -ar "$SR" -f s16le -
}
# pos FILE: seconds of audio in a raw file so far.
pos() { awk -v b="$(stat -c %s "$1")" -v r="$SR" 'BEGIN { printf "%.3f", b / 2 / r }'; }

# The voice-over: one phrase per spine item of timeline.json, 0.6 s apart.
phrases=(
  "a|How to brew pour-over coffee, in a few simple steps."
  "b|Step one. Rinse the paper filter with hot water."
  "c|Step two. Add twenty grams of ground coffee."
  "d|Step three. Pour the water slowly, in small circles."
  "e|Wait a few minutes, and enjoy your coffee."
)
raw="$work/vo.raw"
silence 0.5 >"$raw"
json="["
for p in "${phrases[@]}"; do
  id="${p%%|*}"
  text="${p#*|}"
  start="$(pos "$raw")"
  speak en-us+m3 "$text" >>"$raw"
  end="$(pos "$raw")"
  json+="{\"id\": \"$id\", \"text\": \"$text\", \"start\": $start, \"end\": $end},"
  silence 0.6 >>"$raw"
done
mkdir -p voice take
ffmpeg -nostdin -v error -y -f s16le -ar "$SR" -ac 1 -i "$raw" -c:a libopus -b:a 48k -map_metadata -1 \
  -fflags +bitexact voice/vo.ogg
node -e 'require("fs").writeFileSync("voice/phrases.json", JSON.stringify(JSON.parse(process.argv[1]), null, 1) + "\n")' \
  "${json%,}]"

# Word times for the anchors, in the transcript.json format: {"files": {"vo.ogg": {model, duration, words}}}.
"$RETAKE" transcribe voice/vo.ogg --model "$MODEL" --json >"$work/words.json"
node -e '
const fs = require("fs");
const t = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
const words = t.words.map(w => ({ w: w.w, s: w.s, e: w.e, p: w.p }));
const out = { files: { "vo.ogg": { model: t.model.replace(/ \((cuda|cpu)\)$/, ""), duration: t.duration, words } } };
fs.writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + "\n");
' "$work/words.json" voice/vo.words.json

# The pre-made take's one segment, as a browser records it: Opus in WebM at 32 kb/s.
raw="$work/take.raw"
silence 0.4 >"$raw"
speak en-us+f3 "The labels at the bottom are too small. Make them bigger, please." >>"$raw"
silence 0.6 >>"$raw"
ffmpeg -nostdin -v error -y -f s16le -ar "$SR" -ac 1 -i "$raw" -c:a libopus -b:a 32k -f webm -map_metadata -1 \
  -fflags +bitexact take/seg_01.webm
# Its transcript, so `retake notes sync` has words even where no transcriber is set up. (Where one is, and its model
# differs from this one, the take is transcribed again.)
"$RETAKE" transcribe take --model base --force
echo "make-voice: wrote voice/vo.ogg ($(pos "$work/vo.raw") s), voice/vo.words.json, voice/phrases.json, take/seg_01.webm"
