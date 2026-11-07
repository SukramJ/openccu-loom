//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ingestFixtureDevice runs one embedded fixture model through the production
// ingest and returns the hydrated device.
func ingestFixtureDevice(t *testing.T, model string) *device.Device {
	t.Helper()
	_, dev := ingestFixtureCentral(t, model)
	return dev
}

// ingestFixtureCentral is [ingestFixtureDevice] that also returns the central
// holding the device, for a test that reads it back through a north plane.
func ingestFixtureCentral(t *testing.T, model string) (*central.Unit, *device.Device) {
	t.Helper()
	srv := startMockCCUWithDevices(t, []string{model})
	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "fixture-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, snapshotLocale()).
		WithVisibility(visibility.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	for _, dev := range c.ModelRegistry.List() {
		if dev.Model == model {
			return c, dev
		}
	}
	t.Fatalf("the fixture %s hydrated no device", model)
	return nil, nil
}

// TestGlassThermostatKeepsBothSchedules pins the two week programs of the
// HmIP-WGTC on the recorded device description: the heating schedule lives in
// the MASTER paramset of the climate channel 8 (P<n>_* slots) and the switch
// schedule in that of the SWITCH_WEEK_PROFILE channel 7 (<nn>_WP_* slots). A
// device keeping only one of them leaves the other unreachable.
//
// The switch schedule addresses the backlight 2 and the switch receivers 4, 5
// and 6, with the bit the CCU's weekly-program editor gives each:
// virtualChannels [2, 4, 5, 6] for every HmIP-WGT model
// (OpenCCU-Base/www/config/easymodes/js/HmIPWeeklyProgram.js:425-427) and
// valCheckBox = 2^index over that list (:2926). The thermostat is no target.
func TestGlassThermostatKeepsBothSchedules(t *testing.T) {
	dev := ingestFixtureDevice(t, "HmIP-WGTC")

	climate := dev.Channel(dev.Address + ":8").WeekProfile()
	if climate == nil || climate.ScheduleType() != weekprofile.ScheduleTypeClimate {
		t.Errorf("channel 8 carries no climate week profile (got %v)", climate)
	}

	switchCh := dev.Channel(dev.Address + ":7")
	wp := switchCh.WeekProfile()
	if wp == nil || wp.ScheduleType() != weekprofile.ScheduleTypeDefault {
		t.Fatalf("channel 7 carries no switch week profile (got %v)", wp)
	}
	targets := wp.AvailableTargetChannels()
	got := make([]string, 0, len(targets))
	for key, info := range targets {
		got = append(got, fmt.Sprintf("%s=ch%d/bit%d/known=%t", key, info.ChannelNo, info.Bit, info.BitKnown))
	}
	sort.Strings(got)
	want := []string{
		"1_1=ch2/bit0/known=true",
		"2_1=ch4/bit1/known=true",
		"2_2=ch5/bit2/known=true",
		"2_3=ch6/bit3/known=true",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("switch schedule targets = %v, want %v", got, want)
	}
	if n := len(dev.ScheduleChannelSwitches()); n != len(want) {
		t.Errorf("schedule channel switches = %d, want %d", n, len(want))
	}
}

// TestGlassThermostatSchedulesHaveDistinctUniqueIDs reads both week programs
// of the recorded HmIP-WGTC back through the REST route a client builds its
// schedule entities from, on the production device index. Both are
// device-level entities; a key built from the device address alone gave them
// one id, so a client keying entities on it kept one schedule and dropped the
// other. The switch schedule keeps its key, the heating schedule takes the
// reference stack's climate family (notes/parity/by_design.md, "Week
// schedules").
func TestGlassThermostatSchedulesHaveDistinctUniqueIDs(t *testing.T) {
	c, dev := ingestFixtureCentral(t, "HmIP-WGTC")
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	idx := adapter.NewDevicesAdapter(reg)
	router := chi.NewRouter()
	router.Get("/devices/{addr}/channels/{no}/week_profile", handlers.GetWeekProfile(idx))

	uniqueID := func(no int) string {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/devices/%s/channels/%d/week_profile", dev.Address, no), http.NoBody))
		if w.Code != http.StatusOK {
			t.Fatalf("channel %d: expected 200, got %d body=%s", no, w.Code, w.Body.String())
		}
		var resp handlers.WeekProfileResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("channel %d: unmarshal: %v", no, err)
		}
		return resp.UniqueID
	}

	switchUID, climateUID := uniqueID(7), uniqueID(8)
	if switchUID == climateUID {
		t.Fatalf("both schedules carry the unique_id %q", switchUID)
	}
	serial := idx.SerialSuffix(c.Name())
	if want := routingkey.CanonicalUniqueID(serial, dev.Address,
		weekprofile.SensorParameter, weekprofile.SensorFamily); switchUID != want {
		t.Errorf("switch schedule unique_id = %q, want the unchanged %q", switchUID, want)
	}
	if want := "loom_climate_week_profile_" + strings.ToLower(dev.Address) + "_week_profile"; climateUID != want {
		t.Errorf("climate schedule unique_id = %q, want %q", climateUID, want)
	}
}
