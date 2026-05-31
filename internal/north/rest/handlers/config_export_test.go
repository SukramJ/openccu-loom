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
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/configui"
)

// --- stubs ---------------------------------------------------------------

// stubConfigExportService is an inline stub for ConfigExportService.
type stubConfigExportService struct {
	readResult map[string]any
	readErr    error
	writeErr   error

	// Capture fields for assertions.
	capturedCentral  string
	capturedChannel  string
	capturedParamset string
	capturedValues   map[string]any
}

func (s *stubConfigExportService) ReadParamset(_ context.Context, centralName, channelAddress, paramsetKey string) (map[string]any, error) {
	s.capturedCentral = centralName
	s.capturedChannel = channelAddress
	s.capturedParamset = paramsetKey
	if s.readErr != nil {
		return nil, s.readErr
	}
	// Return a copy to prevent aliasing.
	out := make(map[string]any, len(s.readResult))
	for k, v := range s.readResult {
		out[k] = v
	}
	return out, nil
}

func (s *stubConfigExportService) WriteParamset(_ context.Context, centralName, channelAddress, paramsetKey string, values map[string]any) error {
	s.capturedCentral = centralName
	s.capturedChannel = channelAddress
	s.capturedParamset = paramsetKey
	s.capturedValues = values
	return s.writeErr
}

// stubChannelInfoReader is an inline stub for ChannelInfoReader.
type stubChannelInfoReader struct {
	deviceAddr  string
	model       string
	channelType string
	centralName string
	found       bool
}

func (s *stubChannelInfoReader) ChannelMeta(_ string) (deviceAddress, model, channelType, centralName string, ok bool) {
	return s.deviceAddr, s.model, s.channelType, s.centralName, s.found
}

// --- helpers -------------------------------------------------------------

// validExportPayload returns a JSON-encoded ExportedConfiguration for
// address and channelAddr that passes ImportConfiguration validation.
func validExportPayload(channelAddr string) []byte {
	cfg := configui.ExportedConfiguration{
		Version:        configui.ExportVersion,
		ExportedAt:     time.Now().UTC(),
		CentralName:    "ccu1",
		DeviceAddress:  strings.Split(channelAddr, ":")[0],
		Model:          "HmIP-eTRV-2",
		ChannelAddress: channelAddr,
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Values:         map[string]any{"TEMPERATURE_OFFSET": 1.5},
	}
	b, _ := json.Marshal(cfg)
	return b
}

// --- Export endpoint tests -----------------------------------------------

// TestExportChannelConfig_HappyPath verifies that a valid request returns
// 200 with an ExportedConfiguration body.
func TestExportChannelConfig_HappyPath(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{
		readResult: map[string]any{"TEMPERATURE_OFFSET": 1.5},
	}
	meta := &stubChannelInfoReader{
		deviceAddr:  "0001ABCD",
		model:       "HmIP-eTRV-2",
		channelType: "CLIMATE_TRANSCEIVER",
		centralName: "ccu1",
		found:       true,
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/devices/0001ABCD/channels/1/config/export", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ExportChannelConfig(svc, meta).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body configui.ExportedConfiguration
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Version != configui.ExportVersion {
		t.Errorf("version=%q want %q", body.Version, configui.ExportVersion)
	}
	if body.ChannelAddress != "0001ABCD:1" {
		t.Errorf("channel_address=%q want 0001ABCD:1", body.ChannelAddress)
	}
	if body.Model != "HmIP-eTRV-2" {
		t.Errorf("model=%q want HmIP-eTRV-2", body.Model)
	}
	if body.CentralName != "ccu1" {
		t.Errorf("central_name=%q want ccu1", body.CentralName)
	}
}

// TestExportChannelConfig_ServiceNil_Returns503 ensures a nil service
// returns 503, not a panic.
func TestExportChannelConfig_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ExportChannelConfig(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestExportChannelConfig_ChannelNotFound_Returns404 tests the 404 path
// when the ChannelInfoReader reports the channel as unknown.
func TestExportChannelConfig_ChannelNotFound_Returns404(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{}
	meta := &stubChannelInfoReader{found: false}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "UNKNOWN", "no": "1"}))
	w := httptest.NewRecorder()
	ExportChannelConfig(svc, meta).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestExportChannelConfig_InvalidParamset_Returns400 verifies that an
