# Tangent design rule tooling

This private npm package keeps the design-rules parser separate from Tangent's
TypeScript 7 compiler. TypeScript 5.9.3 and typescript-eslint 8.46.4 satisfy each
other's peers and run under Tangent's pinned Node 22.12.0. eslint-visitor-keys
stays on the compatible 4.x line. All dependencies come from the npm registry;
the lockfile pins the toolchain. Biome remains the app's primary linter.

From the repository root:

```sh
mise --no-config exec node@22.12.0 -- npm ci --prefix ui/tools/design-lint
mise --no-config exec node@22.12.0 -- npm --prefix ui run check:design
```

The frontend CI job installs this package with `npm ci` and runs the same
`check:design` script from `ui/`. The wrapper uses its own dependencies, builds
only the design-rule config, and checks the app's `src/` against
`ui/.eslint-design-baseline.json`. It does not read an ESLint app config.

Exit 0 means the per-rule totals stay within the baseline; exit 1 means a rule's
count increased. Exit 2 means configuration or parsing failed, including a wrong
source path or unavailable TS parser; CI treats either nonzero exit as failure.
A supported older parser must parse every source file: a parse error cannot be
accepted as baseline debt.

After a deliberate cleanup, refresh the baseline from `ui/`:

```sh
npm run check:design:update -- --note "describe the cleanup"
```

Review per-rule count changes before committing. `--update` can raise the
baseline, so it must never run automatically in CI. The totals are per rule,
so fixing one violation and adding another of the same rule can cancel out.
