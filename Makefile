# Run through mise so the pinned toolchain is used: `mise exec -- make build`, or activate mise in your shell.
.PHONY: build web test lint e2e clean

build: web
	go build -o bin/retake ./cmd/retake

web:
	cd web && npm ci && npm run build

test:
	go test ./...
	cd web && npm test

lint:
	golangci-lint run
	cd web && npm run check && npm run lint

e2e: build
	cd e2e && npm ci && npm test

clean:
	rm -rf bin web/dist
