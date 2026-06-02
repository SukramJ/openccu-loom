// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// boundWriter captures (central, interface) so each generic data
// point can satisfy [generic.Writer] without knowing its own routing
// tuple. The pipeline creates one instance per device, passes it into
// every constructed data point, and the point dispatches to the
// configured [ValueWriter].
type boundWriter struct {
	centralName string
	interfaceID string
	writer      ValueWriter
}

func newBoundWriter(centralName, interfaceID string, w ValueWriter) *boundWriter {
	return &boundWriter{centralName: centralName, interfaceID: interfaceID, writer: w}
}

// SetValue routes to [ValueWriter.SetValue] with the captured tuple.
func (b *boundWriter) SetValue(
	ctx context.Context, channelAddress string,
	parameter hmenum.Parameter, value any, priority hmenum.CommandPriority,
) error {
	if b == nil || b.writer == nil {
		return ErrNoWriter
	}
	return b.writer.SetValue(ctx, b.centralName, b.interfaceID, channelAddress, parameter, value, priority)
}

var _ generic.Writer = (*boundWriter)(nil)

// channelWriterAdapter bridges a [boundWriter] (SetValue routing) and a
// [backends.Operations] backend (PutParamset) into the [device.ChannelWriter]
// interface. One instance is created per channel during hydration and
// installed via [device.Channel.SetWriter].
type channelWriterAdapter struct {
	bw      *boundWriter
	backend backends.Operations
}

// SetValue routes through the bound writer so the (central, interface)
// routing tuple is preserved.
func (a *channelWriterAdapter) SetValue(
	ctx context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	return a.bw.SetValue(ctx, channelAddress, parameter, value, priority)
}

// PutParamset routes through the backend's PutParamset directly.
func (a *channelWriterAdapter) PutParamset(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	// Priority hint is advisory for PutParamset; the backend does not
	// consume it on this path but we accept it to satisfy the interface.
	_ = priority
	return a.backend.PutParamset(ctx, channelAddress, paramsetKey, values, hmenum.CommandRxModeUnset)
}

var _ device.ChannelWriter = (*channelWriterAdapter)(nil)
