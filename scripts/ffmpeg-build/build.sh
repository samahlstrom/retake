#!/bin/sh
# Retake's Linux ffmpeg and ffprobe (docs/install-plan.md, "ffmpeg"): built here from pinned sources, static against
# musl in an Alpine container, so they run on any x64 or arm64 Linux with nothing but the kernel. GPL, with libx264 and
# zimg (zscale), which retake-edit renders with, plus FFmpeg's own decoders, encoders, formats, filters and protocols.
# No other outside library except zlib (PNG, compressed MP4 headers), no TLS, no ffplay, no hardware encoders.
#
# The release workflow runs it on an x64 and an arm64 runner (.github/workflows/ffmpeg.yml); scripts/bundle-deps.sh
# runs it for a local snapshot. The same script, run the same way. It needs Docker (or Podman: DOCKER=podman).
#
#   scripts/ffmpeg-build/build.sh                 this machine's arch; prints the folder the build is in
#   scripts/ffmpeg-build/build.sh --arch arm64    the other arch: Docker runs the container under emulation (slow)
#   scripts/ffmpeg-build/build.sh --out DIR       also copies it to DIR/linux_<arch>/ and DIR/ffmpeg-source-<v>.tar.xz
#   scripts/ffmpeg-build/build.sh --check DIR     runs DIR/ffmpeg and DIR/ffprobe: static, x264 and zimg in, encodes
#
# Kept in ~/.cache/retake-bundle/ffmpeg (RETAKE_BUNDLE_CACHE): the downloads in src/, checked against their SHA-256
# every time, and each arch's build in linux_<arch>/, which is rebuilt only when this file changes (its SHA-256 is the
# build's key, and every pin is in it). A build is:
#
#   linux_<arch>/ffmpeg, ffprobe     stripped, static
#   linux_<arch>/licenses/           FFmpeg's GPLv3 and LICENSE.md, x264's GPLv2, zimg's, zlib's and musl's texts,
#                                    ffmpeg-build.txt (the sources, configure line, compiler and image)
#   ffmpeg-source-<v>.tar.xz         the corresponding source the GPL asks for: every source tarball and this script.
#                                    It ships next to each release's archives.
#
# RETAKE_FFMPEG_SRC=DIR takes the source tarballs from DIR instead of downloading them (the source bundle's README
# says how to rebuild from it). RETAKE_FFMPEG_JOBS sets make's -j (default: every CPU).
set -eu

FFMPEG_VERSION="9.0.2"
# The base image, by digest (Alpine 3.24.3: musl 1.2.6, gcc 15.2), for both arches.
IMAGE="alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a"
# Each source: file, SHA-256, URL, and a second URL with the same file when there is one.
X264_COMMIT="b35605ace3ddf7c1a5d67a2eb553f034aef41d55" # the stable branch, r3222
sources() {
  cat <<EOF
ffmpeg-$FFMPEG_VERSION.tar.xz 8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz https://distfiles.alpinelinux.org/distfiles/edge/ffmpeg-$FFMPEG_VERSION.tar.xz
x264-$X264_COMMIT.tar.gz cd71a7515b0e9a012e1ac9b1f8415bebcaf6fc97d4db32286642ac4c0fbe24f9 https://code.videolan.org/videolan/x264/-/archive/$X264_COMMIT/x264-$X264_COMMIT.tar.gz
zimg-release-3.0.6.tar.gz be89390f13a5c9b2388ce0f44a5e89364a20c1c57ce46d382b1fcc3967057577 https://github.com/sekrit-twc/zimg/archive/refs/tags/release-3.0.6.tar.gz
zlib-1.3.2.tar.gz bb329a0a2cd0274d05519d61c667c062e06990d72e125ee2dfa8de64f0119d16 https://github.com/madler/zlib/releases/download/v1.3.2/zlib-1.3.2.tar.gz
musl-1.2.6.tar.gz d585fd3b613c66151fc3249e8ed44f77020cb5e6c1e635a616d3f9f82460512a https://musl.libc.org/releases/musl-1.2.6.tar.gz https://distfiles.alpinelinux.org/distfiles/v3.24/musl-1.2.6.tar.gz
EOF
}

