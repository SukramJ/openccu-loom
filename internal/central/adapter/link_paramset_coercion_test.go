// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// Parameter names modeled on a real HMIP-PS LINK paramset (SHORT_* actor
// behaviour on a peered switch channel).
const (
	linkTestParamEnum    = "SHORT_JT_ON"
	linkTestParamInteger = "SHORT_COND_VALUE_HI"
	linkTestParamBool    = "SHORT_MULTIEXECUTE"
	linkTestParamFloat   = "SHORT_COND_VALUE_LO"
)

// linkParamsetTestValues is the all-float64 payload every JSON-decoded
// caller (REST, WS) actually hands the domain layer.
func linkParamsetTestValues() map[string]any {
	return map[string]any{
		linkTestParamEnum:    4.0,
		linkTestParamInteger: 150.0,
		linkTestParamBool:    0.0,
		linkTestParamFloat:   1.5,
	}
}

func linkParamsetTestDescriptors() map[string]hmproto.ParameterData {
	rw := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent
	return map[string]hmproto.ParameterData{
		linkTestParamEnum: {
			Type:       hmenum.ParameterTypeEnum,
			Operations: rw,
			ValueList:  []string{"OFF", "SHORT", "MEDIUM", "LONG", "PERMANENT"},
		},
		linkTestParamInteger: {
			Type:       hmenum.ParameterTypeInteger,
			Operations: rw,
		},
		linkTestParamBool: {
			Type:       hmenum.ParameterTypeBool,
			Operations: rw,
		},
		linkTestParamFloat: {
			Type:       hmenum.ParameterTypeFloat,
			Operations: rw,
		},
	}
}

// assertLinkParamsetCoerced checks the concrete Go kind of every value the
// fake backend's PutLinkParamset actually received: ENUM and INTEGER must
// arrive as an integer kind, BOOL as bool, FLOAT stays float64. Comparing
// values (4 == 4.0) would pass without the fix under test, so this asserts
// reflect.Kind instead.
func assertLinkParamsetCoerced(t *testing.T, got map[string]any) {
	t.Helper()
	if k := reflect.TypeOf(got[linkTestParamEnum]).Kind(); k != reflect.Int {
		t.Errorf("%s: got kind %v, want Int", linkTestParamEnum, k)
	}
	if k := reflect.TypeOf(got[linkTestParamInteger]).Kind(); k != reflect.Int {
		t.Errorf("%s: got kind %v, want Int", linkTestParamInteger, k)
	}
	if k := reflect.TypeOf(got[linkTestParamBool]).Kind(); k != reflect.Bool {
		t.Errorf("%s: got kind %v, want Bool", linkTestParamBool, k)
	}
	if k := reflect.TypeOf(got[linkTestParamFloat]).Kind(); k != reflect.Float64 {
		t.Errorf("%s: got kind %v, want Float64", linkTestParamFloat, k)
	}
}

// assertLinkParamsetUnchanged checks that every value the fake backend's
// PutLinkParamset received is still float64 — the soft-fail passthrough
// when the descriptor fetch itself fails.
func assertLinkParamsetUnchanged(t *testing.T, got map[string]any) {
	t.Helper()
	for name := range linkParamsetTestValues() {
		if k := reflect.TypeOf(got[name]).Kind(); k != reflect.Float64 {
			t.Errorf("%s: got kind %v, want Float64 (unchanged)", name, k)
		}
	}
}

// linkCoercionFakeBackend is a minimal backends.Operations stub recording
// what PutLinkParamset receives, with GetParamsetDescription answering
// either the fixed descriptor set above or a forced error.
type linkCoercionFakeBackend struct {
	fakeOperations
	descErr   error
	putValues map[string]any
	putCalled bool
}

func (b *linkCoercionFakeBackend) GetParamsetDescription(
	_ context.Context, _ string, key hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	if key != hmenum.ParamsetKeyLink {
		return nil, nil
	}
	if b.descErr != nil {
		return nil, b.descErr
	}
	return linkParamsetTestDescriptors(), nil
}

func (b *linkCoercionFakeBackend) PutLinkParamset(
	_ context.Context, _, _ string, values map[string]any,
) error {
	b.putCalled = true
	b.putValues = values
	return nil
}

func (b *linkCoercionFakeBackend) GetLinkParamset(
	_ context.Context, _, _ string,
) (map[string]any, error) {
	return map[string]any{}, nil
}

// buildLinkCoercionFixture wires a registry + ValueWriter around one
// device/channel and the given fake backend, for both the ParamsetsDomain
// and the LinksDomain entry points.
func buildLinkCoercionFixture(t *testing.T, be *linkCoercionFakeBackend) (
	reg *central.Registry, w *client.ValueWriter,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-link"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "LNK0001",
		Model:       "HMIP-PS",
	})
	dev.AddChannel("LNK0001:4", 4, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	be.fakeOperations = fakeOperations{kind: backends.KindCCU}
	w = client.NewValueWriter()
	w.Register("ccu-link", "HmIP-RF", be)
	return reg, w
}

// A1: ParamsetsDomain.PutLinkParamset coerces float64-in-JSON values
// against the LINK descriptor before forwarding to the backend.
func TestParamsetsDomainPutLinkParamsetCoercesValues(t *testing.T) {
	t.Parallel()
	be := &linkCoercionFakeBackend{}
	reg, w := buildLinkCoercionFixture(t, be)
	domain := NewParamsetsDomain(reg, w)

	if err := domain.PutLinkParamset(
		context.Background(), "LNK0001:4", "PEER0001:1", linkParamsetTestValues(),
	); err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	if !be.putCalled {
		t.Fatal("backend PutLinkParamset was not called")
	}
	assertLinkParamsetCoerced(t, be.putValues)
}

// A2: LinksDomain.PutLinkParamset — the WS route — coerces the same way.
func TestLinksDomainPutLinkParamsetCoercesValues(t *testing.T) {
	t.Parallel()
	be := &linkCoercionFakeBackend{}
	reg, w := buildLinkCoercionFixture(t, be)
	domain := NewLinksDomain(reg, w, nil)

	if err := domain.PutLinkParamset(
		context.Background(), "LNK0001:4", "PEER0001:1", linkParamsetTestValues(),
	); err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	if !be.putCalled {
		t.Fatal("backend PutLinkParamset was not called")
	}
	assertLinkParamsetCoerced(t, be.putValues)
}

// A3: when the descriptor fetch fails, values reach the backend unchanged
// — the soft-fail path coerceParamsetValues already implements.
func TestPutLinkParamsetPassesThroughUnchangedOnDescriptorError(t *testing.T) {
	t.Parallel()
	be := &linkCoercionFakeBackend{descErr: errors.New("ccu unreachable")}
	reg, w := buildLinkCoercionFixture(t, be)
	domain := NewParamsetsDomain(reg, w)

	if err := domain.PutLinkParamset(
		context.Background(), "LNK0001:4", "PEER0001:1", linkParamsetTestValues(),
	); err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	if !be.putCalled {
		t.Fatal("backend PutLinkParamset was not called")
	}
	assertLinkParamsetUnchanged(t, be.putValues)
}
