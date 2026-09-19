SHELL := /bin/sh

VERSION ?= $(shell tr -d '\n' < VERSION)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/littlesho/NodeRampart/internal/version.Version=$(VERSION) \
	-X github.com/littlesho/NodeRampart/internal/version.Commit=$(COMMIT) \
	-X github.com/littlesho/NodeRampart/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build test test-race vet fmt-check check package-deb package-rpm clean

all: check build

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/noderampart ./cmd/noderampart
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/noderampartd ./cmd/noderampartd
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/noderampart-sensor ./cmd/noderampart-sensor

test:
	go test ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

check: fmt-check vet test

package-deb: build
	./scripts/build-deb.sh

package-rpm:
	./scripts/build-rpm.sh

clean:
	rm -f bin/noderampart bin/noderampartd bin/noderampart-sensor coverage.out
