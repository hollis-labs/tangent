.PHONY: help build build-ui build-go dev dev-go dev-ui test test-go test-frontend lint lint-go lint-frontend clean install-hooks

# Default port for the Vite dev server. The Go server (in dev mode)
# reverse-proxies non-API requests to this URL.
DEV_FRONTEND_URL ?= http://localhost:5173

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[1m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ── Build ──────────────────────────────────────────────────────────────

build: build-ui build-go ## Build frontend + Go binary (production)

build-ui: ## Build frontend (Vite production build)
	cd ui && npm run build

build-go: ## Build Go binary (requires internal/server/ui_dist to exist)
	go build -o tangent ./cmd/tangent

# ── Dev ────────────────────────────────────────────────────────────────

dev: ## Run Go server + Vite dev server in parallel (Go proxies to Vite)
	@echo "Starting Vite (port 5173) and Tangent server (port 7842, proxying to Vite)..."
	@$(MAKE) -j 2 dev-ui dev-go

dev-ui:
	cd ui && npm run dev

dev-go:
	TANGENT_DEV_FRONTEND_URL=$(DEV_FRONTEND_URL) go run ./cmd/tangent

# ── Test ───────────────────────────────────────────────────────────────

test: test-go test-frontend ## Run all tests (Go + frontend)

test-go: ## Run Go tests with -race
	go test -race ./...

test-frontend: ## Run vitest
	cd ui && npm test -- --run

# ── Lint ───────────────────────────────────────────────────────────────

lint: lint-go lint-frontend ## All lint (Go + frontend)

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

lint-frontend: ## biome check
	cd ui && npm run lint

# ── Maintenance ────────────────────────────────────────────────────────

clean: ## Remove ui/dist, ui/node_modules, internal/server/ui_dist build output, ./tangent
	rm -f tangent
	rm -rf ui/dist
	rm -rf ui/node_modules
	# Preserve the .gitkeep so go:embed still resolves.
	find internal/server/ui_dist -mindepth 1 ! -name '.gitkeep' -delete 2>/dev/null || true

install-hooks: ## Install lefthook (one-time setup)
	@if ! command -v lefthook >/dev/null 2>&1; then \
		echo "lefthook not installed. Install with: brew install lefthook"; \
		exit 1; \
	fi
	lefthook install
