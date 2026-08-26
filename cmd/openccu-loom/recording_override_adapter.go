// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/history"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// recordingOverrideAdapter bridges the in-memory recording overlay to the
// REST handler interface.
type recordingOverrideAdapter struct {
	overlay *history.RecordingOverrides
}

// newRecordingOverrideAdapter returns a handler service over the overlay,
// or a genuine-nil interface when the overlay is nil (history feature
// off) so the REST route is not mounted.
func newRecordingOverrideAdapter(o *history.RecordingOverrides) handlers.RecordingOverrideService {
	if o == nil {
		return nil
	}
	return &recordingOverrideAdapter{overlay: o}
}

func (a *recordingOverrideAdapter) Effective(
	_ context.Context, central, iface, channel, parameter string,
) (record bool, source string, err error) {
	record, source = a.overlay.Effective(central, iface, channel, parameter)
	return record, source, nil
}

func (a *recordingOverrideAdapter) Set(
	ctx context.Context, central, iface, channel, parameter string, record bool, updatedBy string,
) error {
	return a.overlay.Set(ctx, central, iface, channel, parameter, record, updatedBy)
}

func (a *recordingOverrideAdapter) Clear(
	ctx context.Context, central, iface, channel, parameter, _ string,
) error {
	return a.overlay.Clear(ctx, central, iface, channel, parameter)
}
