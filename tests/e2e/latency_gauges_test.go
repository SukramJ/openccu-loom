// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2ELatencyGaugesReachTheDiagnosticsSurface pins the delivery half of the
// three client-side latency measurements: registering a gauge on the daemon's
// health tracker is only half a feature, and the other half is invisible from
// the registration site.
//
// This is the failure the latency work itself was fixing one level down. The
// `ping_pong.rtt` metric key existed, the aggregator read it back, and no
// production code ever wrote it — so `avg_latency_ms` reported a constant zero
// for every deployment while looking, from every angle inside the package,
// exactly like a working metric. A gauge registered against a tracker the
// diagnostics adapter does not consult would fail the same way: silently, and
// only for the operator.
//
// The daemon under test has no MQTT broker and no Matter controller, so the
// values are zero here. That is the point — the *keys* are what proves the
// chain from RegisterGauge through HealthAdapter.Gauges to the response body.
// A value assertion would require a broker and a commissioner and would test
// the measurement, which the unit guards already do.
func TestE2ELatencyGaugesReachTheDiagnosticsSurface(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	gauges, err := healthGauges(h)
	if err != nil {
		t.Fatalf("GET /api/v1/diagnostics: %v", err)
	}

	// Control: an already-shipped gauge registered through the same call on the
	// same tracker. If this is missing, the chain is broken for a reason that
	// predates this test and the assertions below would be blaming the wrong
	// change.
	if _, ok := gauges["ws.subscribers"]; !ok {
		t.Fatalf("the pre-existing ws.subscribers gauge is absent from the diagnostics dump, so this test is "+
			"not measuring what it claims — the tracker the daemon registers against is not the one "+
			"HealthAdapter consults. Gauges present: %v", keysOf(gauges))
	}

	for _, name := range []string{
		"ws.heartbeat_rtt_ms",
		"ws.heartbeat_rtt_samples",
		"mqtt.publish_ack_ms",
		"mqtt.publish_ack_samples",
	} {
		if _, ok := gauges[name]; !ok {
			t.Errorf("gauge %q is absent from the diagnostics dump — it is registered but nothing reads it, "+
				"so the measurement is invisible to the operator it was added for. Gauges present: %v",
				name, keysOf(gauges))
		}
	}

	// Matter is opt-in and off in this harness, so its gauges must NOT be here.
	// That is the negative control for the whole set: without it, a diagnostics
	// surface that echoed back every requested name would pass the loop above.
	if _, ok := gauges["matter.controller_rtt_ms"]; ok {
		t.Error("matter.controller_rtt_ms is present with the Matter bridge disabled — the gauge is registered " +
			"outside the bridge's own wiring, so it reports on a subsystem that is not running")
	}
}

// healthGauges returns the pull-gauge map from the diagnostics dump.
func healthGauges(h *harness.Harness) (map[string]float64, error) {
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var env struct {
		Health struct {
			Gauges map[string]float64 `json:"gauges"`
		} `json:"health"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	return env.Health.Gauges, nil
}

// keysOf renders a gauge map's keys for a failure message.
func keysOf(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
