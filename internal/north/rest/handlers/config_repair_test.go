// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeConfigRepair records its call and answers from fields.
type fakeConfigRepair struct {
	outcomes []interfaces.ConfigRepairOutcome
	err      error

	called   bool
	address  string
	channels []string
	dryRun   bool
}

func (f *fakeConfigRepair) RepairDeviceConfig(
	_ context.Context, deviceAddress string, channels []string, dryRun bool,
) ([]interfaces.ConfigRepairOutcome, error) {
	f.called = true
	f.address, f.channels, f.dryRun = deviceAddress, channels, dryRun
	return f.outcomes, f.err
}

func postRepair(svc DeviceConfigRepairService, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	}
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	RepairDeviceConfig(svc).ServeHTTP(w, req)
	return w
}

// TestRepairDeviceConfig_DryRunDefaultsTrue pins the safe default: neither
// an absent body nor a body without dry_run may write.
func TestRepairDeviceConfig_DryRunDefaultsTrue(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", `{}`, `{"channels":["0001ABCD:1"]}`} {
		svc := &fakeConfigRepair{}
		if w := postRepair(svc, body); w.Code != http.StatusOK {
			t.Fatalf("body %q: status=%d %s", body, w.Code, w.Body.String())
		}
		if !svc.called || !svc.dryRun || svc.address != "0001ABCD" {
			t.Errorf("body %q: call=%+v, want a dry run for 0001ABCD", body, svc)
		}
	}
	svc := &fakeConfigRepair{}
	postRepair(svc, `{"dry_run":false,"channels":["0001ABCD:1"]}`)
	if svc.dryRun || len(svc.channels) != 1 || svc.channels[0] != "0001ABCD:1" {
		t.Errorf("explicit write: call=%+v, want dry_run=false with the channel", svc)
	}
}

// TestRepairDeviceConfig_OutcomeShape pins the wire projection: lists are
// arrays even when empty, and the read-back report is carried through.
func TestRepairDeviceConfig_OutcomeShape(t *testing.T) {
	t.Parallel()
	svc := &fakeConfigRepair{outcomes: []interfaces.ConfigRepairOutcome{
		{Channel: "0001ABCD:1", Status: interfaces.RepairClean},
		{
			Channel: "0001ABCD:2", Status: interfaces.RepairRepaired,
			Corrections: []interfaces.ConfigRepairCorrection{{Parameter: "TX", Stored: 99, Corrected: 10, Reason: "clamped"}},
			Result:      &interfaces.ParamsetWriteReport{Written: []string{"TX"}},
		},
	}}
	w := postRepair(svc, `{"dry_run":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Items) != 2 {
		t.Fatalf("items=%d, want 2", len(raw.Items))
	}
	clean := raw.Items[0]
	for _, key := range []string{"corrections", "foreign"} {
		if v, ok := clean[key].([]any); !ok || len(v) != 0 {
			t.Errorf("clean.%s=%#v, want []", key, clean[key])
		}
	}
	if _, ok := clean["result"]; ok {
		t.Errorf("clean outcome carries a result: %v", clean)
	}
	repaired := raw.Items[1]
	corr, _ := repaired["corrections"].([]any)
	if len(corr) != 1 || fmt.Sprint(corr[0].(map[string]any)["corrected"]) != "10" {
		t.Errorf("repaired.corrections=%v", repaired["corrections"])
	}
	if _, ok := repaired["result"].(map[string]any); !ok {
		t.Errorf("repaired.result missing: %v", repaired)
	}
}

// TestRepairDeviceConfig_ErrorMapping pins 404 for an unknown device, 502
// for another failure, 503 unwired and 400 for a malformed body.
func TestRepairDeviceConfig_ErrorMapping(t *testing.T) {
	t.Parallel()
	notFound := &fakeConfigRepair{err: fmt.Errorf("x: %w", hmerr.ErrDescriptionNotFound)}
	if w := postRepair(notFound, ""); w.Code != http.StatusNotFound {
		t.Errorf("not found: status=%d", w.Code)
	}
	if w := postRepair(&fakeConfigRepair{err: errors.New("boom")}, ""); w.Code != http.StatusBadGateway {
		t.Errorf("upstream: status=%d", w.Code)
	}
	if w := postRepair(nil, ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("unwired: status=%d", w.Code)
	}
	bad := &fakeConfigRepair{}
	if w := postRepair(bad, `{"dry_run":`); w.Code != http.StatusBadRequest || bad.called {
		t.Errorf("malformed: status=%d called=%v", w.Code, bad.called)
	}
}
