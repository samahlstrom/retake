#!/bin/sh
# Makes the npm packages from goreleaser's archives in dist/ (`make snapshot`, or the release workflow) into dist/npm/.
# Publishes nothing.
#
#   <name>-<version>.tgz               the `retake` command: a small node launcher (npm/retake-cli), with every
#                                      platform package below as an optional dependency, and for Windows (no
#                                      package) the release archive's URL, SHA-256 and size in "archives": the
#                                      launcher downloads it on first run and refuses it unless the SHA-256 matches
#   <name>-<os>-<cpu>-<version>.tgz    one per platform, macOS and Linux, with os/cpu set so npm installs only the
#                                      one that fits: bin/ holds the release archive (retake, retake-edit, herdr,
#                                      ffmpeg, ffprobe, retake-bundle.json), so retake finds its own tools first
#
# No other package names: npm's spam filter refused retake-cli-win32-x64's first publish, and every new name needs
# its owner to publish it once by hand before trusted publishing works (docs/releasing.md). The SHA-256s come from
# dist/checksums.txt, the one the release carries, and each archive here must match it.
#
# <name> is "name" in npm/retake-cli/package.json, the one place it's set. The version is goreleaser's
# (dist/metadata.json), or --version's, without a +build part, which npm drops.
#
#   scripts/npm-pack.sh                 every platform; stops when an archive or one of its tools is missing
#   scripts/npm-pack.sh --partial       only what's there, with a warning for what isn't (a local snapshot has no
#                                       macOS or Windows retake-edit, which builds only on those systems)
#   scripts/npm-pack.sh --version 0.1.0 the archives of that version in dist/ (a release's, downloaded), no metadata
set -eu

cd "$(dirname "$0")/.."
OUT="dist/npm"
partial=0
VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    --partial) partial=1 ;;
    --version)
      [ $# -ge 2 ] || { echo "npm-pack: --version needs a value" >&2; exit 2; }
      VERSION="${2#v}"
      shift
      ;;
    *)
      echo "usage: scripts/npm-pack.sh [--partial] [--version X.Y.Z]" >&2
      exit 2
      ;;
  esac
  shift
done

die() {
  printf 'npm-pack: %s\n' "$*" >&2
  exit 1
}
warn() { printf 'npm-pack: warning: %s\n' "$*" >&2; }

command -v npm >/dev/null 2>&1 || die "needs npm (mise exec -- scripts/npm-pack.sh)"
NAME="$(node -p 'require("./npm/retake-cli/package.json").name')"
PROJECT="retake"
if [ -z "$VERSION" ]; then
  [ -f dist/metadata.json ] || die "no dist/metadata.json: run make snapshot (or goreleaser) first, or pass --version"
  PROJECT="$(node -p 'require("./dist/metadata.json").project_name')"
  VERSION="$(node -p 'require("./dist/metadata.json").version')" # the archives' names have it as it is
fi
NPM_VERSION="${VERSION%%+*}"

rm -rf "$OUT"
mkdir -p "$OUT/stage"

# goreleaser's os_arch, then npm's os and cpu (process.platform, process.arch).
PLATFORMS="linux_amd64:linux:x64 linux_arm64:linux:arm64 darwin_arm64:darwin:arm64 darwin_amd64:darwin:x64"
# No package: the launcher downloads this archive. Windows on ARM runs the x64 build, emulated (as install.ps1 does).
DOWNLOADS="windows_amd64:win32:x64,arm64"
NEED="$PROJECT retake-edit ffmpeg ffprobe herdr whisper-cli"

downloads=""
for entry in $DOWNLOADS; do
  goos_arch="${entry%%:*}"
  rest="${entry#*:}"
  archive="dist/${PROJECT}_${VERSION}_${goos_arch}.zip"
  if [ ! -f "$archive" ]; then
    [ "$partial" = 1 ] || die "no $archive"
    warn "no $archive: no download for ${rest%%:*}"
    continue
  fi
  have="$(unzip -Z1 "$archive")"
  missing=""
  for f in $NEED; do printf '%s\n' "$have" | grep -qx "$f.exe" || missing="$missing $f.exe"; done
  for f in retake-bundle.json conpty/conpty.dll; do printf '%s\n' "$have" | grep -qx "$f" || missing="$missing $f"; done
  if [ -n "$missing" ]; then
    [ "$partial" = 1 ] || die "$archive has no$missing"
    warn "$archive has no$missing"
  fi
  downloads="$downloads $archive:$rest"
done

