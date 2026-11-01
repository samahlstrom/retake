# syntax=docker/dockerfile:1
# The retake team server (spec/server.md): `retake serve --data /data`, with the review page embedded, ffmpeg, and
# whisper.cpp's whisper-cli built for the CPU. deploy/docker-compose.yml runs it with one volume (docs/hosting.md).
#
#   docker build -t retake .                         # the image
#   docker build --build-arg VERSION=0.3.0 -t retake .
#   docker build --build-arg WHISPER_CPU=portable .  # whisper-cli for CPUs without AVX2 (slower)
#
# JOBS bounds the parallel compile jobs of the whisper.cpp and Go builds.

ARG JOBS=4

# --- 1. the review page (web/dist)
FROM node:26.7.0-trixie-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- 2. the Go binary: static (pure-Go SQLite, Postgres and S3 clients), with the page embedded (-tags webdist)
FROM golang:1.27.1-trixie AS go
ARG VERSION=dev
ARG JOBS
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -p "$JOBS" -tags webdist -trimpath \
      -ldflags "-s -w -X github.com/samahlstrom/retake/internal/cli.version=${VERSION}" \
      -o /out/retake ./cmd/retake

# --- 3. whisper.cpp's whisper-cli for the CPU: the release scripts/install-whisper.sh pins, checked the same way
FROM debian:trixie-slim AS whisper
ARG JOBS
ARG TARGETARCH
# avx2 (the default): x86-64 CPUs from 2013 on (Haswell, Zen). portable: any x86-64 CPU, several times slower.
# Other architectures (arm64) build ggml's generic CPU code either way.
ARG WHISPER_CPU=avx2
ENV TAG=v1.9.5 \
    SHA256=ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates cmake curl g++ make \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fsSL --retry 3 -o whisper.tar.gz "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/$TAG.tar.gz" \
 && echo "$SHA256  whisper.tar.gz" | sha256sum -c - \
 && mkdir src && tar -xzf whisper.tar.gz -C src --strip-components=1
# GGML_NATIVE=OFF: the binary must not depend on the build machine's CPU. OpenMP stays on (libgomp1 at runtime).
RUN set -eu; cpu=""; \
    if [ "$TARGETARCH" = "amd64" ] && [ "$WHISPER_CPU" = "avx2" ]; then \
      cpu="-DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_BMI2=ON"; \
    fi; \
    cmake -S src -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF $cpu \
      -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF -DWHISPER_SDL2=OFF -DWHISPER_CURL=OFF \
 && cmake --build build --config Release --target whisper-cli -j "$JOBS" \
 && install -D -m 0755 build/bin/whisper-cli /opt/whisper/bin/whisper-cli \
 && /opt/whisper/bin/whisper-cli --help >/dev/null

# --- 4. the runtime
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl ffmpeg libgomp1 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 retake \
 && useradd --system --uid 10001 --gid 10001 --home-dir /data --no-create-home --shell /usr/sbin/nologin retake \
 && mkdir -p /data /config/retake \
 && chown -R 10001:10001 /data /config
COPY --from=go /out/retake /usr/local/bin/retake
COPY --from=whisper /opt/whisper/bin/whisper-cli /usr/local/bin/whisper-cli
# The data folder holds the workspaces (a cache with S3), the SQLite stores (without Postgres), uploads in progress,
# and the whisper models, which download on first use into $XDG_CACHE_HOME/retake/models. Mount a config.toml at
# /config/retake/config.toml for [transcribe] and [server] settings.
ENV RETAKE_DATA=/data \
    RETAKE_LISTEN=:8080 \
    HOME=/data \
    XDG_CONFIG_HOME=/config \
    XDG_DATA_HOME=/data/.local/share \
    XDG_STATE_HOME=/data/.local/state \
    XDG_CACHE_HOME=/data/.cache
VOLUME /data
EXPOSE 8080
USER 10001:10001
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["retake"]
CMD ["serve"]