# What FFmpeg is configured with. Every autodetected library is off (--disable-autodetect), so a build is the same
# whatever the image has; zlib and threads are turned back on by name. The 2 MB thread stack: musl's default is 128 KB.
# Size: -O2 instead of FFmpeg's -O3 (8 MB less a binary; the hot loops are assembly, and x264 and zimg keep their own
# flags), unused functions dropped (--gc-sections), no unwind tables for C, and a plain static executable (-no-pie:
# no relocations to carry). That's about 23 MB a binary instead of 35.
configure_flags() {
  cat <<'EOF'
--optflags=-O2
--disable-autodetect
--enable-pthreads
--enable-zlib
--enable-gpl
--enable-version3
--enable-libx264
--enable-libzimg
--disable-ffplay
--disable-doc
--disable-debug
--disable-shared
--enable-static
--pkg-config-flags=--static
--extra-cflags=-I/opt/ff/include -ffunction-sections -fdata-sections -fno-asynchronous-unwind-tables
--extra-ldflags=-L/opt/ff/lib -static
--extra-ldexeflags=-no-pie -Wl,--gc-sections -Wl,-z,stack-size=2097152
EOF
}

die() {
  printf 'ffmpeg-build: %s\n' "$*" >&2
  exit 1
}
say() { printf 'ffmpeg-build: %s\n' "$*" >&2; }
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
else
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi

# ---------------------------------------------------------------------------------------------------------------------
# Inside the container: build into /out (the cache's linux_<arch>/) from the tarballs in /src.

