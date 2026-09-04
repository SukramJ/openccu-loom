// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2CstRFLockWriter records the SetValue calls an RF lock performs and
// replays each written STATE back onto the data point, the way the CCU's
// own event would.
type w2CstRFLockWriter struct {
	param hmenum.Parameter
	value any
	seen  bool
	dp    *generic.Switch
}

func (w *w2CstRFLockWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	w.param, w.value, w.seen = p, v, true
	if b, ok := v.(bool); ok && w.dp != nil && p == hmenum.ParameterState {
		w.dp.OnEvent(b)
	}
	return nil
}

// w2CstNewRFLock builds a KindRF lock over a channel carrying the bool STATE
// parameter, and hands back the writer that both records and echoes writes.
func w2CstNewRFLock(t *testing.T) (*lock.Lock, *w2CstRFLockWriter) {
	t.Helper()

	const addr = "LEQ0000001:1"
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "LEQ0000001"})
	ch := d.AddChannel(addr, 1, "KEYMATIC", hmenum.ParamsetKeyValues)
	w := &w2CstRFLockWriter{}
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Writer: w,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	w.dp = dp

	l := lock.New(lock.Config{
		Channel:      ch,
		Writer:       w,
		Kind:         lock.KindRF,
		Capabilities: custom.LockCapabilities{},
	})
	return l, w
}

// TestW2CstRFLockStatePolarityIsOneRule crosses the three live readers of the
// RF lock's STATE polarity — the daemon's own write, the state accessor, and
// the payloads Home Assistant is handed — against each other.
//
// The polarity is one fact about the device, and each site used to spell it
// out separately: bool literals on the write path, a bare `if v` on the read
// path, and the strings "false"/"true" in the discovery payload. Three
// spellings agree only by maintenance, and each disagreement is a real-world
// failure: flip the payload alone and Home Assistant's lock button unlocks a
// door the daemon's own command locks; flip the read alone and the same door
// reports locked while standing open.
//
// The check asserts no polarity literal of its own — it is satisfied by any
// consistent choice — so it stays alive whichever way the rule is spelled.
// That the chosen direction is the device's is pinned separately by the
// per-Kind unit tests in internal/model/custom/lock.
func TestW2CstRFLockStatePolarityIsOneRule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	l, w := w2CstNewRFLock(t)
	_, body := l.HADiscoveryPayload(lockTargetLevelDiscoveryCtx{})
	wantTopic := lockTargetLevelDiscoveryCtx{}.WireParameterCommandTopic(string(hmenum.ParameterState))
	if got := body["command_topic"]; got != wantTopic {
		t.Fatalf("command_topic = %v, want %v — the advertised payloads only reach STATE through this topic", got, wantTopic)
	}

	cases := []struct {
		payloadKey string
		invoke     func(context.Context) error
		wantState  lock.State
	}{
		{"payload_lock", func(c context.Context) error { return l.Lock(c, hmenum.CommandPriorityHigh) }, lock.StateLocked},
		{"payload_unlock", func(c context.Context) error { return l.Unlock(c, hmenum.CommandPriorityHigh) }, lock.StateUnlocked},
	}

	for _, tc := range cases {
		advertised, ok := body[tc.payloadKey].(string)
		if !ok {
			t.Errorf("%s missing from the discovery payload", tc.payloadKey)
			continue
		}

		w.seen = false
		if err := tc.invoke(ctx); err != nil {
			t.Fatalf("%s: service path returned %v", tc.payloadKey, err)
		}
		if !w.seen {
			t.Fatalf("%s: the service path wrote nothing", tc.payloadKey)
		}
		if w.param != hmenum.ParameterState {
			t.Fatalf("%s: service path wrote %s, want STATE", tc.payloadKey, w.param)
		}
		written, ok := w.value.(bool)
		if !ok {
			t.Fatalf("%s: service path wrote %T(%v), want a bool", tc.payloadKey, w.value, w.value)
		}

		// Writer ↔ discovery payload. HA publishes the advertised payload
		// onto the STATE command topic, so the two have to be the same
		// wire value.
		if advertised != strconv.FormatBool(written) {
			t.Errorf("%s advertises %q but the service path writes %v on STATE — a Home Assistant command and the daemon's own call would reach the CCU as opposite values",
				tc.payloadKey, advertised, written)
		}

		// Writer ↔ reader. The value just written came back as the CCU's
		// event, so the state accessor has to name the operation that
		// produced it.
		if got, ok := l.LockState(); !ok || got != tc.wantState {
			t.Errorf("%s: STATE=%v was written and echoed back, but LockState() reports %q (present=%v), want %q — the read path and the write path disagree on the polarity",
				tc.payloadKey, written, got, ok, tc.wantState)
		}
	}
}
