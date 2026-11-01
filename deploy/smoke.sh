#!/usr/bin/env bash
# Smoke test of the Docker Compose stack with its scale profile (retake + Postgres + MinIO), end to end, then teardown.
#
#   deploy/smoke.sh            # builds the image, runs the test, removes containers, volumes and the image
#   KEEP=1 deploy/smoke.sh     # leaves the stack running
#
# Needs docker (compose v2), ffmpeg, bin/retake (make build) and the e2e suite's node modules (cd e2e && npm ci).
# It uses the compose project retake3b-test (SMOKE_PROJECT) and host port 8941 (SMOKE_PORT); nothing else is
# touched. The transcription model is "tiny" (a 75 MB download on first use), through a mounted config.toml.
set -euo pipefail
cd "$(dirname "$0")/.."
REPO=$PWD
PROJECT=${SMOKE_PROJECT:-retake3b-test}
PORT=${SMOKE_PORT:-8941}
BASE="http://127.0.0.1:$PORT"
WORK="$REPO/.retake-work/smoke-compose"
rm -rf "$WORK"
mkdir -p "$WORK"

cat >"$WORK/config.toml" <<'EOF'
[transcribe]
model = "tiny"
EOF
chmod 0644 "$WORK/config.toml"
cat >"$WORK/compose.override.yml" <<EOF
services:
  retake:
    volumes:
      - $WORK/config.toml:/config/retake/config.toml:ro
EOF

export RETAKE_PORT=$PORT RETAKE_PUBLIC_URL=$BASE
# The scale profile: Postgres and MinIO, with the compose file's default passwords.
export COMPOSE_PROFILES=scale RETAKE_S3_ENDPOINT=minio:9000 \
  RETAKE_DATABASE_URL="postgres://retake:${POSTGRES_PASSWORD:-retake}@postgres:5432/retake?sslmode=disable"
compose() { docker compose -p "$PROJECT" -f deploy/docker-compose.yml -f "$WORK/compose.override.yml" "$@"; }
down() {
  if [ "${KEEP:-}" = 1 ]; then
    echo "smoke: KEEP=1, the stack stays up: docker compose -p $PROJECT -f deploy/docker-compose.yml down -v --rmi local"
    return
  fi
  compose logs retake >"$WORK/retake.log" 2>&1 || true
  compose down -v --rmi local >/dev/null 2>&1 || true
}
trap down EXIT

echo "smoke: building and starting $PROJECT"
compose up -d --build --wait --wait-timeout 300

echo "smoke: the first admin"
link=$(compose exec -T retake retake user add admin@example.com --name Admin --admin --json |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["link"])')
case "$link" in "$BASE/signin/rs_"*) ;; *) echo "smoke: unexpected link: $link"; exit 1 ;; esac

echo "smoke: browser"
(cd e2e && ulimit -c 0 && SMOKE_URL=$BASE SMOKE_LINK=$link SMOKE_WORK=$WORK RETAKE_BIN="$REPO/bin/retake" \
  npx playwright test -c compose.config.ts)

take=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["take"])' "$WORK/result.json")
echo "smoke: take $take; jobs: $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["jobs"])' "$WORK/result.json")"

echo "smoke: the bucket"
compose exec -T minio mc alias set local http://127.0.0.1:9000 "${MINIO_ROOT_USER:-retake}" \
  "${MINIO_ROOT_PASSWORD:-retake-minio-secret}" >/dev/null
for _ in $(seq 1 30); do # uploads follow the writes within a second or two
  objects=$(compose exec -T minio mc ls --recursive "local/${RETAKE_S3_BUCKET:-retake}/projects/demo/")
  if grep -q "$take/seg_01\." <<<"$objects"; then break; fi
  sleep 1
done
echo "  ${objects//$'\n'/$'\n'  }"
grep -q "media/demo_v1.mp4" <<<"$objects" || { echo "smoke: the version isn't in the bucket"; exit 1; }
grep -q "$take/seg_01\." <<<"$objects" || { echo "smoke: the take's audio isn't in the bucket"; exit 1; }
grep -q "$take/session.json" <<<"$objects" || { echo "smoke: the take isn't in the bucket"; exit 1; }
echo "smoke: passed"
