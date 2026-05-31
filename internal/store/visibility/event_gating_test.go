// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// eventSourceWrapper wraps an [event.Source] to satisfy the
// [device.AttachableEvent] interface so it can be stored on a channel
// via [device.Channel.AttachGenericEvent].
type eventSourceWrapper struct {
	src *event.Source
	key hmtypes.DataPointKey
}

func (w *eventSourceWrapper) DataPointKey() hmtypes.DataPointKey { return w.key }
func (w *eventSourceWrapper) EventKind() string                  { return string(w.src.Kind) }

// SetOperationModeAllowed delegates to the underlying [event.Source].
// This makes the wrapper satisfy [visibility.operationModeGater] and
// the pipeline pass can reach the source.
func (w *eventSourceWrapper) SetOperationModeAllowed(allowed bool) {
	w.src.SetOperationModeAllowed(allowed)
}

// attachEventSource creates a new [event.Source] for param on ch,
// wraps it in an [eventSourceWrapper], attaches it to the channel, and
// returns the source so the test can query its [Usage].
func attachEventSource(ch *device.Channel, interfaceID string, param hmenum.Parameter) *event.Source {
	src := event.NewSource(ch.Address, param)
	if src == nil {
		return nil
	}
	key := hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: ch.Address,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(param),
	}
	ch.AttachGenericEvent(&eventSourceWrapper{src: src, key: key})
	return src
}

// TestEventSourceGatedByChannelOperationMode verifies that a PRESS_SHORT
// event source on a KEY_TRANSCEIVER channel running BINARY_BEHAVIOR mode is
// gated to NoCreate usage.
func TestEventSourceGatedByChannelOperationMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "iface", Address: "EVGAT01", Model: "HmIP-FCI1"})
	ch := d.AddChannel("EVGAT01:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	pressShort := attachEventSource(ch, "iface", hmenum.ParameterPressShort)
	if pressShort == nil {
		t.Fatal("event.NewSource returned nil for PRESS_SHORT — parameter not in click set?")
	}
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "BINARY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	// PRESS_SHORT is not allowed in BINARY_BEHAVIOR → NoCreate.
	if got := pressShort.Usage(); got != hmenum.DataPointUsageIgnored {
		t.Errorf("PRESS_SHORT Usage in BINARY_BEHAVIOR = %q, want Ignored", got)
	}
}

// TestEventSourceAllowedByChannelOperationMode verifies that a
// PRESS_SHORT event source is NOT gated to NoCreate when the channel
// runs in KEY_BEHAVIOR (PRESS_SHORT is in the allowed set for that
// mode). Mirrors the same tri-state logic — M6 positive case.
func TestEventSourceAllowedByChannelOperationMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "iface", Address: "EVGAT02", Model: "HmIP-FCI1"})
	ch := d.AddChannel("EVGAT02:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	pressShort := attachEventSource(ch, "iface", hmenum.ParameterPressShort)
	if pressShort == nil {
		t.Fatal("event.NewSource returned nil for PRESS_SHORT")
	}
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "KEY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	// PRESS_SHORT is allowed in KEY_BEHAVIOR → Event usage.
	if got := pressShort.Usage(); got != hmenum.DataPointUsageEvent {
		t.Errorf("PRESS_SHORT Usage in KEY_BEHAVIOR = %q, want Event", got)
	}
}

// TestEventSourceUnaffectedWhenNoOperationMode verifies that an event
// source's Usage remains Event when no CHANNEL_OPERATION_MODE has been
// observed — the default EVENT usage applies in that nil branch.
// M6 nil-tri-state case.
func TestEventSourceUnaffectedWhenNoOperationMode(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "iface", Address: "EVGAT03", Model: "HmIP-FCI1"})
	ch := d.AddChannel("EVGAT03:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	pressShort := attachEventSource(ch, "iface", hmenum.ParameterPressShort)
	if pressShort == nil {
		t.Fatal("event.NewSource returned nil for PRESS_SHORT")
	}
	// No CHANNEL_OPERATION_MODE seeded — gating must be a no-op.
	visibility.ApplyChannelOperationModeGating(ch)

	if got := pressShort.Usage(); got != hmenum.DataPointUsageEvent {
		t.Errorf("PRESS_SHORT Usage with no mode = %q, want Event (no gating)", got)
	}
}

// TestEventSourceNonGatedParameterUnchanged verifies that a
// SEQUENCE_OK impulse source on an ungated parameter is left with
// Event usage even on a configurable channel with an observed mode.
// The gating table only lists PRESS_* and STATE; SEQUENCE_OK is not
// listed, so it must never receive a SetOperationModeAllowed call.
func TestEventSourceNonGatedParameterUnchanged(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "iface", Address: "EVGAT04", Model: "HmIP-FCI1"})
	ch := d.AddChannel("EVGAT04:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	seqOK := attachEventSource(ch, "iface", hmenum.ParameterSequenceOK)
	if seqOK == nil {
		t.Fatal("event.NewSource returned nil for SEQUENCE_OK")
	}
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "BINARY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	// SEQUENCE_OK is not in the gating table → usage must remain Event.
	if got := seqOK.Usage(); got != hmenum.DataPointUsageEvent {
		t.Errorf("SEQUENCE_OK Usage = %q, want Event (not in gating table)", got)
	}
}
