// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build loadtest

// Package loadtest is the pre-release load / soak-test harness for the
// OpenCCU-Loom daemon. It is gated behind the `loadtest` build tag so it
// never runs on the everyday `go test ./...` path; run it explicitly:
//
//	go test -tags=loadtest ./tests/loadtest/...
//
// # What it exercises
//
// The harness builds the in-process daemon stack — a godevccu virtual
// CCU, a central.Unit with its device fleet ingested through the real
// DevicePipeline, and an httptest.Server routing the two hot REST paths
// through the production chi handlers:
//
//   - GET  /api/v1/devices/{addr}/channels/{no}/data-points        (reads)
//   - PUT  /api/v1/devices/{addr}/channels/{no}/data-points/{param}/value (writes)
//
// Concurrent, errgroup-bounded REST read and write generators drive
// those endpoints, while a WS-style subscriber on the central EventBus
// consumes the same DataPointValueChanged fan-out the live WS pump
// publishes. The write workload is what makes the fan-out plane busy:
// every optimistic PUT rolls a value-changed event.
//
// MQTT is OPTIONAL. The smoke run is hermetic (no Docker, no broker);
// the MQTT plane only engages when LOADTEST_MQTT_URL points at a broker.
// The seam is reserved (see runMQTTWorker) — operators who want the MQTT
// command plane under load run it through the existing
// tests/integration mosquitto harness.
//
// # SMOKE vs. SOAK
//
// With no env set the harness runs a SMOKE workload: ~20-device fleet,
// ~3s window, low rps, loose thresholds. It finishes in a few seconds
// and passes deterministically — that is the CI / developer default.
//
// The operator-run pre-release SOAK scales it up and tightens the gate:
//
//	LOADTEST_DEVICES=1000 \
//	LOADTEST_DURATION=60m \
//	LOADTEST_RPS=10000 \
//	LOADTEST_STRICT=1 \
//	go test -tags=loadtest -timeout=90m -run TestProductionLoad ./tests/loadtest/...
//
// (Add LOADTEST_MQTT_URL=tcp://host:1883 to engage the MQTT plane.)
//
// # Environment knobs
//
//	LOADTEST_DEVICES   fleet size. Default 20. A value larger than the
//	                   curated smoke set loads the full embedded fleet
//	                   (~399 device instances) so the model approaches a
//	                   heavy production CCU.
//	LOADTEST_DURATION  workload window (Go duration). Default 3s.
//	LOADTEST_RPS       target aggregate request rate. Default 200. Worker
//	                   pool sizes derive from this (75% reads / 25% writes).
//	LOADTEST_STRICT    1 selects the pre-release threshold set. Default 0
//	                   (loose smoke thresholds).
//	LOADTEST_MQTT_URL  optional broker URL. Unset → MQTT plane skipped.
//
// # Metrics
//
//   - Latency percentiles (p50/p95/p99) per REST verb, from an exact
//     sorted-sample histogram (metrics.go) — no new dependency.
//   - Dropped-request counter: any non-2xx or transport error.
//   - Goroutine-leak check: runtime.NumGoroutine baseline captured after
//     warmup vs. after teardown, with a settle window + forced GC so
//     idle-conn reapers retire before the snapshot.
//   - Heap stability: runtime.ReadMemStats HeapAlloc before vs. after,
//     reported as a growth ratio.
//
// # Pass / fail thresholds
//
// SMOKE (default) — loose, deterministic under contended CI:
//
//	read  p99 < 750ms,  write p99 < 1500ms,
//	dropped-request rate ≤ 2%, goroutine delta ≤ 50, heap ratio ≤ 8.0.
//
// STRICT (pre-release, LOADTEST_STRICT=1) — the docs/testplan.md gate:
//
//	read  p99 < 50ms,   write p99 < 200ms,
//	zero dropped rows, no goroutine leak (delta 0), heap ratio ≤ 1.5.
//
// If the harness cannot reach the in-process daemon (e.g. a broken
// godevccu bring-up), the test t.Skips with a clear message rather than
// failing — a load test that never started has nothing to assert.
//
// # Status
//
// The harness is implemented and the smoke run is part of the gated
// suite. The ≥1000-device / ≥60-minute production-scale soak is
// operator-run before tagging a release; it is not part of routine CI.
package loadtest
