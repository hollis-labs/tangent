.PHONY: help check-node verify-supported build build-ui build-go dev dev-go dev-ui test test-go test-frontend smoke lint lint-go lint-frontend clean scan-public install-hooks generate-envelopes check-envelopes dev-config db-check

# The DEV instance (CW-20260907-0018). Stable keeps the daemon defaults
# (port 7842, the XDG data directory) so nothing agent-facing rewires; every
# dev-facing target below runs on DEV_PORT against a workspace-local data
# directory (.tangent/ is gitignored and removed by `make clean`). The port is
# set through a Substrate config override (.tangent/substrate.dev.yaml, written
# by `make dev-config`), the data through TANGENT_DATA_DIR. The first run needs
# an owner: `TANGENT_DATA_DIR=$(DEV_DATA_DIR) SUBSTRATE_CONFIG=$(DEV_CONFIG) go run ./cmd/tangent identity setup --username NAME`.
DEV_PORT ?= 7843
DEV_DATA_DIR ?= $(CURDIR)/.tangent/data
DEV_CONFIG ?= $(CURDIR)/.tangent/substrate.dev.yaml
DEV_ENV = TANGENT_DATA_DIR=$(DEV_DATA_DIR) SUBSTRATE_CONFIG=$(DEV_CONFIG)

