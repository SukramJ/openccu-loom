// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// comTestScriptRunner answers the two com-test scripts with fixed payloads.
type comTestScriptRunner struct{}

func (comTestScriptRunner) Run(context.Context, hmenum.RegaScript, map[string]string) (string, error) {
	return "", nil
}

func (comTestScriptRunner) RunJSON(_ context.Context, script hmenum.RegaScript, _ map[string]string, v any) error {
	switch script {
	case hmenum.RegaScriptStartComTest:
		return json.Unmarshal([]byte(`{"success":true,"started":"2026-01-15 14:29:00"}`), v)
	case hmenum.RegaScriptPollComTest:
		return json.Unmarshal([]byte(`{"passed":true,"completed":"2026-01-15 14:30:00"}`), v)
	default:
		return nil
	}
}

// TestCcuBackendComTestCompletedAtUsesCCUZone pins the com-test completion
// stamp to the CCU's zone rather than the daemon's.
//
// The value the CCU returns is offset-free wall clock: the firmware renders
// LastTestCompletedTime() through TimeStamp.fn::TimeStampToString3, which
// slices fixed character offsets and applies no conversion. Reading it in the
// daemon's zone therefore produces the wrong instant whenever the daemon and
// the CCU sit in different zones — and CompletedAt is a published DTO field.
//
// The assertion is on the zone offset and the UTC instant, so it holds
// whatever zone the machine running the test is in.
func TestCcuBackendComTestCompletedAtUsesCCUZone(t *testing.T) {
	t.Parallel()

	b := NewCcuBackend(nil, nil, nil)
	b.SetScriptRunner(comTestScriptRunner{})
	b.SetCCUTimezone("Europe/Berlin")

	res, err := b.TestDevice(context.Background(), "0001ABCD", 5, 0.01)
	if err != nil {
		t.Fatalf("TestDevice: %v", err)
	}
	if !res.Passed {
		t.Fatalf("TestDevice reported the test did not pass")
	}
	if _, off := res.CompletedAt.Zone(); off != 3600 {
		t.Errorf("CompletedAt zone offset = %ds, want 3600 (Europe/Berlin in January)", off)
	}
	if got := res.CompletedAt.UTC().Format(time.RFC3339); got != "2026-01-15T13:30:00Z" {
		t.Errorf("CompletedAt = %s, want 2026-01-15T13:30:00Z", got)
	}
}
