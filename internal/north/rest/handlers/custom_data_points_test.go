// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
	ListCustomDataPoints(idx, nil).ServeHTTP(w, req)

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
	ListCustomDataPoints(idx, nil).ServeHTTP(w, req)

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
	ListCustomDataPoints(idx, nil).ServeHTTP(w, req)

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

// TestInvokeCustomDataPoint_NoBody_Invokes pins the optional request
// body: a parameterless operation is invoked without one, so a bodyless
// POST must reach the writer instead of failing as invalid JSON.
func TestInvokeCustomDataPoint_NoBody_Invokes(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0005", "HmIP-BSM")
	addCustomDP(d, "DEV0005", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0005": d}}
	writer := &stubCustomDPWriter{}

	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0005", "name": "STATE", "operation": "turn_off"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("expected 1 invoke call, got %d", len(writer.calls))
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

func TestInvokeCustomDataPoint_ChannelLocked_Returns423(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0099", "HmIP-BSM")
	addCustomDP(d, "DEV0099", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0099": d}}
	// The operator's per-channel control lock rejects the invoke: a retry
	// cannot help until the lock is lifted, so this must be 423 Locked —
	// mirroring the paramset/value PUT routes — never a 502 upstream failure.
	writer := &stubCustomDPWriter{invokeErr: device.ErrChannelOperationLocked}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0099", "name": "STATE", "operation": "turn_on"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423 for a locked-channel custom-DP invoke, got %d", w.Code)
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

// --- tests: channel-group wire names ---

// addChannelGroupSwitches attaches two switch CDPs that materialise the
// same STATE parameter on channels 3 and 4 so [custom.WireName]
// disambiguates them to STATE@3 / STATE@4. Percent-encoded segments
// (STATE%403) are decoded centrally by the router (the rest package's
// decodedPathRouting middleware and its test), so these tests exercise
// the handler with the decoded values chi delivers.
func addChannelGroupSwitches(d *device.Device, addr string) {
	for _, no := range []int{3, 4} {
		addCustomDP(d, addr, "STATE", no, hmenum.DataPointCategorySwitch)
	}
}

// TestInvokeCustomDataPoint_LiteralAtName pins the channel-group `@`
// lookup on the invoke path.
func TestInvokeCustomDataPoint_LiteralAtName(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0101", "HMIP-PS")
	addChannelGroupSwitches(d, "DEV0101")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0101": d}}
	writer := &stubCustomDPWriter{}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0101", "name": "STATE@4", "operation": "turn_off"}))
	w := httptest.NewRecorder()
	InvokeCustomDataPoint(idx, writer).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for literal @ name, got %d body=%s", w.Code, w.Body.String())
	}
	if len(writer.calls) != 1 || writer.calls[0].name != "STATE@4" {
		t.Fatalf("expected one invoke for STATE@4, got %+v", writer.calls)
	}
}

// TestGetCustomDataPoint_ChannelGroupName pins the channel-group `@`
// lookup on the read path, so GET …/cdps/STATE@3 returns the CDP of
// channel 3.
func TestGetCustomDataPoint_ChannelGroupName(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0102", "HMIP-PS")
	addChannelGroupSwitches(d, "DEV0102")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0102": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0102", "name": "STATE@3"}))
	w := httptest.NewRecorder()
	GetCustomDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out CustomDPDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ChannelNo != 3 {
		t.Fatalf("expected channel_no=3 for STATE@3, got %d", out.ChannelNo)
	}
}

// --- tests: supported operations ---

func TestSupportedOperationsFor_Light(t *testing.T) {
	t.Parallel()
	ops := supportedOperationsFor(hmenum.DataPointCategoryLight)
	if len(ops) == 0 {
		t.Fatal("expected non-empty operations for light")
	}
	found := slices.Contains(ops, "turn_on")
	if !found {
		t.Fatal("expected turn_on in light operations")
	}
}

