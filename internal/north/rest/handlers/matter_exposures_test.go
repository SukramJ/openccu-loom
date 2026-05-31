// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeExposureStore struct {
	mu        sync.Mutex
	records   []matterstore.ExposureRecord
	upserted  []matterstore.ExposureRecord
	listErr   error
	upsertErr error
	count     int
}

func (f *fakeExposureStore) GetExposure(_ context.Context, key matterstore.EndpointKey) (matterstore.ExposureRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.records {
		if f.records[i].Key == key {
			return f.records[i], nil
		}
	}
	return matterstore.ExposureRecord{}, matterstore.ErrExposureNotFound
}

func (f *fakeExposureStore) ListExposures(_ context.Context, _ string) ([]matterstore.ExposureRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records, f.listErr
}

func (f *fakeExposureStore) UpsertExposure(_ context.Context, rec matterstore.ExposureRecord) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, rec)
	return nil
}

func (f *fakeExposureStore) CountEnabled(_ context.Context, _ string) (int, error) {
	return f.count, nil
}

type fakeCandidateProvider struct {
	candidates []eligibility.Candidate
}

func (f *fakeCandidateProvider) MatterCandidates(_ context.Context) []eligibility.Candidate {
	return f.candidates
}

type fakeStatusReader struct {
	resp MatterStatusResponse
}

func (f *fakeStatusReader) MatterStatus(_ context.Context) MatterStatusResponse {
	return f.resp
}

type fakeFabricRevoker struct {
	err error
}

func (f *fakeFabricRevoker) RevokeFabric(_ context.Context, _ uint8) error {
	return f.err
}

type fakeCommissioningCloser struct {
	err error
}

func (f *fakeCommissioningCloser) CloseCommissioningWindow(_ context.Context) error {
	return f.err
}

type fakeMatterEventPublisher struct {
	mu     sync.Mutex
	events []MatterEvent
}