# Keep this exact pin aligned with mise.toml. The package engine range remains
# authoritative for compatible direct Node invocations.
SUPPORTED_NODE_VERSION ?= 22.12.0
MISE ?= mise

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[1m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check-node: ## Fail clearly unless Node satisfies ui/package.json engines
	@node -e 'const [major, minor] = process.versions.node.split(".").map(Number); const range = require("./ui/package.json").engines.node; if (major !== 22 || minor < 12) { console.error(`Unsupported Node.js $${process.version}; Tangent requires $${range}. Run: make verify-supported`); process.exit(1); }'

verify-supported: ## Run all checks with the pinned Node runtime, without trusting repo config
	@command -v $(MISE) >/dev/null 2>&1 || { echo "mise is required (https://mise.jdx.dev/)" >&2; exit 1; }
	$(MISE) --no-config exec node@$(SUPPORTED_NODE_VERSION) -- $(MAKE) test lint check-envelopes

# ── Build ──────────────────────────────────────────────────────────────

build: check-node generate-envelopes build-ui build-go ## Regenerate envelope types, then build frontend + Go binary (production)

build-ui: check-node ## Build frontend (Vite production build)
	cd ui && npm run build
	@touch internal/webui/dist/.gitkeep

# Explicit stamps cover toolchains that omit native VCS metadata in worktrees.
# A source archive stays unstamped; an edited checkout never claims to be clean.
build-go: ## Build Go binary (requires internal/webui/dist to exist)
	@set -eu; \
	commit=$$(git rev-parse --verify HEAD 2>/dev/null || true); dirty=; \
	if [ -n "$$commit" ]; then \
		status=$$(git status --porcelain); dirty=false; \
		if [ -n "$$status" ]; then dirty=true; fi; \
	fi; \
	go build -buildvcs=true \
		-ldflags "-X github.com/hollis-labs/tangent/internal/buildinfo.Commit=$$commit -X github.com/hollis-labs/tangent/internal/buildinfo.Dirty=$$dirty" \
		-o tangent ./cmd/tangent

# The first-party plugins live in github.com/hollis-labs/plugins, one
# tangent-<name> module each. tangent-plugins.version pins the source commit
# operators install; smoke tests use tangent-plugins.smoke-ref separately.
# install-plugins runs go install at that commit, lets the binary emit
# its plugin.yaml, and `tangent plugin install` the result.
#
# TANGENT_PLUGINS_SRC=/path/to/plugins builds from that checkout
# instead, for changing a plugin and Tangent in the same sitting.
PLUGINS = runner tesseract torque
TANGENT_PLUGINS_VERSION := $(shell cat tangent-plugins.version)

install-plugins: build-go ## Install the pinned first-party plugins (tangent-plugins.version) for this user
	@set -e; stage=$$(mktemp -d); trap 'rm -rf "$$stage"' EXIT; \
	for p in $(PLUGINS); do \
		dir="$$stage/tangent.plugin.$$p"; mkdir -p "$$dir/bin"; \
		if [ -n "$$TANGENT_PLUGINS_SRC" ]; then \
			(cd "$$TANGENT_PLUGINS_SRC/tangent-$$p" && go build -o "$$dir/bin/tangent-plugin-$$p" ./cmd/tangent-plugin-$$p); \
		else \
			GOBIN="$$dir/bin" go install github.com/hollis-labs/plugins/tangent-$$p/cmd/tangent-plugin-$$p@$(TANGENT_PLUGINS_VERSION); \
		fi; \
		"$$dir/bin/tangent-plugin-$$p" --manifest > "$$dir/plugin.yaml"; \
		./tangent plugin install "$$dir"; \
	done

dev-config: ## Write the dev instance's port override (.tangent/substrate.dev.yaml)
	@mkdir -p $(dir $(DEV_CONFIG))
	@printf 'httphost:\n  addr: "127.0.0.1:$(DEV_PORT)"\n' > $(DEV_CONFIG)

db-check: dev-config ## Read-only integrity check of the DEV database
	$(DEV_ENV) go run ./cmd/tangent database check

# ── Codegen ────────────────────────────────────────────────────────────
#
# Envelope types are derived from the go-envelopes manifest: the dump
# binary prints the catalog as JSON, the Node script transforms it into
# TypeScript at ui/src/generated/envelope-types.ts. The generated file
# is committed; CI's check-envelopes target gates on staleness.

generate-envelopes: check-node ## Regenerate ui/src/generated/envelope-types.ts from go-envelopes
	node scripts/generate-envelope-types.mjs

check-envelopes: check-node ## Fail if committed envelope types are stale
	node scripts/generate-envelope-types.mjs --check

# ── Dev ────────────────────────────────────────────────────────────────

dev: dev-go ## Run the Go DEV server (DEV_PORT, DEV_DATA_DIR); run `make dev-ui` beside it for Vite

dev-ui: check-node
	cd ui && npm run dev

dev-go: dev-config
	$(DEV_ENV) go run ./cmd/tangent

# ── Test ───────────────────────────────────────────────────────────────

test: check-node test-go test-frontend ## Run all tests (Go + frontend)

test-go: build-ui ## Build the embedded SPA, then run Go tests with -race
	go test -race ./...

test-frontend: check-node ## Run vitest
	cd ui && npm test -- --run

# ── Smoke ──────────────────────────────────────────────────────────────
#
# One command, two layers. By default it builds ./cmd/tangent, boots it on a
# reserved port against a database in a temp directory, and checks direct
# /mcp, legacy /sse, one read-only tool call, and the three health probes —
# no live instance, no Cerberus resource, no Tether catalog, so it is safe in
# CI and it is what `go test ./...` already runs.
#
# The environment-coupled layer — the running deployment, its supervisor, and
# the Tether gateway that fronts it — is behind one explicit gate:
#
#   TANGENT_SMOKE_ENV=1 make smoke
#
# See docs/mcp-smoketest.md for the operator recipe and the four failure modes.

# The environment-coupled arm (TANGENT_SMOKE_ENV=1) targets the DEV instance:
# its URL, its Cerberus resource, and its Tether catalog entry. Point it at
# stable explicitly when that is what you mean:
#   TANGENT_SMOKE_ENV=1 SMOKE_URL=http://127.0.0.1:7842 SMOKE_CATALOG_ENTRY=tangent make smoke
SMOKE_URL ?= http://127.0.0.1:$(DEV_PORT)
SMOKE_RESOURCE ?= tangent-dev
SMOKE_CATALOG_ENTRY ?= tangent-dev

smoke: build-ui ## MCP smoke; TANGENT_SMOKE_ENV=1 adds the live DEV deployment, Cerberus, and Tether checks
	TANGENT_SMOKE_URL=$(SMOKE_URL) TANGENT_SMOKE_RESOURCE=$(SMOKE_RESOURCE) TANGENT_SMOKE_CATALOG_ENTRY=$(SMOKE_CATALOG_ENTRY) \
		go test -count=1 -v ./internal/smoke/...

# ── Lint ───────────────────────────────────────────────────────────────

lint: check-node lint-go lint-frontend ## All lint (Go + frontend)

lint-go: ## golangci-lint (incl. the transport-boundary gate) + go vet + gofmt check
	@unformatted=$$(gofmt -l . | grep -v '^ui/node_modules/' | grep -v '^internal/server/ui_dist/'); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: unformatted files:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run && \
		GOWORK=off golangci-lint run --config .golangci.transport.yml ./...; \
	else \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run)"; \
	fi

scan-public: ## Scan the working tree for secret-shaped literals and private paths (the framework's scanner)
	go run github.com/hollis-labs/substrate/cmd/substrate scan-public --tree

lint-frontend: check-node ## biome check
	cd ui && npm run lint

# ── Maintenance ────────────────────────────────────────────────────────

clean: ## Remove ui/dist, ui/node_modules, internal/webui/dist build output, ./tangent
	rm -f tangent
	rm -rf ui/dist
	rm -rf ui/node_modules
	rm -rf .tangent
	# Preserve the .gitkeep so go:embed still resolves.
	find internal/webui/dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true

install-hooks: ## Install lefthook (one-time setup)
	@if ! command -v lefthook >/dev/null 2>&1; then \
		echo "lefthook not installed. Install with: brew install lefthook"; \
		exit 1; \
	fi
	lefthook install
