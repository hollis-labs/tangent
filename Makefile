.PHONY: help check-node verify-supported build build-ui build-go dev dev-go dev-ui test test-go test-frontend lint lint-go lint-frontend clean install-hooks generate-envelopes check-envelopes db-migrate db-rollback

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

db-migrate: ## Apply local SQLite migrations and exit
	go run ./cmd/tangent --migrate-only

db-rollback: ## Roll back the most recent local SQLite migration and exit
	go run ./cmd/tangent --rollback-one

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

dev: check-node ## Run Go server + Vite dev server in parallel (Go proxies to Vite)
	@echo "Starting Vite (port 5173) and Tangent server (port 7842, proxying to Vite)..."
	@$(MAKE) -j 2 dev-ui dev-go

dev-ui: check-node
	cd ui && npm run dev

dev-go:
	TANGENT_DEV_FRONTEND_URL=$(DEV_FRONTEND_URL) go run ./cmd/tangent

# ── Test ───────────────────────────────────────────────────────────────

test: check-node test-go test-frontend ## Run all tests (Go + frontend)

test-go: build-ui ## Build the embedded SPA, then run Go tests with -race
	go test -race ./...

test-frontend: check-node ## Run vitest
	cd ui && npm test -- --run

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
