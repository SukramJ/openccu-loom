// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- stubs ---

// stubCustomDP is a minimal AttachableDataPoint + CategorisedDataPoint.
type stubCustomDP struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
	state    any
}

func (s *stubCustomDP) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubCustomDP) Category() hmenum.DataPointCategory { return s.category }
func (s *stubCustomDP) DataPointState() any                { return s.state }

// stubCustomDPWriter is an inline stub for CustomDPWriter.
type stubCustomDPWriter struct {
	invokeErr error
	calls     []struct {
		device, name, operation string
	}
}

func (s *stubCustomDPWriter) InvokeCustomDP(
	_ context.Context,
	deviceAddress, name, operation string,
	_ map[string]any,
	_ hmenum.CommandPriority,
	_ string,
) error {
	s.calls = append(s.calls, struct {
		device, name, operation string
	}{deviceAddress, name, operation})
	return s.invokeErr
}

// addCustomDP attaches a stubCustomDP to a device channel at channelNo.
func addCustomDP(d *device.Device, addr, param string, no int, cat hmenum.DataPointCategory) {
	ch := d.AddChannel(addr+":"+string(rune('0'+no)), no, "SWITCH", hmenum.ParamsetKeyValues) //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
	dp := &stubCustomDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: addr + ":" + string(rune('0'+no)), //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		category: cat,
		state:    map[string]any{"on": false},
	}
	ch.SetCustomDataPoint(dp)
}

// --- tests: ListCustomDataPoints ---

func TestListCustomDataPoints_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0001", "HmIP-BSM")
	addCustomDP(d, "DEV0001", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0001": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0001"}))
	w := httptest.NewRecorder()
	ListCustomDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []CustomDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 custom DP, got %d", len(out))
	}
	if out[0].Name != "STATE" {
		t.Fatalf("expected name=STATE, got %q", out[0].Name)
	}
	if out[0].Category != string(hmenum.DataPointCategorySwitch) {
		t.Fatalf("expected category=switch, got %q", out[0].Category)
	}
	if len(out[0].SupportedOperations) == 0 {
		t.Fatal("expected non-empty supported_operations for switch")
	}
}

func TestListCustomDataPoints_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING"}))
	w := httptest.NewRecorder()
	ListCustomDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListCustomDataPoints_NoCustomDPs_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0002", "HmIP-BSM")
	d.AddChannel("DEV0002:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0002": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0002"}))
	w := httptest.NewRecorder()
	ListCustomDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out []CustomDPSummary
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d items", len(out))
	}
}

// --- tests: GetCustomDataPoint ---

func TestGetCustomDataPoint_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0003", "HmIP-BROLL")
	addCustomDP(d, "DEV0003", "LEVEL", 1, hmenum.DataPointCategoryCover)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0003": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0003", "name": "LEVEL"}))
	w := httptest.NewRecorder()
	GetCustomDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out CustomDPDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "LEVEL" {
		t.Fatalf("expected name=LEVEL, got %q", out.Name)
	}
	if out.Category != string(hmenum.DataPointCategoryCover) {
		t.Fatalf("expected category=cover, got %q", out.Category)
	}
}

func TestGetCustomDataPoint_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0004", "HmIP-BSM")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0004": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0004", "name": "MISSING"}))
	w := httptest.NewRecorder()
	GetCustomDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- tests: InvokeCustomDataPoint ---
// Note: operation is now a URL path parameter {operation}, not in the JSON body.

func TestInvokeCustomDataPoint_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0005", "HmIP-BSM")
	addCustomDP(d, "DEV0005", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0005": d}}
	writer := &stubCustomDPWriter{}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0005", "name": "STATE", "operation": "turn_on"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("expected 1 invoke call, got %d", len(writer.calls))
	}
	if writer.calls[0].operation != "turn_on" {
		t.Fatalf("expected operation=turn_on, got %q", writer.calls[0].operation)
	}
}

