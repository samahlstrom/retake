#!/bin/sh
# Writes the Homebrew formula for one release to stdout, from its checksums.txt (docs/releasing.md, "Homebrew"). The
# release workflow pushes it to the tap, samahlstrom/homebrew-retake, as Formula/retake.rb, so that
#
#   brew install samahlstrom/retake/retake
#
# installs that release's archive for the Mac or Linux it runs on: the whole folder into the keg's libexec (retake
# finds its ffmpeg, herdr and retake-edit next to itself through retake-bundle.json), with retake and retake-edit
# linked into Homebrew's bin.
#
#   scripts/brew-formula.sh VERSION CHECKSUMS [BASE_URL]
#     VERSION    0.1.0 (no v)
#     CHECKSUMS  the release's checksums.txt
#     BASE_URL   where the archives are (default: the GitHub release's download folder; file:///… to test locally)
set -eu

[ $# -ge 2 ] || {
  echo "usage: scripts/brew-formula.sh VERSION CHECKSUMS [BASE_URL]" >&2
  exit 2
}
VERSION="${1#v}"
SUMS="$2"
REPO="samahlstrom/retake"
NAME="retake"
BASE="${3:-https://github.com/$REPO/releases/download/v$VERSION}"

die() {
  printf 'brew-formula: %s\n' "$*" >&2
  exit 1
}
[ -f "$SUMS" ] || die "no $SUMS"

# block OS ARCH: the url and sha256 lines for one archive.
block() {
  file="${NAME}_${VERSION}_$1_$2.tar.gz"
  sum="$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1; exit }' "$SUMS")"
  [ -n "$sum" ] || die "$file isn't in $SUMS"
  printf '      url "%s/%s"\n      sha256 "%s"\n' "$BASE" "$file" "$sum"
}

cat <<EOF
# Retake $VERSION. Written by scripts/brew-formula.sh in $REPO; the release workflow replaces it on every release.
class Retake < Formula
  desc "Review videos by talking over them; your AI agent makes the next version"
  homepage "https://github.com/$REPO"
  version "$VERSION"
  license "Apache-2.0"

  on_macos do
    on_arm do
$(block darwin arm64)
    end
    on_intel do
$(block darwin amd64)
    end
  end

  on_linux do
    on_arm do
$(block linux arm64)
    end
    on_intel do
$(block linux amd64)
    end
  end

  def install
    # The release folder as it is: retake-bundle.json makes retake use the ffmpeg, herdr and retake-edit next to it.
    libexec.install Dir["*"]
    bin.install_symlink libexec/"retake", libexec/"retake-edit"
  end

  def caveats
    <<~TEXT
      Start Retake with:
        retake
      It opens in your browser and walks you through setup.
    TEXT
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/retake --version")
    assert_match "libx264", shell_output("#{libexec}/ffmpeg -hide_banner -encoders")
  end
end
EOF
