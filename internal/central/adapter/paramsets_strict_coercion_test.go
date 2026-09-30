// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

const (
	strictTestDevice       = "STR0001"
	strictTestChannel      = "STR0001:1"
	strictTestPeer         = "PEER0002:1"
	strictTestParamFloat   = "TEMPERATURE_OFFSET"
	strictTestParamSpecial = "DURATION_VALUE"
	strictTestParamUnknown = "NOT_IN_DESCRIPTION"
	strictTestSpecialValue = 111
)

// strictTestDescriptors answers the same description for every key: one
// FLOAT with a range, one INTEGER whose SPECIAL sentinel lies above MAX.
func strictTestDescriptors() map[string]hmproto.ParameterData {
	rw := hmenum.OperationsRead | hmenum.OperationsWrite
	return map[string]hmproto.ParameterData{
		strictTestParamFloat: {
			Type:       hmenum.ParameterTypeFloat,
			Operations: rw,
			Min:        json.RawMessage(`-3.5`),
			Max:        json.RawMessage(`3.5`),
		},
		strictTestParamSpecial: {
			Type:       hmenum.ParameterTypeInteger,
			Operations: rw,
			Min:        json.RawMessage(`0`),
			Max:        json.RawMessage(`100`),
			Special:    json.RawMessage(`[{"ID":"NOT_USED","VALUE":111}]`),
		},
	}
}

// strictCoercionFakeBackend records whether any write reached the backend
// and what it carried; the description fetch answers the fixed set above or
// a forced error.
type strictCoercionFakeBackend struct {
	fakeOperations
	descErr   error
	putCalled bool
	putValues map[string]any
}

func (b *strictCoercionFakeBackend) GetParamsetDescription(
	_ context.Context, _ string, _ hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	if b.descErr != nil {
		return nil, b.descErr
	}
	return strictTestDescriptors(), nil
}

func (b *strictCoercionFakeBackend) GetParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey,
) (map[string]any, error) {
	return map[string]any{}, nil
}

func (b *strictCoercionFakeBackend) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any,
	_ hmenum.CommandPriority, _ hmenum.CommandRxMode,
) error {
	b.putCalled = true
	b.putValues = values
	return nil
}

func (b *strictCoercionFakeBackend) GetLinkParamset(
	_ context.Context, _, _ string,
) (map[string]any, error) {
	return map[string]any{}, nil
}

func (b *strictCoercionFakeBackend) PutLinkParamset(
	_ context.Context, _, _ string, values map[string]any,
) error {
	b.putCalled = true
	b.putValues = values
	return nil
}

// describedOps wraps any fake backend so its paramset description answers a
// fixed set for every key. Paramset writes are strict against that
// description, so a fixture that exercises the backend write path must
// describe every parameter it writes.
type describedOps struct {
	backends.Operations
	descs map[string]hmproto.ParameterData
}

func (d describedOps) GetParamsetDescription(
	_ context.Context, _ string, _ hmenum.ParamsetKey,
) (map[string]hmproto.ParameterData, error) {
	return d.descs, nil
}

// describe builds a description in which every named parameter is a
// writable parameter of the given type with no range.
func describe(typ hmenum.ParameterType, names ...string) map[string]hmproto.ParameterData {
	out := make(map[string]hmproto.ParameterData, len(names))
	for _, n := range names {
		out[n] = hmproto.ParameterData{
			Type:       typ,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		}
	}
	return out
}

// buildStrictCoercionDomain registers a device that holds no channel under
// strictTestChannel, so a MASTER write takes the backend branch of
// PutParamsetOn rather than the model's Channel.SetMany.
func buildStrictCoercionDomain(t *testing.T, be *strictCoercionFakeBackend) *ParamsetsDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-strict"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	c.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     strictTestDevice,
		Model:       "HmIP-STH",
	}))
	be.fakeOperations = fakeOperations{kind: backends.KindCCU}
	w := client.NewValueWriter()
	w.Register("ccu-strict", "HmIP-RF", be)
	return NewParamsetsDomain(reg, w)
}

