// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubMasterProfilesService is an inline stub for MasterProfilesService.
// It records the (deviceType, channelType) pair each call was invoked
// with so tests can assert the handler resolved the right target from
// the channel's device.
type stubMasterProfilesService struct {
	profiles map[string][]masterprofile.Profile // key: deviceType+"/"+channelType
	matchID  int

	lastDeviceType, lastChannelType string
}

func mpKey(deviceType, channelType string) string { return deviceType + "/" + channelType }

func (s *stubMasterProfilesService) Profiles(deviceType, channelType string) ([]masterprofile.Profile, error) {
	s.lastDeviceType, s.lastChannelType = deviceType, channelType
	p, ok := s.profiles[mpKey(deviceType, channelType)]
	if !ok {
		return nil, masterprofile.ErrNotFound
	}
	return p, nil
}

func (s *stubMasterProfilesService) Profile(deviceType, channelType string, id int) (masterprofile.Profile, error) {
	s.lastDeviceType, s.lastChannelType = deviceType, channelType
	for _, p := range s.profiles[mpKey(deviceType, channelType)] {
		if p.ID == id {
			return p, nil
		}
	}
	return masterprofile.Profile{}, masterprofile.ErrNotFound
}

func (s *stubMasterProfilesService) MatchActiveProfile(deviceType, channelType string, _ map[string]any) int {
	s.lastDeviceType, s.lastChannelType = deviceType, channelType
	return s.matchID
}

// newMasterProfileTestDevice returns a device with one channel of
// channelType at number 4, registered under addr in a *stubDeviceIndex.
func newMasterProfileTestDevice(addr, model, channelType string) (*stubDeviceIndex, *device.Device) {
	d := newTestDevice(addr, model)
	d.AddChannel(addr+":4", 4, channelType, hmenum.ParamsetKeyMaster)
	return &stubDeviceIndex{devices: map[string]*device.Device{addr: d}}, d
}

// --- ListMasterProfiles ---

func TestListMasterProfiles_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{
		profiles: map[string][]masterprofile.Profile{
			mpKey("HmIP-eTRV", "CLIMATECONTROL"): {
				{ID: 1, Name: map[string]string{"en": "Eco"}, Params: map[string]masterprofile.ParamConstraint{"A": {}}},
				{ID: 2, Name: map[string]string{"en": "Comfort"}},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/channels/4/master-profiles", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4"}))
	w := httptest.NewRecorder()
	ListMasterProfiles(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastDeviceType != "HmIP-eTRV" || svc.lastChannelType != "CLIMATECONTROL" {
		t.Fatalf("resolved target=(%q,%q), want (HmIP-eTRV,CLIMATECONTROL)", svc.lastDeviceType, svc.lastChannelType)
	}
	var body []masterProfileSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 || body[0].Name != "Eco" || body[0].ParamCount != 1 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListMasterProfiles_UnknownDeviceReturns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	svc := &stubMasterProfilesService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/GHOST/channels/4/master-profiles", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "GHOST", "no": "4"}))
	w := httptest.NewRecorder()
	ListMasterProfiles(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListMasterProfiles_NoCatalogueReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-Unknown", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/channels/4/master-profiles", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4"}))
	w := httptest.NewRecorder()
	ListMasterProfiles(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []masterProfileSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty array, got %+v", body)
	}
}

// --- GetMasterProfile ---

func TestGetMasterProfile_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{
		profiles: map[string][]masterprofile.Profile{
			mpKey("HmIP-eTRV", "CLIMATECONTROL"): {{ID: 7, Name: map[string]string{"en": "Eco"}}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/channels/4/master-profiles/7", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4", "id": "7"}))
	w := httptest.NewRecorder()
	GetMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body masterprofile.Profile
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ID != 7 {
		t.Fatalf("ID=%d want 7", body.ID)
	}
}

func TestGetMasterProfile_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/channels/4/master-profiles/99", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4", "id": "99"}))
	w := httptest.NewRecorder()
	GetMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetMasterProfile_InvalidIDReturns400(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/channels/4/master-profiles/not-a-number", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4", "id": "not-a-number"}))
	w := httptest.NewRecorder()
	GetMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- MatchMasterProfile ---

func TestMatchMasterProfile_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{matchID: 3}
	body := bytes.NewBufferString(`{"current_values":{"MODE":1}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/DEV001/channels/4/master-profiles/match", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4"}))
	w := httptest.NewRecorder()
	MatchMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]int
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["active_id"] != 3 {
		t.Fatalf("active_id=%d want 3", out["active_id"])
	}
	if svc.lastDeviceType != "HmIP-eTRV" || svc.lastChannelType != "CLIMATECONTROL" {
		t.Fatalf("resolved target=(%q,%q), want (HmIP-eTRV,CLIMATECONTROL)", svc.lastDeviceType, svc.lastChannelType)
	}
}

func TestMatchMasterProfile_NoBodyDefaultsToEmptyValues(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{matchID: 0}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/DEV001/channels/4/master-profiles/match", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4"}))
	w := httptest.NewRecorder()
	MatchMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMatchMasterProfile_InvalidJSONReturns400(t *testing.T) {
	t.Parallel()
	idx, _ := newMasterProfileTestDevice("DEV001", "HmIP-eTRV", "CLIMATECONTROL")
	svc := &stubMasterProfilesService{}
	body := bytes.NewBufferString(`{not-json`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/DEV001/channels/4/master-profiles/match", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "4"}))
	w := httptest.NewRecorder()
	MatchMasterProfile(idx, svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}