func TestSupportedOperationsFor_Unknown_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	ops := supportedOperationsFor(hmenum.DataPointCategoryUndefined)
	// Must be non-nil so the required wire array never serialises as null.
	if ops == nil {
		t.Fatal("expected non-nil empty slice for undefined category, got nil")
	}
	if len(ops) != 0 {
		t.Fatalf("expected empty slice for undefined category, got %v", ops)
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
	// Default case returns a non-nil empty slice (the wire array is required).
	if got := supportedOperationsFor(hmenum.DataPointCategoryBinarySensor); got == nil {
		t.Error("default case: expected non-nil empty slice, got nil")
	} else if len(got) != 0 {
		t.Errorf("default case: expected empty slice, got %v", got)
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

// TestCdpUniqueID_UniqueID verifies that cdpUniqueID stamps a loom_-prefixed
// key when serialSuffix is non-empty, and returns "" for empty serial.
func TestCdpUniqueID_UniqueID(t *testing.T) {
	t.Parallel()
	dp := &stubCustomDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: "DEV0200:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		category: hmenum.DataPointCategorySwitch,
	}

	t.Run("with serialSuffix produces loom_ prefix", func(t *testing.T) {
		t.Parallel()
		got := cdpUniqueID(dp, "vccu0000000")
		if got == "" {
			t.Fatal("cdpUniqueID must not return empty when serialSuffix is set")
		}
		if !strings.HasPrefix(got, "loom_") {
			t.Errorf("cdpUniqueID = %q, want loom_ prefix", got)
		}
	})

	t.Run("empty serialSuffix yields empty string", func(t *testing.T) {
		t.Parallel()
		got := cdpUniqueID(dp, "")
		if got != "" {
			t.Errorf("cdpUniqueID = %q, want empty string when serialSuffix is empty", got)
		}
	})

	t.Run("customDPUniqueID is channel-level (no parameter)", func(t *testing.T) {
		t.Parallel()
		// The reference stack keys custom data points by their primary
		// channel alone; the HA drop-in contract requires the identical shape.
		got := customDPUniqueID(dp, "vccu0000000")
		if got != "loom_dev0200_1" {
			t.Errorf("customDPUniqueID = %q, want %q", got, "loom_dev0200_1")
		}
		if got2 := customDPUniqueID(dp, ""); got2 != "" {
			t.Errorf("customDPUniqueID = %q, want empty string when serialSuffix is empty", got2)
		}
	})

	t.Run("list handler populates unique_id when serial is known", func(t *testing.T) {
		t.Parallel()
		d := newTestDevice("DEV0201", "HmIP-BSM")
		addCustomDP(d, "DEV0201", "STATE", 1, hmenum.DataPointCategorySwitch)
		idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0201": d}}

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0201"}))
		w := httptest.NewRecorder()
		ListCustomDataPoints(idx, nil).ServeHTTP(w, req)

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
		// stubDeviceIndex.SerialSuffix returns "vccu0000000" for non-empty central.
		// stubDeviceIndex.CentralOf returns "ccu-01" for known addresses, so
		// the handler will supply a non-empty suffix and unique_id should be set.
		if out[0].UniqueID == "" {
			t.Error("unique_id must be set when the central serial is known")
		}
		if !strings.HasPrefix(out[0].UniqueID, "loom_") {
			t.Errorf("unique_id = %q, want loom_ prefix", out[0].UniqueID)
		}
	})
}

// stubNamedCustomDP extends stubCustomDP with the optional interfaces
// the wire-name resolution consults (HAComponent, NamePostfix).
type stubNamedCustomDP struct {
	stubCustomDP
	haComponent string
	postfix     string
}

func (s *stubNamedCustomDP) HAComponent() string { return s.haComponent }
func (s *stubNamedCustomDP) NamePostfix() string { return s.postfix }

// addNamedCustomDP attaches a stubNamedCustomDP on a fresh channel.
func addNamedCustomDP(d *device.Device, addr string, no, groupNo int, component, postfix string) *device.Channel {
	chAddr := fmt.Sprintf("%s:%d", addr, no)
	ch := d.AddChannel(chAddr, no, "SWITCH", hmenum.ParamsetKeyValues)
	ch.AssignGroupNumber(groupNo)
	ch.SetCustomDataPoint(&stubNamedCustomDP{
		stubCustomDP: stubCustomDP{
			key: hmtypes.DataPointKey{
				ChannelAddress: chAddr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      "STATE",
			},
			category: hmenum.DataPointCategorySwitch,
			state:    map[string]any{"on": false},
		},
		haComponent: component,
		postfix:     postfix,
	})
	return ch
}

// TestListCustomDataPoints_WireNames pins that the summary ships the
// daemon-composed entity names so clients never rebuild them.
func TestListCustomDataPoints_WireNames(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0003", "HmIP-PSM")
	addNamedCustomDP(d, "DEV0003", 3, 3, "switch", "") // single primary
	addNamedCustomDP(d, "DEV0003", 4, 3, "switch", "") // secondary
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0003": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0003"}))
	w := httptest.NewRecorder()
	ListCustomDataPoints(idx, nil).ServeHTTP(w, req)

	var out []CustomDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byChannel := map[int]CustomDPSummary{}
	for _, s := range out {
		byChannel[s.ChannelNo] = s
	}
	// The single primary collapses to the device name: empty name parts.
	if p := byChannel[3]; p.TranslatedName != "" || p.ParameterName != "" {
		t.Errorf("primary: translated_name=%q parameter_name=%q, want empty/empty",
			p.TranslatedName, p.ParameterName)
	}
	// The secondary carries the vch marker.
	if s := byChannel[4]; s.TranslatedName != "vch4" || s.ParameterName != "vch4" {
		t.Errorf("secondary: translated_name=%q parameter_name=%q, want vch4/vch4",
			s.TranslatedName, s.ParameterName)
	}
}

// TestListCustomDataPoints_PostfixTranslation pins the button-lock shape:
// the postfix renders title-cased and the locale label wins for the
// display name.
func TestListCustomDataPoints_PostfixTranslation(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0004", "HmIP-BWTH")
	addNamedCustomDP(d, "DEV0004", 0, 0, "lock", "BUTTON_LOCK")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0004": d}}
	lab := translatorLabeler{entries: map[string]string{"SWITCH|BUTTON_LOCK": "Tastensperre"}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0004"}))
	w := httptest.NewRecorder()
	ListCustomDataPoints(idx, lab).ServeHTTP(w, req)

	var out []CustomDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 CDP, got %d", len(out))
	}
	if out[0].TranslatedName != "Tastensperre" {
		t.Errorf("translated_name = %q, want %q", out[0].TranslatedName, "Tastensperre")
	}
	if out[0].ParameterName != "Button Lock" {
		t.Errorf("parameter_name = %q, want %q", out[0].ParameterName, "Button Lock")
	}
}
