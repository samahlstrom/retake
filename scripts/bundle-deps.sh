#!/bin/sh
# Puts the tools a release ships with retake into dist-bundle/<os>_<arch>/ (docs/install-plan.md), where goreleaser's
# archives pick them up:
#
#   bin/herdr, bin/ffmpeg, bin/ffprobe, bin/retake-edit   (each when there is one for that platform)
#   licenses/                                            (each tool's license text)
#   retake-bundle.json                                   (what's inside: versions, licenses, sources)
#
# Every download is pinned with its SHA-256 and kept in ~/.cache/retake-bundle, so a second run downloads nothing. A
# checksum that doesn't match stops the script. Nothing downloaded is run.
#
#   scripts/bundle-deps.sh                 # every platform below
#   scripts/bundle-deps.sh linux_amd64     # just one
#
# retake-edit can't be cross-compiled (it links prebuilt Skia): the host's editor/target/release/retake-edit goes into
# the host's folder, and CI's archives in dist-editor/ (retake-edit_<v>_<os>_<arch>.tar.gz) into theirs.
set -eu

cd "$(dirname "$0")/.."
OUT="dist-bundle"
CACHE="${RETAKE_BUNDLE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/retake-bundle}"
PLATFORMS="${*:-linux_amd64 linux_arm64 darwin_amd64 darwin_arm64}"

# herdr: the release Retake's herdr support was built against (docs/agents.md). Apache-2.0.
HERDR_VERSION="0.8.2"
HERDR_URL="https://github.com/herdrdev/herdr/releases/download/v${HERDR_VERSION}"
HERDR_LICENSE_URL="https://raw.githubusercontent.com/herdrdev/herdr/v${HERDR_VERSION}/LICENSE"
HERDR_LICENSE_SHA="c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"
herdr_asset() { # os_arch -> "file sha256"
  case "$1" in
    linux_amd64) echo "herdr-linux-x86_64 976150a14d490c94b243ea2e1a7eb2dfb67f12e36b182db90936f6728e6aecf4" ;;
    linux_arm64) echo "herdr-linux-aarch64 f55610658e1c2e0d2aaef730b4b2ab885f7f8ba00285ab372bfb14f2e3d5b40d" ;;
    darwin_amd64) echo "herdr-macos-x86_64 ab50262c8190cd7aa9056d249d255c08c328c3e8716de9cfa29db4f131b8e2c1" ;;
    darwin_arm64) echo "herdr-macos-aarch64 a5d4f4d504d8b309c91f811050559300faba31258425f53c50852fc96f6ae574" ;;
  esac
}

# ffmpeg: John Van Sickle's static GPL builds (7.0.2, with libx264, which retake-edit encodes with) on Linux. None for
# macOS yet: the plan is to build our own in CI (docs/install-plan.md); until then the Mac app uses one on PATH.
FFMPEG_VERSION="7.0.2"
FFMPEG_URL="https://johnvansickle.com/ffmpeg/releases"
FFMPEG_SOURCE="https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"
ffmpeg_asset() { # os_arch -> "file sha256"
  case "$1" in
    linux_amd64) echo "ffmpeg-release-amd64-static.tar.xz abda8d77ce8309141f83ab8edf0596834087c52467f6badf376a6a2a4c87cf67" ;;
    linux_arm64) echo "ffmpeg-release-arm64-static.tar.xz f4149bb2b0784e30e99bdda85471c9b5930d3402014e934a5098b41d0f7201b1" ;;
  esac
}

die() {
  printf 'bundle-deps: %s\n' "$*" >&2
  exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
else
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi

# fetch URL NAME SHA256: the file in the cache, downloaded once and checked every time.
fetch() {
  mkdir -p "$CACHE"
  if [ ! -f "$CACHE/$2" ]; then
    printf 'bundle-deps: downloading %s\n' "$1" >&2
    curl -fsSL --retry 2 -o "$CACHE/$2.part" "$1" || die "couldn't download $1"
    mv "$CACHE/$2.part" "$CACHE/$2"
  fi
  got="$(sha256 "$CACHE/$2")"
  [ "$got" = "$3" ] || die "$2: SHA-256 is $got, want $3 (refusing it; delete $CACHE/$2 to download it again)"
  printf '%s\n' "$CACHE/$2"
}

host="$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m)"
case "$host" in
  *_x86_64) host="${host%_x86_64}_amd64" ;;
  *_aarch64) host="${host%_aarch64}_arm64" ;;
