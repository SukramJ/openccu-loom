// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"
	"time"
)

// Sample is one forwarded measurement: a numeric value of one data
// point at one instant. Multi-CCU safe: it carries the central name and
// the canonical interface id next to the channel address.
type Sample struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	TS             time.Time
	Value          float64
}

// Exporter is the sink the Forwarder hands samples to: a target that
// stores them outside the daemon. The seam is modelled on the trace
// SpanExporter, so a further target is one more implementation.
type Exporter interface {
	// Export is handed each sample that passed the provenance guard and
	// the recording policy. It must not block.
	Export(sample Sample)
	// Shutdown flushes any buffered samples and stops background work.
	Shutdown(ctx context.Context) error
}
