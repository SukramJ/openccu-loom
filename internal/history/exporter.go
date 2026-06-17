// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package history

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// MeasurementExporter receives each recorded sample for forwarding to an
// external time-series store (InfluxDB, VictoriaMetrics, …). It is the
// opt-in power-user sink alongside the embedded history.db.
//
// Implementations MUST NOT block: Export runs on the EventBus publisher
// goroutine via the recorder, so it must buffer and return immediately.
// The pattern mirrors the span exporter seam in ADR 0037.
type MeasurementExporter interface {
	// Export is handed each sample that passed the recorder's provenance
	// guard. It must not block.
	Export(sample sqlite.MeasurementSample)
	// Shutdown flushes any buffered samples and stops background work.
	Shutdown(ctx context.Context) error
}