made=""
for entry in $PLATFORMS; do
  goos_arch="${entry%%:*}"
  rest="${entry#*:}"
  os="${rest%%:*}"
  cpu="${rest#*:}"
  pkg="$NAME-$os-$cpu"
  archive="dist/${PROJECT}_${VERSION}_${goos_arch}.tar.gz"
  if [ ! -f "$archive" ]; then
    [ "$partial" = 1 ] || die "no $archive"
    warn "no $archive: no $pkg"
    continue
  fi

  stage="$OUT/stage/$pkg"
  mkdir -p "$stage/bin"
  tar -xzf "$archive" -C "$stage/bin"
  # The archive's docs go to the package's top; bin/ keeps the programs and retake-bundle.json side by side.
  rm -f "$stage/bin/README.md" "$stage/bin/CHANGELOG.md"
  for f in LICENSE THIRD_PARTY_NOTICES.md licenses; do
    [ ! -e "$stage/bin/$f" ] || mv "$stage/bin/$f" "$stage/$f"
  done

  missing=""
  for f in $NEED retake-bundle.json; do [ -f "$stage/bin/$f" ] || missing="$missing $f"; done
  if [ -n "$missing" ]; then
    [ "$partial" = 1 ] || die "$archive has no$missing"
    warn "$pkg has no$missing"
  fi

  # shellcheck disable=SC2016 # JavaScript template literals, not shell
  NAME="$NAME" PKG="$pkg" V="$NPM_VERSION" OS="$os" CPU="$cpu" node -e '
    const fs = require("fs");
    const main = require("./npm/retake-cli/package.json");
    const { PKG, V, OS, CPU, NAME } = process.env;
    const label = { linux: "Linux", darwin: "macOS" }[OS] + " " + CPU;
    const p = {
      name: PKG,
      version: V,
      description: `Retake for ${label}: the programs behind the ${NAME} package. Install ${NAME} instead.`,
      license: "Apache-2.0 AND GPL-3.0-or-later",
      homepage: main.homepage,
      repository: main.repository,
      os: [OS],
      cpu: [CPU],
      ...(OS === "linux" ? { libc: ["glibc"] } : {}),
      files: ["bin", "licenses", "THIRD_PARTY_NOTICES.md"],
      preferUnplugged: true,
    };
    fs.writeFileSync(process.argv[1] + "/package.json", JSON.stringify(p, null, 2) + "\n");
    fs.writeFileSync(process.argv[1] + "/README.md",
      `# ${PKG}\n\nThe ${label} programs of [${NAME}](https://www.npmjs.com/package/${NAME}): retake, retake-edit, ` +
      `ffmpeg, ffprobe, herdr and whisper-cli. npm installs it for you on this platform; install ` +
      `${NAME} instead:\n\n    npm i -g ${NAME}\n\nRetake is Apache-2.0. ffmpeg is GPL-3.0-or-later; its license ` +
      `and where its source is are in licenses/, every other component in THIRD_PARTY_NOTICES.md.\n`);
  ' "$stage"
  (cd "$OUT" && npm pack "./stage/$pkg" --silent >/dev/null)
  made="$made $pkg"
done
[ -n "$made" ] || die "no platform package made"

# The command: the launcher, the platform packages as optional dependencies at exactly this version, and the
# archives to download where there's no package: their URL on the GitHub release, SHA-256 (checksums.txt's, which
# the file here must match) and size.
stage="$OUT/stage/$NAME"
mkdir -p "$stage/bin"
cp npm/retake-cli/bin/retake.js "$stage/bin/retake.js"
chmod 755 "$stage/bin/retake.js"
cp npm/retake-cli/README.md LICENSE "$stage/"
# shellcheck disable=SC2016 # JavaScript template literals, not shell
MADE="$made" DOWNLOADS="$downloads" V="$NPM_VERSION" TAG="v$VERSION" node -e '
  const fs = require("fs");
  const crypto = require("crypto");
  const path = require("path");
  const p = require("./npm/retake-cli/package.json");
  const { MADE, DOWNLOADS, V, TAG } = process.env;
  const die = (m) => { console.error("npm-pack: " + m); process.exit(1); };
  p.version = V;
  p.optionalDependencies = Object.fromEntries(MADE.trim().split(/\s+/).map((n) => [n, V]));
  p.archives = {};
  const entries = DOWNLOADS.trim() ? DOWNLOADS.trim().split(/\s+/) : [];
  if (entries.length && !fs.existsSync("dist/checksums.txt")) die("no dist/checksums.txt (the SHA-256 of each archive)");
  const sums = new Map(
    entries.length
      ? fs.readFileSync("dist/checksums.txt", "utf8").split("\n").filter(Boolean).map((l) => {
          const [sum, file] = l.trim().split(/\s+\*?/);
          return [file, sum];
        })
      : [],
  );
  const repo = p.repository.url.replace(/^git\+/, "").replace(/\.git$/, "");
  for (const entry of entries) {
    const [archive, os, cpus] = entry.split(":");
    const file = path.basename(archive);
    const sha256 = sums.get(file);
    if (!sha256) die(`dist/checksums.txt has no ${file}`);
    const here = crypto.createHash("sha256").update(fs.readFileSync(archive)).digest("hex");
    if (here !== sha256) die(`${archive} is not the file in dist/checksums.txt (${here}, not ${sha256})`);
    const download = { url: `${repo}/releases/download/${TAG}/${file}`, sha256, size: fs.statSync(archive).size };
    for (const cpu of cpus.split(",")) p.archives[`${os}-${cpu}`] = download;
  }
  fs.writeFileSync(process.argv[1] + "/package.json", JSON.stringify(p, null, 2) + "\n");
' "$stage"
(cd "$OUT" && npm pack "./stage/$NAME" --silent >/dev/null)
rm -rf "$OUT/stage"

# npmjs.org has no documented size limit, but refuses a tarball somewhere between 118 MB (published fine elsewhere)
# and 199 MB (413 Payload Too Large). Stay under the largest size known to publish.
MAX_BYTES="${NPM_PACK_MAX_BYTES:-117000000}"
big=""
for f in "$OUT"/*.tgz; do
  bytes="$(wc -c <"$f" | tr -d ' ')"
  printf 'npm-pack: %s (%s bytes)\n' "$f" "$bytes" >&2
  [ "$bytes" -le "$MAX_BYTES" ] || big="$big $(basename "$f")"
done
[ -z "$big" ] || die "over $MAX_BYTES bytes, which npm may refuse:$big"