esac

for p in $PLATFORMS; do
  dir="$OUT/$p"
  rm -rf "$dir"
  mkdir -p "$dir/bin" "$dir/licenses"
  tools=""
  add_tool() { # name version license source
    tools="${tools}${tools:+,}
    \"$1\": {\"version\": \"$2\", \"license\": \"$3\", \"source\": \"$4\"}"
  }

  # shellcheck disable=SC2046 # "file sha256": two words on purpose
  set -- $(herdr_asset "$p")
  [ $# -eq 2 ] || die "no herdr for $p"
  cp "$(fetch "$HERDR_URL/$1" "herdr-$HERDR_VERSION-$1" "$2")" "$dir/bin/herdr"
  chmod 755 "$dir/bin/herdr"
  cp "$(fetch "$HERDR_LICENSE_URL" "herdr-LICENSE-v$HERDR_VERSION" "$HERDR_LICENSE_SHA")" "$dir/licenses/herdr-LICENSE.txt"
  add_tool herdr "$HERDR_VERSION" "Apache-2.0" "https://github.com/herdrdev/herdr/tree/v$HERDR_VERSION"

  # shellcheck disable=SC2046 # "file sha256": two words on purpose
  if set -- $(ffmpeg_asset "$p") && [ $# -eq 2 ]; then
    archive="$(fetch "$FFMPEG_URL/$1" "$1" "$2")"
    tmp="$(mktemp -d "$OUT/.ffmpeg.XXXXXX")"
    tar -xJf "$archive" -C "$tmp"
    top="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
    for b in ffmpeg ffprobe; do
      [ -f "$top/$b" ] || die "$1 has no $b"
      cp "$top/$b" "$dir/bin/$b"
      chmod 755 "$dir/bin/$b"
    done
    cp "$top/GPLv3.txt" "$dir/licenses/ffmpeg-GPLv3.txt"
    [ ! -f "$top/readme.txt" ] || cp "$top/readme.txt" "$dir/licenses/ffmpeg-build-readme.txt"
    rm -rf "$tmp"
    printf 'ffmpeg %s, a static GPL build by John Van Sickle (%s).\nIts source is FFmpeg %s: %s\n' \
      "$FFMPEG_VERSION" "$FFMPEG_URL" "$FFMPEG_VERSION" "$FFMPEG_SOURCE" >"$dir/licenses/ffmpeg-SOURCE.txt"
    add_tool ffmpeg "$FFMPEG_VERSION" "GPL-3.0-or-later" "$FFMPEG_SOURCE"
  fi

  editor=""
  if [ "$p" = "$host" ] && [ -x editor/target/release/retake-edit ]; then
    editor="editor/target/release/retake-edit"
  else
    for a in dist-editor/retake-edit_*_"$p".tar.gz; do
      [ -f "$a" ] || continue
      tmp="$(mktemp -d "$OUT/.editor.XXXXXX")"
      tar -xzf "$a" -C "$tmp"
      [ ! -f "$tmp/retake-edit" ] || { cp "$tmp/retake-edit" "$dir/bin/.retake-edit" && editor="$dir/bin/.retake-edit"; }
      rm -rf "$tmp"
    done
  fi
  if [ -n "$editor" ]; then
    cp "$editor" "$dir/bin/retake-edit"
    chmod 755 "$dir/bin/retake-edit"
    rm -f "$dir/bin/.retake-edit"
  fi

  printf '{\n  "bundle": 1,\n  "tools": {%s\n  }\n}\n' "$tools" >"$dir/retake-bundle.json"
  names=""
  for b in "$dir"/bin/*; do names="$names $(basename "$b")"; done
  printf 'bundle-deps: %s:%s\n' "$p" "$names" >&2
done
