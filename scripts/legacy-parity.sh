#!/usr/bin/env bash
# Parity proof against the legacy player. The legacy headless e2e driver (tests/e2e_headless.mjs, copied with only
# its devtools port changed) drives the legacy page (index.html, served read-only with --ui-dir) on a retake server.
# Its report and the saved take are then checked like the legacy tests/check_e2e.py does, minus the transcription
# and notes checks.
#
# With --compare-legacy, the same driver also runs against the legacy Python server on a copy of the same fixtures,
# and the two saved takes (and /api/info) are diffed structurally.
#
# usage: scripts/legacy-parity.sh [--compare-legacy] [--keep]
# env:   LEGACY_REVIEW_DIR  the legacy review/ folder (default: ~/deadlock-editing-style/skill/scripts/review)
#        CHROMIUM_PATH      headless chromium (default: /usr/lib/chromium/chromium)
#        RETAKE_BIN         retake binary to test (default: built from this checkout)
#        PARITY_WORK        scratch folder (default: .retake-work/parity), deleted after a passing run
# Skips (exit 0) when the legacy files or tools aren't there. Ports: HTTP 8811 (retake), 8812 (legacy), devtools 9311.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LEGACY="${LEGACY_REVIEW_DIR:-$HOME/deadlock-editing-style/skill/scripts/review}"
CHROME="${CHROMIUM_PATH:-/usr/lib/chromium/chromium}"
WORK="${PARITY_WORK:-$ROOT/.retake-work/parity}"
PORT=8811
LEGACY_PORT=8812
DEVTOOLS_PORT=9311
COMPARE=0
KEEP=0
for arg in "$@"; do
  case "$arg" in
    --compare-legacy) COMPARE=1 ;;
    --keep) KEEP=1 ;;
    -h | --help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "legacy-parity: unknown option $arg" >&2; exit 2 ;;
  esac
done

skip() { echo "legacy-parity: SKIP: $*"; exit 0; }
for f in index.html tests/e2e_headless.mjs; do
  [ -f "$LEGACY/$f" ] || skip "legacy player not found ($LEGACY/$f); set LEGACY_REVIEW_DIR"
done
[ -x "$CHROME" ] || skip "no chromium at $CHROME; set CHROMIUM_PATH"
for b in ffmpeg ffprobe curl; do command -v "$b" >/dev/null || skip "$b is not installed"; done
if [ "$COMPARE" = 1 ] && [ ! -x "$LEGACY/review" ]; then skip "no legacy launcher at $LEGACY/review"; fi

# The pinned toolchain (mise.toml) when mise is around.
tool() { if command -v mise >/dev/null; then (cd "$ROOT" && mise exec -- "$@"); else "$@"; fi; }
tool node -e 'if (typeof WebSocket !== "function") process.exit(1)' 2>/dev/null || skip "node 22+ is needed (global WebSocket)"

PIDS=()
cleanup() {
  for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${PIDS[@]}"; do wait "$pid" 2>/dev/null || true; done
  # The driver kills its browser; make sure, by its unique devtools port.
  pkill -KILL -f -- "--remote-debugging-port=$DEVTOOLS_PORT" 2>/dev/null || true
  rm -rf "$WORK"/e2e-*/chrome-profile
}
trap cleanup EXIT

log() { printf '\n== %s\n' "$*"; }

rm -rf "$WORK"
mkdir -p "$WORK"
FIX="$WORK/fixtures"

# --- 1. fixtures: a v5/v6/v7 family like the legacy selftest.sh (generated, no real footage)
log "fixtures in $FIX"
mkdir -p "$FIX/v5" "$FIX/v6" "$FIX/v7"
FONT=""
FILTERS="$(ffmpeg -hide_banner -filters 2>/dev/null || true)" # not piped into grep -q: SIGPIPE + pipefail
if command -v fc-match >/dev/null && [[ "$FILTERS" == *" drawtext "* ]]; then
  FONT="$(fc-match -f '%{file}' 'sans:bold' 2>/dev/null || true)"
