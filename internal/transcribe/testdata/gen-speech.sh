#!/usr/bin/env bash
# Regenerates the test speech in this folder with espeak-ng (formant synthesis: no recording of any person's voice).
# The files are committed, so tests don't need espeak-ng; run this only to change them. See README.md.
#
# usage: internal/transcribe/testdata/gen-speech.sh [path/to/espeak-ng]
# needs: espeak-ng (1.52.0 was used), ffmpeg with libopus, python3
set -euo pipefail
cd "$(dirname "$0")"
ESPEAK="${1:-${ESPEAK_NG:-espeak-ng}}"
command -v "$ESPEAK" >/dev/null 2>&1 || { echo "gen-speech: espeak-ng not found (pass its path)" >&2; exit 1; }
RATE=145 # words per minute
GAP=2    # extra pause between words, in 10 ms units
SR=16000
work="$(mktemp -d "${TMPDIR:-/tmp}/retake-gen-speech.XXXXXX")"
trap 'rm -rf "$work"' EXIT

# One file per line: name, espeak-ng voice, then silence (seconds) and text alternately, separated by |. The voices
# are formant variants, picked so that whisper's tiny and base models both get every word after Opus encoding.
specs=(
  "seg_01.webm|en-us+m3|0.6|Throw out everything I said about this clip.|0.6"
  "seg_02.ogg|en-us+klatt4|0.6|Actually, add that back.|0.6"
  "gaps.webm|en-us+m3|1.0|Cut the intro and zoom in on the map.|3.0|Make the caption bigger here.|1.0"
)

json="["
for spec in "${specs[@]}"; do
  IFS='|' read -r -a parts <<<"$spec"
  name="${parts[0]}"
  voice="${parts[1]}"
  raw="$work/$name.raw"
  : >"$raw"
  phrases=""
  for ((i = 2; i < ${#parts[@]}; i++)); do
    p="${parts[$i]}"
    if ((i % 2 == 0)); then # silence
      python3 -c "import sys; sys.stdout.buffer.write(bytes(2 * round($p * $SR)))" >>"$raw"
    else # speech
      "$ESPEAK" -v "$voice" -s "$RATE" -g "$GAP" -w "$work/p.wav" "$p"
      start=$(stat -c %s "$raw")
      ffmpeg -nostdin -v error -y -i "$work/p.wav" -ac 1 -ar "$SR" -f s16le - >>"$raw"
      end=$(stat -c %s "$raw")
      phrases+="$(python3 -c "import json,sys; print(json.dumps({'text': sys.argv[1], 'start': round($start / 2 / $SR, 3), 'end': round($end / 2 / $SR, 3)}))" "$p"),"
    fi
  done
  case "$name" in
    *.webm) codec=(-c:a libopus -b:a 32k -f webm) ;;
    *.ogg) codec=(-c:a libopus -b:a 32k -f ogg) ;;
  esac
  ffmpeg -nostdin -v error -y -f s16le -ar "$SR" -ac 1 -i "$raw" "${codec[@]}" -map_metadata -1 -fflags +bitexact "$name"
  json+="{\"file\": \"$name\", \"voice\": \"$voice\", \"phrases\": [${phrases%,}]},"
done
python3 -c "import json,sys; print(json.dumps(json.loads(sys.argv[1]), indent=1))" "${json%,}]" >speech.json
echo "gen-speech: wrote ${#specs[@]} files and speech.json"
