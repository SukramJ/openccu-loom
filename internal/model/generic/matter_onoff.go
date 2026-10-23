// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	fabricdevice "github.com/SukramJ/go-fabric/device"
)

// matterOnOffSink adapts a profile's function onto [onoffdef.Sink].
type matterOnOffSink func(ctx context.Context, on bool) error

func (f matterOnOffSink) SetOnOff(ctx context.Context, on bool) error { return f(ctx, on) }

// MatterOnOffDeclaration is the OnOff cluster of a bridged source as a
// declaration for go-fabric's device kit: the kit's server owns the
// attribute values, the six commands, the OnTime / OffWaitTime
// countdowns and the scene paths. The source contributes the CCU write a command becomes (sink) and the
// state the server starts from (initial); later CCU state reaches the
// server through [SetMatterOnOff].
func MatterOnOffDeclaration(lighting, initial bool, sink func(ctx context.Context, on bool) error) onoffdef.Declare {
	d := onoffdef.Declare{Initial: onoffdef.Patch{OnOff: &initial}}
	if lighting {
		d.Features = onoffdef.FeatureLighting
	}
	if sink != nil {
		d.Sink = matterOnOffSink(sink)
	}
	return d
}

// SetMatterOnOff stores the CCU's confirmed state in the OnOff server of
// d without asking the Sink, so a wall switch or a CCU program reaches a
// subscribed controller and a confirmed command does not echo into a
// second write. A nil device, or one without an OnOff server, has
// nothing to store.
func SetMatterOnOff(d *fabricdevice.Device, on bool) {
	if d == nil {
		return
	}
	srv := d.Server(onoffdef.ClusterID)
	if srv == nil {
		return
	}
	_ = onoffdef.Wrap(srv).Set(onoffdef.Patch{OnOff: &on})
}
