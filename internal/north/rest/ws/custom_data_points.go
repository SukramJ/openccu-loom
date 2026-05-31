// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CustomDPIndex is the minimal read surface the WS custom-DP commands
// need. The concrete implementation wires against the CentralRegistry.
type CustomDPIndex interface {
	// Devices returns all registered devices.
	Devices() []*device.Device
	// Device returns a single device by address.
	Device(address string) (*device.Device, bool)
}

// CustomDPInvoker is the mutating surface for WS custom-DP commands.
// Uses the same contract as the REST writer so one adapter satisfies both.
type CustomDPInvoker interface {
	InvokeCustomDP(
		ctx context.Context,
		deviceAddress string,
		name string,
		operation string,
		params map[string]any,
		priority hmenum.CommandPriority,
		source string,
	) error
}

// CustomDPCommandsConfig bundles the optional providers the custom-DP
// and calculated-DP WS command families consume.
type CustomDPCommandsConfig struct {
	Index   CustomDPIndex
	Invoker CustomDPInvoker
}

// RegisterCustomDPCommands wires the custom/calculated data-point WS
// commands onto router. Call alongside RegisterDefaultCommands and
// RegisterExtendedCommands.
func RegisterCustomDPCommands(router *Router, cfg CustomDPCommandsConfig) {
	if router == nil {
		return
	}
	if cfg.Index != nil {
		router.Register("cdp.list", customDPListHandler(cfg.Index))
		router.Register("cdp.get", customDPGetHandler(cfg.Index))
		router.Register("calc_dp.list", calculatedDPListHandler(cfg.Index))
		router.Register("calc_dp.get", calculatedDPGetHandler(cfg.Index))
	}
	if cfg.Index != nil && cfg.Invoker != nil {
		router.Register("cdp.invoke", customDPSetHandler(cfg.Index, cfg.Invoker))
	}
}

// --- cdp.list ---

func customDPListHandler(idx CustomDPIndex) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Device string `json:"device"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}

		if p.Device != "" {
			d, ok := idx.Device(p.Device)
			if !ok {
				return nil, errors.New("ws: device not found: " + p.Device)
			}
			return customDPsForDevice(d), nil
		}

		// Return all devices.
		out := make(map[string]any)
		for _, d := range idx.Devices() {
			dps := customDPsForDevice(d)
			if len(dps) > 0 {
				out[d.Address] = dps
			}
		}
		return out, nil
	}
}

// --- cdp.get ---

func customDPGetHandler(idx CustomDPIndex) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Device string `json:"device"`
			Name   string `json:"name"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Device == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device is required")
		}
		if p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name is required")
		}
		d, ok := idx.Device(p.Device)
		if !ok {
			return nil, errors.New("ws: device not found: " + p.Device)
		}
		for _, ch := range d.Channels() {
			dp := ch.CustomDataPoint()
			if dp == nil {
				continue
			}
			if dp.DataPointKey().Parameter == p.Name {
				return customDPEntry(dp, ch.Number), nil
			}
		}
		return nil, errors.New("ws: custom data point not found: " + p.Name)
	}
}

// --- cdp.invoke ---

func customDPSetHandler(idx CustomDPIndex, invoker CustomDPInvoker) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Device    string         `json:"device"`
			Name      string         `json:"name"`
			Operation string         `json:"operation"`
			Params    map[string]any `json:"params"`
			Priority  string         `json:"priority"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Device == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device is required")
		}
		if p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name is required")
		}
		if p.Operation == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "operation is required")
		}
		if _, ok := idx.Device(p.Device); !ok {
			return nil, errors.New("ws: device not found: " + p.Device)
		}
		prio := wsParsePriority(p.Priority)
		if err := invoker.InvokeCustomDP(ctx, p.Device, p.Name, p.Operation, p.Params, prio, "ws:cdp.invoke"); err != nil {
			if errors.Is(err, handlers.ErrUnknownOperation) {
				return nil, errors.New("ws: unknown operation: " + p.Operation)
			}
			return nil, err
		}
		return map[string]any{"device": p.Device, "name": p.Name, "operation": p.Operation}, nil
	}
}

// --- calc_dp.list ---

func calculatedDPListHandler(idx CustomDPIndex) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Device    string `json:"device"`
			ChannelNo *int   `json:"channel_no"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Device != "" {
			d, ok := idx.Device(p.Device)
			if !ok {
				return nil, errors.New("ws: device not found: " + p.Device)
			}
			return calculatedDPsForDevice(d, p.ChannelNo), nil
		}
		// All devices.
		out := make(map[string]any)
		for _, d := range idx.Devices() {
			dps := calculatedDPsForDevice(d, nil)
			if len(dps) > 0 {
				out[d.Address] = dps
			}
		}
		return out, nil
	}
}

