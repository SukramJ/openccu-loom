//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package integration

import (
	"context"
	"log/slog"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// unboundSchemaFields declares a "<model>/<profile>.<field>" the fleet
// materialises without the composing custom data point holding a pointer to
// the resolved parameter.
//
// An entry is not an exemption from the rule — it is a statement that this
// particular field reaches its consumer some other way, with the way named.
// The common case is a field consumed through Subscribe or through an
// embedded struct rather than held as a data-point pointer, which the
// reflection walk below cannot see.
//
// Anything not listed here and not bound is the defect this guard exists to
// catch: the schema says the device carries the parameter, the device does
// carry it, and the custom data point looked somewhere else.
// candidateNotYetMeasured marks a slot this guard found unbound and nobody
// has decided yet.
//
// It is not a clean bill of health. Each one is either a field consumed
// through Subscribe or through an embedded struct — which this reflection
// walk cannot see — or the very defect the guard exists to catch, of the same
// shape as the three that shipped broken: HM-CC-TC with no temperature,
// HmIP-DLD with no jam report, the RF jalousies with no tilt. Deciding one
// means feeding the parameter on the resolved channel and reading the
// accessor back, the way notes/plans/custom-dp-profile-schema-binding.md
// describes; the answer then either names the other consumer here or removes
// the entry along with a fix.
//
// What the list does buy, unfinished: no *new* unbound slot can appear
// without failing this test.
const candidateNotYetMeasured = "candidate — not yet measured against the wire"

var unboundSchemaFields = map[string]string{
	// IPCover.group_level_2 — 3 device(s)
	"HmIP-DRBLI4/IPCover.group_level_2": candidateNotYetMeasured,
	"HmIP-FBL/IPCover.group_level_2":    candidateNotYetMeasured,
	"HmIPW-DRBL4/IPCover.group_level_2": candidateNotYetMeasured,
	// IPDRGDALI.hue — 1 device(s)
	"HmIP-DRG-DALI/IPDRGDALI.hue": candidateNotYetMeasured,
	// IPDRGDALI.saturation — 1 device(s)
	"HmIP-DRG-DALI/IPDRGDALI.saturation": candidateNotYetMeasured,
	// IPIrrigationValve.group_state — 1 device(s)
	"ELV-SH-WSM/IPIrrigationValve.group_state": candidateNotYetMeasured,
	// IPRGBW.direction — 2 device(s)
	"HmIP-LSC/IPRGBW.direction":  candidateNotYetMeasured,
	"HmIP-RGBW/IPRGBW.direction": candidateNotYetMeasured,
	// IPSoundPlayer.direction — 1 device(s)
	"HmIP-MP3P/IPSoundPlayer.direction": candidateNotYetMeasured,
	// IPSoundPlayerLed.direction — 1 device(s)
	"HmIP-MP3P/IPSoundPlayerLed.direction": candidateNotYetMeasured,
	// IPSwitch.group_state — 23 device(s)
	"ELV-SH-BS2/IPSwitch.group_state":     candidateNotYetMeasured,
	"ELV-SH-SW1-BAT/IPSwitch.group_state": candidateNotYetMeasured,
	"HMIP-PS/IPSwitch.group_state":        candidateNotYetMeasured,
	"HMIP-PSM/IPSwitch.group_state":       candidateNotYetMeasured,
	"HmIP-BSL/IPSwitch.group_state":       candidateNotYetMeasured,
	"HmIP-BSM/IPSwitch.group_state":       candidateNotYetMeasured,
	"HmIP-DRSI1/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-DRSI4/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-FSI16/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-FSM16/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-MOD-OC8/IPSwitch.group_state":   candidateNotYetMeasured,
	"HmIP-PCBS-BAT/IPSwitch.group_state":  candidateNotYetMeasured,
	"HmIP-PCBS/IPSwitch.group_state":      candidateNotYetMeasured,
	"HmIP-PCBS2/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-PSMCO/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-SCTH230/IPSwitch.group_state":   candidateNotYetMeasured,
	"HmIP-SMO230-A/IPSwitch.group_state":  candidateNotYetMeasured,
	"HmIP-USBSM/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIP-WGC/IPSwitch.group_state":       candidateNotYetMeasured,
	"HmIP-WGTC/IPSwitch.group_state":      candidateNotYetMeasured,
	"HmIP-WHS2/IPSwitch.group_state":      candidateNotYetMeasured,
	"HmIPW-DRS8/IPSwitch.group_state":     candidateNotYetMeasured,
	"HmIPW-FIO6/IPSwitch.group_state":     candidateNotYetMeasured,
	// IPSwitch.state — 1 device(s)
	"HmIP-WGTC/IPSwitch.state": candidateNotYetMeasured,
	// IPThermostat.active_profile — 19 device(s)
	"ALPHA-IP-RBG/IPThermostat.active_profile":      candidateNotYetMeasured,
	"HmIP-BWTH/IPThermostat.active_profile":         candidateNotYetMeasured,
	"HmIP-STH/IPThermostat.active_profile":          candidateNotYetMeasured,
	"HmIP-STHD/IPThermostat.active_profile":         candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.active_profile":         candidateNotYetMeasured,
	"HmIP-WTH-1/IPThermostat.active_profile":        candidateNotYetMeasured,
	"HmIP-WTH-2/IPThermostat.active_profile":        candidateNotYetMeasured,
	"HmIP-eTRV-2 I9F/IPThermostat.active_profile":   candidateNotYetMeasured,
	"HmIP-eTRV-2/IPThermostat.active_profile":       candidateNotYetMeasured,
	"HmIP-eTRV-B-2 R4M/IPThermostat.active_profile": candidateNotYetMeasured,
	"HmIP-eTRV-B/IPThermostat.active_profile":       candidateNotYetMeasured,
	"HmIP-eTRV-B1/IPThermostat.active_profile":      candidateNotYetMeasured,
	"HmIP-eTRV-C-2/IPThermostat.active_profile":     candidateNotYetMeasured,
	"HmIP-eTRV-E/IPThermostat.active_profile":       candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.active_profile":       candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.active_profile":       candidateNotYetMeasured,
	"HmIPW-STH/IPThermostat.active_profile":         candidateNotYetMeasured,
	"HmIPW-STHD/IPThermostat.active_profile":        candidateNotYetMeasured,
	"HmIPW-WTH/IPThermostat.active_profile":         candidateNotYetMeasured,
	// IPThermostat.boost_mode — 19 device(s)
	"ALPHA-IP-RBG/IPThermostat.boost_mode":      candidateNotYetMeasured,
	"HmIP-BWTH/IPThermostat.boost_mode":         candidateNotYetMeasured,
	"HmIP-STH/IPThermostat.boost_mode":          candidateNotYetMeasured,
	"HmIP-STHD/IPThermostat.boost_mode":         candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.boost_mode":         candidateNotYetMeasured,
	"HmIP-WTH-1/IPThermostat.boost_mode":        candidateNotYetMeasured,
	"HmIP-WTH-2/IPThermostat.boost_mode":        candidateNotYetMeasured,
	"HmIP-eTRV-2 I9F/IPThermostat.boost_mode":   candidateNotYetMeasured,
	"HmIP-eTRV-2/IPThermostat.boost_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B-2 R4M/IPThermostat.boost_mode": candidateNotYetMeasured,
	"HmIP-eTRV-B/IPThermostat.boost_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B1/IPThermostat.boost_mode":      candidateNotYetMeasured,
	"HmIP-eTRV-C-2/IPThermostat.boost_mode":     candidateNotYetMeasured,
	"HmIP-eTRV-E/IPThermostat.boost_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.boost_mode":       candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.boost_mode":       candidateNotYetMeasured,
	"HmIPW-STH/IPThermostat.boost_mode":         candidateNotYetMeasured,
	"HmIPW-STHD/IPThermostat.boost_mode":        candidateNotYetMeasured,
	"HmIPW-WTH/IPThermostat.boost_mode":         candidateNotYetMeasured,
	// IPThermostat.concentration — 2 device(s)
	"HmIP-WGTC/IPThermostat.concentration":   candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.concentration": candidateNotYetMeasured,
	// IPThermostat.heating_cooling — 12 device(s)
	"ALPHA-IP-RBG/IPThermostat.heating_cooling": candidateNotYetMeasured,
	"HmIP-BWTH/IPThermostat.heating_cooling":    candidateNotYetMeasured,
	"HmIP-STH/IPThermostat.heating_cooling":     candidateNotYetMeasured,
	"HmIP-STHD/IPThermostat.heating_cooling":    candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.heating_cooling":    candidateNotYetMeasured,
	"HmIP-WTH-1/IPThermostat.heating_cooling":   candidateNotYetMeasured,
	"HmIP-WTH-2/IPThermostat.heating_cooling":   candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.heating_cooling":  candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.heating_cooling":  candidateNotYetMeasured,
	"HmIPW-STH/IPThermostat.heating_cooling":    candidateNotYetMeasured,
	"HmIPW-STHD/IPThermostat.heating_cooling":   candidateNotYetMeasured,
	"HmIPW-WTH/IPThermostat.heating_cooling":    candidateNotYetMeasured,
	// IPThermostat.level — 9 device(s)
	"HmIP-WGTC/IPThermostat.level":         candidateNotYetMeasured,
	"HmIP-eTRV-2 I9F/IPThermostat.level":   candidateNotYetMeasured,
	"HmIP-eTRV-2/IPThermostat.level":       candidateNotYetMeasured,
	"HmIP-eTRV-B-2 R4M/IPThermostat.level": candidateNotYetMeasured,
	"HmIP-eTRV-B/IPThermostat.level":       candidateNotYetMeasured,
	"HmIP-eTRV-B1/IPThermostat.level":      candidateNotYetMeasured,
	"HmIP-eTRV-C-2/IPThermostat.level":     candidateNotYetMeasured,
	"HmIP-eTRV-E/IPThermostat.level":       candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.level":       candidateNotYetMeasured,
	// IPThermostat.party_mode — 19 device(s)
	"ALPHA-IP-RBG/IPThermostat.party_mode":      candidateNotYetMeasured,
	"HmIP-BWTH/IPThermostat.party_mode":         candidateNotYetMeasured,
	"HmIP-STH/IPThermostat.party_mode":          candidateNotYetMeasured,
	"HmIP-STHD/IPThermostat.party_mode":         candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.party_mode":         candidateNotYetMeasured,
	"HmIP-WTH-1/IPThermostat.party_mode":        candidateNotYetMeasured,
	"HmIP-WTH-2/IPThermostat.party_mode":        candidateNotYetMeasured,
	"HmIP-eTRV-2 I9F/IPThermostat.party_mode":   candidateNotYetMeasured,
	"HmIP-eTRV-2/IPThermostat.party_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B-2 R4M/IPThermostat.party_mode": candidateNotYetMeasured,
	"HmIP-eTRV-B/IPThermostat.party_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B1/IPThermostat.party_mode":      candidateNotYetMeasured,
	"HmIP-eTRV-C-2/IPThermostat.party_mode":     candidateNotYetMeasured,
	"HmIP-eTRV-E/IPThermostat.party_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.party_mode":       candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.party_mode":       candidateNotYetMeasured,
	"HmIPW-STH/IPThermostat.party_mode":         candidateNotYetMeasured,
	"HmIPW-STHD/IPThermostat.party_mode":        candidateNotYetMeasured,
	"HmIPW-WTH/IPThermostat.party_mode":         candidateNotYetMeasured,
	// IPThermostat.set_point_mode — 19 device(s)
	"ALPHA-IP-RBG/IPThermostat.set_point_mode":      candidateNotYetMeasured,
	"HmIP-BWTH/IPThermostat.set_point_mode":         candidateNotYetMeasured,
	"HmIP-STH/IPThermostat.set_point_mode":          candidateNotYetMeasured,
	"HmIP-STHD/IPThermostat.set_point_mode":         candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.set_point_mode":         candidateNotYetMeasured,
	"HmIP-WTH-1/IPThermostat.set_point_mode":        candidateNotYetMeasured,
	"HmIP-WTH-2/IPThermostat.set_point_mode":        candidateNotYetMeasured,
	"HmIP-eTRV-2 I9F/IPThermostat.set_point_mode":   candidateNotYetMeasured,
	"HmIP-eTRV-2/IPThermostat.set_point_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B-2 R4M/IPThermostat.set_point_mode": candidateNotYetMeasured,
	"HmIP-eTRV-B/IPThermostat.set_point_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-B1/IPThermostat.set_point_mode":      candidateNotYetMeasured,
	"HmIP-eTRV-C-2/IPThermostat.set_point_mode":     candidateNotYetMeasured,
	"HmIP-eTRV-E/IPThermostat.set_point_mode":       candidateNotYetMeasured,
	"HmIP-eTRV-F/IPThermostat.set_point_mode":       candidateNotYetMeasured,
	"HmIPW-SCTHD/IPThermostat.set_point_mode":       candidateNotYetMeasured,
	"HmIPW-STH/IPThermostat.set_point_mode":         candidateNotYetMeasured,
	"HmIPW-STHD/IPThermostat.set_point_mode":        candidateNotYetMeasured,
	"HmIPW-WTH/IPThermostat.set_point_mode":         candidateNotYetMeasured,
	// IPThermostat.state — 2 device(s)
	"HmIP-BWTH/IPThermostat.state": candidateNotYetMeasured,
	"HmIP-WGTC/IPThermostat.state": candidateNotYetMeasured,
	// IPThermostatGroup.active_profile — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.active_profile": candidateNotYetMeasured,
	// IPThermostatGroup.boost_mode — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.boost_mode": candidateNotYetMeasured,
	// IPThermostatGroup.heating_cooling — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.heating_cooling": candidateNotYetMeasured,
	// IPThermostatGroup.level — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.level": candidateNotYetMeasured,
	// IPThermostatGroup.party_mode — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.party_mode": candidateNotYetMeasured,
	// IPThermostatGroup.set_point_mode — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.set_point_mode": candidateNotYetMeasured,
	// IPThermostatGroup.state — 1 device(s)
	"HmIP-HEATING/IPThermostatGroup.state": candidateNotYetMeasured,
	// RfThermostat.control_mode — 3 device(s)
	"HM-CC-RT-DN-BoM/RfThermostat.control_mode":  candidateNotYetMeasured,
	"HM-CC-RT-DN/RfThermostat.control_mode":      candidateNotYetMeasured,
	"HM-TC-IT-WM-W-EU/RfThermostat.control_mode": candidateNotYetMeasured,
	// RfThermostat.valve_state — 2 device(s)
	"HM-CC-RT-DN-BoM/RfThermostat.valve_state": candidateNotYetMeasured,
	"HM-CC-RT-DN/RfThermostat.valve_state":     candidateNotYetMeasured,
	// RfThermostatGroup.control_mode — 1 device(s)
	"HM-CC-VG-1/RfThermostatGroup.control_mode": candidateNotYetMeasured,
}

// TestEverySchemaFieldTheDeviceCarriesIsBound is the per-family companion to
// [TestEveryCustomDataPointFieldIsFilledBySomeDevice].
//
// That guard asks whether *some* device in the fleet fills a field, so a
// field filled by one family and dead in another passes it. That is exactly
// how three defects survived: HM-CC-TC had no temperature at all, HmIP-DLD
// never reported a jammed motor, and the classic RF jalousie actuators lost
// their tilt axis — each time because the custom data point reached for a
// fixed parameter name on its own channel while the profile named another
// parameter, another channel, or both.
//
// This guard asks the question per device instead: for every field the
// profile maps, if the device actually carries the parameter the schema
// resolves it to, the composing custom data point must hold a pointer to
// exactly that data point. It is derived from the schema rather than
// restating what the consumer does, so it cannot agree with the consumer by
// construction.
func TestEverySchemaFieldTheDeviceCarriesIsBound(t *testing.T) {
	srv := startMockCCUWithDevices(t, snapshotDevices(t))

	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "schema-binding-ccu"})
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("the fleet hydrated no devices at all — the walk is broken and this test would pass vacuously")
	}

	registry := custom.DefaultRegistry()
	unbound := map[string]string{} // key -> a concrete channel to look at
	checked := 0
	customDPs := 0

	for _, dev := range devices {
		for _, ch := range dev.Channels() {
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			customDPs++
			bound := boundDataPoints(cdp)
			for _, profile := range registry.GetConfigs(dev.Model) {
				if profile.Config == nil {
					continue
				}
				group := profile.Rebase(ch.GroupNumber())
				for field := range schemaFields(group) {
					target, param, ok := custom.ResolveFieldSlot(ch, group, field)
					if !ok || target == nil {
						continue // the schema does not map it onto a channel this device has
					}
					dp := target.Parameter(param)
					if dp == nil {
						continue // the device does not carry it there — nothing to bind
					}
					// A write-only parameter is addressed through the
					// channel's writer, not held as a pointer, so "is it
					// bound" is not a question about it. Asking it anyway is
					// what turned this walk's first run into 375 findings
					// that were all ON_TIME, RAMP_TIME, STOP and friends.
					if ops := dp.ParameterData().Operations; ops&hmenum.OperationsRead == 0 &&
						ops&hmenum.OperationsEvent == 0 {
						continue
					}
					id, ok := dataPointIdentity(dp)
					if !ok {
						continue
					}
					checked++
					if bound[id] {
						continue
					}
					key := dev.Model + "/" + string(profile.Name) + "." + string(field)
					if _, seen := unbound[key]; !seen {
						unbound[key] = target.Address + " " + string(param)
					}
				}
			}
		}
	}

	// Negative controls: a walk that inspects nothing reports everything as
	// bound, and so does one that resolves no field.
	if customDPs == 0 {
		t.Fatal("no channel in the fleet carries a custom data point — the walk is broken and this test " +
			"would pass vacuously")
	}
	if checked == 0 {
		t.Fatal("no schema field resolved onto a parameter any device carries — the walk is broken and " +
			"this test would pass vacuously")
	}
	t.Logf("checked %d schema-resolved slots across %d custom data points", checked, customDPs)

	keys := make([]string, 0, len(unbound))
	for k := range unbound {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, declared := unboundSchemaFields[key]; declared {
			continue
		}
		t.Errorf("%s: the profile resolves this field onto %s, the device carries it, and the custom "+
			"data point holds no pointer to it — bind it through custom.ResolveSlotOr, or declare in "+
			"unboundSchemaFields how it reaches its consumer instead", key, unbound[key])
	}
	for key, reason := range unboundSchemaFields {
		if _, still := unbound[key]; !still {
			t.Errorf("unboundSchemaFields lists %s (%q) but it is bound now — drop the entry so the list "+
				"keeps meaning what it says", key, reason)
		}
	}
}

