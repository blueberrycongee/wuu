.PHONY: build install test vet clean release-dry snapshot

VERSION_FILE := VERSION
BASE_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null || echo "0.1.0")
VERSION ?= v$(BASE_VERSION)-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/blueberrycongee/wuu/internal/version.Version=$(VERSION) \
	-X github.com/blueberrycongee/wuu/internal/version.Commit=$(COMMIT) \
	-X github.com/blueberrycongee/wuu/internal/version.Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/wuu ./cmd/wuu

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/wuu

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -rf bin/ dist/

release-dry:
	goreleaser check
	goreleaser release --snapshot --clean --skip=publish

snapshot:
	goreleaser release --snapshot --clean