func (f *fakeMatterEventPublisher) PublishMatterEvent(_ context.Context, ev MatterEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeMatterEventPublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// ---------------------------------------------------------------------------
// MatterStatus
// ---------------------------------------------------------------------------

func TestMatterStatus_NilReader_Returns200_Disabled(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/status", http.NoBody)
	w := httptest.NewRecorder()
	MatterStatus(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Enabled {
		t.Error("expected enabled=false when reader is nil")
	}
}

func TestMatterStatus_WithReader_Returns200_WithSnapshot(t *testing.T) {
	t.Parallel()
	reader := &fakeStatusReader{resp: MatterStatusResponse{
		Enabled:       true,
		Listening:     true,
		ListenAddr:    "0.0.0.0:5540",
		EndpointCount: 3,
		FabricCount:   1,
		EnabledCount:  2,
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/status", http.NoBody)
	w := httptest.NewRecorder()
	MatterStatus(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Enabled {
		t.Error("expected enabled=true")
	}
	if body.EndpointCount != 3 {
		t.Errorf("endpoint_count: got %d, want 3", body.EndpointCount)
	}
	if body.FabricCount != 1 {
		t.Errorf("fabric_count: got %d, want 1", body.FabricCount)
	}
	if body.ListenAddr != "0.0.0.0:5540" {
		t.Errorf("listen_addr: got %q", body.ListenAddr)
	}
}

// ---------------------------------------------------------------------------
// MatterExposable
// ---------------------------------------------------------------------------

func TestMatterExposable_NilProvider_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/exposable", http.NoBody)
	w := httptest.NewRecorder()
	MatterExposable(nil, &fakeExposureStore{}, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposable_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	provider := &fakeCandidateProvider{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/exposable", http.NoBody)
	w := httptest.NewRecorder()
	MatterExposable(provider, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposable_HappyPath_Returns200_MergesData(t *testing.T) {
	t.Parallel()
	key := matterstore.EndpointKey{
		CentralName:   "ccu-01",
		DeviceAddress: "DEV001",
		ChannelNo:     1,
		DPKind:        matterstore.DPKindCustom,
		DPKey:         "onoff",
	}
	provider := &fakeCandidateProvider{
		candidates: []eligibility.Candidate{
			{
				Key:         key,
				DisplayName: "Living Room Switch",
				Verdict: eligibility.Verdict{
					State:      eligibility.StateMappable,
					DeviceType: 0x010A,
					Clusters:   []uint32{0x0006},
				},
			},
		},
	}
	store := &fakeExposureStore{
		records: []matterstore.ExposureRecord{
			{
				Key:          key,
				Enabled:      true,
				FriendlyName: "Living Room",
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/exposable", http.NoBody)
	w := httptest.NewRecorder()
	MatterExposable(provider, store, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterExposureList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Items))
	}
	item := body.Items[0]
	if !item.Enabled {
		t.Error("expected enabled=true (merged from store)")
	}
	if item.FriendlyName != "Living Room" {
		t.Errorf("friendly_name: got %q", item.FriendlyName)
	}
	if item.DisplayName != "Living Room Switch" {
		t.Errorf("display_name: got %q", item.DisplayName)
	}
	if item.Mappable != "mappable" {
		t.Errorf("mappable: got %q", item.Mappable)
	}
	if item.DeviceType != 0x010A {
		t.Errorf("device_type: got 0x%04X, want 0x010A", item.DeviceType)
	}
	if item.DeviceTypeLabel != "On/Off Plug-in Unit" {
		t.Errorf("device_type_label: got %q, want %q", item.DeviceTypeLabel, "On/Off Plug-in Unit")
	}
}

// ---------------------------------------------------------------------------
// MatterExposeUpdate
// ---------------------------------------------------------------------------

func TestMatterExposeUpdate_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matter/exposable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeUpdate(nil, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeUpdate_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matter/exposable", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeUpdate(&fakeExposureStore{}, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeUpdate_InvalidKey_Returns400(t *testing.T) {
	t.Parallel()
	// Missing central_name
	body := `{"central_name":"","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matter/exposable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeUpdate(&fakeExposureStore{}, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty central_name, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeUpdate_HappyPath_Returns204_PublishesEvent(t *testing.T) {
	t.Parallel()
	store := &fakeExposureStore{}
	pub := &fakeMatterEventPublisher{}
	body := `{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matter/exposable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeUpdate(store, pub, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if pub.count() != 1 {
		t.Errorf("expected 1 published event, got %d", pub.count())
	}
	if pub.events[0].Topic != MatterTopicExposableChanged {
		t.Errorf("event topic: got %q, want %q", pub.events[0].Topic, MatterTopicExposableChanged)
	}
}

// ---------------------------------------------------------------------------
// MatterExposeBulk
// ---------------------------------------------------------------------------

func TestMatterExposeBulk_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"items":[{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/exposable/bulk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeBulk(nil, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeBulk_EmptyItems_Returns204(t *testing.T) {
	t.Parallel()
	body := `{"items":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/exposable/bulk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeBulk(&fakeExposureStore{}, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for empty items, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeBulk_BadItemKey_Returns400(t *testing.T) {
	t.Parallel()
	// First item valid, second item has invalid dp_kind.
	body := `{"items":[
		{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true},
		{"central_name":"ccu","device_address":"DEV2","channel_no":1,"dp_kind":"invalid_kind","dp_key":"onoff","enabled":true}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/exposable/bulk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeBulk(&fakeExposureStore{}, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterExposeBulk_HappyPath_Returns200_AppliedCount_PublishesOnce(t *testing.T) {
	t.Parallel()
	store := &fakeExposureStore{}
	pub := &fakeMatterEventPublisher{}
	body := `{"items":[
		{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true},
		{"central_name":"ccu","device_address":"DEV2","channel_no":1,"dp_kind":"generic","dp_key":"LEVEL","enabled":false}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/exposable/bulk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeBulk(store, pub, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	applied, ok := resp["applied"]
	if !ok {
		t.Fatal("response missing 'applied' field")
	}
	// JSON numbers unmarshal as float64.
	if int(applied.(float64)) != 2 {
		t.Errorf("applied: got %v, want 2", applied)
	}
	if pub.count() != 1 {
		t.Errorf("expected exactly 1 published event, got %d", pub.count())
	}
}

// ---------------------------------------------------------------------------
// MatterFabricRevoke
// ---------------------------------------------------------------------------

func TestMatterFabricRevoke_NilRevoker_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/matter/fabrics/{id}", MatterFabricRevoke(nil, nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/matter/fabrics/1", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabricRevoke_NonNumericID_Returns400(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/matter/fabrics/{id}", MatterFabricRevoke(&fakeFabricRevoker{}, nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/matter/fabrics/abc", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabricRevoke_ZeroID_Returns400(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/matter/fabrics/{id}", MatterFabricRevoke(&fakeFabricRevoker{}, nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/matter/fabrics/0", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for id=0, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabricRevoke_ValidID_Returns204(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/matter/fabrics/{id}", MatterFabricRevoke(&fakeFabricRevoker{}, nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/matter/fabrics/3", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterFabricRevoke_RevokerError_Returns500(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/matter/fabrics/{id}", MatterFabricRevoke(&fakeFabricRevoker{err: errors.New("db fail")}, nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/matter/fabrics/2", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// MatterCommissioningClose
// ---------------------------------------------------------------------------

func TestMatterCommissioningClose_NilCloser_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window/close", http.NoBody)
	w := httptest.NewRecorder()
	MatterCommissioningClose(nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningClose_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window/close", http.NoBody)
	w := httptest.NewRecorder()
	MatterCommissioningClose(&fakeCommissioningCloser{}, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatterCommissioningClose_CloserError_Returns500(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window/close", http.NoBody)
	w := httptest.NewRecorder()
	MatterCommissioningClose(&fakeCommissioningCloser{err: errors.New("bridge dead")}, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- MatterShare delegates to MatterCommissioningWindow ---

func TestMatterShare_NilOpener_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/share", http.NoBody)
	w := httptest.NewRecorder()
	MatterShare(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- actorFromRequest with context value set ---

func TestActorFromRequest_WithContextValue(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	ctx := context.WithValue(req.Context(), actorContextKey{}, "alice")
	req = req.WithContext(ctx)
	got := actorFromRequest(req)
	if got != "alice" {
		t.Errorf("expected alice, got %q", got)
	}
}

func TestActorFromRequest_NoContext_ReturnsAnonymous(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	got := actorFromRequest(req)
	if got != "anonymous" {
		t.Errorf("expected anonymous, got %q", got)
	}
}

func TestActorFromRequest_EmptyStringInContext_ReturnsAnonymous(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	ctx := context.WithValue(req.Context(), actorContextKey{}, "")
	req = req.WithContext(ctx)
	got := actorFromRequest(req)
	if got != "anonymous" {
		t.Errorf("expected anonymous for empty string, got %q", got)
	}
}

// --- MatterExposable with list error ---

func TestMatterExposable_StoreListError_Returns500(t *testing.T) {
	t.Parallel()
	provider := &fakeCandidateProvider{}
	store := &fakeExposureStore{listErr: errors.New("db fail")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/exposable", http.NoBody)
	w := httptest.NewRecorder()
	MatterExposable(provider, store, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- MatterCandidateProviderFunc ---

func TestMatterCandidateProviderFunc_Nil(t *testing.T) {
	t.Parallel()
	var f MatterCandidateProviderFunc
	if got := f.MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil func should return nil, got %v", got)
	}
}

func TestMatterCandidateProviderFunc_NonNil(t *testing.T) {
	t.Parallel()
	want := []eligibility.Candidate{{DisplayName: "test"}}
	f := MatterCandidateProviderFunc(func(_ context.Context) []eligibility.Candidate {
		return want
	})
	got := f.MatterCandidates(context.Background())
	if len(got) != 1 || got[0].DisplayName != "test" {
		t.Errorf("unexpected result: %v", got)
	}
}

// --- MatterExposeUpdate with store error ---

func TestMatterExposeUpdate_StoreError_Returns500(t *testing.T) {
	t.Parallel()
	store := &fakeExposureStore{upsertErr: errors.New("write fail")}
	body := `{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matter/exposable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeUpdate(store, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- MatterExposeBulk with store error on item ---

func TestMatterExposeBulk_StoreError_Returns500(t *testing.T) {
	t.Parallel()
	store := &fakeExposureStore{upsertErr: errors.New("write fail")}
	body := `{"items":[{"central_name":"ccu","device_address":"DEV1","channel_no":1,"dp_kind":"custom","dp_key":"onoff","enabled":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/exposable/bulk", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterExposeBulk(store, nil, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- validateExposureKey unknown dp_kind branch ---

func TestValidateExposureKey_UnknownKind(t *testing.T) {
	t.Parallel()
	if err := validateExposureKey("ccu", "DEV1", "unknown_kind", "key"); err == nil {
		t.Fatal("unknown dp_kind: expected error")
	}
}

func TestValidateExposureKey_AllKnownKinds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"custom", "generic", "calculated", "combined", "measurement"} {
		if err := validateExposureKey("ccu", "DEV1", kind, "key"); err != nil {
			t.Errorf("kind=%s: unexpected error: %v", kind, err)
		}
	}
}

func TestValidateExposureKey_EmptyFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		central, addr, kind, key string
	}{
		{"", "D", "custom", "k"},
		{"c", "", "custom", "k"},
		{"c", "D", "", "k"},
		{"c", "D", "custom", ""},
	}
	for _, tc := range cases {
		if err := validateExposureKey(tc.central, tc.addr, tc.kind, tc.key); err == nil {
			t.Errorf("(%q,%q,%q,%q): expected error for empty field", tc.central, tc.addr, tc.kind, tc.key)
		}
	}
}

// --- MatterExposable candidates sorting ---

func TestMatterExposable_SortOrder(t *testing.T) {
	t.Parallel()
	provider := &fakeCandidateProvider{
		candidates: []eligibility.Candidate{
			{
				Key: matterstore.EndpointKey{
					CentralName: "ccu-02", DeviceAddress: "DEV002",
					ChannelNo: 1, DPKind: matterstore.DPKindGeneric, DPKey: "STATE",
				},
				DisplayName: "Second",
				Verdict:     eligibility.Verdict{State: eligibility.StateMappable},
			},
			{
				Key: matterstore.EndpointKey{
					CentralName: "ccu-01", DeviceAddress: "DEV001",
					ChannelNo: 1, DPKind: matterstore.DPKindGeneric, DPKey: "STATE",
				},
				DisplayName: "First",
				Verdict:     eligibility.Verdict{State: eligibility.StateMappable},
			},
		},
	}
	store := &fakeExposureStore{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/matter/exposable", http.NoBody)
	w := httptest.NewRecorder()
	MatterExposable(provider, store, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body MatterExposureList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(body.Items))
	}
	// ccu-01 should come before ccu-02 (sorted by central_name).
	if body.Items[0].CentralName != "ccu-01" {
		t.Errorf("sort order: first item should be ccu-01, got %q", body.Items[0].CentralName)
	}
}

// --- typeFromTopic ---

func TestTypeFromTopic_NoPrefix_ReturnsTopic(t *testing.T) {
	t.Parallel()
	if got := typeFromTopic("onoff"); got != "onoff" {
		t.Errorf("expected onoff, got %q", got)
	}
}

func TestTypeFromTopic_WithPrefix_StripsMatterDot(t *testing.T) {
	t.Parallel()
	if got := typeFromTopic("matter.onoff"); got != "onoff" {
		t.Errorf("expected onoff, got %q", got)
	}
}

// --- recordMatterAudit non-nil recorder path ---

func TestRecordMatterAudit_NonNilRecorder(t *testing.T) {
	t.Parallel()
	buf := audit.NewBuffer(10)
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	recordMatterAudit(buf, req, audit.ActionMatterShare, "test note")
	entries := buf.List(10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Note != "test note" {
		t.Errorf("expected note 'test note', got %q", entries[0].Note)
	}
}
