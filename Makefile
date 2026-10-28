# GNU make; recipes are POSIX sh.
MODULE  := github.com/saphan-ai/discover
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X $(MODULE)/internal/cli.version=$(VERSION) -X $(MODULE)/internal/cli.commit=$(COMMIT)
TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build build-all test blackout

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o ./bin/discover ./cmd/discover

build-all:
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=''; \
		if [ "$$os" = windows ]; then ext='.exe'; fi; \
		out="./dist/discover_$${os}_$${arch}$$ext"; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o "$$out" ./cmd/discover; \
	done

test:
	go vet ./... && go test ./...

blackout:
	sh scripts/blackout-check.sh
