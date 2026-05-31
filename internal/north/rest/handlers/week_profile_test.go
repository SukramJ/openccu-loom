// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newWeekProfileFixture constructs a *device.Device with one climate-
// capable channel at address "0001ABCD:1", attaches a ProfileDataPoint
// with ProfileCount=6, MinTemp=4.5, MaxTemp=30.5, and pins the active
// profile to "P3". It returns both the device and the attached DP so
// callers can further manipulate the fixture.
func newWeekProfileFixture() (*device.Device, *weekprofile.ProfileDataPoint) {
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-eTRV-2",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test Thermostat",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyMaster)

	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "Test",
		ChannelAddress: "0001ABCD:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   6,
	})
	wp.ApplyDeviceMetadata(weekprofile.DeviceMetadata{
		MinTemp:      4.5,
		MaxTemp:      30.5,
		ProfileCount: 6,
	})
	if err := wp.SetCurrentProfile("P3"); err != nil {
		panic("test fixture: SetCurrentProfile: " + err.Error())
	}
	ch.AttachWeekProfile(wp)
	return d, wp
}

func newWeekProfileIndex(d *device.Device) *stubDeviceIndex {
	return &stubDeviceIndex{
		devices: map[string]*device.Device{d.Address: d},
	}
}

// TestGetWeekProfile_HappyPath verifies that a channel with an attached
// ProfileDataPoint returns 200 with the expected JSON shape.
func TestGetWeekProfile_HappyPath(t *testing.T) {
	t.Parallel()
	d, _ := newWeekProfileFixture()
	idx := newWeekProfileIndex(d)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	GetWeekProfile(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp WeekProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Address != "0001ABCD:1" {
		t.Errorf("address: want %q, got %q", "0001ABCD:1", resp.Address)
	}
	if resp.ScheduleType != "climate" {
		t.Errorf("schedule_type: want %q, got %q", "climate", resp.ScheduleType)
	}
	if resp.MinTemp != 4.5 {
		t.Errorf("min_temp: want 4.5, got %v", resp.MinTemp)
	}
	if resp.MaxTemp != 30.5 {
		t.Errorf("max_temp: want 30.5, got %v", resp.MaxTemp)
	}
	if resp.ProfileCount != 6 {
		t.Errorf("profile_count: want 6, got %d", resp.ProfileCount)
	}
	if resp.CurrentProfile != "P3" {
		t.Errorf("current_profile: want %q, got %q", "P3", resp.CurrentProfile)
	}
	if len(resp.AvailableProfiles) != 6 {
		t.Errorf("available_profiles: want 6 entries, got %d", len(resp.AvailableProfiles))
	}
	// has_climate_schedule is false because no ClimateProfile backend was
	// attached (that requires a real CCU pipeline); the field must be present.
	if resp.HasClimateSchedule {
		t.Error("has_climate_schedule: expected false (no backend attached in fixture)")
	}
}

// TestGetWeekProfile_NoWeekProfile verifies that a channel with no
// attached ProfileDataPoint returns 404 with a descriptive error body.
func TestGetWeekProfile_NoWeekProfile(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test Switch",
	})
	d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	idx := newWeekProfileIndex(d)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	GetWeekProfile(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetWeekProfile_UnknownChannel verifies that a valid device address
// with an unknown channel number returns 404.
func TestGetWeekProfile_UnknownChannel(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test Switch",
	})
	d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	idx := newWeekProfileIndex(d)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// Channel "99" does not exist on this device.
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "99"}))
	w := httptest.NewRecorder()
	GetWeekProfile(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetWeekProfile_UnknownDevice verifies that an address that does not
// exist in the registry returns 404.
func TestGetWeekProfile_UnknownDevice(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEADBEEF", "no": "1"}))
	w := httptest.NewRecorder()
	GetWeekProfile(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetWeekProfile_EmptyScheduleEnabled verifies that schedule_enabled is
// omitted from the response when the DP has no registered channels (i.e.
// the map is empty / nil).
func TestGetWeekProfile_EmptyScheduleEnabled(t *testing.T) {
	t.Parallel()
	d, _ := newWeekProfileFixture() // fixture never calls RegisterChannel
	idx := newWeekProfileIndex(d)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	GetWeekProfile(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	// Decode into a raw map to detect key presence.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := raw["schedule_enabled"]; present {
		t.Error("schedule_enabled must be omitted from response when empty, but key was present")
	}
}