// schemaFields returns every field the rebased group maps, from all four
// places the resolver looks.
func schemaFields(group custom.RebasedChannelGroupConfig) map[hmenum.Field]struct{} {
	out := map[hmenum.Field]struct{}{}
	for f := range group.Fields {
		out[f] = struct{}{}
	}
	for _, m := range group.ChannelFields {
		for f := range m {
			out[f] = struct{}{}
		}
	}
	for _, m := range group.FixedChannelFields {
		for f := range m {
			out[f] = struct{}{}
		}
	}
	return out
}

// boundDataPoints returns the identity of every data point the custom data
// point holds a pointer to.
//
// Identity rather than the parameter name: the channel hands out the very
// data point a correctly bound custom DP is holding, so comparing the two
// pointers answers "is this slot bound" without reading anything out of the
// data point. It also cannot be fooled by a second data point that happens to
// carry the same parameter name on another channel.
func boundDataPoints(cdp any) map[uintptr]bool {
	out := map[uintptr]bool{}
	v := reflect.ValueOf(cdp)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		collectBoundDataPoints(v, out)
	}
	return out
}

func collectBoundDataPoints(v reflect.Value, out map[uintptr]bool) {
	for i := range v.NumField() {
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Pointer:
			if fv.IsNil() {
				continue
			}
			out[fv.Pointer()] = true
			// An embedded custom data point (Blind embeds Cover) carries
			// slots of its own; they belong to the same composition.
			if v.Type().Field(i).Anonymous {
				if elem := fv.Elem(); elem.Kind() == reflect.Struct {
					collectBoundDataPoints(elem, out)
				}
			}
		case reflect.Interface:
			if fv.IsNil() {
				continue
			}
			inner := fv.Elem()
			if inner.Kind() == reflect.Pointer && !inner.IsNil() {
				out[inner.Pointer()] = true
			}
		case reflect.Struct:
			if v.Type().Field(i).Anonymous {
				collectBoundDataPoints(fv, out)
			}
		default:
			continue
		}
	}
}

// dataPointIdentity returns the pointer identity of a data point the channel
// handed out.
func dataPointIdentity(dp any) (uintptr, bool) {
	v := reflect.ValueOf(dp)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return 0, false
	}
	return v.Pointer(), true
}
