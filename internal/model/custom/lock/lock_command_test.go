// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newMasterButtonLock builds the shipping button-lock wiring: a
// GLOBAL_BUTTON_LOCK bool in the MASTER paramset on channel 0.
func newMasterButtonLock(t *testing.T) (*Lock, *paramsetStubWriter) {
	t.Helper()
	w := &paramsetStubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BWTH0002"})
	ch := d.AddChannel("BWTH0002:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch.PutMaster(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "BWTH0002:0",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterGlobalButtonLock),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	return New(Config{Channel: ch, Writer: w, Kind: KindButton}), w
}

// TestButtonLockHACommandReachesMasterWrite crosses the seam the HA
// discovery payload names: take the command topic and the payload the
// payload builder advertises, feed exactly those into the invoke path
// the MQTT bridge uses, and assert the write lands on the MASTER slot.
//
// Pinning the discovery payload and the write path separately is what
// let the two drift: the topic named a VALUES parameter the channel does
// not carry, so every lock/unlock from Home Assistant faulted while both
// halves stayed green on their own.
func TestButtonLockHACommandReachesMasterWrite(t *testing.T) {
	t.Parallel()
	l, w := newMasterButtonLock(t)

	_, body := l.HADiscoveryPayload(discoveryCtx{})
	topic, _ := body["command_topic"].(string)
	method, ok := strings.CutPrefix(topic, "test/svc/")
	if !ok {
		t.Fatalf("command_topic %q is not a service-method topic", topic)
	}
	method = strings.TrimSuffix(method, "/set")

	// The MQTT bridge wraps a bare payload under the method's scalar-arg
	// key before invoking, so resolve it the same way the bridge does.
	argKey := payload.GlobalScalarArgKey(method)
	lockPayload, _ := body["payload_lock"].(string)

	err := l.Invoke(context.Background(), method,
		map[string]any{argKey: lockPayload}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("invoke %s(%s=%q): %v", method, argKey, lockPayload, err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("%d PutParamset calls, want 1 (MASTER routing)", len(w.puts))
	}
	put := w.puts[0]
	if put.key != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset = %v, want MASTER", put.key)
	}
	if v, has := put.values[string(hmenum.ParameterGlobalButtonLock)]; !has || v != true {
		t.Errorf("values = %v, want GLOBAL_BUTTON_LOCK=true", put.values)
	}
	if st, known := l.LockState(); !known || st != StateLocked {
		t.Errorf("state = %v known=%v, want LOCKED", st, known)
	}
}

// TestButtonLockHAUnlockPayloadUnlocks pins the other half of the
// multiplexed topic.
func TestButtonLockHAUnlockPayloadUnlocks(t *testing.T) {
	t.Parallel()
	l, w := newMasterButtonLock(t)

	_, body := l.HADiscoveryPayload(discoveryCtx{})
	unlockPayload, _ := body["payload_unlock"].(string)

	err := l.Invoke(context.Background(), serviceLockCommand,
		map[string]any{argLockCommand: unlockPayload}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("invoke %s: %v", serviceLockCommand, err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("%d PutParamset calls, want 1", len(w.puts))
	}
	if v, has := w.puts[0].values[string(hmenum.ParameterGlobalButtonLock)]; !has || v != false {
		t.Errorf("values = %v, want GLOBAL_BUTTON_LOCK=false", w.puts[0].values)
	}
}

// TestLockCommandRejectsUnknownToken pins that an unroutable payload is
// an error rather than a silent no-op.
func TestLockCommandRejectsUnknownToken(t *testing.T) {
	t.Parallel()
	l, w := newMasterButtonLock(t)

	err := l.Invoke(context.Background(), serviceLockCommand,
		map[string]any{argLockCommand: "TOGGLE"}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("unknown command token: want error, got nil")
	}
	if len(w.puts) != 0 {
		t.Errorf("unknown token still wrote: %v", w.puts)
	}
}

// TestLockCommandOpenNeedsCapability pins that OPEN routes to the "open"
// operation only where the device registered it — a button lock has no
// open action and must say so instead of silently locking.
func TestLockCommandOpenNeedsCapability(t *testing.T) {
	t.Parallel()
	l, w := newMasterButtonLock(t)

	err := l.Invoke(context.Background(), serviceLockCommand,
		map[string]any{argLockCommand: commandTokenOpen}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("OPEN on a button lock: want error, got nil")
	}
	if len(w.puts) != 0 {
		t.Errorf("OPEN still wrote: %v", w.puts)
	}
}