func TestInvokeCustomDataPoint_UnknownOperation_Returns400(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0006", "HmIP-BSM")
	addCustomDP(d, "DEV0006", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0006": d}}
	writer := &stubCustomDPWriter{invokeErr: ErrUnknownOperation}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0006", "name": "STATE", "operation": "fly"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInvokeCustomDataPoint_BadParam_Returns422(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0007", "HmIP-BSM")
	addCustomDP(d, "DEV0007", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0007": d}}
	writer := &stubCustomDPWriter{invokeErr: ErrBadParam}

	body := bytes.NewBufferString(`{"params":{"brightness":-1}}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0007", "name": "STATE", "operation": "set_brightness"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestInvokeCustomDataPoint_WriterNil_Returns503(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0008", "HmIP-BSM")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0008": d}}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0008", "name": "STATE", "operation": "turn_on"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestInvokeCustomDataPoint_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	writer := &stubCustomDPWriter{}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "name": "STATE", "operation": "turn_on"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestInvokeCustomDataPoint_MissingOperation_Returns400(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0009", "HmIP-BSM")
	addCustomDP(d, "DEV0009", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0009": d}}
	writer := &stubCustomDPWriter{}

	// operation URL param is empty string — chi gives empty string for missing wildcard
	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0009", "name": "STATE", "operation": ""}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- tests: supported operations ---

func TestSupportedOperationsFor_Light(t *testing.T) {
	t.Parallel()
	ops := supportedOperationsFor(hmenum.DataPointCategoryLight)
	if len(ops) == 0 {
		t.Fatal("expected non-empty operations for light")
	}
	found := false
	for _, op := range ops {
		if op == "turn_on" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected turn_on in light operations")
	}
}

func TestSupportedOperationsFor_Unknown_ReturnsNil(t *testing.T) {
	t.Parallel()
	ops := supportedOperationsFor(hmenum.DataPointCategoryUndefined)
	if ops != nil {
		t.Fatalf("expected nil for undefined category, got %v", ops)
	}
}

// --- tests: CustomDPWriter error wrapping ---

func TestCustomDPWriterError_IsWrappedCorrectly(t *testing.T) {
	t.Parallel()
	err := errors.Join(ErrUnknownOperation, errors.New("no such op"))
	if !errors.Is(err, ErrUnknownOperation) {
		t.Fatal("ErrUnknownOperation not detected via errors.Is on wrapped error")
	}
}

// --- supportedOperationsFor full coverage ---

func TestSupportedOperationsFor_AllCategories(t *testing.T) {
	t.Parallel()
	checkCat := func(name string, ops []string) {
		t.Helper()
		if ops == nil {
			t.Errorf("%s: expected non-nil ops slice", name)
		}
	}
	checkCat("Light", supportedOperationsFor(hmenum.DataPointCategoryLight))
	checkCat("Climate", supportedOperationsFor(hmenum.DataPointCategoryClimate))
	checkCat("Cover", supportedOperationsFor(hmenum.DataPointCategoryCover))
	checkCat("Lock", supportedOperationsFor(hmenum.DataPointCategoryLock))
	checkCat("Siren", supportedOperationsFor(hmenum.DataPointCategorySiren))
	checkCat("TextDisplay", supportedOperationsFor(hmenum.DataPointCategoryTextDisplay))
	checkCat("Valve", supportedOperationsFor(hmenum.DataPointCategoryValve))
	checkCat("Switch", supportedOperationsFor(hmenum.DataPointCategorySwitch))
	// Default case returns nil — that's fine.
	if got := supportedOperationsFor(hmenum.DataPointCategoryBinarySensor); got != nil {
		t.Errorf("default case: expected nil, got %v", got)
	}
}

// --- customDPState fallback (no DataPointState method) ---

// stubCustomDPNoState implements AttachableDataPoint but NOT DataPointState.
type stubCustomDPNoState struct {
	key hmtypes.DataPointKey
	cat hmenum.DataPointCategory
}

func (s *stubCustomDPNoState) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubCustomDPNoState) Category() hmenum.DataPointCategory { return s.cat }

func TestCustomDPState_FallbackMap(t *testing.T) {
	t.Parallel()
	dp := &stubCustomDPNoState{
		key: hmtypes.DataPointKey{
			ChannelAddress: "DEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		cat: hmenum.DataPointCategorySwitch,
	}
	got := customDPState(dp)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map fallback, got %T", got)
	}
	if m["channel_address"] != "DEV001:1" {
		t.Errorf("channel_address: got %v", m["channel_address"])
	}
	if m["parameter"] != "STATE" {
		t.Errorf("parameter: got %v", m["parameter"])
	}
}
