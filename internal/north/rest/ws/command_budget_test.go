// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ctxDeadline is what a capturing command handler reports about the
// context the dispatcher handed it.
type ctxDeadline struct {
	remaining time.Duration
	bounded   bool
}

// TestHandleCommand_LongRunningCommandsOutliveTheDefaultBudget pins that
// the per-command budget the socket applies is not one constant for every
// command: `device.test` waits on the CCU's own 30 s communication-test
// window, and `devices.export_definition` issues one
// getParamsetDescription per (channel, paramset), which the REST twin runs
// without any router deadline. Under the default 10 s cap both are cut
// mid-way and answer `internal_error` — after the CCU has already paid the
// radio cost.
func TestHandleCommand_LongRunningCommandsOutliveTheDefaultBudget(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	seen := make(chan ctxDeadline, 4)
	capture := func(ctx context.Context, _ json.RawMessage) (any, error) {
		d, ok := ctx.Deadline()
		seen <- ctxDeadline{remaining: time.Until(d), bounded: ok}
		return map[string]any{}, nil
	}
	hub.Router().Register("device.test", capture)
	hub.Router().Register("devices.export_definition", capture)

	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "op", Scheme: auth.SchemeSession, Role: auth.RoleOperator}, Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)
	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	for _, cmd := range []string{"device.test", "devices.export_definition"} {
		if res := c.call("b-"+cmd, cmd, map[string]any{"address": "ABC0001"}); res.Error != nil {
			t.Fatalf("%s: unexpected error %+v", cmd, res.Error)
		}
		got := <-seen
		if !got.bounded {
			t.Fatalf("%s: dispatched with no deadline at all; every command must stay bounded", cmd)
		}
		if got.remaining <= commandTimeout {
			t.Fatalf("%s: dispatched with a %s budget, want more than the default %s", cmd, got.remaining.Round(time.Second), commandTimeout)
		}
	}
}

// blockingTestDevices answers the communication test only when the
// caller's context ends, the way the CCU backend behaves for a device
// that never ACKs.
type blockingTestDevices struct{ *stubDevices }

func (b *blockingTestDevices) TestDeviceCommunication(ctx context.Context, _ string) (hmapi.CommunicationTestResult, error) {
	<-ctx.Done()
	return hmapi.CommunicationTestResult{}, ctx.Err()
}

// TestDeviceTest_PollDeadlineIsTheTimedOutResult pins the documented
// `timed_out: true` outcome of `device.test`: a poll window that elapses
// while the command is still alive is the device not answering, which
// wsapi.json declares as a result, not an `internal_error` frame.
func TestDeviceTest_PollDeadlineIsTheTimedOutResult(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Devices: &blockingTestDevices{stubDevices: &stubDevices{}}})

	ctx, cancel := context.WithTimeout(opCtx(), 200*time.Millisecond)
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"address": "ABC0001"})
	res := r.Dispatch(ctx, "device.test", raw)
	if res.Error != nil {
		t.Fatalf("device.test on an unanswered poll: got error %+v, want the timed_out result", res.Error)
	}
	out, ok := res.Data.(hmapi.CommunicationTestResult)
	if !ok {
		t.Fatalf("result type %T, want hmapi.CommunicationTestResult", res.Data)
	}
	if !out.TimedOut || out.Passed {
		t.Fatalf("result=%+v, want TimedOut=true Passed=false", out)
	}
}