fi
ENC=(-c:v libx264 -preset veryfast -crf 22 -g 30 -pix_fmt yuv420p -threads 6 -c:a aac -movflags +faststart)
video() { # out seconds filter label tone_hz
  local vf="$3"
  if [ -n "$FONT" ]; then
    vf+=",drawtext=fontfile=$FONT:fontsize=64:fontcolor=white:box=1:boxcolor=black@0.5:boxborderw=14:x=w-tw-40:y=40:text=$4"
  fi
  nice -n 19 ffmpeg -nostdin -v error -y -f lavfi -i "testsrc2=s=1920x1080:r=30:d=$2" \
    -f lavfi -i "sine=f=$5:sample_rate=48000:d=$2" -vf "$vf" "${ENC[@]}" -shortest "$1"
}
video "$FIX/v6/clip_v6.mp4" 40 "null" "v6" 440
video "$FIX/v5/clip_v5.mp4" 40 "hue=h=40:s=0.6" "v5" 330
video "$FIX/v5/clip_v5_draft.mp4" 6 "null" "v5 draft" 550 # newer than clip_v5.mp4, and still loses
video "$FIX/v7/clip_v7_0-010_preview.mp4" 10 "null" "v7 preview" 660
[ -n "$FONT" ] && echo "labelled with drawtext ($FONT)" || echo "drawtext unavailable: unlabelled"

# --- 2. fake mic: 40 s, 48 kHz mono, tone bursts on the selftest's schedule. 9.5 s falls in the unrecorded loop.
SPEECH="$WORK/speech.wav"
STARTS=(1.0 3.3 9.5 13.0 19.0 22.5)
LENS=(1.4 4.2 1.45 1.8 1.45 1.9)
TONES=(220 247 262 196 233 208)
inputs=()
graph=""
mix=""
for i in "${!STARTS[@]}"; do
  inputs+=(-f lavfi -i "sine=f=${TONES[$i]}:sample_rate=48000:d=${LENS[$i]}")
  ms=$(awk -v t="${STARTS[$i]}" 'BEGIN { printf "%d", t * 1000 + 0.5 }')
  graph+="[$i:a]tremolo=f=5:d=0.6,volume=0.6,adelay=$ms:all=1[s$i];"
  mix+="[s$i]"
done
nice -n 19 ffmpeg -nostdin -v error -y "${inputs[@]}" \
  -filter_complex "${graph}${mix}amix=inputs=${#STARTS[@]}:normalize=0,apad=whole_dur=40[out]" \
  -map "[out]" -ac 1 -ar 48000 -c:a pcm_s16le -t 40 "$SPEECH"

if [ "$COMPARE" = 1 ]; then # a pristine copy for the legacy server, mtimes kept (the draft decoy is the newer file)
  cp -a "$FIX" "$WORK/fixtures-legacy"
fi

# --- 3. the driver: the legacy file, only the devtools port changed (9339 belongs to another worker)
DRIVER="$WORK/e2e_headless.mjs"
sed "s/^const PORT = 9339;$/const PORT = $DEVTOOLS_PORT;/" "$LEGACY/tests/e2e_headless.mjs" > "$DRIVER"
if [ "$(diff "$LEGACY/tests/e2e_headless.mjs" "$DRIVER" | grep -c '^[<>]')" != 2 ] || ! grep -q "^const PORT = $DEVTOOLS_PORT;$" "$DRIVER"; then
  echo "legacy-parity: the driver's devtools port line changed; update this script" >&2
  exit 1
fi

wait_up() { # port pid log
  for _ in $(seq 100); do
    curl -sf "http://127.0.0.1:$1/api/ping" >/dev/null && return 0
    kill -0 "$2" 2>/dev/null || { cat "$3" >&2; return 1; }
    sleep 0.1
  done
  cat "$3" >&2
  return 1
}

