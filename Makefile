.PHONY: build test lint install release
build:
	go build -o bx ./cmd/bx
test:
	go test ./...
lint:
	go vet ./... && (command -v golangci-lint >/dev/null && golangci-lint run || true)
install:
	go install -ldflags "-X github.com/dinhhuy258/bx/internal/cli.Version=$$(git describe --tags --always)" ./cmd/bx
release:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/dinhhuy258/bx/internal/cli.Version=$$(git describe --tags --always)" -o dist/bx-linux-amd64 ./cmd/bx
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X github.com/dinhhuy258/bx/internal/cli.Version=$$(git describe --tags --always)" -o dist/bx-darwin-arm64 ./cmd/bx
