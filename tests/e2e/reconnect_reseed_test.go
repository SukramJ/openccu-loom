// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EReconnectReseedsValuesChangedDuringDowntime pins that a CCU
// reconnect re-measures device values instead of trusting the model's state
// from before the outage.
//
// While the CCU is down no event can reach the daemon, so a value that
// changed in that window is only learned by reading it once the CCU is back.
// The scenario:
//
//  1. Boot against godevccu, read a data point's value over REST.
//  2. Stop the simulated CCU and wait until the daemon reports the central
//     unhealthy, so a recovery is actually in flight.
//  3. Change the value in the simulator's ReGa value store. That store never
//     fires events, and the CCU is stopped anyway, so the only way the new
//     value can reach the model is a read after the reconnect.
//  4. Restart the simulator on the same ports and wait for healthy=true.
//  5. Assert that the data point now carries the value from step 3 — well
//     inside the periodic reconciliation sweep's 5-minute cadence, so the
//     sweep cannot be what repairs it.
func TestE2EReconnectReseedsValuesChangedDuringDowntime(t *testing.T) {
	t.Parallel()

	const checkInterval = 4 * time.Second
	h := harness.Start(t, harness.Options{
		EnableMQTT:              true,
		CheckConnectionInterval: checkInterval,
	})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	var bsmAddress string
	for deadline := time.Now().Add(45 * time.Second); bsmAddress == ""; {
		bsmAddress = findBSMDevice(t, h)
		if bsmAddress != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no HmIP-BSM device appeared in the daemon's device list")
		}
		time.Sleep(250 * time.Millisecond)
	}
	const channelNo = 4
	const parameter = "STATE"
	channelAddress := fmt.Sprintf("%s:%d", bsmAddress, channelNo)
	dpPath := fmt.Sprintf("/api/v1/devices/%s/channels/%d/data-points/%s", bsmAddress, channelNo, parameter)

	var before any
	for deadline := time.Now().Add(30 * time.Second); ; {
		v, err := fetchDataPointValue(h, dpPath)
		if err == nil {
			before = v
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: %v", dpPath, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	beforeBool, _ := before.(bool)
	want := !beforeBool
	t.Logf("%s before the outage: %v; will change it to %v during the downtime", dpPath, before, want)

	statusTopic := "openccu-loom/ccu-e2e/system/status"
	unhealthy := make(chan struct{}, 1)
	healthy := make(chan struct{}, 1)
	if err := h.MQTT().Subscribe(statusTopic, func(_ string, payload []byte, _ bool) {
		var p struct {
			Healthy bool `json:"healthy"`
		}
		if json.Unmarshal(payload, &p) != nil {
			return
		}
		target := unhealthy
		if p.Healthy {
			target = healthy
		}
		select {
		case target <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatalf("subscribe %s: %v", statusTopic, err)
	}

	if err := h.CCU().Stop(); err != nil {
		t.Fatalf("stop mock CCU: %v", err)
	}
	select {
	case <-unhealthy:
	case <-time.After(10*checkInterval + 20*time.Second):
		t.Fatal("daemon never reported the central unhealthy after the CCU stopped — no recovery ran, so the reseed cannot be measured")
	}
	// Drain any healthy=true published before the outage was detected.
	select {
	case <-healthy:
	default:
	}

	// The ReGa value store is what the bulk value read serves; writing it
	// fires no event.
	h.CCU().V().State().SetDeviceValue(channelAddress, parameter, want)

	if err := h.CCU().V().Start(); err != nil {
		t.Fatalf("restart mock CCU: %v", err)
	}
	select {
	case <-healthy:
	case <-time.After(120 * time.Second):
		t.Fatal("daemon never reported the central healthy again after the CCU restarted")
	}

	var got any
	for deadline := time.Now().Add(20 * time.Second); ; {
		v, err := fetchDataPointValue(h, dpPath)
		if err == nil {
			got = v
			if b, ok := v.(bool); ok && b == want {
				t.Logf("%s after recovery: %v", dpPath, v)
				return
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s still reports %v after the reconnect completed, want %v — the value changed while "+
		"the CCU was down and the recovery did not re-read it, so the model keeps the state from "+
		"before the outage until the next event or reconciliation sweep", dpPath, got, want)
}

// fetchDataPointValue reads the `value` field of one data point summary.
func fetchDataPointValue(h *harness.Harness, path string) (any, error) {
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
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
	var obj struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("decode %s: %w", body, err)
	}
	return obj.Value, nil
}
