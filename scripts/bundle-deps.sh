#!/bin/sh
# Puts the tools a release ships with retake into dist-bundle/<os>_<arch>/ (docs/install-plan.md), where goreleaser's
# archives pick them up:
#
#   bin/herdr, bin/ffmpeg, bin/ffprobe, bin/retake-edit   (each when there is one for that platform; .exe on Windows)
#   conpty/                                              (Windows: what herdr.exe loads from next to itself)
#   licenses/                                            (each tool's license text)
#   retake-bundle.json                                   (what's inside: versions, licenses, sources)
#
# Every download is pinned with its SHA-256 and kept in ~/.cache/retake-bundle, so a second run downloads nothing. A
# checksum that doesn't match stops the script. Nothing downloaded is run here; the release workflow runs each
# platform's ffmpeg on that platform (scripts/npm-smoke.mjs checks it has libx264 and zscale).
#
#   scripts/bundle-deps.sh                 # every platform below
#   scripts/bundle-deps.sh linux_amd64     # just one
#
# retake-edit can't be cross-compiled (it links prebuilt Skia): the host's editor/target/release/retake-edit goes into
# the host's folder, and CI's archives in dist-editor/ (retake-edit_<v>_<os>_<arch>.tar.gz, .zip on Windows) into
# theirs. The npm packages, the Homebrew formula and install.sh are all made from goreleaser's archives of these
# folders (docs/releasing.md).
set -eu

cd "$(dirname "$0")/.."
OUT="dist-bundle"
CACHE="${RETAKE_BUNDLE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/retake-bundle}"
PLATFORMS="${*:-linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64}"

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
    windows_amd64) echo "herdr-windows-x86_64.zip 0ab3d0fe1434d55757997542b978c771d642987bb15a7130f4160f0db38821d5" ;;
  esac
}

