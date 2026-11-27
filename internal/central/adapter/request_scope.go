// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// requestUnits returns the centrals an address lookup made on behalf of ctx
// may consult: every registered central in name order when ctx carries no
// central binding, only the bound central when it does, and none when the
// bound central is not registered.
//
// A device address is unique within one CCU but not across several — the
// virtual-remote roots, the INT000* group devices and CUxD devices repeat
// verbatim on every CCU. The REST device routes bind the request to the
// central the caller named, and every lookup behind them walks this list so
// the request reaches that CCU's device rather than the first one by name.
// A caller without a binding (event bridge, MQTT sink, boot) passes an
// unscoped ctx and keeps the name-ordered walk.
func requestUnits(ctx context.Context, reg *central.Registry) []*central.Unit {
	if reg == nil {
		return nil
	}
	return reg.Scoped(ctx)
}

// unitHolding returns the first central among [requestUnits] whose model
// holds deviceAddress, together with that device. It reports false when no
// central the request may consult holds the address; a scoped request never
// falls back to another central.
func unitHolding(ctx context.Context, reg *central.Registry, deviceAddress string) (*central.Unit, *device.Device, bool) {
	for _, u := range requestUnits(ctx, reg) {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		if dev, ok := u.ModelRegistry.Get(deviceAddress); ok && dev != nil {
			return u, dev, true
		}
	}
	return nil, nil, false
}
