# Contributing to Tangent

Tangent is pre-release software. Contributions are welcome; the bar is correct,
minimal, well-tested Go and TypeScript that passes the full quality gate.

## Before you start

- `README.md` has the quick start; [`docs/developing.md`](docs/developing.md) is
  the contributor guide; `AGENTS.md` is the fastest orientation to the code
  layout and the boundaries that are not obvious from reading the code.
- Read `docs/architecture.md` (including **Current limitations**) and
  [ADR 0005](docs/adr/0005-product-boundary-and-portfolio-composition.md) before
  proposing that Tangent own something new. It is a separate-window interaction
  surface, not a task tracker, agent launcher or session manager.
- License: see [`LICENSE`](LICENSE) and [`TRADEMARK.md`](TRADEMARK.md). Code
  contributions are accepted under the repository's MIT license; the Tangent and
  Hollis Labs names remain protected marks.

## Workflow

1. **Open an issue or discussion first** for anything larger than a small fix.
2. **Branch from `main`** using `feat/<topic>`, `fix/<topic>` or `docs/<topic>`.
3. **Change one thing per branch** and keep commits small and coherent.
4. **Run the checks** before opening a pull request:

   ```bash
   make verify-supported   # test + lint + check-envelopes under the pinned Node
   make smoke              # when the MCP surface, a workflow or tool docs change
   ```

   `mise install` pins Node. Build from source with `make build`; `go install`
   does not produce a binary with the UI.
5. **Open a pull request.** Say what changed, what it deliberately leaves alone,
   and the commands you ran with their results. A maintainer will review it.

## Conventions

- Never write a tool, envelope-kind or workflow count into prose; the
  documentation gate (`make smoke`) checks documented tools against the shipped
  surface.
- A change that moves a manifest's `contract_digest` is a version bump, not a
  revision bump (ADR 0003).
- Conventional Commits (`feat(scope): …`, `fix: …`, `docs: …`) are welcome; use
  the body to explain *why*.

## Security

Report vulnerabilities privately as described in [`SECURITY.md`](SECURITY.md).
