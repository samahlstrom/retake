#!/bin/sh
# One-time npm setup for the owner (docs/releasing.md, "One-time setup"). npm's trusted publishing (the release
# workflow publishes with no token) can only be turned on for a package that exists, so each name is published once
# by hand first, as a 0.0.0 placeholder, and then told to trust .github/workflows/release.yml in samahlstrom/retake.
#
#   scripts/npm-claim.sh             says what it would do; changes nothing
#   scripts/npm-claim.sh --publish   does it: needs `npm login` first (an npmjs.com account with 2FA), npm 11.10+
#
# The names: "name" in npm/retake-cli/package.json, and one per platform (the PLATFORMS of scripts/npm-pack.sh).
set -eu

cd "$(dirname "$0")/.."
NAME="$(node -p 'require("./npm/retake-cli/package.json").name')"
REPO="samahlstrom/retake"
WORKFLOW="release.yml"
SUFFIXES="linux-x64 linux-arm64 darwin-arm64 darwin-x64 win32-x64"
publish=0
[ "${1:-}" != "--publish" ] || publish=1

names="$NAME"
for s in $SUFFIXES; do names="$names $NAME-$s"; done

if [ "$publish" = 0 ]; then
  echo "This would publish a 0.0.0 placeholder of each of these, then trust $REPO's $WORKFLOW to publish them:"
  for n in $names; do echo "  $n"; done
  echo "Run it with --publish after npm login. Nothing was changed."
  exit 0
fi

npm whoami >/dev/null 2>&1 || {
  echo "npm-claim: not logged in: run npm login first" >&2
  exit 1
}
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for n in $names; do
  if npm view "$n" name >/dev/null 2>&1; then
    echo "npm-claim: $n exists already, not publishing a placeholder"
  else
    mkdir -p "$tmp/$n/bin"
    if [ "$n" = "$NAME" ]; then
      printf '#!/usr/bin/env node\nconsole.error("Retake is not released yet: https://github.com/%s");\nprocess.exit(1);\n' \
        "$REPO" >"$tmp/$n/bin/retake.js"
      bin=',"bin":{"retake":"bin/retake.js"}'
    else
      bin=""
    fi
    printf '{"name":"%s","version":"0.0.0","description":"Placeholder: Retake is not released yet.","license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/%s.git"}%s}\n' \
      "$n" "$REPO" "$bin" >"$tmp/$n/package.json"
    printf '# %s\n\nA placeholder until the first release of [Retake](https://github.com/%s).\n' "$n" "$REPO" >"$tmp/$n/README.md"
    (cd "$tmp/$n" && npm publish --access public)
  fi
  # A package that trusts the workflow already answers 409 Conflict: note it and go on to the next one (npm's own
  # output stays on the terminal, so its browser sign-in still works).
  if ! npm trust github "$n" --file "$WORKFLOW" --repo "$REPO" --allow-publish --yes; then
    echo "npm-claim: $n: no new trust added (a 409 above means it trusts the workflow already); going on"
  fi
done
echo "npm-claim: done. The release workflow can publish $NAME and its platform packages now."
