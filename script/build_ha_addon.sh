#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# Build the Home Assistant add-on image locally for testing.
#
# This builds the HOST architecture only, as a quick smoke build. The
# multi-arch publish (amd64 + aarch64 + armv7) is done in CI by the official
# `home-assistant/builder` action, which reads packaging/ha-addon/openccu-loom/
# {config.yaml,build.yaml} and pushes one tag per arch
# (ghcr.io/sukramj/openccu-loom-ha-<arch>:<version>). See .github/workflows/release.yml.
#
# The add-on image is thin: it COPYs the daemon binary + OpenAPI spec out of the
# already-published release image (ghcr.io/sukramj/openccu-loom:<tag>) on top of
# the Home Assistant base image. That upstream tag must therefore be pullable —
# for a local smoke test against an unreleased version, point it at an existing
# tag via OPENCCU_LOOM_BASE_TAG=latest.
#
# Usage:
#   script/build_ha_addon.sh [version]
#   OPENCCU_LOOM_BASE_TAG=latest script/build_ha_addon.sh 0.1.0
#   ADDON=openccu-loom-remote script/build_ha_addon.sh        # remote proxy add-on
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${1:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
ADDON="${ADDON:-openccu-loom}"
CTX="$ROOT/packaging/ha-addon/${ADDON}"
if [ ! -f "$CTX/config.yaml" ]; then
  echo "unknown add-on '${ADDON}' (no ${CTX}/config.yaml)" >&2
  exit 1
fi

# Upstream release image to COPY the binary from (override the tag for local
# smoke tests where :$VERSION is not published yet).
UPSTREAM_IMAGE="${OPENCCU_LOOM_IMAGE:-ghcr.io/sukramj/openccu-loom}"
# Which tag of the upstream image to COPY the daemon binary from. An explicit
# OPENCCU_LOOM_BASE_TAG always wins. Otherwise: a clean release version (e.g.
# 1.2.3) has a matching published image, so use it; but a dev checkout's
# `git describe` (e.g. 1.2.3-4-gabcdef / -dirty / dev) has no published image,
# so fall back to :latest for a local smoke build. The LOCAL add-on image is
# still tagged with the real $VERSION either way.
if [ -n "${OPENCCU_LOOM_BASE_TAG:-}" ]; then
  BASE_TAG="$OPENCCU_LOOM_BASE_TAG"
elif printf '%s' "$VERSION" | grep -qE '(-g[0-9a-f]+|-dirty$|^dev$)'; then
  BASE_TAG="latest"
  echo "note: dev version '${VERSION}' has no published image; pulling the daemon from :latest"
  echo "      (override with OPENCCU_LOOM_BASE_TAG=<tag>)"
else
  BASE_TAG="$VERSION"
fi

# Map the host arch to the Home Assistant arch name + base image. The base pins
# mirror build.yaml; keep them in sync when bumping there.
case "$(uname -m)" in
  x86_64|amd64)        HA_ARCH=amd64;   BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.19 ;;
  aarch64|arm64)       HA_ARCH=aarch64; BUILD_FROM=ghcr.io/home-assistant/aarch64-base:3.19 ;;
  armv7l|armv7|armhf)  HA_ARCH=armv7;   BUILD_FROM=ghcr.io/home-assistant/armv7-base:3.19 ;;
  *) echo "unsupported host arch: $(uname -m)" >&2; exit 1 ;;
esac

TAG="${ADDON}-ha-${HA_ARCH}:${VERSION}"
echo "Building HA add-on ${TAG}"
echo "  base image:     ${BUILD_FROM}"
echo "  upstream image: ${UPSTREAM_IMAGE}:${BASE_TAG}"

docker buildx build --load \
  --build-arg "BUILD_FROM=${BUILD_FROM}" \
  --build-arg "OPENCCU_LOOM_IMAGE=${UPSTREAM_IMAGE}" \
  --build-arg "BUILD_VERSION=${BASE_TAG}" \
  -t "${TAG}" \
  "$CTX"

echo "Built ${TAG}"
echo "Run it (host networking, persistent /data):"
echo "  docker run --rm --network host -v ocl-data:/data ${TAG}"
