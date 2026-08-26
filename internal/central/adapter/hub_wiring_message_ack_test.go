// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// hub_wiring_message_ack_test.go covers hubMessageAck in hub_wiring.go: the
// adapter that wires the ReGa runner's single- and bulk-message acknowledge
// calls to the hub.MessageAcknowledger / hub.BulkMessageAcknowledger
// contracts consumed by the hub model.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
)

// newRegaRunnerScripted builds a rega.Runner backed by a JSON-RPC fake that
// inspects the dispatched script body (via respond) and returns the
// corresponding CCU stdout. Lets a single fake server answer differently for
// each of the three message-acknowledge scripts, distinguished by their
// header comments (which survive substitution unchanged).
func newRegaRunnerScripted(t *testing.T, respond func(script string) string) *rega.Runner {
	t.Helper()
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript": func(params map[string]any) any {
			script, _ := params["script"].(string)
			return respond(script)
		},
	})
	jc := newJSONRPCClient(t, srv.URL)
	r, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return r
}

// scriptedAckResponses dispatches by script identity for the three
// message-acknowledge scripts, defaulting to the single-message reply.
func scriptedAckResponses(serviceAllJSON, alarmAllJSON, singleJSON string) func(string) string {
	return func(script string) string {
		switch {
		case strings.Contains(script, "acknowledge_all_service_messages"):
			return serviceAllJSON
		case strings.Contains(script, "acknowledge_all_alarm_messages"):
			return alarmAllJSON
		default:
			return singleJSON
		}
	}
}

// ============================================================
// hubMessageAck.AcknowledgeMessage — single-message acknowledge
// ============================================================

func TestHubMessageAckAcknowledgeMessage_Success(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses("", "", `{"success":true,"error":""}`))
	ack := hubMessageAck{runner: r}

	if err := ack.AcknowledgeMessage(context.Background(), "A1"); err != nil {
		t.Fatalf("AcknowledgeMessage: %v", err)
	}
}

// TestHubMessageAckAcknowledgeMessage_CCUSideError verifies that a CCU-side
// structured failure (success=false) surfaces as a non-nil error even though
// the runner's confirmation boolean is dropped by the adapter.
func TestHubMessageAckAcknowledgeMessage_CCUSideError(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses("", "", `{"success":false,"error":"Message not found"}`))
	ack := hubMessageAck{runner: r}

	err := ack.AcknowledgeMessage(context.Background(), "GHOST")
	if err == nil {
		t.Fatal("expected error for CCU-side rejection")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should carry the CCU reason, got: %v", err)
	}
}

// TestHubMessageAckAcknowledgeMessage_TransportError verifies that a
// malformed CCU reply (parse failure) propagates as an error.
func TestHubMessageAckAcknowledgeMessage_TransportError(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses("", "", "not json"))
	ack := hubMessageAck{runner: r}

	if err := ack.AcknowledgeMessage(context.Background(), "A1"); err == nil {
		t.Fatal("expected parse error to propagate")
	}
}

// ============================================================
// hubMessageAck.AcknowledgeAllServiceMessages / AcknowledgeAllAlarmMessages
// ============================================================

func TestHubMessageAckAcknowledgeAllServiceMessages_ReturnsCount(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses(`{"acknowledged":2}`, `{"acknowledged":9}`, ""))
	ack := hubMessageAck{runner: r}

	n, err := ack.AcknowledgeAllServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllServiceMessages: %v", err)
	}
	if n != 2 {
		t.Fatalf("count=%d, want 2 (must not read the alarm script's count)", n)
	}
}

func TestHubMessageAckAcknowledgeAllAlarmMessages_ReturnsCount(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses(`{"acknowledged":2}`, `{"acknowledged":9}`, ""))
	ack := hubMessageAck{runner: r}

	n, err := ack.AcknowledgeAllAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("AcknowledgeAllAlarmMessages: %v", err)
	}
	if n != 9 {
		t.Fatalf("count=%d, want 9 (must not read the service script's count)", n)
	}
}

// TestHubMessageAckAcknowledgeAllServiceMessages_TransportError verifies a
// malformed CCU reply surfaces as a wrapped error with a zero count, so a
// bulk-acknowledge REST/WS call answers 502 instead of silently reporting
// zero as if nothing needed acknowledging.
func TestHubMessageAckAcknowledgeAllServiceMessages_TransportError(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses("not json", `{"acknowledged":1}`, ""))
	ack := hubMessageAck{runner: r}

	n, err := ack.AcknowledgeAllServiceMessages(context.Background())
	if err == nil {
		t.Fatal("expected parse error to propagate")
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0 on error", n)
	}
}

// TestHubMessageAckAcknowledgeAllAlarmMessages_TransportError mirrors the
// service-side transport-error case for the alarm script.
func TestHubMessageAckAcknowledgeAllAlarmMessages_TransportError(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerScripted(t, scriptedAckResponses(`{"acknowledged":1}`, "not json", ""))
	ack := hubMessageAck{runner: r}

	n, err := ack.AcknowledgeAllAlarmMessages(context.Background())
	if err == nil {
		t.Fatal("expected parse error to propagate")
	}
	if n != 0 {
		t.Fatalf("count=%d, want 0 on error", n)
	}
}
