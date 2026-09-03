# Local development. Releases are for distribution, not for iterating —
# `make install` gives you the working tree as a real binary in seconds.

BIN     ?= $(HOME)/.local/bin/pgctl
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=9 HEAD 2>/dev/null || echo none)
LDFLAGS  = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# A disposable PostgreSQL 17 to run the integration tests against. Nothing in
# the test suite touches a real environment, and nothing should ever need to.
# Where captured frames land. Outside the repo: they are an intermediate, and
# docs/screens is the committed output.
FRAMES ?= /tmp/pgctl-frames

LAB_IMAGE ?= postgres:17
LAB_PORT  ?= 55432
LAB_NAME  ?= pgctl-lab

.PHONY: install build test test-all lint check frames watch lab-up lab-down help

## install: build the working tree and replace the pgctl on your PATH
install:
	@CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o "$(BIN)" ./cmd/pgctl
	@echo "installed $$("$(BIN)" version) -> $(BIN)"

## build: build to ./bin/pgctl without touching your PATH
build:
	@CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/pgctl ./cmd/pgctl
	@echo "built $$(./bin/pgctl version) -> bin/pgctl"

## test: unit tests (no server needed)
test:
	go test ./...

## test-all: unit tests plus the ones that need a server (make lab-up first)
test-all:
	PGCTL_TEST_DSN="postgres://postgres:pgctl@127.0.0.1:$(LAB_PORT)/postgres" \
	POSTGRES_USERNAME=postgres POSTGRES_PASSWORD=pgctl \
	go test ./... -count=1

## lint: what CI enforces
lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	golangci-lint run ./...

## frames: regenerate docs/screens.md and its SVGs from the capture test
frames:
	@rm -rf $(FRAMES) docs/screens
	@PGCTL_FRAMES=$(FRAMES) go test ./internal/tui/ -run CaptureFrames -count=1 >/dev/null
	@tuikit frames $(FRAMES) -md -out docs/screens.md -title "pgctl screens"
	@echo "$$(ls docs/screens | wc -l | tr -d ' ') frames -> docs/screens.md"

## watch: recapture on save and reload a browser, for building a screen
watch:
	@tuikit watch ./internal/tui 		-capture "PGCTL_FRAMES=$(FRAMES) go test ./internal/tui -run CaptureFrames" 		-frames $(FRAMES)

## check: everything CI runs, before you push
check: test lint
	@echo "all checks passed"

## lab-up: start a disposable PostgreSQL to test against
lab-up:
	@docker run -d --name $(LAB_NAME) -e POSTGRES_PASSWORD=pgctl \
		-p $(LAB_PORT):5432 $(LAB_IMAGE) >/dev/null
	@echo "waiting for $(LAB_NAME)"
	@until docker exec $(LAB_NAME) pg_isready -q 2>/dev/null; do sleep 1; done
	@echo "postgres on 127.0.0.1:$(LAB_PORT), user postgres, password pgctl"

## lab-down: delete the disposable PostgreSQL and its data
lab-down:
	@docker rm -f -v $(LAB_NAME) >/dev/null 2>&1 || true
	@echo "removed $(LAB_NAME)"

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
