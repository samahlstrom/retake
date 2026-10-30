#!/bin/sh
# Prints the build's version as semver, from the newest vX.Y.Z git tag:
#   on a tag vX.Y.Z                       X.Y.Z
#   N commits after vX.Y.Z                X.Y.(Z+1)-dev.N+g<sha>
#   N commits after vX.Y.Z-rc.1           X.Y.Z-dev.N+g<sha>
#   no vX.Y.Z tag yet                     0.0.0-dev+g<sha>
#   uncommitted changes                   ".dirty" added to the build metadata (+g<sha>.dirty, or +dirty on a tag)
#   not a git checkout (a source tarball) 0.0.0-dev
# The Makefile passes it to the linker, and `retake version` prints it. A release is cut by pushing a vX.Y.Z tag.
#
# usage: scripts/version.sh
#        scripts/version.sh --from TAG N SHA DIRTY   format given inputs, without git (tests; TAG may be "", DIRTY 0|1)
set -eu

# semver TAG N SHA DIRTY: the version for N commits after TAG at commit SHA.
semver() {
  tag="$1" n="$2" sha="$3" dirty=""
  [ "$4" = 1 ] && dirty="dirty"
  if [ -z "$tag" ]; then
    echo "0.0.0-dev+g${sha}${dirty:+.$dirty}"
    return
  fi
  base="${tag#v}"
  if [ "$n" = 0 ]; then
    echo "${base}${dirty:++$dirty}"
    return
  fi
  core="${base%%-*}" # without the tag's own pre-release suffix
  major="${core%%.*}"
  rest="${core#*.}"
  minor="${rest%%.*}"
  patch="${rest#*.}"
  case "$major.$minor.$patch" in
    *[!0-9.]* | *..* | .* | *.)
      echo "version.sh: tag $tag isn't vX.Y.Z" >&2
      return 1
      ;;
  esac
  # A pre-release of the next patch, so it sorts after the tag and before the next release. After an rc tag, the
  # same X.Y.Z: still before the release it leads to.
  [ "$core" = "$base" ] && patch=$((patch + 1))
  echo "${major}.${minor}.${patch}-dev.${n}+g${sha}${dirty:+.$dirty}"
}

if [ "${1:-}" = "--from" ]; then
  [ $# = 5 ] || { echo "usage: version.sh --from TAG N SHA DIRTY" >&2; exit 2; }
  semver "$2" "$3" "$4" "$5"
  exit
fi

cd "$(dirname "$0")/.."
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "0.0.0-dev"
  exit 0
fi
sha="$(git rev-parse --short=7 HEAD)"
dirty=0
[ -n "$(git status --porcelain --untracked-files=no)" ] && dirty=1
tag="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)"
n=0
[ -n "$tag" ] && n="$(git rev-list --count "${tag}..HEAD")"
semver "$tag" "$n" "$sha" "$dirty"
