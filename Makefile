# Run through mise so the pinned toolchain is used: `mise exec -- make build`, or activate mise in your shell.
.PHONY: build server web test test-go lint lint-go e2e parity snapshot mac-app npm-pack npm-smoke version clean

# The binary's name. The product name isn't final: renaming it means changing this, project_name in .goreleaser.yaml
# and NAME in install.sh.
NAME ?= retake
# The Go module path, read from go.mod, so a new module path needs no change here.
MODULE := $(shell go list -m 2>/dev/null)
# Semver from the newest vX.Y.Z git tag (scripts/version.sh). `retake version` prints it.
VERSION ?= $(shell scripts/version.sh)
GO_LDFLAGS := -X $(MODULE)/internal/cli.version=$(VERSION)

# The page (web/dist) is compiled into the binary only with -tags webdist; see web/embed.go.
build: web
	go build -tags webdist -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/$(NAME) ./cmd/retake

# The binary without the page: / says "page not built"; use --ui-dir to serve a page folder.
server:
	go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/$(NAME) ./cmd/retake

web:
	cd web && npm ci && npm run build

test: test-go
	cd web && npm test && npx vitest run -c vite.scripts.config.ts
	node --test npm/launcher.test.js

test-go:
	go test -race ./...

lint: lint-go
	cd web && npm run check && npm run lint && npm run check:scripts

lint-go:
	golangci-lint run

e2e: build
	cd e2e && npm ci && npm test

# The legacy player's own end-to-end driver against this server (needs the legacy files; skips without them).
parity: server
	scripts/legacy-parity.sh

# Release archives for every platform in dist/, built by .goreleaser.yaml, with the tools that ship with retake
# (scripts/bundle-deps.sh; downloads are pinned and cached). Publishes nothing. `make mac-app` then makes Retake.app.
# Linux's ffmpeg is built from source in Docker the first time (scripts/ffmpeg-build/build.sh, cached), unless
# RETAKE_FFMPEG_DIR points at a build (a release run's ffmpeg-linux-* artifacts, downloaded into one folder).
snapshot:
	scripts/bundle-deps.sh
	nice -n 19 goreleaser release --snapshot --clean --parallelism 2

# The Mac app (unsigned, universal) from the darwin archives in dist/: dist/Retake.app and its zip (scripts/macos-app.sh).
mac-app:
	scripts/macos-app.sh

# The npm packages from the snapshot's archives, into dist/npm/ (scripts/npm-pack.sh). --partial: a local snapshot has
# no macOS or Windows retake-edit, which only builds on those systems. Publishes nothing.
npm-pack:
	scripts/npm-pack.sh --partial

# The npm install end to end against a registry of its own on 127.0.0.1 (scripts/npm-smoke.mjs): npm i -g, the bundled
# ffmpeg, the setup page, an agent calling retake with nothing on PATH, npx's cache.
npm-smoke:
	node scripts/npm-smoke.mjs

version:
	@echo $(VERSION)

clean:
	rm -rf bin web/dist dist dist-bundle dist-ffmpeg