in_container() {
  set -x
  apk add --no-cache build-base nasm pkgconf autoconf automake libtool linux-headers bash xz tar
  jobs="${RETAKE_FFMPEG_JOBS:-$(nproc)}"
  P=/opt/ff
  B=/tmp/build
  rm -rf "$B" && mkdir -p "$B" "$P"
  cd "$B"

  tar -xzf /src/zlib-1.3.2.tar.gz
  (cd zlib-1.3.2 && ./configure --static --prefix="$P" && make -j"$jobs" && make install)

  tar -xzf "/src/x264-$X264_COMMIT.tar.gz"
  (cd "x264-$X264_COMMIT" && ./configure --prefix="$P" --enable-static --enable-pic --disable-cli --disable-opencl &&
    make -j"$jobs" && make install)

  tar -xzf /src/zimg-release-3.0.6.tar.gz
  (cd zimg-release-3.0.6 && ./autogen.sh && ./configure --prefix="$P" --enable-static --disable-shared &&
    make -j"$jobs" && make install)

  tar -xJf "/src/ffmpeg-$FFMPEG_VERSION.tar.xz"
  cd "ffmpeg-$FFMPEG_VERSION"
  # One flag per line, so --extra-ldflags keeps its space.
  set --
  while IFS= read -r f; do set -- "$@" "$f"; done <<EOF
$(configure_flags)
EOF
  PKG_CONFIG_PATH="$P/lib/pkgconfig" ./configure --prefix="$P" "$@" || {
    tail -n 60 ffbuild/config.log
    exit 1
  }
  make -j"$jobs"
  set +x

  rm -rf /out/ffmpeg /out/ffprobe /out/licenses
  mkdir -p /out/licenses
  cp ffmpeg ffprobe /out/ # FFmpeg's make strips these (ffmpeg_g is the unstripped one)
  cp COPYING.GPLv3 /out/licenses/ffmpeg-GPLv3.txt
  cp LICENSE.md /out/licenses/ffmpeg-LICENSE.md
  cp "$B/x264-$X264_COMMIT/COPYING" /out/licenses/x264-COPYING.txt
  cp "$B/zimg-release-3.0.6/COPYING" /out/licenses/zimg-COPYING.txt
  cp "$B/zlib-1.3.2/LICENSE" /out/licenses/zlib-LICENSE.txt
  tar -xzOf /src/musl-1.2.6.tar.gz musl-1.2.6/COPYRIGHT >/out/licenses/musl-COPYRIGHT.txt
  {
    echo "ffmpeg and ffprobe $FFMPEG_VERSION for linux_${ARCH:?}, built by Retake's scripts/ffmpeg-build/build.sh"
    echo "(its SHA-256: $KEY). Static, against musl: they need no shared library."
    echo
    echo "Sources (all in ffmpeg-source-$FFMPEG_VERSION.tar.xz, next to the release's archives), by SHA-256:"
    sources | while read -r name sum _; do echo "  $name  $sum"; done
    echo "x264 is the commit $X264_COMMIT of its stable branch. musl is Alpine's build of musl 1.2.6."
    echo
    echo "Image: $IMAGE"
    echo "Compiler: $(gcc --version | head -n 1)"
    echo "Packages: $(apk info -v 2>/dev/null | grep -E '^(musl|gcc|g\+\+|libstdc\+\+|binutils|nasm)-[0-9]' | sort | tr '\n' ' ')"
    echo
    ./ffmpeg -hide_banner -version | grep '^configuration:'
  } >/out/licenses/ffmpeg-build.txt

  # The corresponding source: the tarballs and this script, with a README, the same bytes from any build.
  s="ffmpeg-source-$FFMPEG_VERSION"
  mkdir -p "$B/$s"
  sources | while read -r name _; do cp "/src/$name" "$B/$s/"; done
  cp /build.sh "$B/$s/build.sh"
  cat >"$B/$s/README.txt" <<EOF
The source of the ffmpeg and ffprobe that Retake's Linux archives and npm packages carry: FFmpeg $FFMPEG_VERSION,
x264 (commit $X264_COMMIT, stable branch), zimg 3.0.6, zlib 1.3.2 and musl 1.2.6, as their authors released them,
and build.sh, the script that built them (scripts/ffmpeg-build/build.sh in https://github.com/samahlstrom/retake).
The binaries' licenses/ffmpeg-build.txt lists the configure line and the build image.

To build them again (needs Docker), from this folder:

    RETAKE_FFMPEG_SRC="\$PWD" sh build.sh

ffmpeg is GPL-3.0-or-later as built (--enable-gpl --enable-version3); x264 is GPL-2.0-or-later, zimg WTFPL, zlib
the zlib license, musl MIT. GCC's runtime libraries (libgcc, libstdc++) are linked under the GCC Runtime Library
Exception.
EOF
  rm -f "/out/$s.tar.xz"
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 -C "$B" -cf - "$s" | xz -1 -T1 >"/out/$s.tar.xz"
  printf '%s\n' "$KEY" >/out/build-key
  chown -R "$HOST_UID:$HOST_GID" /out
}

# ---------------------------------------------------------------------------------------------------------------------
# --check DIR: runs the build on this machine.

check() {
  d="$1"
  ff="$d/ffmpeg"
  fp="$d/ffprobe"
  [ -x "$ff" ] && [ -x "$fp" ] || die "no $ff and $fp"
  t="$(mktemp -d "${TMPDIR:-/tmp}/ffmpeg-check.XXXXXX")"
  trap 'rm -rf "$t"' EXIT
  ok() { printf -- '- ok: %s\n' "$*"; }
  fail() {
    printf -- '- FAILED: %s\n' "$1"
    [ ! -s "$t/err" ] || sed 's/^/    /' "$t/err" | tail -n 20
    exit 1
  }
  for b in "$ff" "$fp"; do
    # Static: no program interpreter (ld.so) and no shared library needed.
    if command -v readelf >/dev/null 2>&1; then
      ! readelf -lW "$b" | grep -q 'INTERP' || fail "$(basename "$b") asks for a program interpreter"
      ! readelf -dW "$b" 2>/dev/null | grep -q 'NEEDED' || fail "$(basename "$b") needs shared libraries"
    fi
    linked="$(file -b "$b" 2>/dev/null | tr ',' '\n' | grep 'linked' | sed 's/^ *//')" || linked="static"
    ok "$(basename "$b"): ${linked:-static}, $(wc -c <"$b" | tr -d ' ') bytes"
  done
  "$ff" -hide_banner -buildconf >"$t/conf" 2>&1 || fail "ffmpeg -buildconf"
  grep -q -- --enable-libx264 "$t/conf" || fail "x264 in -buildconf"
  grep -q -- --enable-libzimg "$t/conf" || fail "zimg in -buildconf"
  ok "built with libx264 and zimg ($("$ff" -hide_banner -version | head -n 1))"
  # What the Go server and retake-edit run (docs/install-plan.md lists it), by name.
  has() { # -filters|-encoders|... name...
    kind="$1"
    shift
    "$ff" -hide_banner "$kind" >"$t/list" 2>/dev/null || fail "ffmpeg $kind"
    if [ "$kind" = -bsfs ]; then cp "$t/list" "$t/names"; else awk '{ print $2 }' "$t/list" | tr ',' '\n' >"$t/names"; fi
    missing=""
    for n in "$@"; do grep -qx -- "$n" "$t/names" || missing="$missing $n"; done
    [ -z "$missing" ] || fail "ffmpeg $kind has no$missing"
    ok "$kind: $*"
  }
  has -filters zscale scale format colorspace crop pad overlay fps tile select setsar split ebur128 loudnorm \
    aresample aformat pan volume asplit testsrc2 color sine anullsrc concat
  has -encoders libx264 aac png mjpeg pcm_s16le pcm_f32le rawvideo
  has -decoders h264 hevc mpeg4 prores vp8 vp9 aac opus vorbis mp3 flac pcm_s16le png mjpeg
  has -muxers mp4 mov matroska webm image2 image2pipe hls adts f32le wav mpegts null
  has -demuxers mov mp4 matroska webm concat image2 aac mp3 ogg flac wav f32le rawvideo hls
  has -devices lavfi
  has -bsfs h264_metadata aac_adtstoasc

  run() { "$@" >"$t/out" 2>"$t/err"; }
  # retake-edit's conversion (src/ffmpeg.rs) and its encoder, as scripts/npm-smoke.mjs does on each platform.
  run "$ff" -nostdin -hide_banner -v error -f lavfi -i testsrc2=s=640x360:d=1:r=30 -f lavfi -i sine=d=1 \
    -vf zscale=w=1280:h=720:filter=spline36:dither=none:matrixin=709:rangein=limited,format=gbrp,format=yuv420p \
    -c:v libx264 -preset veryfast -threads 2 -c:a aac -b:a 192k -shortest -movflags +faststart -y "$t/a.mp4" ||
    fail "an encode through zscale with libx264 and aac"
  run "$fp" -v error -select_streams v:0 -show_entries stream=codec_name,width -of csv=p=0 "$t/a.mp4" || fail "ffprobe"
  [ "$(tr -d '\r\n' <"$t/out")" = "h264,1280" ] || fail "ffprobe reads the video back as h264, 1280 wide: $(cat "$t/out")"
  run "$fp" -v error -select_streams a:0 -show_entries stream=codec_name -of csv=p=0 "$t/a.mp4" || fail "ffprobe"
  [ "$(tr -d '\r\n' <"$t/out")" = "aac" ] || fail "ffprobe reads the audio back as aac: $(cat "$t/out")"
  ok "an encode through zscale with libx264 and aac, read back by ffprobe"
  # The Go server and retake-edit's other calls: a PNG (zlib) and a JPEG still, the BT.709 tag rewrite, a concat,
  # Matroska, the loudness filters, raw f32le audio, HLS.
  run "$ff" -nostdin -v error -ss 0.5 -i "$t/a.mp4" -frames:v 1 -f image2pipe -c:v png "$t/f.png" || fail "a PNG still"
  run "$ff" -nostdin -v error -i "$t/f.png" -vf "scale=min(320\\,iw):-2" -q:v 4 -f image2 -c:v mjpeg -y "$t/f.jpg" ||
    fail "a JPEG thumbnail from the PNG"
  ok "PNG and JPEG stills"
  run "$ff" -nostdin -v error -i "$t/a.mp4" -map 0:v -c copy \
    -bsf:v h264_metadata=colour_primaries=1:transfer_characteristics=1:matrix_coefficients=1:video_full_range_flag=0 \
    -f mp4 -y "$t/b.mp4" || fail "h264_metadata"
  printf "file '%s'\nfile '%s'\n" "$t/a.mp4" "$t/a.mp4" >"$t/list.txt"
  run "$ff" -nostdin -v error -f concat -safe 0 -i "$t/list.txt" -c copy -y "$t/c.mkv" || fail "concat into Matroska"
  run "$fp" -v error -show_entries format=format_name -of csv=p=0 "$t/c.mkv" || fail "ffprobe on Matroska"
  ok "h264_metadata, concat, Matroska"
  run "$ff" -nostdin -hide_banner -nostats -i "$t/a.mp4" -map 0:a:0 -af ebur128=peak=true,loudnorm=I=-14:TP=-1 \
    -f null - || fail "ebur128 and loudnorm"
  run "$ff" -nostdin -v error -i "$t/a.mp4" -vn -ac 2 -ar 48000 -f f32le "$t/a.f32" || fail "f32le audio"
  run "$ff" -nostdin -v error -i "$t/a.mp4" -c copy -f hls -hls_time 1 -hls_playlist_type vod \
    -hls_segment_filename "$t/s%d.ts" "$t/a.m3u8" || fail "HLS"
  ok "ebur128, loudnorm, f32le, HLS"
  # Threads: musl's threads get the 2 MB stack linked in. 1080p with every core encoding, then frame-threaded decoding.
  run "$ff" -nostdin -v error -f lavfi -i testsrc2=s=1920x1080:d=2:r=30 -c:v libx264 -preset medium -threads 0 \
    -pix_fmt yuv420p -y "$t/hd.mp4" || fail "a threaded 1080p encode"
  run "$ff" -nostdin -v error -threads 0 -i "$t/hd.mp4" -vf scale=640:-2 -f null - || fail "a threaded decode"
  ok "threaded 1080p encode and decode"
}

# ---------------------------------------------------------------------------------------------------------------------
# The build. (A function, read whole before it runs: editing this file during a build can't change what it does.)

main() {
  arch=""
  out=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --arch) arch="${2:-}" && shift ;;
      --out) out="${2:-}" && shift ;;
      --check)
        [ -n "${2:-}" ] || die "--check needs a folder"
        check "$2"
        exit 0
        ;;
      *)
        echo "usage: scripts/ffmpeg-build/build.sh [--arch amd64|arm64] [--out DIR] | --check DIR" >&2
        exit 2
        ;;
    esac
    shift
  done

  here="$(cd "$(dirname "$0")" && pwd)"
  script="$here/$(basename "$0")"
  KEY="$(sha256 "$script")"
  case "$(uname -m)" in
    x86_64 | amd64) host=amd64 ;;
    aarch64 | arm64) host=arm64 ;;
    *) host="$(uname -m)" ;;
  esac
  arch="${arch:-$host}"
  case "$arch" in amd64 | arm64) ;; *) die "--arch is amd64 or arm64, not $arch" ;; esac

  CACHE="${RETAKE_BUNDLE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/retake-bundle}/ffmpeg"
  SRC="${RETAKE_FFMPEG_SRC:-$CACHE/src}"
  dir="$CACHE/linux_$arch"
  bundle="ffmpeg-source-$FFMPEG_VERSION.tar.xz"

  if [ "$(cat "$dir/build-key" 2>/dev/null)" = "$KEY" ] && [ -x "$dir/ffmpeg" ] && [ -x "$dir/ffprobe" ] &&
    [ -f "$dir/$bundle" ]; then
    say "linux_$arch: built already by this script ($dir)"
  else
    # The sources, each checked against its SHA-256. Anything else in the cache's src/ is an old pin's: removed.
    mkdir -p "$SRC"
    sources | while read -r name sum url url2; do
      f="$SRC/$name"
      if [ ! -f "$f" ] || [ "$(sha256 "$f")" != "$sum" ]; then
        [ -z "${RETAKE_FFMPEG_SRC:-}" ] || die "$name in $SRC is missing or its SHA-256 isn't $sum"
        got=""
        for u in $url $url2; do
          say "downloading $u"
          if curl -fsSL --retry 4 --retry-all-errors --connect-timeout 30 -o "$f.part" "$u"; then
            got=1
            break
          fi
        done
        [ -n "$got" ] || die "couldn't download $name"
        mv "$f.part" "$f"
      fi
      have="$(sha256 "$f")"
      [ "$have" = "$sum" ] || die "$name: SHA-256 is $have, want $sum (refusing it; delete $f to download it again)"
    done
    if [ -z "${RETAKE_FFMPEG_SRC:-}" ]; then
      for f in "$SRC"/*; do
        [ -f "$f" ] || continue
        sources | grep -q "^$(basename "$f") " || rm -f "$f"
      done
    fi

    docker="${DOCKER:-}"
    if [ -z "$docker" ]; then
      if command -v docker >/dev/null 2>&1; then docker=docker; elif command -v podman >/dev/null 2>&1; then docker=podman; fi
    fi
    [ -n "$docker" ] || die "needs Docker or Podman to build linux_$arch (or set RETAKE_FFMPEG_DIR to prebuilt binaries)"
    [ "$arch" = "$host" ] || say "linux_$arch on a $host machine: the container runs under emulation, which takes a while"
    say "building linux_$arch in $IMAGE (FFmpeg $FFMPEG_VERSION, x264, zimg; several minutes)"
    rm -rf "$dir"
    mkdir -p "$dir"
    "$docker" run --rm --platform "linux/$arch" \
      -v "$SRC:/src:ro" -v "$script:/build.sh:ro" -v "$dir:/out" \
      -e ARCH="$arch" -e KEY="$KEY" -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
      -e RETAKE_FFMPEG_JOBS="${RETAKE_FFMPEG_JOBS:-}" \
      "$IMAGE" sh /build.sh --in-container >&2 || die "the build failed"
    [ -x "$dir/ffmpeg" ] && [ -x "$dir/ffprobe" ] && [ -f "$dir/$bundle" ] || die "the build made no ffmpeg, ffprobe and $bundle"
  fi

  if [ -n "$out" ]; then
    mkdir -p "$out/linux_$arch"
    cp "$dir/ffmpeg" "$dir/ffprobe" "$out/linux_$arch/"
    rm -rf "$out/linux_$arch/licenses"
    cp -R "$dir/licenses" "$out/linux_$arch/licenses"
    cp "$dir/$bundle" "$out/$bundle"
  fi
  printf '%s\n' "$dir"
}

if [ "${1:-}" = --in-container ]; then
  in_container
else
  main "$@"
fi
