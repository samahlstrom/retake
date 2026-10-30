#!/usr/bin/env bash
# Generates the demo's media with ffmpeg: test patterns, gradients and tones. Nothing personal or licensed.
#
# usage: make-media.sh [out-dir] [retake-edit binary]
#   DEMO_SIZE=480x270 makes small sources (the integration test uses it); the timeline doesn't change.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="${1:-$here/media}"
bin="${2:-retake-edit}"
size="${DEMO_SIZE:-1920x1080}"
w="${size%x*}"
h="${size#*x}"
clip_size="$((w * 2 / 3 / 2 * 2))x$((h * 2 / 3 / 2 * 2))"
mkdir -p "$out"
ff() { ffmpeg -nostdin -v error -y "$@"; }
enc=(-c:v libx264 -preset veryfast -crf 23 -g 30 -pix_fmt yuv420p)

# "Footage": the testsrc2 pattern, 30 s, no sound.
ff -f lavfi -i "testsrc2=size=${size}:rate=30" -t 30 "${enc[@]}" "$out/footage.mp4"
# An insert / picture-in-picture clip with a 440 Hz tone.
ff -f lavfi -i "testsrc=size=${clip_size}:rate=30" -f lavfi -i "sine=frequency=440:sample_rate=48000" \
  -t 10 "${enc[@]}" -c:a aac -b:a 128k -shortest "$out/clip.mp4"
# A looping animated background (8 s).
ff -f lavfi -i "gradients=size=${size}:rate=30:duration=8:speed=0.02:c0=0x1d2b53:c1=0x7e2553:c2=0x008751:nb_colors=3" \
  "${enc[@]}" "$out/loop.mp4"
# A music bed: an A minor chord with a slow tremolo, 20 s, stereo.
chord="0.20*sin(2*PI*220*t)+0.15*sin(2*PI*261.63*t)+0.12*sin(2*PI*329.63*t)"
ff -f lavfi -i "aevalsrc=(${chord})*(0.8+0.2*sin(2*PI*1.5*t))|(${chord})*(0.8+0.2*sin(2*PI*1.5*t+1)):s=48000:d=20" \
  "$out/music.wav"
# A "logo": one frame of testsrc2.
ff -f lavfi -i "testsrc2=size=256x256:rate=1" -frames:v 1 "$out/logo.png"
# The VO: tone bursts at exactly the times in vo.words.json (its transcript).
"$bin" synth-vo "$here/vo.words.json" -o "$out/vo.wav" --dur 14.8
echo "media in $out"
