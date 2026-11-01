.PHONY: build test lint install release deploy

LDFLAGS = -X github.com/dinhhuy258/bx/internal/cli.Version=$$(git describe --tags --always)

# Oracle Incus host and the container that runs bx serve as the user bx.
ORACLE ?= ubuntu@161.118.199.101
CONTAINER ?= bx
REMOTE_BIN = /home/bx/go/bin/bx

build:
	go build -o bx ./cmd/bx
test:
	go test ./...
lint:
	go vet ./... && (command -v golangci-lint >/dev/null && golangci-lint run || true)
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/bx
release:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/bx-linux-amd64 ./cmd/bx
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/bx-linux-arm64 ./cmd/bx
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/bx-darwin-arm64 ./cmd/bx

# Build for the Oracle host, replace the binary in the container and restart bx serve.
# Claude sessions keep running: the unit uses KillMode=process.
deploy:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/bx-linux-arm64 ./cmd/bx
	scp dist/bx-linux-arm64 $(ORACLE):/tmp/bx-linux-arm64
	ssh $(ORACLE) 'set -e; \
		incus file push /tmp/bx-linux-arm64 $(CONTAINER)$(REMOTE_BIN).new --uid 1000 --gid 1000 --mode 755; \
		rm /tmp/bx-linux-arm64; \
		incus exec $(CONTAINER) -- mv $(REMOTE_BIN).new $(REMOTE_BIN); \
		incus exec $(CONTAINER) -- systemctl --user -M bx@ restart bx; \
		sleep 2; \
		incus exec $(CONTAINER) -- systemctl --user -M bx@ is-active bx; \
		incus exec $(CONTAINER) -- $(REMOTE_BIN) version'
