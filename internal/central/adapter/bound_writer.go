// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/client"
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

// The writer the daemon wires into the device pipeline must carry the
// paramset capability, or the atomic path in generic.Switch is
// unreachable in production and every bounded switch-on splits into two
// radio transmissions — the defect this assertion was added for, found
// on the wire rather than by any test.
var _ ParamsetValueWriter = (*client.ValueWriter)(nil)

// paramsetBoundWriter is the boundWriter of a writer that can address a
// whole paramset. It exists as its own type because the capability has
// to be visible in the method set: a data point selects the atomic path
// by asking whether its writer implements [generic.ParamsetWriter], and
// a single type carrying a PutParamset that sometimes fails would turn
// a missing capability into a failed command instead of a fallback.
type paramsetBoundWriter struct {
	*boundWriter
	writer ParamsetValueWriter
}

// PutParamset routes to [ParamsetValueWriter.PutParamset] with the
// captured tuple.
func (b *paramsetBoundWriter) PutParamset(
	ctx context.Context, channelAddress string, paramsetKey hmenum.ParamsetKey,
	values map[string]any, priority hmenum.CommandPriority,
) error {
	if b == nil || b.writer == nil {
		return ErrNoWriter
	}
	return b.writer.PutParamset(ctx, b.centralName, b.interfaceID, channelAddress, paramsetKey, values, priority)
}

// newBoundWriter returns a writer bound to (central, interface). When
// the underlying writer can address a paramset, the returned value
// carries that capability so data points whose semantics require an
// atomic write can find it — without it the atomic branch in
// generic.Switch is unreachable and every bounded switch-on splits into
// two radio transmissions.
func newBoundWriter(centralName, interfaceID string, w ValueWriter) generic.Writer {
	b := &boundWriter{centralName: centralName, interfaceID: interfaceID, writer: w}
	if pw, ok := w.(ParamsetValueWriter); ok {
		return &paramsetBoundWriter{boundWriter: b, writer: pw}
	}
	return b
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
	bw      generic.Writer
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
	return a.backend.PutParamset(ctx, channelAddress, paramsetKey, values, priority, hmenum.CommandRxModeUnset)
}

var _ device.ChannelWriter = (*channelWriterAdapter)(nil)
