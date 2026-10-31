#!/usr/bin/env bash
# The review loop on the demo, end to end (spec/bridge.md), headless and from generated media only:
#
#   1. a timeline project (demo/edit/timeline.json) renders v1 into a version family, with its render manifest;
#   2. the review server starts on a test port (no browser);
#   3. a review take is written by hand: three notes, each with a pen stroke, and synthetic words (no audio);
#   4. `retake notes sync` turns it into notes, and each note gets the timeline elements under its stroke;
#   5. the agent's patches: one answers the note on the picture-in-picture, one trims the VO (its own initiative);
#   6. `retake push --render` renders v2 and pushes it, its changelog written from the patches;
#   7. the server shows v1's notes carried over onto v2, and the time map from v1 to v2.
#
# usage: loop.sh [work dir]      (default <repo>/.retake-work/loop; wiped first, kept afterwards for a look)
# env:   RETAKE (default <repo>/bin/retake), RETAKE_EDIT (default <repo>/editor/target/release/retake-edit),
#        PORT (default 8911), DEMO_SIZE (default 640x360). Needs ffmpeg, curl and jq. The server is always stopped.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
work="$(mkdir -p "${1:-$repo/.retake-work/loop}" && cd "${1:-$repo/.retake-work/loop}" && pwd)"
retake="${RETAKE:-$repo/bin/retake}"
edit="${RETAKE_EDIT:-$repo/editor/target/release/retake-edit}"
port="${PORT:-8911}"
size="${DEMO_SIZE:-640x360}"
for b in "$retake" "$edit"; do
  [[ -x "$b" ]] || { echo "loop.sh: $b is missing (make build; cd editor && cargo build --release)" >&2; exit 2; }
done
ulimit -c 0
say() { printf '\n== %s\n' "$*"; }
fail() { echo "loop.sh: FAILED: $*" >&2; exit 1; }

