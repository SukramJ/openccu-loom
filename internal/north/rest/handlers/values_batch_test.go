// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestValuesBatch_NilIdx_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"queries":[{"address":"DEV0001","channel":1,"parameter":"STATE"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestValuesBatch_EmptyQueries_Returns422(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	body := `{"queries":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestValuesBatch_OversizedQueries_Returns422(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	queries := make([]ValuesBatchQuery, ValuesBatchMaxQueries+1)
	for i := range queries {
		queries[i] = ValuesBatchQuery{Address: fmt.Sprintf("DEV%04d", i), Channel: 1, Parameter: "STATE"}
	}
	body, _ := json.Marshal(ValuesBatchRequest{Queries: queries})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestValuesBatch_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(`{not valid json`))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestValuesBatch_DeviceNotFound_ReturnsErrorInResult(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	body := `{"queries":[{"address":"MISSING001","channel":1,"parameter":"STATE"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp ValuesBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results len=%d, want 1", len(resp.Results))
	}
	if resp.Results[0].Error == "" {
		t.Fatal("expected error in result for missing device")
	}
	if resp.Results[0].Summary != nil {
		t.Fatal("summary must be nil for missing device")
	}
}

func TestValuesBatch_ChannelNotFound_ReturnsErrorInResult(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0001", "HmIP-BSM")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0001": d}}
	// channel 99 does not exist on the device
	body := `{"queries":[{"address":"DEV0001","channel":99,"parameter":"STATE"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp ValuesBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results len=%d, want 1", len(resp.Results))
	}
	if resp.Results[0].Error == "" {
		t.Fatal("expected error in result for missing channel")
	}
}

func TestValuesBatch_ParameterNotFound_ReturnsErrorInResult(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0002", "HmIP-BSM")
	d.AddChannel("DEV0002:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0002": d}}
	// channel 1 exists but parameter MISSING_PARAM does not
	body := `{"queries":[{"address":"DEV0002","channel":1,"parameter":"MISSING_PARAM"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/values/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	ValuesBatch(idx, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp ValuesBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results len=%d, want 1", len(resp.Results))
	}
	if resp.Results[0].Error == "" {
		t.Fatal("expected error in result for missing parameter")
	}
}
