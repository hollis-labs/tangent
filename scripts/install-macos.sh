#!/bin/bash
# Install or upgrade the stable Tangent on this Mac, or remove it. Thin
# wrapper over cmd/tangent-install (CW-20260907-0020); run it from a tagged
# checkout after `make build` and `make build-app` have produced ./tangent and
# ./Tangent.app, or point --artifacts at a directory holding both.
#
#   scripts/install-macos.sh install   [--dry-run] [--artifacts DIR] [...]
#   scripts/install-macos.sh uninstall [--dry-run]
#
# install is also the upgrade. It refuses (and changes nothing) when the
# artifacts are not a matching pair, when something other than the installed
# stable daemon is serving the port, when the daemon is mid-migration, when
# the artifact's --db-check rejects the existing database, or when the
# artifact is older than what is installed. It exits non-zero if /readyz
# never passes after the LaunchAgent is reloaded. The database is never
# removed. See docs/installing.md.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repo_root"
command -v go >/dev/null 2>&1 || { echo "install-macos: go is required to run cmd/tangent-install" >&2; exit 1; }
exec go run ./cmd/tangent-install "$@"
