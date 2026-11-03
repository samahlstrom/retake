#!/bin/sh
# Installs Retake from its GitHub release, without Node: downloads the archive for this OS and CPU, checks its SHA-256
# against the release's checksums.txt, and installs it. It never uses sudo.
#
#   curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh | sh -s -- --version 0.1.0
#
# The archive (with herdr, ffmpeg, retake-edit and retake-bundle.json) is unpacked whole into
# ~/.local/share/retake/<version>/, with retake and retake-edit linked into ~/.local/bin and, on Linux, a Retake entry
# in the app menu that runs `retake start --app`. Older versions' folders are removed. A plain archive (just retake)
# is copied into ~/.local/bin. Run it again to update.
#
# Options:
#   --version X.Y.Z   install this version (default: the latest release)
#   --dir DIR         put the commands in DIR (default: $INSTALL_DIR, else ~/.local/bin)
#   --with-editor     also install retake-edit, when the archive doesn't have it
#   --no-menu         no app-menu entry (Linux)
#   -h, --help        show this help
# Environment:
#   INSTALL_DIR       the commands' folder, as --dir
#   INSTALL_REPO      the GitHub owner/repo the releases come from (default samahlstrom/retake)
#   INSTALL_BASE_URL  the folder holding one release's files, instead of GitHub's URL for it ({version} in it is
#                     replaced by the version; https:// or file://). Needs --version.
#   XDG_DATA_HOME     where the versions' folders and the menu entry go (default ~/.local/share)
#
# Linux and macOS, on x86_64 or arm64. Windows has install.ps1 next to this file:
#   irm https://raw.githubusercontent.com/samahlstrom/retake/main/install.ps1 | iex
set -eu

# The product's name isn't final: these are the only places the installer names it.
NAME="retake"
EDITOR_NAME="retake-edit"
# Where releases are published.
REPO="${INSTALL_REPO:-samahlstrom/retake}"
RELEASES="https://github.com/${REPO}/releases"

say() { printf '%s\n' "$*"; }
die() {
  printf '%s install: %s\n' "$NAME" "$*" >&2
  exit 1
}
usage() { sed -n '2,27p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true; }

version=""
dir="${INSTALL_DIR:-}"
with_editor=0
menu=1
while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || die "--version needs a value"
      version="$2"
      shift 2
      ;;
    --version=*)
      version="${1#*=}"
      shift
      ;;
    --dir)
      [ $# -ge 2 ] || die "--dir needs a value"
      dir="$2"
      shift 2
      ;;
    --dir=*)
      dir="${1#*=}"
      shift
      ;;
    --with-editor)
      with_editor=1
      shift
      ;;
    --no-menu)
      menu=0
      shift
      ;;
    -h | --help)
      if [ -f "$0" ]; then
        usage
      else # piped into sh: $0 isn't this file
        say "usage: sh install.sh [--version X.Y.Z] [--dir DIR] [--with-editor] [--no-menu]"
      fi
      exit 0
      ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
done

version="${version#v}"
case "$version" in
  *[!0-9A-Za-z.+-]*) die "--version wants X.Y.Z, not $version" ;;
  "" | [0-9]*.[0-9]*.[0-9]*) ;;
  *) die "--version wants X.Y.Z, not $version" ;;
esac
[ -n "$dir" ] || {
  [ -n "${HOME:-}" ] || die "HOME isn't set: pass --dir"
  dir="$HOME/.local/bin"
}
data="${XDG_DATA_HOME:-}"
case "$data" in
  /*) ;;
  *) data="${HOME:-}/.local/share" ;;
esac

# This machine, in the archives' naming (goreleaser's: linux|darwin, amd64|arm64).
os="$(uname -s)"
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN* | Windows_NT)
    die "on Windows, use install.ps1 in PowerShell: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex"
    ;;
  *) die "no build for $os: the releases cover Linux and macOS (and Windows as a .zip)" ;;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "no build for the $arch CPU: the releases cover x86_64 and arm64" ;;
esac
# A shell under Rosetta reports x86_64 on Apple silicon: install the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi

command -v curl >/dev/null 2>&1 || die "needs curl"
command -v tar >/dev/null 2>&1 || die "needs tar"
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  die "needs sha256sum or shasum to check the download"
fi

if [ -z "$version" ]; then
  [ -z "${INSTALL_BASE_URL:-}" ] || die "INSTALL_BASE_URL needs --version"
  # The latest release, from where /releases/latest redirects (…/releases/tag/vX.Y.Z), so no API token or rate limit.
  latest="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$RELEASES/latest")" ||
    die "no latest release at $RELEASES (check INSTALL_REPO, or pass --version)"
  version="${latest##*/}"
  version="${version#v}"
  case "$version" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) die "couldn't find the latest release at $RELEASES (pass --version)" ;;
  esac