// TestParamsetWritesAreStrictAgainstTheDescription pins that the backend
// branch of a MASTER write and every LINK write send nothing the channel's
// own description does not carry, and send nothing at all when that
// description cannot be read.
func TestParamsetWritesAreStrictAgainstTheDescription(t *testing.T) {
	t.Parallel()

	validPayload := func() map[string]any {
		return map[string]any{
			strictTestParamFloat:   1.0,
			strictTestParamSpecial: float64(strictTestSpecialValue),
		}
	}
	withUnknown := func() map[string]any {
		v := validPayload()
		v[strictTestParamUnknown] = 1.0
		return v
	}

	writeMaster := func(d *ParamsetsDomain, v map[string]any) error {
		return d.PutParamsetOn(context.Background(), "", strictTestChannel, hmenum.ParamsetKeyMaster, v)
	}
	writeLink := func(d *ParamsetsDomain, v map[string]any) error {
		return d.PutLinkParamset(context.Background(), strictTestChannel, strictTestPeer, v)
	}

	cases := []struct {
		name           string
		write          func(*ParamsetsDomain, map[string]any) error
		values         map[string]any
		descErr        error
		wantErr        bool
		wantErrContain string
		wantValidation bool
	}{
		{
			name:           "master unknown parameter is rejected",
			write:          writeMaster,
			values:         withUnknown(),
			wantErr:        true,
			wantErrContain: strictTestParamUnknown,
			wantValidation: true,
		},
		{
			name:           "link unknown parameter is rejected",
			write:          writeLink,
			values:         withUnknown(),
			wantErr:        true,
			wantErrContain: strictTestParamUnknown,
			wantValidation: true,
		},
		{
			name:           "master description failure refuses the write",
			write:          writeMaster,
			values:         validPayload(),
			descErr:        errors.New("ccu unreachable"),
			wantErr:        true,
			wantErrContain: "ccu unreachable",
		},
		{
			name:           "link description failure refuses the write",
			write:          writeLink,
			values:         validPayload(),
			descErr:        errors.New("ccu unreachable"),
			wantErr:        true,
			wantErrContain: "ccu unreachable",
		},
		{
			name:   "master valid payload with special value is coerced and sent",
			write:  writeMaster,
			values: validPayload(),
		},
		{
			name:   "link valid payload with special value is coerced and sent",
			write:  writeLink,
			values: validPayload(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			be := &strictCoercionFakeBackend{descErr: tc.descErr}
			domain := buildStrictCoercionDomain(t, be)

			err := tc.write(domain, tc.values)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("write: %v", err)
				}
				if !be.putCalled {
					t.Fatal("backend write was not called")
				}
				if k := reflect.TypeOf(be.putValues[strictTestParamSpecial]).Kind(); k != reflect.Int {
					t.Errorf("%s: got kind %v, want Int", strictTestParamSpecial, k)
				}
				if got := be.putValues[strictTestParamSpecial]; got != strictTestSpecialValue {
					t.Errorf("%s: got %v, want %d", strictTestParamSpecial, got, strictTestSpecialValue)
				}
				if k := reflect.TypeOf(be.putValues[strictTestParamFloat]).Kind(); k != reflect.Float64 {
					t.Errorf("%s: got kind %v, want Float64", strictTestParamFloat, k)
				}
				return
			}

			if err == nil {
				t.Fatal("write succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErrContain) {
				t.Errorf("error %q does not contain %q", err, tc.wantErrContain)
			}
			if got := errors.Is(err, hmerr.ErrValidation); got != tc.wantValidation {
				t.Errorf("errors.Is(err, ErrValidation) = %v, want %v (err: %v)", got, tc.wantValidation, err)
			}
			if be.putCalled {
				t.Fatalf("backend write was called with %v, want no write", be.putValues)
			}
		})
	}
}
