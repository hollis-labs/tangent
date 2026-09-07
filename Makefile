.PHONY: help check-node verify-supported build build-ui build-go dev dev-go dev-ui test test-go test-frontend smoke lint lint-go lint-frontend clean install-hooks generate-envelopes check-envelopes db-migrate db-rollback

# The DEV instance (CW-20260907-0018). Stable keeps the daemon defaults
# (port 7842, ~/.tangent/tangent.db) so nothing agent-facing rewires; every
# dev-facing target below runs on DEV_PORT against a workspace-local database
# (.tangent/ is gitignored and removed by `make clean`). A different database
# path is a different `.owner` lock file, so dev and stable can never collide
# on the single-writer flock or the port. `./tangent` with no environment
# still means the stable defaults; only make targets are pointed at dev.
DEV_PORT ?= 7843
DEV_DB_PATH ?= $(CURDIR)/.tangent/dev.db
DEV_ENV = TANGENT_HTTP_PORT=$(DEV_PORT) TANGENT_DB_PATH=$(DEV_DB_PATH)

# Default port for the Vite dev server. The Go server (in dev mode)
# reverse-proxies non-API requests to this URL.
DEV_FRONTEND_URL ?= http://localhost:5173

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
	@touch internal/server/ui_dist/.gitkeep

build-go: ## Build Go binary (requires internal/server/ui_dist to exist)
	go build -o tangent ./cmd/tangent

db-migrate: ## Apply migrations to the DEV database (DEV_DB_PATH) and exit
	$(DEV_ENV) go run ./cmd/tangent --migrate-only

db-rollback: ## Roll back the most recent migration on the DEV database and exit
	$(DEV_ENV) go run ./cmd/tangent --rollback-one

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

dev: check-node ## Run Go server (DEV_PORT, DEV_DB_PATH) + Vite dev server in parallel (Go proxies to Vite)
	@echo "Starting Vite (port 5173) and the Tangent DEV server (port $(DEV_PORT), db $(DEV_DB_PATH), proxying to Vite)..."
	@$(MAKE) -j 2 dev-ui dev-go

dev-ui: check-node
	cd ui && npm run dev

dev-go:
	$(DEV_ENV) TANGENT_DEV_FRONTEND_URL=$(DEV_FRONTEND_URL) go run ./cmd/tangent

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

lint-go: ## golangci-lint + go vet + gofmt check
	@unformatted=$$(gofmt -l . | grep -v '^ui/node_modules/' | grep -v '^internal/server/ui_dist/'); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: unformatted files:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run)"; \
	fi

lint-frontend: check-node ## biome check
	cd ui && npm run lint

# ── Maintenance ────────────────────────────────────────────────────────

clean: ## Remove ui/dist, ui/node_modules, internal/server/ui_dist build output, ./tangent
	rm -f tangent
	rm -rf ui/dist
	rm -rf ui/node_modules
	rm -rf .tangent
	# Preserve the .gitkeep so go:embed still resolves.
	find internal/server/ui_dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true

install-hooks: ## Install lefthook (one-time setup)
	@if ! command -v lefthook >/dev/null 2>&1; then \
		echo "lefthook not installed. Install with: brew install lefthook"; \
		exit 1; \
	fi
	lefthook install