// --- calc_dp.get ---

func calculatedDPGetHandler(idx CustomDPIndex) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Device    string `json:"device"`
			ChannelNo int    `json:"channel_no"`
			Name      string `json:"name"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Device == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "device is required")
		}
		if p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "name is required")
		}
		d, ok := idx.Device(p.Device)
		if !ok {
			return nil, errors.New("ws: device not found: " + p.Device)
		}
		for _, ch := range d.Channels() {
			if p.ChannelNo > 0 && ch.Number != p.ChannelNo {
				continue
			}
			for _, dp := range ch.CalculatedDataPoints() {
				if dp.DataPointKey().Parameter == p.Name {
					return toCalculatedDPEntry(dp), nil
				}
			}
		}
		return nil, errors.New("ws: calculated data point not found: " + p.Name)
	}
}

// --- shared renderers ---

func customDPsForDevice(d *device.Device) []map[string]any {
	out := make([]map[string]any, 0)
	for _, ch := range d.Channels() {
		dp := ch.CustomDataPoint()
		if dp == nil {
			continue
		}
		// A custom DP wrapping a half-formed channel (e.g. a Cover
		// whose LEVEL parameter is missing) returns the zero
		// DataPointKey from its DataPointKey() override. Skip those —
		// downstream calls (Category, DataPointState) would otherwise
		// dispatch through autogenerated forwarders to a nil-pointer
		// receiver and panic.
		if dp.DataPointKey().Parameter == "" {
			continue
		}
		out = append(out, customDPEntry(dp, ch.Number))
	}
	return out
}

func customDPEntry(dp device.AttachableDataPoint, channelNo int) map[string]any {
	key := dp.DataPointKey()
	cat := hmenum.DataPointCategoryUndefined
	if cdp, ok := dp.(device.CategorisedDataPoint); ok {
		cat = cdp.Category()
	}
	entry := map[string]any{
		"name":       key.Parameter,
		"category":   string(cat),
		"channel_no": channelNo,
		"operations": supportedOperationsForWS(cat),
	}
	// Include state snapshot when possible.
	type stater interface{ DataPointState() any }
	if s, ok2 := dp.(stater); ok2 {
		entry["state"] = s.DataPointState()
	}
	return entry
}

func calculatedDPsForDevice(d *device.Device, channelNo *int) []map[string]any {
	out := make([]map[string]any, 0)
	for _, ch := range d.Channels() {
		if channelNo != nil && ch.Number != *channelNo {
			continue
		}
		for _, dp := range ch.CalculatedDataPoints() {
			out = append(out, toCalculatedDPEntry(dp))
		}
	}
	return out
}

func toCalculatedDPEntry(dp device.AttachableDataPoint) map[string]any {
	key := dp.DataPointKey()
	entry := map[string]any{
		"name": key.Parameter,
	}
	if cdp, ok := dp.(device.CategorisedDataPoint); ok {
		entry["category"] = string(cdp.Category())
	}
	type rawValuer interface {
		RawValue() (any, bool)
	}
	if rv, ok := dp.(rawValuer); ok {
		v, observed := rv.RawValue()
		entry["value"] = v
		entry["observed"] = observed
	}
	return entry
}

// supportedOperationsForWS mirrors handlers.supportedOperationsFor.
func supportedOperationsForWS(cat hmenum.DataPointCategory) []string { //nolint:exhaustive // non-custom categories return nil deliberately
	switch cat {
	case hmenum.DataPointCategoryLight:
		return []string{"turn_on", "turn_off", "set_brightness", "set_color", "set_color_temperature", "set_effect"}
	case hmenum.DataPointCategoryClimate:
		return []string{"set_temperature", "enable_boost", "disable_boost", "set_mode", "set_profile", "enable_away", "disable_away"}
	case hmenum.DataPointCategoryCover:
		return []string{"open", "close", "set_position", "stop", "set_tilt"}
	case hmenum.DataPointCategoryLock:
		return []string{"lock", "unlock", "open"}
	case hmenum.DataPointCategorySiren:
		return []string{"turn_on", "turn_off"}
	case hmenum.DataPointCategoryTextDisplay:
		return []string{"write", "clear"}
	case hmenum.DataPointCategoryValve:
		return []string{"open", "close", "set_level"}
	case hmenum.DataPointCategorySwitch:
		return []string{"turn_on", "turn_off", "turn_on_for", "toggle"}
	default:
		return nil
	}
}

func wsParsePriority(s string) hmenum.CommandPriority {
	switch s {
	case "critical":
		return hmenum.CommandPriorityCritical
	case "low":
		return hmenum.CommandPriorityLow
	}
	return hmenum.CommandPriorityHigh
}
