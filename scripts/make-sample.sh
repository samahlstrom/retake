#!/usr/bin/env bash
# Makes the sample project (examples/sample/, CC0): a short how-to video in two versions, rendered from a timeline,
# ready for `retake open`. Everything in it is generated: ffmpeg test patterns, drawn title cards and an eSpeak NG
# voice. See docs/quickstart.md for what to do with it.
#
# usage: scripts/make-sample.sh [DIR] [--with-take] [--edit PATH]
#   DIR          where to make it (default: ./retake-sample). It must not exist yet, or be an earlier sample.
#   --with-take  also add a pre-made review take on v2, in place of recording one with a mic
#   --edit PATH  the retake-edit binary (default: retake-edit on $PATH, else editor/target/release/retake-edit)
# needs: ffmpeg, retake-edit
#
# DIR then holds:
#   how-to/v1/how-to_v1.mp4, how-to/v2/how-to_v2.mp4   the version family (and each render's manifest)
#   edit/                                              the editor project: timeline.json, patches/, voice/, media/
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/examples/sample"
TAKE_ID="2026-01-15_10-00-00"
dir=""
with_take=0
edit=""
while [ $# -gt 0 ]; do
  case "$1" in
    --with-take) with_take=1 ;;
    --edit) edit="${2:?--edit needs a path}"; shift ;;
    --edit=*) edit="${1#*=}" ;;
    -h | --help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "make-sample: unknown option $1" >&2; exit 2 ;;
    *) [ -z "$dir" ] || { echo "make-sample: one DIR only" >&2; exit 2; }; dir="$1" ;;
  esac
  shift
done
dir="${dir:-retake-sample}"

die() { echo "make-sample: $*" >&2; exit 1; }
for b in ffmpeg ffprobe; do command -v "$b" >/dev/null || die "$b is not installed"; done
if [ -z "$edit" ]; then
  if command -v retake-edit >/dev/null; then
    edit="$(command -v retake-edit)"
  elif [ -x "$ROOT/editor/target/release/retake-edit" ]; then
    edit="$ROOT/editor/target/release/retake-edit"
  else
    die "retake-edit not found: install it (install.sh --with-editor), build it (cd editor && cargo build --release), or pass --edit"
  fi
fi
[ -x "$edit" ] || die "$edit is not executable"

# Only ever (re)write a folder this script made.
if [ -e "$dir" ]; then
  [ -f "$dir/.retake-sample" ] || [ -z "$(ls -A "$dir")" ] || die "$dir exists and isn't a sample folder; pick another DIR"
  rm -rf "$dir"
fi
mkdir -p "$dir"
dir="$(cd "$dir" && pwd)"
touch "$dir/.retake-sample"

echo "== the editor project in $dir/edit"
mkdir -p "$dir/edit/media"
cp -r "$SRC/timeline.json" "$SRC/patches" "$SRC/voice" "$dir/edit/"

# Footage for the three steps: ffmpeg's test sources, 8 s each, no sound.
ff() { nice -n 19 ffmpeg -nostdin -v error -y -threads 4 "$@"; }
enc=(-c:v libx264 -preset veryfast -crf 23 -g 30 -pix_fmt yuv420p -an)
ff -f lavfi -i "testsrc2=size=1280x720:rate=30" -t 8 "${enc[@]}" "$dir/edit/media/rinse.mp4"
ff -f lavfi -i "testsrc=size=1280x720:rate=30" -vf "hue=h=30:s=0.7" -t 8 "${enc[@]}" "$dir/edit/media/grind.mp4"
ff -f lavfi -i "life=size=320x180:rate=30:ratio=0.2:death_color=#1b1410:life_color=#c8a070:seed=7" \
  -vf "scale=1280:720:flags=neighbor" -t 8 "${enc[@]}" "$dir/edit/media/pour.mp4"

tl="$dir/edit/timeline.json"
ed() { nice -n 19 "$edit" "$@"; }
ed validate "$tl" >/dev/null

echo "== v1 (timeline revision 0)"
mkdir -p "$dir/how-to/v1" "$dir/how-to/v2"
# how-to/ is a timeline project (spec/bridge.md): `retake push --render` renders this timeline as the next version.
printf '# The timeline the versions of how-to/ are rendered from (spec/bridge.md).\n[edit]\ntimeline = "../edit/timeline.json"\n' \
  >"$dir/how-to/retake.toml"
ed render "$tl" -o "$dir/how-to/v1/how-to_v1.mp4"

echo "== v2 (patches/01-v2.json)"
ed patch apply "$tl" "$dir/edit/patches/01-v2.json" >/dev/null
ed render "$tl" -o "$dir/how-to/v2/how-to_v2.mp4"

if [ "$with_take" = 1 ]; then
  take="$dir/how-to/v2/how-to_v2.review/$TAKE_ID"
  echo "== a pre-made review take on v2: $take"
  mkdir -p "$take"
  for f in "$SRC"/take/*; do
    case "$f" in
      *.json) sed "s|@SAMPLE@|$dir|g" "$f" >"$take/$(basename "$f")" ;;
      *) cp "$f" "$take/" ;;
    esac
  done
fi

cat <<EOF

The sample is in $dir. Next:
  retake open $dir/how-to/v2/how-to_v2.mp4
See docs/quickstart.md for the rest of the loop.
EOF
