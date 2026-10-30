# Run through mise so the pinned toolchain is used: `mise exec -- make build`, or activate mise in your shell.
.PHONY: build server web test test-go lint lint-go e2e parity clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS := -X github.com/samahlstrom/retake/internal/cli.version=$(VERSION)

# The page (web/dist) is compiled into the binary only with -tags webdist; see web/embed.go.
build: web
	go build -tags webdist -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/retake ./cmd/retake

# The binary without the page: / says "page not built"; use --ui-dir to serve a page folder.
server:
	go build -trimpath -ldflags "$(GO_LDFLAGS)" -o bin/retake ./cmd/retake

web:
	cd web && npm ci && npm run build

test: test-go
	cd web && npm test

test-go:
	go test -race ./...

lint: lint-go
	cd web && npm run check && npm run lint

lint-go:
	golangci-lint run

e2e: build
	cd e2e && npm ci && npm test

# The legacy player's own end-to-end driver against this server (needs the legacy files; skips without them).
parity: server
	scripts/legacy-parity.sh

clean:
	rm -rf bin web/dist
