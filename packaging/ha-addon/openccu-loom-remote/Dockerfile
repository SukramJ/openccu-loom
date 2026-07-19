# check=skip=InvalidDefaultArgInFrom
# (BuildKit can't see that BUILD_FROM / OPENCCU_LOOM_IMAGE / BUILD_VERSION are
# always supplied as --build-args by the HA builder and script/build_ha_addon.sh;
# the FROM lines are never actually empty. Silences the static-analysis warning.)
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 OpenCCU-Loom authors.
#
# Same thin-image strategy as the main add-on: copy the pre-built static
# proxy binary out of the upstream release image instead of rebuilding
# from source. The HA base image provides s6-overlay + bashio.

ARG BUILD_FROM
ARG OPENCCU_LOOM_IMAGE
ARG BUILD_VERSION

FROM ${OPENCCU_LOOM_IMAGE}:${BUILD_VERSION} AS upstream

FROM ${BUILD_FROM}

COPY --from=upstream /app/openccu-loom-remote /usr/bin/openccu-loom-remote

COPY rootfs /

# Ingress proxy for remote OpenCCU-Loom instances (no local daemon).
EXPOSE 8234/tcp
