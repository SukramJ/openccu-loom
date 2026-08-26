#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# Assemble the CCU / RaspberryMatic add-on tarball:
#   openccu-loom-ccu-<version>.tar.gz
#
# The tarball bundles every supported build (amd64 + arm64 + armv7); the
# add-on's update_script picks the matching one from `uname -m` at install
# time, mirroring RaspberryMatic's own platform matrix (x86-64 OVA /
# generic, 64-bit Pi, CCU3 / 32-bit Pi).
#
# Usage:
#   script/build_ccu_addon.sh [version]
#
# Reuse goreleaser's cross-compiled binaries instead of rebuilding by
# pointing BIN_AMD64 / BIN_ARM64 / BIN_ARMV7 at them (the release workflow
# does this):
#   BIN_AMD64=dist/openccu-loom_linux_amd64*/openccu-loom \
#   BIN_ARM64=dist/openccu-loom_linux_arm64*/openccu-loom \
#   BIN_ARMV7=dist/openccu-loom_linux_arm_7/openccu-loom \
#   script/build_ccu_addon.sh 1.2.3
#
# When rebuilding from source (no BIN_* set), the SPA must already be
# compiled into internal/north/ui/spa_dist (run `make ui-build` first),
# since the daemon embeds it via //go:embed. The release path sidesteps
# this by reusing goreleaser binaries that already have the SPA embedded.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${1:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
SRC="$ROOT/packaging/ccu-addon/ccu"
OUT="${OUT:-$ROOT/dist}"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# write_sha256 <path> — writes "<path>.sha256" next to path in the
# `sha256sum`-format line "<hash>  <basename>" (two spaces; the
# filename is the bare basename, not the full staged path, since
# `sha256sum -c` resolves it relative to its own working directory —
# i.e. wherever the tarball was unpacked to). ADR 0057 decision 2: the
# daemon's own downloader already verifies the whole tarball against
# the GitHub release's checksums.txt before staging it; these
# per-file sidecars are the firmware installer's OWN second check
# after unpacking, so a corrupted or truncated extraction is caught
# even if the outer tarball hash matched.
write_sha256() {
  local path="$1"
  local dir base hash
  dir="$(dirname "$path")"
  base="$(basename "$path")"
  if command -v sha256sum >/dev/null 2>&1; then
    hash="$(sha256sum "$path" | awk '{print $1}')"
  else
    hash="$(shasum -a 256 "$path" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$hash" "$base" > "$dir/$base.sha256"
}

# build_bin <goarch> <goarm> <out-path> <prebuilt-env-var>
build_bin() {
  local goarch="$1" goarm="$2" out="$3" prebuilt="${4:-}"
  if [ -n "${prebuilt}" ] && [ -n "${!prebuilt:-}" ]; then
    # Reused binaries lack the AddonBuild ldflags stamp — build.IsAddon()
    # also detects the add-on at runtime from the install path, which is
    # what keeps release-packaged tarballs (this path) behaving as add-on.
    echo "  reuse ${!prebuilt} -> $(basename "$out")"
    cp "${!prebuilt}" "$out"
    return
  fi
  echo "  build linux/${goarch}${goarm:+v$goarm} -> $(basename "$out")"
  ( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
    go build -trimpath \
      -ldflags "-s -w -X github.com/SukramJ/openccu-loom/internal/build.Version=${VERSION} -X github.com/SukramJ/openccu-loom/internal/build.AddonBuild=true" \
      -o "$out" ./cmd/openccu-loom )
}

echo "Staging CCU add-on ${VERSION}"
cp -a "$SRC/." "$STAGE/"
mkdir -p "$STAGE/addon/assets"

build_bin amd64 "" "$STAGE/addon/openccu-loom.amd64" BIN_AMD64
build_bin arm64 "" "$STAGE/addon/openccu-loom.arm64" BIN_ARM64
build_bin arm   7  "$STAGE/addon/openccu-loom.armv7" BIN_ARMV7

echo "${VERSION}" > "$STAGE/addon/VERSION"
# The daemon reads assets/openapi.yaml from its working dir at runtime.
cp "$ROOT/assets/openapi.yaml" "$STAGE/addon/assets/openapi.yaml"

chmod 755 "$STAGE/update_script" "$STAGE/rc.d/"* "$STAGE/www/"*.cgi
chmod 755 "$STAGE/addon/openccu-loom.amd64" \
          "$STAGE/addon/openccu-loom.arm64" \
          "$STAGE/addon/openccu-loom.armv7"

# Embed a *.sha256 sidecar for every staged binary + asset (ADR 0057).
write_sha256 "$STAGE/addon/openccu-loom.amd64"
write_sha256 "$STAGE/addon/openccu-loom.arm64"
write_sha256 "$STAGE/addon/openccu-loom.armv7"
write_sha256 "$STAGE/addon/assets/openapi.yaml"

mkdir -p "$OUT"
TARBALL="$OUT/openccu-loom-ccu-${VERSION}.tar.gz"
tar -C "$STAGE" -czf "$TARBALL" .
echo "Wrote ${TARBALL}"
