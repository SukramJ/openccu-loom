// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestWriteUnconfirmedValueRoutesCombinedParameter verifies that
// WriteUnconfirmedValue decomposes a COMBINED_PARAMETER wire string into its
// constituent sub-parameters (LEVEL, LEVEL_2) recorded under ParamsetKeyValues,
// rather than storing the raw combined string under a single key.
func TestWriteUnconfirmedValueRoutesCombinedParameter(t *testing.T) {
	t.Parallel()

	const (
		iface   = hmenum.InterfaceHmIPRF
		channel = "VCU0123456:1"
		// "L=100,L2=50" → LEVEL=1.0, LEVEL_2=0.50
		wireValue = "L=100,L2=50"
	)

	ic := newOrchIC(t, iface)
	ic.WriteUnconfirmedValue(channel, hmenum.ParameterCombinedParameter, hmenum.ParamsetKeyValues, wireValue)

	tr := ic.CommandTracker()
	// The tracker stamps entries with the wire interface id — the same
	// `<central>-<interface>` form the CCU echo carries — so the lookup key
	// has to be built the same way.
	interfaceID := string(hmtypes.NewWireInterfaceID("test", iface))

	// The raw COMBINED_PARAMETER key must NOT be present as a plain set-value.
	rawKey := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(hmenum.ParameterCombinedParameter),
	}
	if _, ok := tr.GetLastSentValue(rawKey); ok {
		t.Error("raw COMBINED_PARAMETER key must not be stored as a plain set-value after decomposition")
	}

	// The constituent sub-parameter LEVEL must be tracked.
	levelKey := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}
	levelVal, ok := tr.GetLastSentValue(levelKey)
	if !ok {
		t.Fatal("expected LEVEL sub-parameter to be tracked after COMBINED_PARAMETER decomposition")
	}
	if levelVal != 1.0 {
		t.Errorf("LEVEL: got %v, want 1.0", levelVal)
	}

	// The constituent sub-parameter LEVEL_2 must also be tracked.
	level2Key := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL_2",
	}
	level2Val, ok := tr.GetLastSentValue(level2Key)
	if !ok {
		t.Fatal("expected LEVEL_2 sub-parameter to be tracked after COMBINED_PARAMETER decomposition")
	}
	if level2Val != 0.5 {
		t.Errorf("LEVEL_2: got %v, want 0.5", level2Val)
	}
}

// TestWriteUnconfirmedValueRoutesLevelCombined verifies that a LEVEL_COMBINED
// wire string is also routed through AddCombinedParameter, decomposing into
// LEVEL and LEVEL_SLATS sub-parameters recorded under ParamsetKeyValues.
func TestWriteUnconfirmedValueRoutesLevelCombined(t *testing.T) {
	t.Parallel()

	const (
		iface   = hmenum.InterfaceHmIPRF
		channel = "VCU9876543:2"
		// "0x64,0x32" → LEVEL = 0x64/200 = 0.5, LEVEL_SLATS = 0x32/200 = 0.25
		wireValue = "0x64,0x32"
	)

	ic := newOrchIC(t, iface)
	ic.WriteUnconfirmedValue(channel, hmenum.ParameterLevelCombined, hmenum.ParamsetKeyValues, wireValue)

	tr := ic.CommandTracker()
	// The tracker stamps entries with the wire interface id — the same
	// `<central>-<interface>` form the CCU echo carries — so the lookup key
	// has to be built the same way.
	interfaceID := string(hmtypes.NewWireInterfaceID("test", iface))

	// The raw LEVEL_COMBINED key must NOT be recorded as a plain set-value.
	rawKey := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(hmenum.ParameterLevelCombined),
	}
	if _, ok := tr.GetLastSentValue(rawKey); ok {
		t.Error("raw LEVEL_COMBINED key must not be stored as a plain set-value after decomposition")
	}

	// LEVEL sub-parameter must be present with the decoded float value.
	levelKey := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}
	levelVal, ok := tr.GetLastSentValue(levelKey)
	if !ok {
		t.Fatal("expected LEVEL sub-parameter to be tracked after LEVEL_COMBINED decomposition")
	}
	// hex 0x64 is decimal 100, scaled against the 200-unit range yields 0.5.
	const wantLevel = 0.5
	if levelVal != wantLevel {
		t.Errorf("LEVEL: got %v, want %v", levelVal, wantLevel)
	}

	// LEVEL_SLATS sub-parameter must also be present.
	slatsKey := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL_SLATS",
	}
	slatsVal, ok := tr.GetLastSentValue(slatsKey)
	if !ok {
		t.Fatal("expected LEVEL_SLATS sub-parameter to be tracked after LEVEL_COMBINED decomposition")
	}
	// hex 0x32 is decimal 50, scaled against the 200-unit range yields 0.25.
	const wantSlats = 0.25
	if slatsVal != wantSlats {
		t.Errorf("LEVEL_SLATS: got %v, want %v", slatsVal, wantSlats)
	}
}

// TestWriteUnconfirmedValuePlainParameterRecordsDirectly verifies that a
// non-convertable parameter (STATE with a bool value) is stored directly via
// AddSetValue — exactly one entry keyed by that parameter, with the raw value.
func TestWriteUnconfirmedValuePlainParameterRecordsDirectly(t *testing.T) {
	t.Parallel()

	const (
		iface   = hmenum.InterfaceHmIPRF
		channel = "VCU0000001:1"
	)

	ic := newOrchIC(t, iface)
	ic.WriteUnconfirmedValue(channel, hmenum.ParameterState, hmenum.ParamsetKeyValues, true)

	tr := ic.CommandTracker()
	stateKey := hmtypes.DataPointKey{
		InterfaceID:    string(hmtypes.NewWireInterfaceID("test", iface)),
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(hmenum.ParameterState),
	}
	val, ok := tr.GetLastSentValue(stateKey)
	if !ok {
		t.Fatal("expected STATE to be tracked via AddSetValue for a plain parameter")
	}
	if val != true {
		t.Errorf("STATE: got %v, want true", val)
	}

	// Tracker must hold exactly one entry (STATE only; no decomposition noise).
	if got := tr.Size(); got != 1 {
		t.Errorf("tracker size: got %d, want 1 (no spurious entries)", got)
	}
}
