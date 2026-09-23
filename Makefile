# jenkins-mcp — build & quality targets

BINARY      := jenkins
PKG         := ./cmd/jenkins
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -ldflags "-X github.com/rangertaha/jenkins-mcp/internal.version=$(VERSION)"
GOFILES     := $(shell find . -name '*.go' -not -path './vendor/*')
# Release bump. With svu installed, the next version is computed from
# conventional-commit history; override with BUMP=major|minor|patch or TAG=vX.Y.Z.
BUMP        ?=
# Resolve golangci-lint from PATH, else the Go install dir (GOBIN or GOPATH/bin).
GOPATH_BIN  := $(if $(shell go env GOBIN),$(shell go env GOBIN),$(shell go env GOPATH)/bin)
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo $(GOPATH_BIN)/golangci-lint)
SVU         ?= $(shell command -v svu 2>/dev/null || echo $(GOPATH_BIN)/svu)
SHELL       := /usr/bin/env bash

.DEFAULT_GOAL := help

.PHONY: help all build install test e2e cover vet fmt fmt-check lint tidy clean run version next bump snapshot

## help: show self-documenting target list
help:
	@awk 'BEGIN {printf "\nUsage:\n  make <target>\n\nTargets:\n"} /^## / {doc = substr($$0, 4); next} /^[a-zA-Z0-9_.-]+:/ {if (doc != "") {split($$1, t, ":"); printf "  %-18s %s\n", t[1], doc; doc = ""}}' $(MAKEFILE_LIST)

## all: run the full check + build pipeline (fmt-check, vet, lint, test, build)
all: fmt-check vet lint test build

## build: compile the server binary into ./bin
build:
	@mkdir -p bin
	go build -trimpath $(LDFLAGS) -o bin/$(BINARY) $(PKG)

## install: install the server into $GOBIN
install:
	go install -trimpath $(LDFLAGS) $(PKG)

## test: run the test suite with the race detector
test:
	go test -race ./...

## e2e: run end-to-end tests against a real Jenkins in Docker (needs Docker; several minutes)
# -count=1 is required, not just hygiene: the suite builds the binary in a
# subprocess, so Go's test cache has no idea it depends on internal/**. Without
# it, a run after changing the server happily replays a stale PASS.
e2e:
	@command -v docker >/dev/null 2>&1 || { echo "docker not found; the e2e suite needs a running Docker daemon"; exit 1; }
	go test -tags=e2e -count=1 -timeout=20m -v ./e2e/...

## cover: run tests and open a coverage summary
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## vet: run go vet, including the build-tagged e2e sources
# The second pass is not redundant: e2e is behind //go:build e2e, so the
# untagged run never compiles it and it can stop building unnoticed.
vet:
	go vet ./...
	go vet -tags=e2e ./...

## lint: run golangci-lint (config in .golangci.yml)
lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found. Install: https://golangci-lint.run/welcome/install/"; exit 1; }
	$(GOLANGCI_LINT) run --build-tags=e2e ./...

## fmt: format all Go files
fmt:
	gofmt -w $(GOFILES)

## fmt-check: fail if any file is not gofmt-clean
fmt-check:
	@out=$$(gofmt -l $(GOFILES)); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

## tidy: tidy go.mod/go.sum
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin dist coverage.out

## run: build and run (expects the JENKINS_* env vars in the environment)
run: build
	./bin/$(BINARY)

## version: print the version that build/install would embed
version:
	@echo $(VERSION)

## next: print the next version svu would compute (no tag); honors BUMP=major|minor|patch
next:
	@command -v $(SVU) >/dev/null 2>&1 || { echo "svu not found. Install: go install github.com/caarlos0/svu@latest"; exit 1; }
	@if [ -n "$(BUMP)" ]; then $(SVU) $(BUMP); else $(SVU) next; fi

## bump: tag a release; version from svu (conventional commits), override with BUMP=... or TAG=vX.Y.Z
bump:
	@test -z "$$(git status --porcelain)" || { echo "working tree is dirty; commit or stash first"; exit 1; }
	@latest=$$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0); \
	if [ -n "$(TAG)" ]; then \
	  new="$(TAG)"; \
	elif command -v $(SVU) >/dev/null 2>&1; then \
	  if [ -n "$(BUMP)" ]; then new=$$($(SVU) $(BUMP)); else new=$$($(SVU) next); fi; \
	else \
	  echo "svu not found; computing $(or $(BUMP),patch) bump manually. Install svu for conventional-commit versioning: go install github.com/caarlos0/svu@latest"; \
	  ver=$${latest#v}; maj=$${ver%%.*}; rest=$${ver#*.}; min=$${rest%%.*}; pat=$${rest##*.}; \
	  case "$(or $(BUMP),patch)" in \
	    major) maj=$$((maj+1)); min=0; pat=0;; \
	    minor) min=$$((min+1)); pat=0;; \
	    patch) pat=$$((pat+1));; \
	    *) echo "BUMP must be major, minor, or patch (got '$(BUMP)')"; exit 1;; \
	  esac; \
	  new="v$$maj.$$min.$$pat"; \
	fi; \
	case "$$new" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "version must look like vX.Y.Z (got '$$new')"; exit 1;; esac; \
	git rev-parse -q --verify "refs/tags/$$new" >/dev/null && { echo "tag $$new already exists"; exit 1; }; \
	echo "Bumping $$latest -> $$new"; \
	git tag -a "$$new" -m "Release $$new"; \
	echo "Created tag $$new. Push it to trigger the release workflow:"; \
	echo "  git push origin $$new"

## snapshot: build release artifacts locally with GoReleaser (no publish)
snapshot:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not found. Install: https://goreleaser.com/install/"; exit 1; }
	goreleaser release --snapshot --clean
