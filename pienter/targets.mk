# Pienter fork targets, included from the upstream Makefile. Kept in a separate
# file so upstream syncs rarely conflict; never send this file upstream.

# Upstream ref to merge: a Windshiftapp/core branch or tag.
REF ?= main
# Optional Windshift item key, e.g. KEY=WCORE-20.
KEY ?=

.PHONY: sync-upstream install-ws

# Version stamped into locally built binaries: the nearest release tag, as the
# release workflows do, so `ws version` does not print "dev".
GIT_VERSION := $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
WS_INSTALL_DIR ?= $(HOME)/.local/bin

# Usage: make sync-upstream [REF=v0.9.0] [KEY=WCORE-20]
sync-upstream: ## Merge Windshiftapp/core REF (default main) into a branch and open a Forgejo PR
	@pienter/sync-upstream.sh "$(REF)" "$(KEY)"

# Usage: make install-ws [WS_INSTALL_DIR=~/.local/bin]
install-ws: ## Build the ws CLI with the git version and install it
	@mkdir -p "$(WS_INSTALL_DIR)"
	go build -ldflags "-s -w -X main.version=$(GIT_VERSION:v%=%) -X main.commit=$(GIT_COMMIT) -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o "$(WS_INSTALL_DIR)/ws" ./cmd/ws
	@"$(WS_INSTALL_DIR)/ws" version
