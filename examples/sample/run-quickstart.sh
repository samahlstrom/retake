#!/usr/bin/env bash
# Runs docs/quickstart.md from start to end without a person: the pre-made take stands in for recording with a mic,
# a fixed patch (patches/02-v3.json) stands in for the agent, and headless Chromium presses C. It's the quickstart's
# smoke test; every command it runs is one the quickstart tells you to run.
#
# usage: examples/sample/run-quickstart.sh [DIR]     (default DIR: .retake-work/quickstart, deleted when it passes)
# env:   RETAKE, RETAKE_EDIT   the binaries (default: bin/retake and editor/target/release/retake-edit, else $PATH)
#        QUICKSTART_PORT       the first of two ports for `retake open` (default 8931)
#        CHROMIUM_PATH         for the C check (default /usr/lib/chromium/chromium; skipped without one, or without
#                              e2e/node_modules from `cd e2e && npm ci`)
#        QUICKSTART_KEEP=1     keep DIR after a passing run
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIR="${1:-$ROOT/.retake-work/quickstart}"
PORT="${QUICKSTART_PORT:-8931}"
pick() { # pick <repo path> <name>: the repo's build, else the one on PATH
  if [ -x "$ROOT/$1" ]; then echo "$ROOT/$1"; else command -v "$2" || { echo "quickstart: $2 not found" >&2; exit 1; }; fi
}
RETAKE="${RETAKE:-$(pick bin/retake retake)}"
EDIT="${RETAKE_EDIT:-$(pick editor/target/release/retake-edit retake-edit)}"
CHROME="${CHROMIUM_PATH:-/usr/lib/chromium/chromium}"

PIDS=()
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
  for p in "${PIDS[@]}"; do wait "$p" 2>/dev/null || true; done
}
trap cleanup EXIT
step() { printf '\n$'; printf ' %q' "$@"; printf '\n'; }
run() { step "$@"; "$@"; }
wait_up() { # port pid
  for _ in $(seq 100); do
    curl -sf "http://127.0.0.1:$1/api/ping" >/dev/null && return 0
    kill -0 "$2" 2>/dev/null || return 1
    sleep 0.1
  done
  return 1
}

# A config of its own: an author name, and the small whisper model if transcription is set up.
export XDG_CONFIG_HOME="$DIR/config"
mkdir -p "$XDG_CONFIG_HOME/retake"
printf 'name = "Quickstart Agent"\nopen_browser = false\n\n[transcribe]\nmodel = "base"\n' \
  >"$XDG_CONFIG_HOME/retake/config.toml"
SAMPLE="$DIR/retake-sample"
# The commands below read as in the quickstart: plain names, paths relative to the sample folder.
mkdir -p "$DIR/bin"
ln -sf "$RETAKE" "$DIR/bin/retake"
ln -sf "$EDIT" "$DIR/bin/retake-edit"
export PATH="$DIR/bin:$PATH"

# 1. The sample, with the pre-made take on v2 (instead of pressing R and talking).
step scripts/make-sample.sh "$SAMPLE" --with-take
"$ROOT/scripts/make-sample.sh" "$SAMPLE" --with-take | grep '^=='
cd "$SAMPLE"
V2=how-to/v2/how-to_v2.mp4

# 2. Open v2. The page would show the take's stroke in its history.
step retake open "$V2" --no-open --port "$PORT" --idle-exit 0
retake open "$V2" --no-open --port "$PORT" --idle-exit 0 >"$DIR/open-v2.log" 2>&1 &
PIDS+=($!)
wait_up "$PORT" "${PIDS[-1]}" || { cat "$DIR/open-v2.log"; exit 1; }
cat "$DIR/open-v2.log"
curl -sf "http://127.0.0.1:$PORT/api/info" |
  node -e 'const i=JSON.parse(require("fs").readFileSync(0,"utf8")); console.log("versions:", i.versions.map(v=>v.label).join(" "), "| takes on v2:", i.sessions.length)'

# 3. Turn the take into notes: transcribe what needs it, run the notes engine, sync the store.
run retake notes sync "$V2"
run retake notes list "$V2"

# 4. The agent: it reads the note, patches the timeline (the patch answers it), then renders v3 and pushes it; the
#    changelog comes from the patch.
run retake-edit patch apply edit/timeline.json edit/patches/02-v3.json
run retake push "$V2" --render --no-stream --parent v2
run retake versions "$V2"
run retake notes list "$V2"
kill "${PIDS[-1]}" && wait "${PIDS[-1]}" 2>/dev/null || true

# 5. Open v3 and press C: it flips to v2 at the same moment.
V3=how-to/v3/how-to_v3.mp4
P3=$((PORT + 1))
step retake open "$V3" --no-open --port "$P3" --idle-exit 0
retake open "$V3" --no-open --port "$P3" --idle-exit 0 >"$DIR/open-v3.log" 2>&1 &
PIDS+=($!)
wait_up "$P3" "${PIDS[-1]}" || { cat "$DIR/open-v3.log"; exit 1; }
# What the sidebar shows on v2 now that v3 answered its note: the note as resolved, with the change.
step curl -s "http://127.0.0.1:$P3/api/notes?v=v2"
curl -sf "http://127.0.0.1:$P3/api/notes?v=v2"
echo
if [ -x "$CHROME" ] && [ -d "$ROOT/e2e/node_modules/@playwright/test" ]; then
  printf "\n(headless Chromium: seek to 11.0 s, press C, press C again)\n"
  (cd "$ROOT/e2e" && ulimit -c 0 && CHROME="$CHROME" URL="http://127.0.0.1:$P3/" node --input-type=module -e '
    import { chromium } from "@playwright/test";
    const browser = await chromium.launch({ executablePath: process.env.CHROME, headless: true });
    try {
      const page = await browser.newPage();
      await page.goto(process.env.URL);
      await page.waitForFunction(() => window.__review?.info && document.querySelector("video").readyState >= 1);
      await page.evaluate(() => { document.querySelector("video").currentTime = 11; });
      await page.waitForTimeout(500);
      const show = async (key) => {
        if (key) { await page.keyboard.press(key); await page.waitForFunction(() => !window.__review.view.switching); }
        await page.waitForTimeout(300);
        return page.evaluate(() => ({ ...window.__review.view, t: +document.querySelector("video").currentTime.toFixed(2) }));
      };
      const a = await show(null), b = await show("c"), c = await show("c");
      console.log("before C:", JSON.stringify(a)); console.log("after C: ", JSON.stringify(b)); console.log("C again:", JSON.stringify(c));
      if (b.shown !== "v2" || !b.flipped || Math.abs(b.t - a.t) > 0.05 || c.shown !== "v3" || c.flipped) process.exit(1);
    } finally { await browser.close(); }
  ')
else
  echo "(no headless Chromium or e2e/node_modules: skipping the C check)"
fi

echo
echo "quickstart: PASS"
if [ "${QUICKSTART_KEEP:-}" = 1 ]; then echo "kept $DIR"; else cleanup; PIDS=(); rm -rf "$DIR"; fi
