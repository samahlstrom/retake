#!/usr/bin/env bash
# Regenerates the golden notes of every case in this folder by running the LEGACY Python engine (`review notes`) on a
# scratch copy of the case. The Go tests (golden_test.go) compare internal/notes against these files.
#
# Per case <name>/ (case.json says the target, the extra arguments and an optional generated video):
#   golden/notes.md, golden/notes.json   the legacy output, with the scratch root replaced by @ROOT@
#   golden/snaps.txt                      "<snapshot path> <width>x<height>" per snapshot
#
# The fixture transcripts match legacy's cache rule for `--cpu` (model "whisper small (cpu)", prompt null, the
# segment file's size), so legacy never transcribes: the seg_*.webm files are placeholders, not audio.
#
# usage: internal/notes/testdata/regen-goldens.sh [case ...]
# env:   LEGACY_REVIEW_DIR  the legacy review/ folder (required: there is no default)
#        REVIEW_PY          the legacy venv's python (required)
#        REGEN_WORK         scratch folder (default: <repo>/.retake-work/notes-goldens), deleted afterwards
# Skips (exit 0) when the legacy engine isn't installed. Needs ffmpeg for cases with a video.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO="$(cd "$HERE/../../.." && pwd -P)"
LEGACY="${LEGACY_REVIEW_DIR:-}"
PY="${REVIEW_PY:-}"
WORK="${REGEN_WORK:-$REPO/.retake-work/notes-goldens}"

skip() { echo "regen-goldens: SKIP: $*"; exit 0; }
[ -n "$LEGACY" ] || skip "LEGACY_REVIEW_DIR is not set (the legacy review/ folder)"
[ -n "$PY" ] || skip "REVIEW_PY is not set (the legacy venv's python)"
[ -x "$LEGACY/review" ] && [ -f "$LEGACY/notes.py" ] || skip "legacy engine not found at $LEGACY; set LEGACY_REVIEW_DIR"
[ -x "$PY" ] || skip "legacy venv python not found at $PY; set REVIEW_PY"

# Legacy is read-only: never write its bytecode next to it.
export PYTHONDONTWRITEBYTECODE=1 REVIEW_PY="$PY"

cases=("$@")
if [ ${#cases[@]} -eq 0 ]; then
  for d in "$HERE"/*/; do [ -f "$d/case.json" ] && cases+=("$(basename "$d")"); done
fi

rm -rf "$WORK"
mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT

field() { "$PY" -c 'import json,sys; c=json.load(open(sys.argv[1])); v=eval(sys.argv[2], {}, {"c": c}); print("" if v is None else v)' "$@"; }

for name in "${cases[@]}"; do
  src="$HERE/$name"
  [ -f "$src/case.json" ] || { echo "regen-goldens: no case $name" >&2; exit 1; }
  work="$WORK/$name"
  mkdir -p "$work"
  (cd "$src" && tar --exclude=./golden -cf - .) | (cd "$work" && tar -xf -)
  root="$(cd "$work" && pwd -P)"
  # Fixtures name absolute paths (inherit.json dirs, session video paths) as @ROOT@.
  find "$root" -name '*.json' -exec sed -i "s|@ROOT@|$root|g" {} +

  vfile="$(field "$src/case.json" '(c.get("video") or {}).get("file")')"
  if [ -n "$vfile" ]; then
    command -v ffmpeg >/dev/null || { echo "regen-goldens: $name needs ffmpeg" >&2; exit 1; }
    size="$(field "$src/case.json" 'c["video"]["size"]')"
    fps="$(field "$src/case.json" 'c["video"]["fps"]')"
    secs="$(field "$src/case.json" 'c["video"]["seconds"]')"
    nice -n 19 ffmpeg -nostdin -v error -y -threads 2 -f lavfi -i "testsrc2=s=$size:r=$fps:d=$secs" \
      -c:v libx264 -preset ultrafast -pix_fmt yuv420p -an -movflags +faststart "$root/$vfile"
  fi

  target="$root/$(field "$src/case.json" 'c["target"]')"
  mapfile -t args < <(field "$src/case.json" '"\n".join(c["args"])' | sed '/^$/d')
  for i in "${!args[@]}"; do # file arguments are relative to the case folder
    if [ "${args[$i]}" = "--video-transcript" ]; then args[i+1]="$root/${args[i+1]}"; fi
  done

  echo "== $name"
  (cd "$root" && nice -n 19 "$LEGACY/review" notes "$target" --cpu "${args[@]}")

  if [ -f "$target/session.json" ]; then
    out="$target"
  elif [ -f "$target" ]; then
    base="$(basename "$target")"
    out="$(dirname "$target")/${base%.*}.review"
  else
    out="$target"
  fi
  mkdir -p "$src/golden"
  sed "s|$root|@ROOT@|g" "$out/notes.md" >"$src/golden/notes.md"
  sed -e "s|$root|@ROOT@|g" -e 's|^ "generated": "[^"]*",$| "generated": "(ignored by the tests)",|' \
    "$out/notes.json" >"$src/golden/notes.json"
  "$PY" - "$out" >"$src/golden/snaps.txt" <<'EOF'
import sys
from pathlib import Path
from PIL import Image
out = Path(sys.argv[1])
for p in sorted((out / "snaps").glob("*.png")) if (out / "snaps").is_dir() else []:
    with Image.open(p) as im:
        print(f"snaps/{p.name} {im.width}x{im.height}")
EOF
done
echo "regen-goldens: wrote ${#cases[@]} golden sets"
