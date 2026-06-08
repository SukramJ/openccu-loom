# SPDX-License-Identifier: MIT
# Multi-stage build for openccu-loom. Pure-Go (CGO disabled) so the
# final image can be distroless-based and statically linked. The Config UI
# (Svelte SPA) is built from source in its own stage and embedded into the
# daemon binary, so `docker build .` / `make docker` produce a fully-working
# image with no host-side prerequisites (no prior `make ui-build` needed).
#
# The goreleaser release path uses ./Dockerfile.goreleaser instead, which
# COPYs an already-built binary; this file is the from-source build.

# --- Stage 1: build the Svelte SPA -----------------------------------------
# Pinned to the build platform ($BUILDPLATFORM) so the SPA — which is
# architecture-independent JS/HTML — is built once natively instead of under
# QEMU emulation for each target arch. Vite's outDir is
# ../../internal/north/ui/spa_dist (relative to assets/ui), so building from
# /src/assets/ui lands the bundle at /src/internal/north/ui/spa_dist.
FROM --platform=$BUILDPLATFORM node:24-alpine AS spa
WORKDIR /src/assets/ui
COPY assets/ui/package.json assets/ui/package-lock.json ./
RUN npm ci
COPY assets/ui/ ./
RUN npm run build

# --- Stage 2: build the Go binary ------------------------------------------
FROM golang:1.26.4-alpine AS builder
WORKDIR /src

# Cache module downloads separately so unrelated source edits don't
# bust the layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Overlay the freshly-built SPA so the //go:embed picks up a real bundle —
# the repo's spa_dist is gitignored, so the build context carries none.
COPY --from=spa /src/internal/north/ui/spa_dist ./internal/north/ui/spa_dist

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w \
      -X github.com/SukramJ/openccu-loom/internal/build.Version=${VERSION} \
      -X github.com/SukramJ/openccu-loom/internal/build.Commit=${COMMIT} \
      -X github.com/SukramJ/openccu-loom/internal/build.BuildDate=${BUILD_DATE}" \
    -o /out/openccu-loom ./cmd/openccu-loom

# --- Stage 3: distroless runtime -------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/openccu-loom /app/openccu-loom
COPY --from=builder /src/assets/openapi.yaml /app/assets/openapi.yaml
USER nonroot:nonroot

# REST API + Config UI (SPA)
EXPOSE 8080/tcp
# first-run /setup + bootstrap HTMX (pre-auth)
EXPOSE 8081/tcp
# XML-RPC callback (CCU -> daemon)
EXPOSE 8120/tcp
# BIN-RPC callback (CUxD -> daemon)
EXPOSE 8129/tcp

ENTRYPOINT ["/app/openccu-loom"]
CMD ["run"]
