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

// unboundSchemaFields declares a schema-resolved slot the composing custom
// data point does not hold a pointer to, together with how the value reaches
// its consumer instead.
//
// Two key forms are accepted. "<model>/<profile>.<field>" pins one device
// family; "<profile>.<field>" covers every model the profile serves. The
// broad form exists because a field consumed through Subscribe is consumed
// that way for all of them, and writing the same sentence once per model
// would bury the one entry that means something else.
//
// An entry is not an exemption from the rule. It is a measured statement:
// each was decided by feeding the parameter on the channel the schema
// resolves it to and reading the accessor back, never by reading the code.
// Anything not listed and not bound is the defect this guard exists to
// catch — the schema says the device carries the parameter, the device does
// carry it, and the custom data point looked somewhere else. Five defects of
// exactly that shape were found and fixed while this list was being
// measured.

// The reasons a schema-resolved slot is legitimately not held as a pointer.
const (
	// consumedThroughSubscribe: the value arrives through an OnAnyUpdate
	// subscription and lands in a plain field, so there is no data-point
	// pointer for the reflection walk to find. Proven per field in
	// internal/model/custom/climate/schema_field_binding_test.go.
	consumedThroughSubscribe = "consumed through Subscribe, not held as a data-point pointer"

	// reachedThroughActivityStateChannels: the profile names relay-state
	// channels at offsets the custom DP resolves separately, outside the
	// composed-field path.
	reachedThroughActivityStateChannels = "bound via Config.ActivityStateChannels, outside the composed-field path"

	// promotedAsAStandaloneDataPoint: not composed at all — the profile
	// marks the standalone sensor visible and it reaches consumers as its
	// own data point. Matches the reference stack, whose climate model has
	// no concentration field either.
	promotedAsAStandaloneDataPoint = "not composed: the profile marks the standalone sensor visible"
)

var unboundSchemaFields = map[string]string{
	"IPThermostat.set_point_mode":       consumedThroughSubscribe,
	"IPThermostat.party_mode":           consumedThroughSubscribe,
	"IPThermostat.boost_mode":           consumedThroughSubscribe,
	"IPThermostat.active_profile":       consumedThroughSubscribe,
	"IPThermostat.heating_cooling":      consumedThroughSubscribe,
	"IPThermostat.level":                consumedThroughSubscribe,
	"IPThermostatGroup.set_point_mode":  consumedThroughSubscribe,
	"IPThermostatGroup.party_mode":      consumedThroughSubscribe,
	"IPThermostatGroup.boost_mode":      consumedThroughSubscribe,
	"IPThermostatGroup.active_profile":  consumedThroughSubscribe,
	"IPThermostatGroup.heating_cooling": consumedThroughSubscribe,
	"IPThermostatGroup.level":           consumedThroughSubscribe,
	"RfThermostat.control_mode":         consumedThroughSubscribe,
	"RfThermostat.valve_state":          consumedThroughSubscribe,
	"RfThermostatGroup.control_mode":    consumedThroughSubscribe,

	"IPThermostat.state":      reachedThroughActivityStateChannels,
	"IPThermostatGroup.state": reachedThroughActivityStateChannels,

	"IPThermostat.concentration": promotedAsAStandaloneDataPoint,
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
	unbound := map[string]string{}        // key -> a concrete channel to look at
	usedDeclarations := map[string]bool{} // which entries the walk reached
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
				// A device can register several profiles covering different
				// channels — an HmIP-WGTC carries IPSwitch and IPDimmer at
				// once. Applying every profile to every custom-DP channel
				// asks a switch to bind a slot that belongs to a light and
				// reports the miss as a defect. Scope each profile to the
				// channels it claims, the way the materializer does.
				if !profileClaimsChannel(profile, ch.Number) {
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
					broad := string(profile.Name) + "." + string(field)
					narrow := dev.Model + "/" + broad
					if _, declared := unboundSchemaFields[broad]; declared {
						usedDeclarations[broad] = true
						continue
					}
					if _, declared := unboundSchemaFields[narrow]; declared {
						usedDeclarations[narrow] = true
						continue
					}
					if _, seen := unbound[narrow]; !seen {
						unbound[narrow] = target.Address + " " + string(param)
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
		t.Errorf("%s: the profile resolves this field onto %s, the device carries it, and the custom "+
			"data point holds no pointer to it — bind it through custom.ResolveSlotOr, or declare in "+
			"unboundSchemaFields how it reaches its consumer instead", key, unbound[key])
	}
	for key, reason := range unboundSchemaFields {
		if !usedDeclarations[key] {
			t.Errorf("unboundSchemaFields lists %s (%q) but nothing reached it — the slot is bound now, or "+
				"the key matches no device any more; drop the entry so the list keeps meaning what it says",
				key, reason)
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

// profileClaimsChannel reports whether profile covers channel number chNo,
// mirroring the materializer's relevantChannels: the primary channel of each
// declared base, plus its secondary offsets.
func profileClaimsChannel(profile custom.Profile, chNo int) bool {
	if profile.Config == nil {
		return false
	}
	cg := profile.Config.ChannelGroup
	for _, base := range profile.Channels {
		if cg.PrimaryChannelSet && base.Channel+cg.PrimaryChannel == chNo {
			return true
		}
		for _, sec := range cg.SecondaryChannels {
			if base.Channel+sec == chNo {
				return true
			}
		}
	}
	return false
}
