// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubTDWriter is a no-op writer for text-display tests.
type stubTDWriter struct{ err error }

func (s *stubTDWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return s.err
}

// newBurstSensor creates a BinarySensor DP usable as burst-limit-warning.
func newBurstSensor() *generic.BinarySensor {
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ADDR:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterBurstLimitWarning),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// TestRowValidate_AcceptsShortText passes for text within MaxRowLength.
func TestRowValidate_AcceptsShortText(t *testing.T) {
	r := Row{ID: 1, Text: "Hello"}
	if err := r.Validate(); err != nil {
		t.Fatalf("expected nil for short text, got %v", err)
	}
}

// TestRowValidate_RejectsTooLongText returns ErrRowTooLong when text exceeds
// MaxRowLength.
func TestRowValidate_RejectsTooLongText(t *testing.T) {
	longText := make([]byte, MaxRowLength+1)
	for i := range longText {
		longText[i] = 'x'
	}
	r := Row{ID: 1, Text: string(longText)}
	if err := r.Validate(); !errors.Is(err, ErrRowTooLong) {
		t.Fatalf("expected ErrRowTooLong, got %v", err)
	}
}

// TestBurstLimitWarning_FalseWhenDPAbsent verifies BurstLimitWarning returns
// false when no burst-limit DP is installed.
func TestBurstLimitWarning_FalseWhenDPAbsent(t *testing.T) {
	td := New("ADDR:1", &stubTDWriter{})
	if td.BurstLimitWarning() {
		t.Fatal("expected false with no burst-limit DP")
	}
}

// TestBurstLimitWarning_TrueWhenDPAsserted verifies BurstLimitWarning returns
// true after the DP receives a true push.
func TestBurstLimitWarning_TrueWhenDPAsserted(t *testing.T) {
	td := New("ADDR:1", &stubTDWriter{})
	dp := newBurstSensor()
	td.SetBurstLimitWarningDP(dp)
	dp.OnEvent(true)
	if !td.BurstLimitWarning() {
		t.Fatal("expected BurstLimitWarning=true after DP asserted")
	}
}

// TestTextDisplayAvailabilityGatesOnBurstLimitWarning pins the availability
// gate to its primary state carrier (BURST_LIMIT_WARNING); see
// notes/parity/by_design.md.
func TestTextDisplayAvailabilityGatesOnBurstLimitWarning(t *testing.T) {
	td := New("ADDR:1", &stubTDWriter{})
	dp := newBurstSensor()
	td.SetBurstLimitWarningDP(dp)

	if td.IsRefreshed() {
		t.Fatal("IsRefreshed() must be false before any wire event")
	}
	dp.OnEvent(true)
	if !td.IsRefreshed() {
		t.Fatal("IsRefreshed() must be true after BURST_LIMIT_WARNING observed")
	}
}

// TestWriteRowValidation_ErrRowTooLong is returned by Write before any wire call.
func TestWriteRowValidation_ErrRowTooLong(t *testing.T) {
	td := New("ADDR:1", &stubTDWriter{})
	longText := make([]byte, MaxRowLength+1)
	for i := range longText {
		longText[i] = 'A'
	}
	err := td.Write(context.Background(), Row{ID: 1, Text: string(longText)}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrRowTooLong) {
		t.Fatalf("expected ErrRowTooLong from Write, got %v", err)
	}
}

// --- Constructor wiring ---

// TestConstructorWiresBurstLimitWarningDP verifies that the init.go constructor
// calls SetBurstLimitWarningDP when the channel carries a BURST_LIMIT_WARNING
// binary sensor, so that subsequent Write calls can log the warning.
func TestConstructorWiresBurstLimitWarningDP(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-WRCD:3"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GHI0001"})
	ch := d.AddChannel(addr, 3, "TEXT_DISPLAY", hmenum.ParamsetKeyValues)

	// Install a BURST_LIMIT_WARNING binary sensor on the channel.
	blwDP := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterBurstLimitWarning),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(blwDP)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	if !ok {
		t.Fatal("constructor not registered for DeviceProfileIPTextDisplay")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	td, ok := dp.(*TextDisplay)
	if !ok {
		t.Fatalf("expected *TextDisplay, got %T", dp)
	}

	// The BurstLimitWarning accessor must be wired — drive a true event and
	// confirm it propagates.
	blwDP.OnEvent(true)
	if !td.BurstLimitWarning() {
		t.Error("BurstLimitWarning() must return true after BURST_LIMIT_WARNING event=true")
	}

	blwDP.OnEvent(false)
	if td.BurstLimitWarning() {
		t.Error("BurstLimitWarning() must return false after BURST_LIMIT_WARNING event=false")
	}
}

// TestConstructorWithoutBurstLimitWarningDP verifies that the constructor
// succeeds when no BURST_LIMIT_WARNING parameter is present, and that
// BurstLimitWarning() returns false.
func TestConstructorWithoutBurstLimitWarningDP(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-WRCD:4"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GHI0002"})
	ch := d.AddChannel(addr, 4, "TEXT_DISPLAY", hmenum.ParamsetKeyValues)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPTextDisplay)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	td, ok := dp.(*TextDisplay)
	if !ok {
		t.Fatalf("expected *TextDisplay, got %T", dp)
	}

	if td.BurstLimitWarning() {
		t.Error("BurstLimitWarning() must be false when parameter absent")
	}
}

// TestTextDisplayBaseDPMethodsExist verifies that TextDisplay embeds custom.BaseDP
// and exposes its observability methods without panicking.
func TestTextDisplayBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	td := New("VCU0001:3", nil)

	// Must compile and return zero values before any event.
	_, _ = td.ModifiedAt()
	_, _ = td.RefreshedAt()
	_ = td.UnconfirmedLastValuesSend()

	td.MarkModified()
	td.MarkRefreshed()

	if _, ok := td.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := td.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