fi

if [ -n "${INSTALL_BASE_URL:-}" ]; then
  base="$(printf '%s' "$INSTALL_BASE_URL" | sed "s/{version}/$version/g")"
else
  base="$RELEASES/download/v$version"
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/$NAME-install.XXXXXX")" || die "couldn't make a temporary folder"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

fetch() { # url out
  curl -fsSL --retry 2 -o "$2" "$1"
}

say "$NAME $version for $os/$arch"
fetch "$base/checksums.txt" "$tmp/checksums.txt" ||
  die "no release $version at $base (checksums.txt not found)"

# unpack BIN: download BIN's archive, check it and unpack it into $tmp/BIN.
unpack() {
  bin="$1"
  archive="${bin}_${version}_${os}_${arch}.tar.gz"
  fetch "$base/$archive" "$tmp/$archive" || die "no $archive in release $version"
  want="$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")"
  [ -n "$want" ] || die "$archive isn't listed in checksums.txt: refusing to install it"
  got="$(sha256 "$tmp/$archive")"
  [ "$got" = "$want" ] || die "checksum mismatch for $archive (got $got, want $want): refusing to install it"
  mkdir -p "$tmp/$bin"
  tar -xzf "$tmp/$archive" -C "$tmp/$bin"
  [ -f "$tmp/$bin/$bin" ] || die "$archive has no $bin binary"
}

# install_one BIN: copy the unpacked BIN into $dir.
install_one() {
  bin="$1"
  mkdir -p "$dir" || die "can't create $dir"
  # Copy next to the target, then rename over it: a running copy keeps working, and nothing is half-written.
  cp "$tmp/$bin/$bin" "$dir/.$bin.new" || die "can't write to $dir"
  chmod 755 "$dir/.$bin.new"
  mv -f "$dir/.$bin.new" "$dir/$bin"
  say "installed $dir/$bin (sha256 verified)"
}

# link BIN FOLDER: $dir/BIN becomes a link to FOLDER/BIN.
link() {
  mkdir -p "$dir" || die "can't create $dir"
  ln -sfn "$2/$1" "$dir/.$1.new" || die "can't write to $dir"
  mv -f "$dir/.$1.new" "$dir/$1"
  say "linked $dir/$1 -> $2/$1"
}

# install_bundle: the whole archive into $data/$NAME/$version, its commands linked into $dir, a menu entry on Linux.
install_bundle() {
  home="$data/$NAME"
  target="$home/$version"
  mkdir -p "$home" || die "can't create $home"
  rm -rf "$home/.new"
  cp -R "$tmp/$NAME" "$home/.new" || die "can't write to $home"
  rm -rf "$target"
  mv "$home/.new" "$target"
  say "installed $NAME $version into $target (sha256 verified)"
  link "$NAME" "$target"
  [ ! -f "$target/$EDITOR_NAME" ] || link "$EDITOR_NAME" "$target"
  # Older versions: nothing links to them any more. A running one keeps its open files.
  for old in "$home"/*; do
    [ -d "$old" ] && [ "$old" != "$target" ] || continue
    rm -rf "$old"
    say "removed the older $(basename "$old")"
  done
  if [ "$os" = linux ] && [ "$menu" = 1 ]; then
    apps="$data/applications"
    mkdir -p "$apps" || die "can't create $apps"
    printf '%s\n' "[Desktop Entry]" "Type=Application" "Name=Retake" "Comment=Review videos with an AI agent" \
      "Exec=$dir/$NAME start --app" "Terminal=false" "Categories=AudioVideo;Video;" >"$apps/$NAME.desktop"
    say "added Retake to the app menu"
  fi
}

unpack "$NAME"
if [ -f "$tmp/$NAME/retake-bundle.json" ]; then
  install_bundle
  if [ "$with_editor" = 1 ] && [ ! -f "$tmp/$NAME/$EDITOR_NAME" ]; then
    unpack "$EDITOR_NAME"
    install_one "$EDITOR_NAME"
  fi
else
  install_one "$NAME"
  if [ "$with_editor" = 1 ]; then
    unpack "$EDITOR_NAME"
    install_one "$EDITOR_NAME"
  fi
fi

case ":${PATH:-}:" in
  *":$dir:"*) ;;
  *) say "note: $dir isn't on your PATH. Add it, e.g. in ~/.profile: export PATH=\"$dir:\$PATH\"" ;;
esac
if [ -f "$tmp/$NAME/retake-bundle.json" ]; then
  if [ "$os" = linux ] && [ "$menu" = 1 ]; then
    say "start it: run $NAME (or open Retake from your app menu)"
  else
    say "start it: run $NAME"
  fi
else
  say "run: $NAME version"
fi
