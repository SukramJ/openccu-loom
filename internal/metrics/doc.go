// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package metrics exposes a Prometheus-style metrics surface.
//
// The MVP is stdlib-only — no prometheus/client_golang dependency.
// Collectors expose counters and gauges through a [Registry]. The
// REST handler (`/metrics`) renders the registry as OpenMetrics text
// so scrapers can ingest without fuss.
package metrics
