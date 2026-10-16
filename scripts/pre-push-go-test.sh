#!/usr/bin/env bash
# Pre-push Go tests: the packages this push changes, under the race detector,
# with bounded parallelism and a per-package timeout. CI runs the full suite
# and stays the gate; this hook is only the fast local check before pushing.
#
# Why not `go test -race ./...`: that starts one race-instrumented test binary
# per CPU at once. On a busy host internal/mcp alone took ~8 minutes and the
# default 10 minute package timeout killed it with no assertion and no race
# report, which pushed people to --no-verify (CW-20261016-0206).
#
# Environment:
#   TANGENT_PREPUSH_FULL=1     test every package (the old behaviour)
#   TANGENT_PREPUSH_PARALLEL   packages tested at once (default 2)
#   TANGENT_PREPUSH_TIMEOUT    per-package timeout (default 20m)
#   TANGENT_PREPUSH_DRYRUN=1   print the packages and stop
#   TANGENT_PREPUSH_BASE       ref to diff against (default: the branch's
#                              upstream merge-base, else origin/main)
set -euo pipefail

parallel="${TANGENT_PREPUSH_PARALLEL:-2}"
timeout="${TANGENT_PREPUSH_TIMEOUT:-20m}"

# cmd/tangent-app and internal/appshell link Wails (GTK/webkit via pkg-config) on
# Linux. Without pkg-config they cannot build, so they are skipped here; CI
# runs them.
skip_wails=0
if [ "$(uname -s)" = Linux ] && ! command -v pkg-config >/dev/null 2>&1; then
  skip_wails=1
  echo "pre-push: pkg-config not found: skipping cmd/tangent-app and internal/appshell (CI runs them)"
fi

all_packages() {
  local pkgs
  pkgs=$(go list ./...) || return 1
  if [ "$skip_wails" = 1 ]; then
    pkgs=$(printf '%s\n' "$pkgs" | grep -v -e '/cmd/tangent-app$' -e '/internal/appshell$' || true)
  fi
  printf '%s\n' "$pkgs"
}

# changed_packages prints the import path of every package that has a changed
# file between base and HEAD. A changed migration counts as internal/db, whose
# tests apply and roll back the whole chain.
changed_packages() {
  local base="$1" file dir
  git diff --name-only --diff-filter=ACMR "$base"...HEAD | while IFS= read -r file; do
    case "$file" in
      internal/db/migrations/*) echo "internal/db" ;;
      *.go) dir=$(dirname "$file"); echo "$dir" ;;
    esac
  done | sort -u | while IFS= read -r dir; do
    [ -d "$dir" ] && go list "./$dir" 2>/dev/null || true
  done
}

if [ "${TANGENT_PREPUSH_FULL:-0}" = 1 ]; then
  pkgs=$(all_packages)
else
  base="${TANGENT_PREPUSH_BASE:-}"
  if [ -z "$base" ]; then
    if upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null); then
      base=$(git merge-base HEAD "$upstream")
    else
      base=$(git merge-base HEAD origin/main)
    fi
  fi
  pkgs=$(changed_packages "$base")
  if [ "$skip_wails" = 1 ]; then
    pkgs=$(printf '%s\n' "$pkgs" | grep -v -e '/cmd/tangent-app$' -e '/internal/appshell$' || true)
  fi
  # go.mod / go.sum changes can break any package; a build catches that
  # without running every suite.
  if git diff --name-only "$base"...HEAD | grep -qE '^go\.(mod|sum)$'; then
    echo "pre-push: go.mod/go.sum changed: building all packages"
    # shellcheck disable=SC2046
    go build $(all_packages)
  fi
fi

pkgs=$(printf '%s\n' "$pkgs" | sed '/^$/d')
if [ -z "$pkgs" ]; then
  echo "pre-push: no Go packages changed; CI runs the full suite"
  exit 0
fi

echo "pre-push: go test -race -p $parallel -timeout $timeout on:"
printf '  %s\n' $pkgs
if [ "${TANGENT_PREPUSH_DRYRUN:-0}" = 1 ]; then
  exit 0
fi
# shellcheck disable=SC2086
exec go test -race -p "$parallel" -timeout "$timeout" $pkgs
