SHELL := bash

# --match keeps the stamp inside the release tag namespace, the same one
# cliff.toml selects with tag_pattern (C6.2). Without it `git describe`
# also picks up milestone tags like `phase-1`, and a build stamps
# `phase-1-10-gabc1234` — a version string that matches no release.
VERSION := $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Every shell script the repo carries. The pre-commit hook checks only the
# files a commit touches; this list is what `make lint`, and so CI's lint
# job, checks in full.
SHELLCHECK_FILES := .envrc contrib/check-commit-msg contrib/check-gofumpt

.PHONY: fmt lint test check hooks build cross-compile changelog release-notes

fmt:
	gofumpt -w .

lint:
	shellcheck $(SHELLCHECK_FILES)
	golangci-lint run

test:
	go test ./...

check: lint test

# Both hook types on purpose (C6.6): the commit-msg hook does not install
# with the default stage.
hooks:
	pre-commit install --hook-type pre-commit --hook-type commit-msg

# CGO_ENABLED=0 everywhere in this file (and in CI, and in the Nix package)
# is load-bearing, not a default we happened to keep (C1.1): it's what
# makes the single-static-binary/cross-compile promise hold regardless of
# later dependency choices. In particular it constrains any future SQLite
# dependency to a pure-Go driver (modernc.org/sqlite), never
# mattn/go-sqlite3.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/clast ./cmd/clast

cross-compile:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/clast-darwin-arm64 ./cmd/clast
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/clast-linux-amd64  ./cmd/clast

# CHANGELOG.md is generated per-release, never committed (C6.3): the tag is
# the only human action, and git-cliff derives everything else from the
# conventional-commit history. `changelog` writes the full history for
# attachment as a release asset; `release-notes` writes just the slice
# belonging to TAG, header-stripped, for the GitHub release body.
changelog:
	mkdir -p dist
	git-cliff --output dist/CHANGELOG.md

# TAG may name a tag that already exists (CI, where the push of the tag is
# what started us) or one about to be cut (a local dry run before tagging).
# Those need different git-cliff selections, so probe for the ref first.
release-notes:
	@test -n "$(TAG)" || { echo "error: TAG is required, e.g. make release-notes TAG=v0.1.0" >&2; exit 1; }
	@mkdir -p dist
	@if git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null; then \
		git-cliff --current --strip header --output dist/RELEASE_NOTES.md; \
	else \
		git-cliff --unreleased --tag "$(TAG)" --strip header --output dist/RELEASE_NOTES.md; \
	fi