rm -rf "${work:?}"/*
proj="$work/demo"
mkdir -p "$proj/edit/patches" "$proj/v1" "$work/config/retake"
# No user config leaks in. Transcription is a no-op: the take below brings its transcript (its model is this
# command's name), so nothing is ever transcribed.
export XDG_CONFIG_HOME="$work/config"
cat >"$work/config/retake/config.toml" <<EOF
name = "Demo Reviewer"
[transcribe]
engine = "command"
command = "true"
[edit]
retake_edit = "$edit"
EOF

say "1. timeline project, v1 rendered into the family"
cp "$here/timeline.json" "$here/vo.words.json" "$proj/edit/"
DEMO_SIZE="$size" nice -n 19 bash "$here/make-media.sh" "$proj/edit/media" "$edit" >/dev/null
video="$proj/v1/demo_v1.mp4"
# Drafts (half size, fast encoder) here and in the push below: the same settings, so v2 reuses v1's units.
nice -n 19 "$edit" render "$proj/edit/timeline.json" -o "$video" --draft | sed 's/^/   /'
[[ -f "$proj/v1/demo_v1.manifest.json" ]] || fail "no render manifest beside v1"

say "2. review server on port $port"
"$retake" open "$video" --no-open --port "$port" --idle-exit 0 >"$work/server.log" 2>&1 &
server=$!
cleanup() {
  kill "$server" 2>/dev/null || true
  wait "$server" 2>/dev/null || true
}
trap cleanup EXIT
api="http://127.0.0.1:$port"
for _ in $(seq 100); do curl -fsS "$api/api/ping" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "$api/api/ping" >/dev/null || fail "the server didn't start (see $work/server.log)"
echo "   $api (pid $server)"

say "3. a review take: three notes with strokes, synthetic words"
take="2026-10-30_12-00-00"
td="$proj/v1/demo_v1.review/$take"
mkdir -p "$td"
# One paused segment per note: on the picture-in-picture at 9.0 s, the title at 1.0 s, the music card at 13.0 s.
note_words() { # file seg-index start text...
  local i="$1" s="$2"
  shift 2
  local out="" t="$s" w
  for w in "$@"; do
    out+="{\"w\": \" $w\", \"s\": $t, \"e\": $(awk -v t="$t" 'BEGIN { print t + 0.3 }'), \"p\": 0.95},"
    t="$(awk -v t="$t" 'BEGIN { print t + 0.35 }')"
  done
  printf '"seg_0%s.webm": {"size": 16, "model": "command true", "prompt": null, "duration": 3.5, "words": [%s]}' \
    "$i" "${out%,}"
}
circle() { # cx cy r -> points
  awk -v cx="$1" -v cy="$2" -v r="$3" 'BEGIN { for (i = 0; i <= 16; i++) { a = i * 6.2832 / 16;
    printf "%s[%.4f, %.4f]", (i ? ", " : ""), cx + r * cos(a), cy + r * 1.78 * sin(a) } }'
}
seg() { # n session-start video-t
  printf '{"id": "S%s", "file": "seg_0%s.webm", "mime": "audio/webm;codecs=opus", "version": "v1", "flip": false,
    "t_start": %s, "t_end": %s, "recording": false, "video_start": %s, "video_end": %s, "video_min": %s,
    "video_max": %s}' "$1" "$1" "$2" "$(awk -v t="$2" 'BEGIN { print t + 3.5 }')" "$3" "$3" "$3" "$3"
}
stroke() { # id seg session-start video-t cx cy r
  printf '{"id": %s, "seg": "S%s", "version": "v1", "flip": false, "color": "red", "session_t": %s,
    "session_t_end": %s, "video_t": %s, "video_t_end": %s, "paused": true, "points": [%s]}' "$1" "$2" "$3" \
    "$(awk -v t="$3" 'BEGIN { print t + 0.3 }')" "$4" "$4" "$(circle "$5" "$6" "$7")"
}
ev() { printf '{"session_t": %s, "video_t": %s, "event": "%s"%s}' "$1" "$2" "$3" "${4:-}"; }
cat >"$td/session.json" <<EOF
{"format": 3, "version": "v1", "session": "$take", "created": "2026-10-30T18-00-00Z", "saved": "2026-10-30T18-01-00Z",
 "clock": "session_t", "recorded": 10.5, "closed": true,
 "video": {"name": "demo_v1.mp4", "duration": 16.1, "fps": 30, "width": 960, "height": 540},
 "display": {"show_within": 1.5}, "author": {"name": "Demo Reviewer"}}
EOF
printf '[%s,\n%s,\n%s]\n' "$(seg 1 0.5 9.0)" "$(seg 2 5.0 1.0)" "$(seg 3 9.5 13.0)" >"$td/segments.json"
printf '[%s,\n%s,\n%s]\n' "$(stroke 1 1 0.6 9.0 0.76 0.72 0.06)" "$(stroke 2 2 5.1 1.0 0.5 0.52 0.12)" \
  "$(stroke 3 3 9.6 13.0 0.5 0.44 0.1)" >"$td/strokes.json"
{
  printf '['
  ev 0 9.0 start ', "playing": false, "version": "v1", "base": "v1", "flip": false'
  printf ',\n'; ev 0.5 9.0 rec_start ', "seg": "S1", "playing": false, "version": "v1", "flip": false'
  printf ',\n'; ev 0.6 9.0 draw_start ', "id": 1, "seg": "S1", "color": "red"'
  printf ',\n'; ev 0.9 9.0 draw_end ', "id": 1, "seg": "S1"'
  printf ',\n'; ev 4.0 9.0 rec_stop ', "seg": "S1"'
  printf ',\n'; ev 4.5 1.0 seek ', "from": 9.0'
  printf ',\n'; ev 5.0 1.0 rec_start ', "seg": "S2", "playing": false, "version": "v1", "flip": false'
  printf ',\n'; ev 5.1 1.0 draw_start ', "id": 2, "seg": "S2", "color": "red"'
  printf ',\n'; ev 5.4 1.0 draw_end ', "id": 2, "seg": "S2"'
  printf ',\n'; ev 8.5 1.0 rec_stop ', "seg": "S2"'
  printf ',\n'; ev 9.0 13.0 seek ', "from": 1.0'
  printf ',\n'; ev 9.5 13.0 rec_start ', "seg": "S3", "playing": false, "version": "v1", "flip": false'
  printf ',\n'; ev 9.6 13.0 draw_start ', "id": 3, "seg": "S3", "color": "red"'
  printf ',\n'; ev 9.9 13.0 draw_end ', "id": 3, "seg": "S3"'
  printf ',\n'; ev 13.0 13.0 rec_stop ', "seg": "S3"'
  printf ']\n'
} >"$td/events.json"
printf '{"bin": 0.25, "bins": []}\n' >"$td/voice.json"
printf '{"files": {%s,\n%s,\n%s}}\n' \
  "$(note_words 1 0.1 Make the picture in picture bigger.)" \
  "$(note_words 2 0.1 Love this title, keep it.)" \
  "$(note_words 3 0.1 This card is too dark.)" >"$td/transcript.json"
for i in 1 2 3; do head -c 16 /dev/zero >"$td/seg_0$i.webm"; done # placeholders: the transcript is already there
echo "   $td"

say "4. notes sync: each note gets the elements under its stroke"
"$retake" notes sync "$video" | sed 's/^/   /'
"$retake" notes list "$video" | sed 's/^/   /'
notes="$("$retake" notes list "$video" --json)"
id_of() { jq -r --arg w "$1" '.notes[] | select(.text | contains($w)) | .id' <<<"$notes"; }
pip_note="$(id_of picture)"
title_note="$(id_of title)"
card_note="$(id_of card)"
[[ -n "$pip_note" && -n "$title_note" && -n "$card_note" ]] || fail "expected three notes: $notes"
jq -r '.notes[] | "   #\(.id) targets: \([.targets[:3][] | "\(.id) (\(.score))"] | join(", "))"' <<<"$notes"
top="$(jq -r --argjson id "$pip_note" '.notes[] | select(.id == $id) | .targets[0].id' <<<"$notes")"
[[ "$top" == "pip/v" ]] || fail "note #$pip_note should target pip/v, got $top"
echo "   ok: note #$pip_note (the stroke on the picture-in-picture) targets pip/v"

say "5. the agent's patches"
cat >"$proj/edit/patches/01-bigger-pip.json" <<EOF
{"base": 0, "answers": ["v1#$pip_note"], "summary": "Picture-in-picture 30% -> 40%, its frame to match",
 "ops": [{"op": "set", "id": "pip/v", "path": "transform/scale", "value": 0.4},
         {"op": "set", "id": "pip/frame", "path": "size", "value": [776, 448]}]}
EOF
cat >"$proj/edit/patches/02-trim-vo.json" <<EOF
{"base": 1, "answers": [], "summary": "Trim 0.3 s of air after 'generated.'",
 "ops": [{"op": "set", "id": "a", "path": "out", "value": 3.8}]}
EOF
"$edit" patch apply "$proj/edit/timeline.json" "$proj/edit/patches/01-bigger-pip.json" | sed 's/^/   /'
"$edit" patch apply "$proj/edit/timeline.json" "$proj/edit/patches/02-trim-vo.json" | sed 's/^/   /'

say "6. push v2 with --render: the changelog comes from the patches"
nice -n 19 "$retake" push "$video" --render --no-stream --draft --change "$title_note=Kept as it is" \
  --ask "$card_note=Lighter card, or keep it dark and brighten the text?" | sed 's/^/   /'
[[ -f "$proj/v2/demo_v2.mp4" && -f "$proj/v2/demo_v2.manifest.json" && -f "$proj/v2/demo_v2.push.json" ]] ||
  fail "v2, its manifest and its push record should be in demo/v2/"
"$retake" versions "$video" | sed 's/^/   /'

say "7. carry-over and the time map, from the server"
curl -fsS "$api/api/info" >/dev/null # the server rescans the family
sidebar="$(curl -fsS "$api/api/notes?v=v2")"
jq -r '.items[] | "   \(.kind) #\(.id) at \(.t0) (was \(.carried.t0) on \(.carried.from)): \(.carried.as // "-") - \(.text)"' \
  <<<"$sidebar"
as_of() { jq -r --argjson id "$1" '[.items[] | select(.id == $id or .note == $id)][0].carried.as' <<<"$sidebar"; }
[[ "$(as_of "$pip_note")" == "check" ]] || fail "the answered pip note should be 'check' (answered, check it)"
[[ "$(as_of "$title_note")" == "unchanged" ]] || fail "the title note's unit didn't change: 'unchanged'"
[[ "$(as_of "$card_note")" == "changed" ]] || fail "the music card's unit re-rendered: 'changed'"
map="$(curl -fsS "$api/api/timemap?from=v1&to=v2")"
jq -r '.segments[] | "   v1 \(.a[0])-\(.a[1]) -> v2 \(.b[0])-\(.b[1])  by \(.by)"' <<<"$map"
mapped() { jq -r --argjson t "$1" '[.segments[] | select(.a[0] <= $t and $t < .a[1])][0] as $s
  | $s.b[0] + ($t - $s.a[0]) * ($s.b[1] - $s.b[0]) / ($s.a[1] - $s.a[0]) | . * 1000 | round / 1000' <<<"$map"; }
echo "   v1 1.0 -> v2 $(mapped 1.0)   v1 9.0 -> v2 $(mapped 9.0)   v1 13.0 -> v2 $(mapped 13.0)"
[[ "$(mapped 9.0)" == "8.7" ]] || fail "9.0 s on v1 should be 8.7 s on v2 (0.3 s of VO trimmed before it)"

say "summary"
cat <<EOF
   v1 rendered from rev 0; review take with 3 notes; notes sync resolved #$pip_note -> pip/v
   patches p1 (answers #$pip_note) and p2 (unrequested VO trim); v2 pushed with --render from rev 2
   changelog: $(jq -r '[.versions[] | select(.label == "v2") | .changelog[] | "#\(.note) \(.summary)\(if .patches then " (" + (.patches | join(",")) + ")" else "" end)"] | join("; ")' <<<"$("$retake" versions "$video" --json)")
   carried onto v2: #$pip_note check, #$title_note unchanged, #$card_note changed (asked)
   time map: v1 9.0 s -> v2 $(mapped 9.0) s; C, [ ], chips and "see it in v2" land there
   work dir: $work
EOF
