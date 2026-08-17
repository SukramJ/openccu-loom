// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestSysvarMetadataConcurrentRefreshAndRead is the race tripwire for the hub
// scan rewriting a sysvar's descriptor in place while north-bound surfaces read
// it. The refresh calls [Sysvar.ApplyMeta] on every pass; the REST /sysvars
// summary, the MQTT discovery fan-out, the payload projections and the typed
// wrappers snapshot through [Sysvar.Meta] and the guarded accessors on other
// goroutines. Field-by-field reads against the in-place rewrite are torn
// string / slice-header reads. Direct field access would be a data race; every
// reader must funnel through the guarded snapshot. Run with -race.
func TestSysvarMetadataConcurrentRefreshAndRead(t *testing.T) {
	t.Parallel()

	pv := func(v hmtypes.ParamValue) *hmtypes.ParamValue { return &v }
	sv := NewSysvar("race-central", "MyVar", "desc", hmenum.HubValueTypeList, &countingSysvarWriter{})
	ctx := context.Background()

	// Two descriptors of different shape and string length, so a torn read
	// straddling a refresh is observable rather than accidentally consistent.
	metas := []SysvarMeta{
		{
			Unit:           "C",
			ValueType:      hmenum.HubValueTypeList,
			ValueList:      []string{"low", "mid", "high"},
			Description:    "first description",
			EnabledDefault: true,
			IsExtended:     true,
			IsVisible:      true,
			ValueName0:     "closed",
			ValueName1:     "open",
			Min:            pv(hmtypes.FloatValue(0)),
			Max:            pv(hmtypes.FloatValue(100)),
			Vid:            1234,
		},
		{
			Unit:        "percent",
			ValueType:   hmenum.HubValueTypeFloat,
			Description: "second description that is a different length",
			IsLogged:    true,
			Min:         pv(hmtypes.FloatValue(-40)),
			Max:         pv(hmtypes.FloatValue(40)),
			Vid:         9999,
		},
	}

	var wg sync.WaitGroup

	// Writer: the hub scan rewrites the whole descriptor on every pass.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 3000 {
			sv.ApplyMeta(metas[i%len(metas)])
		}
	}()

	// Readers: the north-bound surfaces and typed wrappers that read the
	// descriptor while the refresh rewrites it.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3000 {
				m := sv.Meta()
				_ = m.Unit + m.Description + m.ValueName0 + m.ValueName1
				_ = len(m.ValueList)
				_, _ = m.Min, m.Max
				_ = strconv.Itoa(m.Vid)
				_ = sv.EnabledByDefault()
				_ = sv.Extended()
				_ = sv.PathData()
				_ = sv.Info()
				_ = sv.State()
				_ = sv.Config()
				_ = WrapSysvar(sv)
				_, _ = (&SysvarDpSensor{Sysvar: sv}).SensorValue()
				// Exercise the write-coercion path (toWire / resolveListIndex),
				// which snapshots ValueType and ValueList under the same lock.
				_ = sv.Set(ctx, hmtypes.IntValue(1))
			}
		}()
	}
	wg.Wait()
}

// TestSysvarNameConcurrentRenameAndErrorFormatting is the race tripwire for
// [Sysvar.SetName] rewriting the name in place (an operator renames the
// system variable) while error-formatting paths read it — toWire's
// value-rejection branches, SetTextValue's length guard, and
// SysvarDpNumber.SendVariable's range guard all interpolate the sysvar's
// name into the returned error. Every one of those sites must read
// through [HubDataPoint.LegacyName] (which takes the data point's own
// lock, the same one SetName writes under) rather than the bare Name
// field — a direct field read races the rename's string-header write. Run
// with -race.
func TestSysvarNameConcurrentRenameAndErrorFormatting(t *testing.T) {
	t.Parallel()

	sv := NewSysvar("race-central", "InitialName", "desc", hmenum.HubValueTypeFloat, &countingSysvarWriter{})
	minVal := hmtypes.FloatValue(0)
	maxVal := hmtypes.FloatValue(10)
	sv.ApplyMeta(SysvarMeta{
		ValueType: hmenum.HubValueTypeFloat,
		Min:       &minVal,
		Max:       &maxVal,
	})
	text := &SysvarDpText{Sysvar: sv, MaxLength: 2}
	number := &SysvarDpNumber{Sysvar: sv}
	ctx := context.Background()

	var wg sync.WaitGroup

	// Writer: an operator renames the sysvar on the CCU, rewriting the name
	// in place on every pass.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 3000 {
			sv.SetName("RenamedVar" + strconv.Itoa(i%7))
		}
	}()

	// Readers: trip every error-formatting site that interpolates the name.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3000 {
				_ = sv.Set(ctx, hmtypes.ParamValue{Kind: hmtypes.ValueKindNone})
				_ = text.SetTextValue(ctx, "too long for MaxLength")
				_ = number.SendVariable(ctx, 999.0) // out of the configured [0,10] range
			}
		}()
	}
	wg.Wait()
}
