// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestUISchemaValuesParamset_LevelDisplayValue pins the config-panel side
// of the DisplayValue projection through the real UISchema entry point
// (not by calling generic.DisplayValue directly): a LEVEL data point
// observed at the raw wire value 0.42 must surface both `multiplier`
// (100) and `display_value` (42) on its VALUES-paramset parameter entry.
func TestUISchemaValuesParamset_LevelDisplayValue(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis-dv"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV010",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-BROLL",
	})
	chAddr := "UISDEV010:1"
	ch := d.AddChannel(chAddr, 1, "BLIND", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Unit:       "100%",
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Flags:      hmenum.FlagVisible,
		},
	})
	ch.Put(dp)
	dp.OnEvent(0.42)
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	schema, err := a.UISchema(context.Background(), hmapi.UISchemaRequest{
		Address:  "UISDEV010",
		Channel:  1,
		Paramset: "VALUES",
		Locale:   "en",
	})
	if err != nil {
		t.Fatalf("UISchema: %v", err)
	}

	var found *hmapi.UISchemaParameter
	for i := range schema.Parameters {
		if schema.Parameters[i].Name == string(hmenum.ParameterLevel) {
			found = &schema.Parameters[i]
		}
	}
	if found == nil {
		t.Fatal("LEVEL parameter not present in UISchema VALUES parameters")
	}
	if fv, ok := found.Value.(float64); !ok || fv != 0.42 {
		t.Fatalf("value = %#v, want the untouched raw wire value 0.42", found.Value)
	}
	if found.Multiplier != 100 {
		t.Fatalf("multiplier = %v, want 100", found.Multiplier)
	}
	if fv, ok := found.DisplayValue.(float64); !ok || fv != 42 {
		t.Fatalf("display_value = %#v, want 42 (0.42 * multiplier 100)", found.DisplayValue)
	}
}

// TestUISchemaLinkParamset_NeverReachesValueProjection guards the
// LINK/DisplayAsPercent split: UISchema must route a LINK paramset
// request into buildLinkSchema BEFORE buildParameters ever runs, because
// LINK parameters already scale through DisplayAsPercent
// (internal/central/adapter/uischema_link.go) — running the VALUES/MASTER
// DisplayValue projection on top would double-scale.
//
// Driven without a wired [client.ValueWriter] on purpose: buildLinkSchema
// fails fast with its own "requires wired value writer" error before
// touching the backend, and buildParameters has no such requirement — so
// seeing that exact error proves the request took the LINK branch, not
// the VALUES/MASTER one buildParameters serves.
func TestUISchemaLinkParamset_NeverReachesValueProjection(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis-link"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV011",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-BROLL",
	})
	_ = d.AddChannel("UISDEV011:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg} // writer is deliberately nil
	_, err = a.UISchema(context.Background(), hmapi.UISchemaRequest{
		Address:  "UISDEV011",
		Channel:  1,
		Paramset: "LINK",
		Peer:     "PEERDEV01:1",
		Locale:   "en",
	})
	if err == nil {
		t.Fatal("expected an error routing a LINK request without a wired writer")
	}
	wantErr := errors.New("ui-schema: LINK paramset requires wired value writer")
	if err.Error() != wantErr.Error() {
		t.Fatalf("error = %q, want %q (proves the LINK early return fired, not buildParameters)", err.Error(), wantErr.Error())
	}
}

// TestUISchemaValuesParamset_MultiplierReportedBeforeFirstValue pins the
// multiplier as descriptor metadata rather than as part of the observed
// state: it is reported for a writable parameter the CCU has not pushed a
// value for yet.
//
// The editor divides the number the operator types by this factor before
// writing, because the CCU takes the wire value. Reporting the factor only
// alongside an observed value would leave a freshly-booted daemon writing
// the displayed number straight through — an operator typing 42 into a
// dimmer that has not reported yet would send 42 where 0.42 belongs, and
// the CCU clamps that to fully on.
func TestUISchemaValuesParamset_MultiplierReportedBeforeFirstValue(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-uis-dv-unobserved"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := device.New(device.Config{
		Address:     "UISDEV011",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-BROLL",
	})
	chAddr := "UISDEV011:1"
	ch := d.AddChannel(chAddr, 1, "BLIND", hmenum.ParamsetKeyValues)
	// Deliberately no OnEvent: the parameter has never been observed.
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Unit:       "100%",
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Flags:      hmenum.FlagVisible,
		},
	}))
	c.ModelRegistry.Put(d)

	a := &UISchemaAdapter{registry: reg}
	schema, err := a.UISchema(context.Background(), hmapi.UISchemaRequest{
		Address:  "UISDEV011",
		Channel:  1,
		Paramset: "VALUES",
		Locale:   "en",
	})
	if err != nil {
		t.Fatalf("UISchema: %v", err)
	}

	var found *hmapi.UISchemaParameter
	for i := range schema.Parameters {
		if schema.Parameters[i].Name == string(hmenum.ParameterLevel) {
			found = &schema.Parameters[i]
		}
	}
	if found == nil {
		t.Fatal("LEVEL parameter not present in UISchema VALUES parameters")
	}
	if found.Observed {
		t.Fatalf("observed = true, want false — the fixture pushes no value")
	}
	if found.Multiplier != 100 {
		t.Fatalf("multiplier = %v, want 100 even without an observed value", found.Multiplier)
	}
	if found.DisplayValue != nil {
		t.Fatalf("display_value = %#v, want absent — there is no value to project", found.DisplayValue)
	}
}
