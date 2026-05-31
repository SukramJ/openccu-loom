// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeFabricStore struct {
	recs []store.FabricRecord
	err  error
}

func (f *fakeFabricStore) ListFabrics(_ context.Context) ([]store.FabricRecord, error) {
	return f.recs, f.err
}

type fakeCommissioningOpener struct {
	result MatterCommissioningWindowResult
	err    error
}

func (f *fakeCommissioningOpener) OpenCommissioningWindow(_ context.Context, durationSeconds uint16) (MatterCommissioningWindowResult, error) {
	if f.err != nil {
		return MatterCommissioningWindowResult{}, f.err
	}
	res := f.result
	res.DurationSeconds = durationSeconds
	return res, nil
}

// ---------------------------------------------------------------------------
// MatterFabrics
// ---------------------------------------------------------------------------

func TestMatterFabrics_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/fabrics", http.NoBody)
	w := httptest.NewRecorder()
	MatterFabrics(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabrics_StoreError_Returns500(t *testing.T) {
	t.Parallel()
	s := &fakeFabricStore{err: errors.New("db gone")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/fabrics", http.NoBody)
	w := httptest.NewRecorder()
	MatterFabrics(s).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabrics_EmptyList_Returns200(t *testing.T) {
	t.Parallel()
	s := &fakeFabricStore{recs: []store.FabricRecord{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/fabrics", http.NoBody)
	w := httptest.NewRecorder()
	MatterFabrics(s).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterFabricList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Fabrics) != 0 {
		t.Fatalf("expected empty fabrics, got %+v", body.Fabrics)
	}
}

func TestMatterFabrics_SingleRecord_Returns200(t *testing.T) {
	t.Parallel()
	compID := [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	rootPK := []byte{0x04, 0xAB, 0xCD}
	rec := store.FabricRecord{
		FabricIndex:   1,
		FabricID:      0xDEADBEEFCAFEBABE,
		NodeID:        0x0000000000000001,
		VendorID:      0x1234,
		Label:         "home",
		CompressedID:  compID,
		RootPublicKey: rootPK,
	}
	s := &fakeFabricStore{recs: []store.FabricRecord{rec}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/fabrics", http.NoBody)
	w := httptest.NewRecorder()
	MatterFabrics(s).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterFabricList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Fabrics) != 1 {
		t.Fatalf("expected 1 fabric, got %d", len(body.Fabrics))
	}
	got := body.Fabrics[0]
	if got.FabricIndex != 1 {
		t.Errorf("fabric_index: got %d, want 1", got.FabricIndex)
	}
	if got.FabricID != 0xDEADBEEFCAFEBABE {
		t.Errorf("fabric_id: got %d, want 0xDEADBEEFCAFEBABE", got.FabricID)
	}
	if got.Label != "home" {
		t.Errorf("label: got %q, want %q", got.Label, "home")
	}
	if got.CompressedID != hex.EncodeToString(compID[:]) {
		t.Errorf("compressed_id: got %q, want %q", got.CompressedID, hex.EncodeToString(compID[:]))
	}
	if got.RootPublicKey != hex.EncodeToString(rootPK) {
		t.Errorf("root_public_key: got %q, want %q", got.RootPublicKey, hex.EncodeToString(rootPK))
	}
}

func TestMatterFabrics_MultipleRecords_Returns200(t *testing.T) {
	t.Parallel()
	recs := []store.FabricRecord{
		{FabricIndex: 1, FabricID: 100, NodeID: 1, VendorID: 0x1111, Label: "alpha", CompressedID: [8]byte{1}, RootPublicKey: []byte{0x04, 0x01}},
		{FabricIndex: 2, FabricID: 200, NodeID: 2, VendorID: 0x2222, Label: "beta", CompressedID: [8]byte{2}, RootPublicKey: []byte{0x04, 0x02}},
		{FabricIndex: 3, FabricID: 300, NodeID: 3, VendorID: 0x3333, Label: "", CompressedID: [8]byte{3}, RootPublicKey: []byte{0x04, 0x03}},
	}
	s := &fakeFabricStore{recs: recs}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/fabrics", http.NoBody)
	w := httptest.NewRecorder()
	MatterFabrics(s).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterFabricList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Fabrics) != 3 {
		t.Fatalf("expected 3 fabrics, got %d", len(body.Fabrics))
	}
	if body.Fabrics[0].FabricIndex != 1 || body.Fabrics[1].FabricIndex != 2 || body.Fabrics[2].FabricIndex != 3 {
		t.Errorf("unexpected fabric indices: %+v", body.Fabrics)
	}
}

// ---------------------------------------------------------------------------
// MatterSetupPayload
// ---------------------------------------------------------------------------

func TestMatterSetupPayload_PasscodeZero_Returns503(t *testing.T) {
	t.Parallel()
	c := MatterCommissioning{Discriminator: 0xF00, Passcode: 0}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/setup-payload", http.NoBody)
	w := httptest.NewRecorder()
	MatterSetupPayload(c).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterSetupPayload_ValidConfig_Returns200(t *testing.T) {
	t.Parallel()
	c := MatterCommissioning{
		Discriminator: 0xF00,
		Passcode:      20202021,
		VendorID:      0xFFF1,
		ProductID:     0x8000,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/setup-payload", http.NoBody)
	w := httptest.NewRecorder()
	MatterSetupPayload(c).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterSetupPayloadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Discriminator != c.Discriminator {
		t.Errorf("discriminator: got %d, want %d", body.Discriminator, c.Discriminator)
	}
	if body.Passcode != c.Passcode {
		t.Errorf("passcode: got %d, want %d", body.Passcode, c.Passcode)
	}
	if body.VendorID != c.VendorID {
		t.Errorf("vendor_id: got %d, want %d", body.VendorID, c.VendorID)
	}
	if body.ProductID != c.ProductID {
		t.Errorf("product_id: got %d, want %d", body.ProductID, c.ProductID)
	}
	if !strings.HasPrefix(body.QRCode, "MT:") {
		t.Errorf("qr_code must start with MT:, got %q", body.QRCode)
	}
	if len(body.ManualCode) != 11 {
		t.Errorf("manual_code must be 11 digits, got %q (len=%d)", body.ManualCode, len(body.ManualCode))
	}
	for _, ch := range body.ManualCode {
		if !unicode.IsDigit(ch) {
			t.Errorf("manual_code must be all digits, got %q", body.ManualCode)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// MatterCommissioningWindow
// ---------------------------------------------------------------------------

func TestMatterCommissioningWindow_NilOpener_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"duration_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_InvalidBody_Returns400(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{result: MatterCommissioningWindowResult{
		Discriminator: 0x700, Passcode: 99999999,
		QRCode: "MT:Y.GHY00-0007217580", ManualCode: "12345678901",
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_DurationTooShort_Returns400(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{}
	body := `{"duration_seconds":100}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too-short duration, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_DurationTooLong_Returns400(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{}
	body := `{"duration_seconds":901}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too-long duration, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_DurationZero_UsesDefault900(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{result: MatterCommissioningWindowResult{
		Discriminator: 0x700, Passcode: 99999999,
		QRCode: "MT:Y.GHY00-0007217580", ManualCode: "12345678901",
	}}
	body := `{"duration_seconds":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for zero duration (default 900), got %d body=%s", w.Code, w.Body.String())
	}
	var resp MatterCommissioningWindowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.DurationSeconds != 900 {
		t.Errorf("expected duration_seconds=900, got %d", resp.DurationSeconds)
	}
}

func TestMatterCommissioningWindow_CommissioningInProgress_Returns409(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{err: ErrCommissioningInProgress}
	body := `{"duration_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_OpenerError_Returns500(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{err: errors.New("chip stack crash")}
	body := `{"duration_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningWindow_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	opener := &fakeCommissioningOpener{result: MatterCommissioningWindowResult{
		Discriminator: 0xABC,
		Passcode:      12345678,
		QRCode:        "MT:Y.GHY00-0007217580",
		ManualCode:    "12345678901",
	}}
	body := `{"duration_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp MatterCommissioningWindowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Discriminator != 0xABC {
		t.Errorf("discriminator: got %d, want 0xABC", resp.Discriminator)
	}
	if resp.Passcode != 12345678 {
		t.Errorf("passcode: got %d, want 12345678", resp.Passcode)
	}
	if resp.DurationSeconds != 300 {
		t.Errorf("duration_seconds: got %d, want 300", resp.DurationSeconds)
	}
	if resp.QRCode != "MT:Y.GHY00-0007217580" {
		t.Errorf("qr_code: got %q", resp.QRCode)
	}
	if resp.ManualCode != "12345678901" {
		t.Errorf("manual_code: got %q", resp.ManualCode)
	}
}