// unknown paramset query value is rejected with 400.
func TestExportChannelConfig_InvalidParamset_Returns400(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{}
	meta := &stubChannelInfoReader{found: true, deviceAddr: "0001ABCD"}

	req := httptest.NewRequest(http.MethodGet, "/?paramset=LINK", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ExportChannelConfig(svc, meta).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestExportChannelConfig_ReadError_Returns500 verifies that a CCU read
// failure maps to 500.
func TestExportChannelConfig_ReadError_Returns500(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{readErr: errors.New("CCU not reachable")}
	meta := &stubChannelInfoReader{
		deviceAddr:  "0001ABCD",
		model:       "HmIP-eTRV-2",
		channelType: "CLIMATE_TRANSCEIVER",
		centralName: "ccu1",
		found:       true,
	}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ExportChannelConfig(svc, meta).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- Import endpoint tests -----------------------------------------------

// TestImportChannelConfig_HappyPath checks that a valid payload returns 200
// and the writer receives the expected central/channel/paramset scope.
func TestImportChannelConfig_HappyPath(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{}
	payload := validExportPayload("0001ABCD:1")

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.capturedChannel != "0001ABCD:1" {
		t.Errorf("writer channel=%q want 0001ABCD:1", svc.capturedChannel)
	}
	if svc.capturedParamset != "MASTER" {
		t.Errorf("writer paramset=%q want MASTER", svc.capturedParamset)
	}
	if svc.capturedCentral != "ccu1" {
		t.Errorf("writer central=%q want ccu1", svc.capturedCentral)
	}
}

// TestImportChannelConfig_ServiceNil_Returns503 ensures nil service is safe.
func TestImportChannelConfig_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(validExportPayload("0001ABCD:1")))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestImportChannelConfig_InvalidJSON_Returns400 checks that malformed JSON
// is rejected with 400.
func TestImportChannelConfig_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json at all"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestImportChannelConfig_VersionMismatch_Returns400 ensures an unknown
// format version is rejected before touching the CCU.
func TestImportChannelConfig_VersionMismatch_Returns400(t *testing.T) {
	t.Parallel()

	payload := `{
		"version":        "2.0",
		"exported_at":    "2026-04-28T12:00:00Z",
		"device_address": "0001ABCD",
		"model":          "HmIP-eTRV-2",
		"channel_address":"0001ABCD:1",
		"channel_type":   "CLIMATE_TRANSCEIVER",
		"paramset_key":   "MASTER",
		"values":         {}
	}`

	svc := &stubConfigExportService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (version mismatch), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestImportChannelConfig_ChannelMismatch_Returns400 verifies that a
// payload targeting a different channel than the URL is rejected.
func TestImportChannelConfig_ChannelMismatch_Returns400(t *testing.T) {
	t.Parallel()

	// Payload says channel :1, URL says channel :2.
	payload := validExportPayload("0001ABCD:1")
	svc := &stubConfigExportService{}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "2"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestImportChannelConfig_WriteError_Returns500 verifies that a CCU write
// failure is mapped to 500.
func TestImportChannelConfig_WriteError_Returns500(t *testing.T) {
	t.Parallel()

	svc := &stubConfigExportService{writeErr: errors.New("CCU write failed")}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(validExportPayload("0001ABCD:1")))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestImportChannelConfig_MultiCCUScope verifies that a payload with a
// non-empty central_name is forwarded as-is to the writer so multi-CCU
// setups receive the correct scoping.
func TestImportChannelConfig_MultiCCUScope(t *testing.T) {
	t.Parallel()

	// Use ccu2 as the central name in the payload.
	cfg := configui.ExportedConfiguration{
		Version:        configui.ExportVersion,
		ExportedAt:     time.Now().UTC(),
		CentralName:    "ccu2",
		DeviceAddress:  "BEEF1234",
		Model:          "HmIP-WTH-2",
		ChannelAddress: "BEEF1234:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		ParamsetKey:    "MASTER",
		Values:         map[string]any{"DECALCIFICATION": true},
	}
	payload, _ := json.Marshal(cfg)

	svc := &stubConfigExportService{}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "BEEF1234", "no": "1"}))
	w := httptest.NewRecorder()
	ImportChannelConfig(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.capturedCentral != "ccu2" {
		t.Errorf("writer central=%q want ccu2", svc.capturedCentral)
	}
	if svc.capturedChannel != "BEEF1234:1" {
		t.Errorf("writer channel=%q want BEEF1234:1", svc.capturedChannel)
	}
}
