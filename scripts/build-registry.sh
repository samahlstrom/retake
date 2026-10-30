#!/usr/bin/env bash
# Builds library/index.json: a static registry of the starter library (spec/components.md, "Registries"), with every
# package validated and its license gated by `retake lib pack`. Serve library/ from any static host, or point
# `retake lib add --registry` at library/index.json.
#
# usage: scripts/build-registry.sh [--retake BIN]
#   --retake BIN  the retake binary (default: bin/retake, else one built into .retake-work/)
# needs: go (through mise) when bin/retake isn't built
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
retake=""
while [ $# -gt 0 ]; do
  case "$1" in
    --retake) retake="${2:?--retake needs a path}"; shift ;;
    --retake=*) retake="${1#*=}" ;;
    -h | --help) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "build-registry: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done
if [ -z "$retake" ]; then
  if [ -x "$ROOT/bin/retake" ]; then
    retake="$ROOT/bin/retake"
  else
    mkdir -p "$ROOT/.retake-work"
    retake="$ROOT/.retake-work/retake-registry"
    (cd "$ROOT" && nice -n 19 go build -o "$retake" ./cmd/retake)
  fi
fi

index="$ROOT/library/index.json"
rm -f "$index"
n=0
for dir in "$ROOT"/library/kits/*/ "$ROOT"/library/components/*/; do
  # Packing reads no catalog of yours: the library's packages name only placeholder assets.
  RETAKE_PROJECT="$ROOT/.retake-work/no-project" "$retake" lib pack "${dir%/}" --index "$index" >/dev/null
  n=$((n + 1))
done
echo "$index: $n packages"
