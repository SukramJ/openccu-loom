// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWSAPISubscriptionAckProseMatchesTheAlwaysAckBehaviour pins the
// published contract for the subscribe / unsubscribe ACK to what the
// daemon does. [client.sendAck] answers every frame, an empty or missing
// topics list included (TestSubscribeWithEmptyTopicsStillGetsAcked), so a
// client waiting on the ACK before considering itself connected never
// hangs. A spec sentence promising a suppressed ACK for empty topics tells
// a preference-only `{op:"subscribe", topics:[], classify:true}` client to
// expect silence and then hands it an unexpected frame.
func TestWSAPISubscriptionAckProseMatchesTheAlwaysAckBehaviour(t *testing.T) {
	t.Parallel()
	_, thisFile, _, _ := runtime.Caller(0)
	specPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "assets", "wsapi.json")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}
	var spec struct {
		SubscriptionAck struct {
			Description string `json:"description"`
		} `json:"subscription_ack"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode wsapi.json: %v", err)
	}
	desc := spec.SubscriptionAck.Description
	if desc == "" {
		t.Fatal("wsapi.json has no subscription_ack.description")
	}
	if strings.Contains(desc, "suppresses the ACK") {
		t.Fatalf("subscription_ack.description still promises a suppressed ACK for empty topics; the daemon always answers:\n%s", desc)
	}
	if !strings.Contains(desc, "empty") {
		t.Fatalf("subscription_ack.description must state what happens for an empty topics array:\n%s", desc)
	}
}
