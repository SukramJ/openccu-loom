// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"testing"

	mattercore "thingfabric.dev/go-fabric/cluster/core"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMatterBootReasonStatesWhatTheDaemonKnows drives the Matter node's
// GeneralDiagnostics BootReason through the built daemon across the stops
// it can tell apart, reading GET /api/v1/node/status after each start:
//
//   - a fresh installation, and a plain SIGTERM (a supervisor's stop, power
//     loss and a crash look the same to the daemon): Unspecified;
//   - a start after POST /api/v1/system/restart: SoftwareReset;
//   - the start after that, ended by a plain SIGTERM again: Unspecified — the
//     marker describes one stop;
//   - a start of another version on the same data directory:
//     SoftwareUpdateCompleted.
//
// It crosses every hand-over the unit tests in cmd/openccu-loom construct
// themselves: the restart claim read at the end of the real shutdown, the
// marker retired at boot, and the version recorded after the node started.
func TestE2EMatterBootReasonStatesWhatTheDaemonKnows(t *testing.T) {
	h := harness.Start(t, harness.Options{
		EnableMatter: true,
		AuthMode:     harness.AuthSession,
		Env:          []string{"OPENCCU_LOOM_SUPERVISOR=1"},
	})
	steps := []struct {
		name    string
		restart func(t *testing.T)
		want    uint8
	}{
		{name: "fresh installation", restart: func(*testing.T) {}, want: mattercore.BootReasonUnspecified},
		{name: "plain SIGTERM", restart: h.Restart, want: mattercore.BootReasonUnspecified},
		{name: "operator restart", restart: h.RestartOnRequest, want: mattercore.BootReasonSoftwareReset},
		{name: "plain SIGTERM after the operator restart", restart: h.Restart, want: mattercore.BootReasonUnspecified},
		{name: "update", restart: func(t *testing.T) {
			t.Helper()
			h.RestartWithBinary(t, harness.BuildDaemonWithVersion(t, "0.0.0-e2e-update"))
		}, want: mattercore.BootReasonSoftwareUpdate},
	}
	for _, step := range steps {
		step.restart(t)
		st := h.NodeStatus(t)
		if st.BootReason == nil {
			t.Fatalf("%s: GET /api/v1/node/status carries no boot_reason", step.name)
		}
		if *st.BootReason != step.want {
			t.Fatalf("%s: boot_reason = %d, want %d", step.name, *st.BootReason, step.want)
		}
	}
}