drive() { # url outdir
  log "driver -> $1"
  if (ulimit -c 0 && cd "$ROOT" && tool nice -n 19 node "$DRIVER" "$1" "$SPEECH" "$2" "$CHROME") > "$2.log" 2>&1; then
    echo "driver finished"
  else
    echo "driver FAILED (see $2.log, $2/e2e_report.json)"
  fi
  rm -rf "$2/chrome-profile"
}

# --- 4. retake serving the legacy page
if [ -n "${RETAKE_BIN:-}" ]; then
  BIN="$RETAKE_BIN"
else
  BIN="$WORK/retake"
  log "building retake"
  tool go build -o "$BIN" ./cmd/retake
fi
log "retake on :$PORT with --ui-dir $LEGACY"
mkdir -p "$WORK/config/retake"
printf 'name = "Parity"\n' > "$WORK/config/retake/config.toml" # isolated from the user's config
XDG_CONFIG_HOME="$WORK/config" "$BIN" open "$FIX/v6/clip_v6.mp4" --no-open --port "$PORT" --idle-exit 0 \
  --ui-dir "$LEGACY" > "$WORK/retake-server.log" 2>&1 &
PIDS+=($!)
wait_up "$PORT" "${PIDS[-1]}" "$WORK/retake-server.log"
cat "$WORK/retake-server.log"
curl -sf "http://127.0.0.1:$PORT/api/info" > "$WORK/info-retake.json"
mkdir -p "$WORK/e2e-retake"
drive "http://127.0.0.1:$PORT/" "$WORK/e2e-retake"
kill "${PIDS[-1]}" && wait "${PIDS[-1]}" 2>/dev/null || true

log "checks: retake"
status=0
tool node "$ROOT/scripts/legacy-parity-check.mjs" "$FIX" "$WORK/e2e-retake" --author Parity || status=1

# --- 5. optional: the legacy Python server on the same fixtures, then a structural diff of the takes
if [ "$COMPARE" = 1 ]; then
  FIXL="$WORK/fixtures-legacy"
  log "legacy server on :$LEGACY_PORT"
  PYTHONDONTWRITEBYTECODE=1 "$LEGACY/review" "$FIXL/v6/clip_v6.mp4" --no-open --port "$LEGACY_PORT" --idle-exit 0 \
    > "$WORK/legacy-server.log" 2>&1 &
  PIDS+=($!)
  wait_up "$LEGACY_PORT" "${PIDS[-1]}" "$WORK/legacy-server.log"
  cat "$WORK/legacy-server.log"
  curl -sf "http://127.0.0.1:$LEGACY_PORT/api/info" > "$WORK/info-legacy.json"
  mkdir -p "$WORK/e2e-legacy"
  drive "http://127.0.0.1:$LEGACY_PORT/" "$WORK/e2e-legacy"
  kill "${PIDS[-1]}" && wait "${PIDS[-1]}" 2>/dev/null || true

  log "checks: legacy (same assertions, as a control)"
  tool node "$ROOT/scripts/legacy-parity-check.mjs" "$FIXL" "$WORK/e2e-legacy" || status=1

  log "diff: retake vs legacy"
  tool node "$ROOT/scripts/legacy-parity-diff.mjs" info "$WORK/info-retake.json" "$WORK/info-legacy.json" "$FIX" "$FIXL" || status=1
  TR="$(ls -d "$FIX"/v6/clip_v6.review/20* 2>/dev/null | head -1)"
  TL="$(ls -d "$FIXL"/v6/clip_v6.review/20* 2>/dev/null | head -1)"
  if [ -n "$TR" ] && [ -n "$TL" ]; then
    tool node "$ROOT/scripts/legacy-parity-diff.mjs" takes "$TR" "$TL" || status=1
  else
    echo "  DIFF  a take is missing (retake: ${TR:-none}, legacy: ${TL:-none})"
    status=1
  fi
fi

if [ "$status" = 0 ]; then
  log "legacy parity: PASS"
  [ "$KEEP" = 1 ] && echo "kept $WORK" || rm -rf "$WORK"
else
  log "legacy parity: FAIL (scratch kept in $WORK)"
fi
exit "$status"
