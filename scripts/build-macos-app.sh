#!/bin/bash
# Assemble Tangent.app: build the desktop shell binary (CGO on), stage it
# together with Info.plist and the icon into a bundle, verify the result
# with cmd/tangent-packagecheck, then atomically promote it into place.
#
# Invoked by `make build-app`, which runs check-node, generate-envelopes,
# and build-ui first — this script only builds the Go binary and assembles
# the bundle around it. Not code-signed: CW-20260905-0032 explicitly leaves
# signing, notarization, and a release channel for later.
set -euo pipefail

readonly bundle_id="com.hollislabs.tangent"
readonly app_name="Tangent"
readonly executable_name="tangent-app"
# Matches packaging/macos/Info.plist's LSMinimumSystemVersion. Wails v3's
# darwin backend guards its newer AppKit calls with @available checks and
# degrades gracefully below them, but the compiled binary's own deployment
# target should still agree with what the bundle advertises.
readonly macos_deployment_target="12.0"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repo_root"

die() {
	printf 'build-macos-app: %s\n' "$*" >&2
	exit 1
}

[[ "$(uname -s)" == "Darwin" ]] || die "macOS is required to build the desktop shell bundle"
for command in go node sips iconutil plutil; do
	command -v "$command" >/dev/null 2>&1 || die "required command not found: $command"
done

# The version lives in ui/package.json — already this repo's one source of
# truth for "the version" (ui/package.json's own engines field gates Node,
# scripts/generate-envelope-types.mjs reads it, etc.) — rather than `git
# describe`, which needs tags and full history that a shallow CI checkout
# may not have, and which would print ugly `0.0.0-g<sha>` strings for this
# still-untagged, unreleased, single-user tool.
version="$(node -p "require('./ui/package.json').version")"
[[ -n "$version" ]] || die "could not read version from ui/package.json"

stage_app="$repo_root/$app_name.app-bin"
final_app="$repo_root/$app_name.app"

rm -rf -- "$stage_app"
mkdir -p "$stage_app/Contents/MacOS" "$stage_app/Contents/Resources"

printf 'Building %s (CGO_ENABLED=1, deployment target %s)...\n' "$executable_name" "$macos_deployment_target"
CGO_ENABLED=1 \
	CGO_CFLAGS="-mmacosx-version-min=$macos_deployment_target" \
	CGO_LDFLAGS="-mmacosx-version-min=$macos_deployment_target" \
	MACOSX_DEPLOYMENT_TARGET="$macos_deployment_target" \
	go build -o "$stage_app/Contents/MacOS/$executable_name" ./cmd/tangent-app

printf 'Writing Info.plist (version %s)...\n' "$version"
sed "s/__TANGENT_VERSION__/$version/g" packaging/macos/Info.plist >"$stage_app/Contents/Info.plist"
plutil -lint "$stage_app/Contents/Info.plist" >/dev/null

printf 'Generating AppIcon.icns...\n'
iconset_root="$(mktemp -d "${TMPDIR:-/tmp}/tangent-iconset.XXXXXX")"
trap 'rm -rf -- "$iconset_root"' EXIT
iconset_dir="$iconset_root/AppIcon.iconset"
mkdir -p "$iconset_dir"
# Same size/scale table as Tachyon's packaging/macos icon generation
# (~/dev/projects/tachyon/scripts/build-macos.sh) so both apps' icons
# render identically sharp across Finder, Dock, and Spotlight.
for spec in "16:16" "16:32" "32:32" "32:64" "128:128" "128:256" "256:256" "256:512" "512:512" "512:1024"; do
	name="${spec%%:*}"
	pixels="${spec##*:}"
	suffix=""
	[[ "$pixels" -eq $((name * 2)) ]] && suffix="@2x"
	sips -z "$pixels" "$pixels" packaging/macos/AppIcon.png \
		--out "$iconset_dir/icon_${name}x${name}${suffix}.png" >/dev/null
done
iconutil -c icns "$iconset_dir" -o "$stage_app/Contents/Resources/AppIcon.icns"
rm -rf -- "$iconset_root"
trap - EXIT

printf 'Verifying bundle...\n'
go run ./cmd/tangent-packagecheck "$stage_app"

rm -rf -- "$final_app"
mv "$stage_app" "$final_app"

printf '\nBuilt %s\n' "$final_app"
printf 'Bundle identifier: %s\n' "$bundle_id"
printf 'Version: %s\n' "$version"
printf 'Not code-signed — expected for local, single-user use (CW-20260905-0032 scopes signing/notarization out).\n'
printf 'Launch with: open "%s"\n' "$final_app"
