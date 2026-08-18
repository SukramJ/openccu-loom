// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

const (
	// secretsHealthComponent is the /health component reporting the at-rest
	// secret-cipher state (ADR 0027).
	secretsHealthComponent = "config.secrets"
	// secretsPlaintextMetric is 1 when config secrets are stored in plaintext
	// (no master key resolved), else 0.
	secretsPlaintextMetric = "config_secrets_plaintext"
)

// recordSecretHealth surfaces whether the at-rest secret cipher resolved a
// master key. When it did not, the daemon stores config secrets in plaintext
// (the ADR 0027 resilient fallback) — previously visible only as a single boot
// warning. Recording it as a degraded /health component and a Prometheus gauge
// lets an operator dashboard catch the condition without scraping logs.
//
// Recorded exactly once, at boot, with no periodic refresher — Sticky so it
// does not decay to StatusUnknown 90s later and drag a genuinely healthy
// daemon's /health to "unknown" for the rest of the process lifetime.
func recordSecretHealth(tracker *health.Tracker, reg *metrics.Registry, available bool) {
	if tracker != nil {
		// Sticky: this is a one-shot boot-time fact (the cipher either
		// found a master key or it did not) recorded exactly once, never
		// re-recorded on a heartbeat — without Sticky the tracker's
		// staleAfter decay would downgrade it to StatusUnknown 90 s after
		// boot regardless of how healthy the rest of the daemon is.
		if available {
			tracker.Record(secretsHealthComponent, health.Sample{Healthy: true, Note: "encrypted at rest", Sticky: true})
		} else {
			tracker.Record(secretsHealthComponent, health.Sample{
				Healthy: false,
				Note:    "no master key resolved — config secrets stored in plaintext",
				Sticky:  true,
			})
		}
	}
	if reg != nil {
		g := reg.Gauge(secretsPlaintextMetric,
			"1 when config secrets are stored in plaintext (no master key resolved), else 0")
		if available {
			g.Set(0)
		} else {
			g.Set(1)
		}
	}
}