# ffmpeg and ffprobe: static GPL builds with libx264 and zimg (zscale), which retake-edit renders with
# (docs/install-plan.md, "ffmpeg").
#   Linux: John Van Sickle's 7.0.2, one .tar.xz with both.
#   macOS: Martin Riedl's 9.0.2, signed, a .zip each (https://ffmpeg.martin-riedl.de).
#   Windows: Gyan Doshi's 9.0.2 "essentials", one .zip (https://www.gyan.dev/ffmpeg/builds/, kept on GitHub).
FFMPEG_VERSION="7.0.2"
FFMPEG_URL="https://johnvansickle.com/ffmpeg/releases"
ffmpeg_asset() { # os_arch -> "file sha256"
  case "$1" in
    linux_amd64) echo "ffmpeg-release-amd64-static.tar.xz abda8d77ce8309141f83ab8edf0596834087c52467f6badf376a6a2a4c87cf67" ;;
    linux_arm64) echo "ffmpeg-release-arm64-static.tar.xz f4149bb2b0784e30e99bdda85471c9b5930d3402014e934a5098b41d0f7201b1" ;;
  esac
}
MAC_FFMPEG_VERSION="9.0.2"
MAC_FFMPEG_URL="https://ffmpeg.martin-riedl.de/download/macos"
mac_ffmpeg_asset() { # os_arch program -> "path sha256"
  case "$1/$2" in
    darwin_arm64/ffmpeg) echo "arm64/1789931890_9.0.2/ffmpeg.zip c8ed4c4e6978a03c485edbfe4e0a5dc2380f8a30bba5150531b31b094492d924" ;;
    darwin_arm64/ffprobe) echo "arm64/1789931890_9.0.2/ffprobe.zip fcbe839537485eaee7a7a8bc5cbc0f90d53617e80943e8a5b2e31cb851197ea6" ;;
    darwin_amd64/ffmpeg) echo "amd64/1789931006_9.0.2/ffmpeg.zip 7c6b4125b191cbf773832dc51f424cf2b6bb7da43007d1e066f95909e47cacd4" ;;
    darwin_amd64/ffprobe) echo "amd64/1789931006_9.0.2/ffprobe.zip 2322438ed2f6319a691291b247d09c69dcaa3a982460d1f269a7e1af335cfdfd" ;;
  esac
}
# The macOS zips hold just the program: FFmpeg's own GPLv3 text, at that version, goes with them.
MAC_FFMPEG_LICENSE_URL="https://raw.githubusercontent.com/FFmpeg/FFmpeg/n${MAC_FFMPEG_VERSION}/COPYING.GPLv3"
MAC_FFMPEG_LICENSE_SHA="8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903"
WIN_FFMPEG_VERSION="9.0.2"
WIN_FFMPEG_URL="https://github.com/GyanD/codexffmpeg/releases/download/${WIN_FFMPEG_VERSION}"
WIN_FFMPEG_FILE="ffmpeg-${WIN_FFMPEG_VERSION}-essentials_build.zip"
WIN_FFMPEG_SHA="60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba"
ffmpeg_source() { printf 'https://ffmpeg.org/releases/ffmpeg-%s.tar.xz' "$1"; }

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

  exe=""
  case "$p" in windows_*) exe=".exe" ;; esac

  # herdr. Windows' is a .zip: herdr.exe, and conpty/ (Microsoft's ConPTY, OpenConsole.exe per CPU), which herdr
  # loads from next to itself. conpty/ stays a folder of its own here; .goreleaser.yaml puts it next to herdr.exe.
  # shellcheck disable=SC2046 # "file sha256": two words on purpose
  set -- $(herdr_asset "$p")
  [ $# -eq 2 ] || die "no herdr for $p"
  got="$(fetch "$HERDR_URL/$1" "herdr-$HERDR_VERSION-$1" "$2")"
  case "$1" in
    *.zip)
      tmp="$(mktemp -d "$OUT/.herdr.XXXXXX")"
      unzip -q -o "$got" -d "$tmp"
      [ -f "$tmp/herdr.exe" ] && [ -d "$tmp/conpty" ] || die "$1 has no herdr.exe and conpty/"
      cp "$tmp/herdr.exe" "$dir/bin/herdr.exe"
      cp -R "$tmp/conpty" "$dir/conpty"
      for f in "$tmp"/THIRD-PARTY-NOTICES/*; do
        [ ! -f "$f" ] || cp "$f" "$dir/licenses/herdr-$(basename "$f")"
      done
      rm -rf "$tmp"
      ;;
    *) cp "$got" "$dir/bin/herdr" ;;
  esac
  chmod 755 "$dir/bin/herdr$exe"
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
      "$FFMPEG_VERSION" "$FFMPEG_URL" "$FFMPEG_VERSION" "$(ffmpeg_source "$FFMPEG_VERSION")" >"$dir/licenses/ffmpeg-SOURCE.txt"
    add_tool ffmpeg "$FFMPEG_VERSION" "GPL-3.0-or-later" "$(ffmpeg_source "$FFMPEG_VERSION")"
  elif [ -n "$(mac_ffmpeg_asset "$p" ffmpeg)" ]; then
    for b in ffmpeg ffprobe; do
      # shellcheck disable=SC2046 # "path sha256": two words on purpose
      set -- $(mac_ffmpeg_asset "$p" "$b")
      zip="$(fetch "$MAC_FFMPEG_URL/$1" "ffmpeg-mriedl-$MAC_FFMPEG_VERSION-macos-${p#darwin_}-$b.zip" "$2")"
      tmp="$(mktemp -d "$OUT/.ffmpeg.XXXXXX")"
      unzip -q -o "$zip" -d "$tmp"
      [ -f "$tmp/$b" ] || die "$zip has no $b"
      cp "$tmp/$b" "$dir/bin/$b"
      chmod 755 "$dir/bin/$b"
      rm -rf "$tmp"
    done
    cp "$(fetch "$MAC_FFMPEG_LICENSE_URL" "ffmpeg-COPYING.GPLv3-n$MAC_FFMPEG_VERSION" "$MAC_FFMPEG_LICENSE_SHA")" \
      "$dir/licenses/ffmpeg-GPLv3.txt"
    printf 'ffmpeg %s, a static GPL build by Martin Riedl, signed (%s).\nIts source is FFmpeg %s: %s\nThe build scripts: https://gitlab.com/martinr92/ffmpeg\n' \
      "$MAC_FFMPEG_VERSION" "https://ffmpeg.martin-riedl.de" "$MAC_FFMPEG_VERSION" "$(ffmpeg_source "$MAC_FFMPEG_VERSION")" \
      >"$dir/licenses/ffmpeg-SOURCE.txt"
    add_tool ffmpeg "$MAC_FFMPEG_VERSION" "GPL-3.0-or-later" "$(ffmpeg_source "$MAC_FFMPEG_VERSION")"
  elif [ "$p" = windows_amd64 ]; then
    zip="$(fetch "$WIN_FFMPEG_URL/$WIN_FFMPEG_FILE" "$WIN_FFMPEG_FILE" "$WIN_FFMPEG_SHA")"
    tmp="$(mktemp -d "$OUT/.ffmpeg.XXXXXX")"
    top="${WIN_FFMPEG_FILE%.zip}"
    unzip -q -o -j "$zip" "$top/bin/ffmpeg.exe" "$top/bin/ffprobe.exe" "$top/LICENSE" "$top/README.txt" -d "$tmp"
    for b in ffmpeg.exe ffprobe.exe; do
      [ -f "$tmp/$b" ] || die "$WIN_FFMPEG_FILE has no $b"
      cp "$tmp/$b" "$dir/bin/$b"
      chmod 755 "$dir/bin/$b"
    done
    cp "$tmp/LICENSE" "$dir/licenses/ffmpeg-GPLv3.txt"
    cp "$tmp/README.txt" "$dir/licenses/ffmpeg-build-readme.txt"
    rm -rf "$tmp"
    printf 'ffmpeg %s, a static GPL build ("essentials") by Gyan Doshi (%s).\nIts source is FFmpeg %s: %s\n' \
      "$WIN_FFMPEG_VERSION" "https://www.gyan.dev/ffmpeg/builds/" "$WIN_FFMPEG_VERSION" "$(ffmpeg_source "$WIN_FFMPEG_VERSION")" \
      >"$dir/licenses/ffmpeg-SOURCE.txt"
    add_tool ffmpeg "$WIN_FFMPEG_VERSION" "GPL-3.0-or-later" "$(ffmpeg_source "$WIN_FFMPEG_VERSION")"
  fi

  editor=""
  if [ "$p" = "$host" ] && [ -x editor/target/release/retake-edit ]; then
    editor="editor/target/release/retake-edit"
  else
    for a in dist-editor/retake-edit_*_"$p".tar.gz dist-editor/retake-edit_*_"$p".zip; do
      [ -f "$a" ] || continue
      tmp="$(mktemp -d "$OUT/.editor.XXXXXX")"
      case "$a" in
        *.zip) unzip -q -o "$a" -d "$tmp" ;;
        *) tar -xzf "$a" -C "$tmp" ;;
      esac
      [ ! -f "$tmp/retake-edit$exe" ] || { cp "$tmp/retake-edit$exe" "$dir/bin/.retake-edit" && editor="$dir/bin/.retake-edit"; }
      rm -rf "$tmp"
    done
  fi
  if [ -n "$editor" ]; then
    cp "$editor" "$dir/bin/retake-edit$exe"
    chmod 755 "$dir/bin/retake-edit$exe"
    rm -f "$dir/bin/.retake-edit"
  fi

  printf '{\n  "bundle": 1,\n  "tools": {%s\n  }\n}\n' "$tools" >"$dir/retake-bundle.json"
  names=""
  for b in "$dir"/bin/*; do names="$names $(basename "$b")"; done
  printf 'bundle-deps: %s:%s\n' "$p" "$names" >&2
done
