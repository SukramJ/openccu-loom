// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Shared wire-call logic for the six Operations methods that are
// identical (modulo the underlying [Caller] and the backend name used
// in error messages) across CcuBackend, CuxdBackend, and
// HomegearBackend: ListDevices, GetParamsetDescription, GetParamset,
// PutParamset, SetValue, GetValue. Each backend method is a thin
// wrapper around the corresponding helper here so the wire-decoding
// logic exists exactly once.

package backends

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// listDevicesViaCaller implements the ListDevices wire call shared by
// every backend. prefix names the backend in error messages ("ccu",
// "cuxd", "homegear").
func listDevicesViaCaller(ctx context.Context, caller Caller, prefix string) ([]hmproto.DeviceDescription, error) {
	if caller == nil {
		return nil, ErrNotWired
	}
	raw, err := caller.Call(ctx, "listDevices")
	if err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s.ListDevices: unexpected type %T", prefix, raw)
	}
	out := make([]hmproto.DeviceDescription, 0, len(list))
	for i, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s.ListDevices[%d]: not a struct", prefix, i)
		}
		dd, err := toDeviceDescription(m)
		if err != nil {
			return nil, fmt.Errorf("%s.ListDevices[%d]: %w", prefix, i, err)
		}
		out = append(out, dd)
	}
	return out, nil
}

// listReplaceableDevicesViaCaller implements the ListReplaceableDevices
// wire call: the interface daemon computes which already-paired devices
// the given new device may replace (type / channel compatibility) and
// returns their descriptions. Mirrors [listDevicesViaCaller] but with
// the one-argument `listReplaceableDevices(newAddress)` method.
func listReplaceableDevicesViaCaller(ctx context.Context, caller Caller, prefix, newAddress string) ([]hmproto.DeviceDescription, error) {
	return listStructArrayViaCaller(ctx, caller, prefix, "listReplaceableDevices", newAddress)
}

// listStructArrayViaCaller runs a wire method returning an array of
// device-description structs (`listReplaceableDevices`, `listTeams`, …)
// and decodes each into a [hmproto.DeviceDescription]. args are the
// positional method arguments (none for listTeams).
func listStructArrayViaCaller(ctx context.Context, caller Caller, prefix, method string, args ...any) ([]hmproto.DeviceDescription, error) {
	if caller == nil {
		return nil, ErrNotWired
	}
	raw, err := caller.Call(ctx, method, args...)
	if err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s: unexpected type %T", prefix, method, raw)
	}
	out := make([]hmproto.DeviceDescription, 0, len(list))
	for i, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s.%s[%d]: not a struct", prefix, method, i)
		}
		dd, err := toDeviceDescription(m)
		if err != nil {
			return nil, fmt.Errorf("%s.%s[%d]: %w", prefix, method, i, err)
		}
		out = append(out, dd)
	}
	return out, nil
}

// getParamsetDescriptionViaCaller implements the GetParamsetDescription
// wire call shared by every backend.
func getParamsetDescriptionViaCaller(
	ctx context.Context, caller Caller, prefix, address string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	if caller == nil {
		return nil, ErrNotWired
	}
	raw, err := caller.Call(ctx, "getParamsetDescription", address, string(key))
	if err != nil {
		return nil, err
	}
	outer, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.GetParamsetDescription: unexpected type %T", prefix, raw)
	}
	out := make(map[string]hmproto.ParameterData, len(outer))
	for param, inner := range outer {
		m, ok := inner.(map[string]any)
		if !ok {
			continue
		}
		pd, err := toParameterData(m)
		if err != nil {
			return nil, fmt.Errorf("%s.GetParamsetDescription[%s]: %w", prefix, param, err)
		}
		out[param] = pd
	}
	return out, nil
}

// getParamsetViaCaller implements the GetParamset wire call shared by
// every backend.
func getParamsetViaCaller(
	ctx context.Context, caller Caller, prefix, address string, key hmenum.ParamsetKey,
) (map[string]any, error) {
	if caller == nil {
		return nil, ErrNotWired
	}
	raw, err := caller.Call(ctx, "getParamset", address, string(key))
	if err != nil {
		return nil, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.GetParamset: unexpected type %T", prefix, raw)
	}
	return m, nil
}

// putParamsetViaCaller implements the PutParamset wire call shared by
// every backend. When appendRxMode is false, rxMode is never placed on
// the wire regardless of its value — CUxD (BIN-RPC) and Homegear have
// no rx_mode argument slot; only the CCU's XML-RPC putParamset accepts
// one, and even there only when the caller actually set it.
func putParamsetViaCaller(
	ctx context.Context, caller Caller, address string, key hmenum.ParamsetKey,
	values map[string]any, priority hmenum.CommandPriority,
	rxMode hmenum.CommandRxMode, appendRxMode bool,
) error {
	if caller == nil {
		return ErrNotWired
	}
	if appendRxMode && rxMode != hmenum.CommandRxModeUnset {
		_, err := caller.CallAt(ctx, priority, "putParamset", address, string(key), values, string(rxMode))
		return err
	}
	_, err := caller.CallAt(ctx, priority, "putParamset", address, string(key), values)
	return err
}

// setValueViaCaller implements the SetValue wire call shared by every
// backend. The command's priority is forwarded rather than dropped: the
// throttle and the circuit breaker both read it, and a stop that
// arrives as ordinary traffic queues behind pending writes and is
// refused outright while the breaker is open.
// See [putParamsetViaCaller] for the appendRxMode contract.
func setValueViaCaller(
	ctx context.Context, caller Caller, address string, parameter hmenum.Parameter,
	value any, priority hmenum.CommandPriority,
	rxMode hmenum.CommandRxMode, appendRxMode bool,
) error {
	if caller == nil {
		return ErrNotWired
	}
	if appendRxMode && rxMode != hmenum.CommandRxModeUnset {
		_, err := caller.CallAt(ctx, priority, "setValue", address, string(parameter), value, string(rxMode))
		return err
	}
	_, err := caller.CallAt(ctx, priority, "setValue", address, string(parameter), value)
	return err
}

// getValueViaCaller implements the GetValue wire call shared by every
// backend.
func getValueViaCaller(ctx context.Context, caller Caller, address string, parameter hmenum.Parameter) (any, error) {
	if caller == nil {
		return nil, ErrNotWired
	}
	return caller.Call(ctx, "getValue", address, string(parameter))
}
