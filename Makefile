.PHONY: build install test vet clean release-dry snapshot print-version tag-release zig-lib

VERSION_FILE := VERSION
BASE_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null || echo "0.1.0")
VERSION ?= v$(BASE_VERSION)-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/blueberrycongee/wuu/internal/version.Version=$(VERSION) \
	-X github.com/blueberrycongee/wuu/internal/version.Commit=$(COMMIT) \
	-X github.com/blueberrycongee/wuu/internal/version.Date=$(DATE)

zig-lib:
	cd internal/jsonl/zig && zig build

build: zig-lib
	go build -ldflags "$(LDFLAGS)" -o bin/wuu ./cmd/wuu

install: zig-lib
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

print-version:
	@echo v$(BASE_VERSION)

tag-release:
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "working tree is dirty; commit or stash changes first"; \
		exit 1; \
	fi
	@if git rev-parse --verify --quiet "v$(BASE_VERSION)" >/dev/null; then \
		echo "tag v$(BASE_VERSION) already exists"; \
		exit 1; \
	fi
	git tag -a "v$(BASE_VERSION)" -m "release v$(BASE_VERSION)"
	@echo "created tag v$(BASE_VERSION)"
	@echo "push with: git push origin v$(BASE_VERSION)"
