# SPDX-License-Identifier: MIT
# Copyright (C) 2026 OpenCCU-Loom authors.
#
# Strategy: copy the pre-built static binary and the OpenAPI spec directly
# from the upstream release image (ghcr.io/sukramj/openccu-loom:<version>)
# rather than rebuilding from source. This keeps the add-on image thin —
# the HA base image provides the s6-overlay supervisor and bashio tooling;
# the daemon binary comes from the published release artefact. The build
# pipeline passes BUILD_VERSION so both COPYs pull the same digest.

# Global build args (consumed by the FROM lines below). The HA builder passes
# BUILD_FROM (per-arch base image) and BUILD_VERSION (= the add-on version, i.e.
# the release tag); OPENCCU_LOOM_IMAGE comes from build.yaml.
ARG BUILD_FROM
ARG OPENCCU_LOOM_IMAGE
ARG BUILD_VERSION

# Pull the daemon binary + OpenAPI spec from the published release image. A
# named stage (rather than ARG expansion inside COPY --from) is the robust form
# and resolves to the matching arch when buildx builds one platform at a time.
FROM ${OPENCCU_LOOM_IMAGE}:${BUILD_VERSION} AS upstream

FROM ${BUILD_FROM}

COPY --from=upstream /app/openccu-loom /usr/bin/openccu-loom
COPY --from=upstream /app/assets/openapi.yaml /usr/share/openccu-loom/assets/openapi.yaml

COPY rootfs /

ENV OPENCCU_LOOM_DATA_DIR=/data \
    OPENCCU_LOOM_SUPERVISOR=1 \
    OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH=/usr/share/openccu-loom/assets/openapi.yaml

WORKDIR /usr/share/openccu-loom
