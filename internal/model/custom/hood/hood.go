// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hood implements the hood (range-hood / extractor-fan) custom
// data point. The CCU represents fan speed as an integer code on the
// LEVEL parameter (0..3 for OFF/LOW/MEDIUM/HIGH on HmIP-COOK class
// devices). [Hood] layers a semantic FanSpeed view and typed Set command
// on top of the channel's existing [generic.Integer] LEVEL data point.
package hood

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Writer is an alias for [custom.Writer].
type Writer = custom.Writer

// FanSpeed enumerates the semantic fan-speed levels.
type FanSpeed int

// FanSpeed values.
const (
	FanSpeedOff    FanSpeed = 0
	FanSpeedLow    FanSpeed = 1
	FanSpeedMedium FanSpeed = 2
	FanSpeedHigh   FanSpeed = 3
)

// FanSpeedLabel returns a short human-readable label for each speed.
func FanSpeedLabel(s FanSpeed) string {
	switch s {
	case FanSpeedLow:
		return "LOW"
	case FanSpeedMedium:
		return "MEDIUM"
	case FanSpeedHigh:
		return "HIGH"
	default:
		return "OFF"
	}
}

// fanSpeedCodeMap maps the CCU integer wire code to a [FanSpeed].
// The HmIP-COOK class encodes fan speed as an integer on the LEVEL
// parameter: 0 = off, 1 = low, 2 = medium, 3 = high.
var fanSpeedCodeMap = map[int32]FanSpeed{
	0: FanSpeedOff,
	1: FanSpeedLow,
	2: FanSpeedMedium,
	3: FanSpeedHigh,
}

// FanSpeedFromCode decodes a CCU wire code to a [FanSpeed].
// Returns [FanSpeedOff] and false when the code is unrecognised.
func FanSpeedFromCode(code int32) (FanSpeed, bool) {
	s, ok := fanSpeedCodeMap[code]
	return s, ok
}

// Hood is a range-hood / extractor-fan device driven by an integer
// LEVEL parameter encoding the fan-speed stage.
type Hood struct {
	custom.BaseDP

	Address string

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	key    hmtypes.DataPointKey
	writer Writer
	level  *generic.Integer
}

// Config is the constructor record. The channel must carry an INTEGER
// LEVEL data point.
type Config struct {
	Channel *device.Channel
	Writer  Writer
}

// New constructs a Hood.
func New(cfg Config) *Hood {
	addr := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		addr = cfg.Channel.Address
		if dev := cfg.Channel.Device(); dev != nil {
			key = hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterLevel),
			}
		} else {
			key = hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterLevel),
			}
		}
	}
	h := &Hood{
		Address: addr,
		key:     key,
		writer:  cfg.Writer,
		level:   custom.IntegerField(cfg.Channel, hmenum.ParameterLevel),
	}
	return h
}

// DataPointKey returns the composite identifier for this custom data
// point. Satisfies [device.AttachableDataPoint].
func (h *Hood) DataPointKey() hmtypes.DataPointKey { return h.key }

// IsRefreshed reports whether the LEVEL data point has been observed at
// least once.
func (h *Hood) IsRefreshed() bool {
	if h.level == nil {
		return false
	}
	_, ok := h.level.Value()
	return ok
}

// FanSpeed returns the current fan speed decoded from the LEVEL wire code
// and whether a LEVEL value has been observed.
func (h *Hood) FanSpeed() (FanSpeed, bool) {
	if h.level == nil {
		return FanSpeedOff, false
	}
	code, ok := h.level.Value()
	if !ok {
		return FanSpeedOff, false
	}
	speed, found := FanSpeedFromCode(code)
	if !found {
		return FanSpeedOff, true
	}
	return speed, true
}

// SetFanSpeed commands the hood to the given fan speed by writing the
// corresponding wire code to the LEVEL parameter.
func (h *Hood) SetFanSpeed(ctx context.Context, speed FanSpeed, priority hmenum.CommandPriority) error {
	if h.writer == nil {
		return fmt.Errorf("hood: writer required")
	}
	code := int32(speed) //nolint:gosec // FanSpeed values are small constants (0-3); safe to narrow
	if err := h.writer.SetValue(custom.EnsureContext(ctx), h.Address, hmenum.ParameterLevel, code, priority); err != nil {
		return fmt.Errorf("hood: SetFanSpeed %s: %w", FanSpeedLabel(speed), err)
	}
	if h.level != nil {
		h.level.OnEvent(code)
	}
	return nil
}

// Subscribe wires the channel's LEVEL parameter into the Hood so
// push-driven CCU updates reach [FanSpeed] without hand-routing.
// Implements [device.SubscribingDataPoint].
func (h *Hood) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	if h.level != nil {
		unsubs = append(unsubs, h.level.OnAnyUpdate(func(_, _ any) {}))
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}
