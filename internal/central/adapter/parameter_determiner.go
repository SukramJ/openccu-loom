// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// parameter_determiner.go — ParameterDeterminerAdapter wires the
// ws.ParameterDeterminer interface onto the backend's DetermineParameter
// operation.
//
// The WS layer passes (interfaceID, channelAddress, parameterID). This
// adapter resolves the channel address to the owning central and backend
// via the registry, then delegates to backends.Operations.DetermineParameter.
//
// Mirrors the Python path in websocket_api.py:ws_determine_parameter which
// calls device.client.determine_parameter(channel_address, parameter).

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
)

// ErrNoDetermineBackend is returned when no backend can be resolved for
// the given channel address.
var ErrNoDetermineBackend = fmt.Errorf("parameter_determiner: no backend for device")

// ParameterDeterminerAdapter implements ws.ParameterDeterminer by
// routing DetermineParameter calls through the central registry and the
// client ValueWriter.
//
// Construct with [NewParameterDeterminerAdapter]; the zero value is not
// useful.
type ParameterDeterminerAdapter struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewParameterDeterminerAdapter wires the adapter.
func NewParameterDeterminerAdapter(r *central.Registry, w *client.ValueWriter) *ParameterDeterminerAdapter {
	return &ParameterDeterminerAdapter{registry: r, writer: w}
}

// DetermineParameter implements ws.ParameterDeterminer.
//
// Resolves the owning central and backend for channelAddress, then calls
// backends.Operations.DetermineParameter. The interfaceID argument from
// the WS request is ignored in favour of the registry-derived interface
// so the lookup is consistent with how other adapters work. Returns nil
// when the backend does not support DetermineParameter (e.g. CUxD).
func (a *ParameterDeterminerAdapter) DetermineParameter(
	ctx context.Context,
	_ string, // interfaceID — resolved from registry
	channelAddress string,
	parameterID string,
) (any, error) {
	if a.registry == nil || a.writer == nil {
		return nil, ErrNoDetermineBackend
	}
	devAddr := deviceAddressOf(channelAddress)
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		b, ok := a.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return nil, fmt.Errorf("%w: %s/%s", ErrNoDetermineBackend, u.Name(), dev.InterfaceID)
		}
		value, err := b.DetermineParameter(ctx, channelAddress, parameterID)
		if err != nil {
			return nil, fmt.Errorf("parameter_determiner: %w", err)
		}
		return value, nil
	}
	return nil, fmt.Errorf("%w: device %s", ErrNoDetermineBackend, devAddr)
}
