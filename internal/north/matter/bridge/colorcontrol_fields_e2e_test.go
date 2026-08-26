// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestCommandFieldsReader_ColorControl_MoveToHueAndSaturation proves that a
// real TLV-encoded MoveToHueAndSaturation payload (ContextTag0=Hue uint8,
// ContextTag1=Saturation uint8, ContextTag2=TransitionTime uint16) is decoded
// by commandFieldsReader into the typed wire.MoveToHueAndSaturationRequest
// struct the cluster servers expect.
func TestCommandFieldsReader_ColorControl_MoveToHueAndSaturation(t *testing.T) {
	t.Parallel()
	const (
		wantHue            uint8  = 127
		wantSat            uint8  = 200
		wantTransitionTime uint16 = 10
	)

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), uint64(wantHue))
	enc.PutUint(tlv.ContextTag(1), uint64(wantSat))
	enc.PutUint(tlv.ContextTag(2), uint64(wantTransitionTime))
	_ = enc.EndContainer()
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}

	dec := tlv.NewDecoder(raw)
	opener, err := dec.Next()
	if err != nil {
		t.Fatalf("dec.Next (opener): %v", err)
	}

	path := im.ConcreteCommandPath{
		Cluster: wire.ColorControlClusterID,
		Command: wire.ColorCtrlCmdMoveToHueAndSaturation,
	}
	got, err := commandFieldsReader(path, dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader: %v", err)
	}

	req, ok := got.(wire.MoveToHueAndSaturationRequest)
	if !ok {
		t.Fatalf("commandFieldsReader returned %T, want wire.MoveToHueAndSaturationRequest", got)
	}
	if req.Hue != wantHue {
		t.Errorf("Hue = %d, want %d", req.Hue, wantHue)
	}
	if req.Saturation != wantSat {
		t.Errorf("Saturation = %d, want %d", req.Saturation, wantSat)
	}
	if req.TransitionTime != wantTransitionTime {
		t.Errorf("TransitionTime = %d, want %d", req.TransitionTime, wantTransitionTime)
	}
}

// TestCommandFieldsReader_ColorControl_MoveToColorTemperature proves that a
// real TLV-encoded MoveToColorTemperature payload (ContextTag0=ColorTemperatureMireds
// uint16) is decoded into wire.MoveToColorTemperatureRequest.
func TestCommandFieldsReader_ColorControl_MoveToColorTemperature(t *testing.T) {
	t.Parallel()
	const wantMireds uint16 = 370

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), uint64(wantMireds))
	_ = enc.EndContainer()
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}

	dec := tlv.NewDecoder(raw)
	opener, err := dec.Next()
	if err != nil {
		t.Fatalf("dec.Next (opener): %v", err)
	}

	path := im.ConcreteCommandPath{
		Cluster: wire.ColorControlClusterID,
		Command: wire.ColorCtrlCmdMoveToColorTemperature,
	}
	got, err := commandFieldsReader(path, dec, opener)
	if err != nil {
		t.Fatalf("commandFieldsReader: %v", err)
	}

	req, ok := got.(wire.MoveToColorTemperatureRequest)
	if !ok {
		t.Fatalf("commandFieldsReader returned %T, want wire.MoveToColorTemperatureRequest", got)
	}
	if req.ColorTemperatureMireds != wantMireds {
		t.Errorf("ColorTemperatureMireds = %d, want %d", req.ColorTemperatureMireds, wantMireds)
	}
}
