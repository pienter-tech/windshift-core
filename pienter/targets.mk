# Pienter fork targets, included from the upstream Makefile. Kept in a separate
# file so upstream syncs rarely conflict; never send this file upstream.

# Upstream ref to merge: a Windshiftapp/core branch or tag.
REF ?= main
# Optional Windshift item key, e.g. KEY=WCORE-20.
KEY ?=

.PHONY: sync-upstream

# Usage: make sync-upstream [REF=v0.9.0] [KEY=WCORE-20]
sync-upstream: ## Merge Windshiftapp/core REF (default main) into a branch and open a Forgejo PR
	@pienter/sync-upstream.sh "$(REF)" "$(KEY)"
