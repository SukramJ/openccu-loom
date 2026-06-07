// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// homegearSysvarBackend is the minimal Homegear surface the sysvar
// loader needs. The full backend (Operations) satisfies it; tests use a
// narrow fake.
type homegearSysvarBackend interface {
	GetAllSystemVariables(ctx context.Context) ([]map[string]any, error)
	SetSystemVariable(ctx context.Context, name string, value any) error
}

// homegearSysvarWriter implements [hub.SysvarWriter] over a Homegear
// backend's XML-RPC setSystemVariable. The JSON-RPC writer used for a
// CCU is not reachable on Homegear (no JSON-RPC layer), so sysvar value
// writes route through the XML-RPC method instead.
type homegearSysvarWriter struct {
	backend homegearSysvarBackend
}

// SetSysvar writes a single sysvar value via the Homegear XML-RPC
// setSystemVariable method.
func (w *homegearSysvarWriter) SetSysvar(ctx context.Context, name string, value any) error {
	return w.backend.SetSystemVariable(ctx, name, value)
}

// wireHomegearHubIfPresent wires the XML-RPC sysvar path when the central
// has a Homegear backend registered; it is a no-op otherwise. Kept as a
// single-statement helper so the caller (WireCentrals) stays flat.
func wireHomegearHubIfPresent(ctx context.Context, unit *central.Unit, reg *backendRegistry, logger *slog.Logger) {
	hg := reg.homegearBackend()
	if hg == nil {
		return
	}
	wireHomegearHubSysvars(ctx, unit, hg, logger)
}

// wireHomegearHubSysvars wires the XML-RPC sysvar load + periodic-refresh
// path for a Homegear-backed central. Homegear is XML-RPC-only, so the
// JSON-RPC hub path (WireHub → loadSysvars via SysVar.getAll) fails at
// login and never populates the hub model; without this the
// `/api/v1/sysvars` surface stays empty for a Homegear installation.
// The reference Homegear backend fetches sysvars over the XML-RPC
// getAllSystemVariables method; this mirrors that. Programs / rooms /
// functions remain unsupported on Homegear on both stacks, so only the
// Sysvars refresh hook is set.
func wireHomegearHubSysvars(ctx context.Context, unit *central.Unit, backend homegearSysvarBackend, logger *slog.Logger) {
	if unit == nil || unit.HubModel == nil || backend == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	// The per-sysvar value-write path uses this writer (SysvarWriter). The
	// hub-level SysvarMutator (create / update-metadata / delete) is left
	// nil for Homegear: its CreateSysvar/UpdateSysvar signatures carry
	// ReGa-only metadata (valueType, unit, min/max, value-list) that
	// Homegear's setSystemVariable(name, value) does not model, so those
	// operations correctly surface as "not configured" rather than a
	// lossy half-mapping. Reading sysvars and writing their values — the
	// actual Homegear parity surface — work through here.
	writer := &homegearSysvarWriter{backend: backend}

	refresh := func(ctx context.Context) error {
		return loadHomegearSysvars(ctx, backend, unit.HubModel, writer)
	}
	if err := refresh(ctx); err != nil {
		logger.Warn("hub.sysvars.homegear.load",
			slog.String("central", unit.Name()),
			slog.String("err", err.Error()))
	} else {
		logger.Info("hub.sysvars.homegear.ok",
			slog.String("central", unit.Name()),
			slog.Int("count", len(unit.HubModel.Sysvars())))
	}

	// Wire the periodic refresh so the hub.sysvar_refresh scheduler job
	// (default 5 min) reloads the Homegear sysvar set. Only Sysvars is
	// set; the other hub surfaces have no Homegear counterpart.
	if unit.Hub != nil {
		unit.Hub.SetRefreshHooks(coordinators.RefreshHooks{Sysvars: refresh})
	}
}

// loadHomegearSysvars fetches all Homegear system variables via XML-RPC
// and reconciles them into the hub model: existing entries are updated
// in place (so OnUpdate subscribers stay valid across periodic
// refreshes), new ones are created, and entries the backend no longer
// reports are removed. CCU-internal variables (IsExcludedSysvar) are
// skipped, matching the JSON-RPC path.
//
// Homegear's getAllSystemVariables carries only name + value — no type
// descriptor, unit, value-list, or description — so the value type is
// inferred from the Go value (classifyHomegearSysvar), mirroring the
// old-value-based inference the reference stack applies for the same reason.
func loadHomegearSysvars(ctx context.Context, backend homegearSysvarBackend, h *hub.Hub, writer hub.SysvarWriter) error {
	all, err := backend.GetAllSystemVariables(ctx)
	if err != nil {
		return fmt.Errorf("loadHomegearSysvars: %w", err)
	}
	fresh := make(map[string]struct{}, len(all))
	for _, entry := range all {
		name, _ := entry["name"].(string)
		if name == "" || hub.IsExcludedSysvar(name) {
			continue
		}
		fresh[name] = struct{}{}
		vt, pv, ok := classifyHomegearSysvar(entry["value"])
		if existing, found := h.Sysvar(name); found {
			existing.ValueType = vt
			existing.Writer = writer
			if ok {
				existing.OnValue(pv)
			}
			continue
		}
		sv := hub.NewSysvar(h.CentralName, name, "", vt, writer)
		if ok {
			sv.OnValue(pv)
		}
		h.PutSysvar(sv)
	}
	// Drop sysvars the backend no longer reports.
	for _, existing := range h.Sysvars() {
		if _, ok := fresh[existing.Name]; !ok {
			h.RemoveSysvar(existing.Name)
		}
	}
	return nil
}

// classifyHomegearSysvar infers a [hmenum.HubValueType] and builds the
// matching [hmtypes.ParamValue] from a raw Homegear sysvar value. The
// value is first normalised to a Go primitive so named transport types
// (e.g. xmlrpc.IntValue) classify the same as their native counterparts.
// The third return is false when the value is absent / uncoercible, in
// which case the sysvar is created with the inferred type but no value.
func classifyHomegearSysvar(raw any) (hmenum.HubValueType, hmtypes.ParamValue, bool) {
	switch v := normalizeScalar(raw).(type) {
	case bool:
		return hmenum.HubValueTypeLogic, hmtypes.BoolValue(v), true
	case int:
		return hmenum.HubValueTypeInteger, hmtypes.IntValue(v), true
	case float64:
		return hmenum.HubValueTypeFloat, hmtypes.FloatValue(v), true
	case string:
		return hmenum.HubValueTypeString, hmtypes.StringValue(v), true
	default:
		// nil / unsupported: keep it as a string-typed sysvar with no value.
		return hmenum.HubValueTypeString, hmtypes.ParamValue{}, false
	}
}

// normalizeScalar collapses a raw value (native Go primitive or a named
// transport type such as xmlrpc.IntValue / BoolValue / DoubleValue /
// StringValue) onto one of bool, int, float64, string. Returns nil for a
// nil input.
func normalizeScalar(raw any) any {
	switch raw.(type) {
	case nil:
		return nil
	case bool, int, float64, string:
		return raw
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()) //nolint:gosec // sysvar magnitudes are small; overflow is not a practical concern; see #20
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.String:
		return rv.String()
	default:
		return fmt.Sprintf("%v", raw)
	}
}
