.PHONY: setup dev docs-dev check repository-check repository-metadata-check version-check eval-check test-policy-check theme-contract-check generate-theme-contract theme-surface-matrix-check generate-theme-surface-matrix check-go check-desktop check-clients check-docs test test-go \
	test-go-uncached test-desktop test-clients test-native build build-go build-desktop \
	build-clients build-docs build-macos ci install vet clean release-check \
	print-version tag-release version-check release-prepare

VERSION_FILE := VERSION
BASE_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null || echo "0.1.0")
BUILD_VERSION ?= v$(BASE_VERSION)-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/blueberrycongee/wuu/internal/version.Version=$(BUILD_VERSION) \
	-X github.com/blueberrycongee/wuu/internal/version.Commit=$(COMMIT) \
	-X github.com/blueberrycongee/wuu/internal/version.Date=$(DATE)
GO_DIRS := cmd internal packages/plugin-go plugins prompts
GO_PACKAGES := ./cmd/... ./internal/... ./packages/plugin-go/... ./plugins/... ./prompts/...

setup:
	npm ci --prefix desktop
	npm ci --prefix clients/core
	npm ci --prefix clients/mobile
	npm ci --prefix clients/mobile-web
	npm ci --prefix packages/plugin-sdk
	npm ci --prefix packages/protocol
	npm ci --prefix docs-site --no-audit --no-fund

dev:
	cd desktop && npm run dev

docs-dev:
	npm --prefix docs-site run dev

check: repository-check check-go check-desktop check-clients

repository-check: repository-metadata-check test-policy-check

repository-metadata-check: version-check eval-check theme-contract-check

eval-check:
	node scripts/check-evals.mjs --self-test
	node scripts/check-evals.mjs

test-policy-check:
	node scripts/check-test-policy.mjs --self-test
	node scripts/check-test-policy.mjs

theme-contract-check:
	node scripts/generate-desktop-theme-contract.mjs --check

generate-theme-contract:
	node scripts/generate-desktop-theme-contract.mjs

theme-surface-matrix-check:
	cd desktop && ./node_modules/.bin/vite-node ../scripts/generate-theme-surface-matrix.ts --check

generate-theme-surface-matrix:
	cd desktop && ./node_modules/.bin/vite-node ../scripts/generate-theme-surface-matrix.ts

check-go:
	go mod tidy -diff
	@unformatted="$$(find $(GO_DIRS) -name '*.go' -type f -exec gofmt -l {} +)"; \
		test -z "$$unformatted" || { echo "Go files need formatting:"; echo "$$unformatted"; exit 1; }
	go vet $(GO_PACKAGES)
	GOOS=windows go build $(GO_PACKAGES)
	GOOS=darwin go build $(GO_PACKAGES)

check-desktop:
	npm --prefix desktop run typecheck

check-clients:
	npm --prefix packages/protocol run typecheck
	npm --prefix packages/plugin-sdk run typecheck
	npm --prefix clients/core run typecheck
	npm --prefix clients/mobile run typecheck
	npm --prefix clients/mobile-web run typecheck

check-docs:
	npm --prefix docs-site run check

test: test-go test-desktop test-clients

test-go:
	go test $(GO_PACKAGES)

test-go-uncached:
	go test $(GO_PACKAGES) -count=1

test-desktop:
	npm --prefix desktop test

test-clients:
	npm --prefix packages/plugin-sdk test
	npm --prefix clients/core test
	npm --prefix clients/mobile test
	npm --prefix clients/mobile-web test

test-native:
	npm --prefix desktop run test:cua-mac

build: build-go build-desktop build-clients

build-go:
	go build -ldflags "$(LDFLAGS)" -o bin/wuu ./cmd/wuu

build-desktop:
	npm --prefix desktop run build

build-clients:
	npm --prefix clients/mobile run export:web
	npm --prefix clients/mobile-web run build

build-docs:
	npm --prefix docs-site run build

build-macos:
	npm --prefix desktop run pack:mac

ci: check test build

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/wuu

vet:
	go vet $(GO_PACKAGES)

clean:
	rm -rf bin/ dist/

release-check: version-check check-go test-go-uncached test-desktop test-native

version-check:
	node scripts/release-version.mjs check

release-prepare:
	@test -n "$(RELEASE_VERSION)" || { echo "usage: make release-prepare RELEASE_VERSION=0.4.0"; exit 1; }
	node scripts/release-version.mjs prepare "$(RELEASE_VERSION)"

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
	node scripts/release-version.mjs check "v$(BASE_VERSION)"
	git tag -a "v$(BASE_VERSION)" -m "release v$(BASE_VERSION)"
	@echo "created tag v$(BASE_VERSION)"
	@echo "push with: git push origin v$(BASE_VERSION)"
