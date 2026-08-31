// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestWeekProfileSlotHydrationDropsOnlyDecodableCells drives a MASTER
// paramset description through the real hydration path and asserts what
// the daemon ends up with, not what a predicate returns.
//
// The suppression at hydration time is a drop: no data point, so no REST
// field, no MQTT topic and no un_ignore.txt override. That is only
// defensible for cells the week-profile editor can show instead, so the
// test pairs both halves — every key that disappears from the channel
// must be one the week-profile parser consumes, and every key that is not
// a cell must still be there.
func TestWeekProfileSlotHydrationDropsOnlyDecodableCells(t *testing.T) {
	t.Parallel()

	// Cells: the profile-prefixed form of an HmIP thermostat and the bare
	// form of a classic HM-CC-RT-DN. Both must vanish from the channel.
	wantDropped := []string{
		"P1_ENDTIME_MONDAY_1",
		"P1_TEMPERATURE_MONDAY_1",
		"P6_TEMPERATURE_FRIDAY_13",
		"ENDTIME_MONDAY_1",
		"TEMPERATURE_FRIDAY_13",
	}
	// Near misses: same prefix or same first word, but no cell the parser
	// could decode. Each one must survive as a MASTER data point.
	wantKept := []string{
		"P1_X",
		"P1_LEVEL_MONDAY_1",
		"P7_ENDTIME_MONDAY_1",
		"P1_ENDTIME_MONDAY_14",
		"TEMPERATURE_OFFSET",
		"WEEK_PROGRAM_POINTER",
		"WEEK_PROGRAM_CHANNEL_LOCKS",
	}

	desc := make(map[string]hmproto.ParameterData, len(wantDropped)+len(wantKept))
	for _, name := range append(append([]string{}, wantDropped...), wantKept...) {
		desc[name] = hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		}
	}

	const (
		devAddr = "WPSLOT01"
		chAddr  = devAddr + ":1"
	)
	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: devAddr, Type: "HmIP-eTRV-2"},
				{Address: chAddr, Parent: devAddr, Type: "HEATING_CLIMATECONTROL_TRANSCEIVER"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if address == chAddr && key == hmenum.ParamsetKeyMaster {
				return desc, nil
			}
			return nil, nil
		},
	}

	c, err := central.New(central.Config{Name: "ccu-wp-slot"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	w := client.NewValueWriter()
	w.Register("ccu-wp-slot", "HmIP-RF", b)
	p := NewDevicePipeline(c)
	if err := p.IngestFromBackend(context.Background(), "HmIP-RF",
		hmenum.InterfaceHmIPRF, b, w, nil, slog.Default()); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get(devAddr)
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel(chAddr)
	if ch == nil {
		t.Fatal("channel not found")
	}

	for _, name := range wantKept {
		if !ch.HasMasterParameter(name) {
			t.Errorf("MASTER parameter %q was dropped at hydration; it is not a week-profile cell and reaches no surface at all now", name)
		}
	}
	for _, name := range wantDropped {
		if ch.HasMasterParameter(name) {
			t.Errorf("week-profile cell %q survived hydration as a data point; it belongs to the profile entity, not to the parameter list", name)
		}
	}

	// The cells are only replaced, not lost, when the channel actually
	// gains the profile entity that stands in for them.
	if !ch.HasWeekProfile() {
		t.Error("channel carries week-profile cells but gained no week-profile data point")
	}

	// Effect half, computed from what hydration really did rather than
	// from the predicate: every profile-prefixed key that disappeared has
	// to be one the model's parser can decode into a slot.
	var droppedPrefixed []string
	for name := range desc {
		if ch.HasMasterParameter(name) {
			continue
		}
		if len(name) > 1 && name[0] == 'P' && name[1] >= '0' && name[1] <= '9' {
			droppedPrefixed = append(droppedPrefixed, name)
		}
	}
	sort.Strings(droppedPrefixed)
	for _, name := range droppedPrefixed {
		raw, err := weekprofile.ParseClimateRawParamset(map[string]any{name: 360})
		if err != nil || len(raw) == 0 {
			t.Errorf("hydration suppressed a key the week-profile parser cannot consume: %q (parse err=%v, slots=%d)", name, err, len(raw))
		}
	}

	// The bare form has no parser in the model yet, so it is pinned by
	// name here; the set must not grow without a decoder to match.
	var droppedBare []string
	for name := range desc {
		if !ch.HasMasterParameter(name) && !strings.HasPrefix(name, "P") {
			droppedBare = append(droppedBare, name)
		}
	}
	sort.Strings(droppedBare)
	wantBare := []string{"ENDTIME_MONDAY_1", "TEMPERATURE_FRIDAY_13"}
	if strings.Join(droppedBare, ",") != strings.Join(wantBare, ",") {
		t.Errorf("bare-form suppression set = %v, want %v", droppedBare, wantBare)
	}
}
