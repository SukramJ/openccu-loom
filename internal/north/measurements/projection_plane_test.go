// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/projection"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestProjectionAnswersWhatTheForwarderDoes puts one data point per rule
// on a channel, asks the plane, then fires a value for each and compares:
// a sample reaches the exporter exactly for the entries the plane calls
// published, and each withheld entry names its rule.
func TestProjectionAnswersWhatTheForwarderDoes(t *testing.T) {
	t.Parallel()
	const chanAddr = "DEV100:1"
	exp := &fakeExporter{}
	reg, u, dev, ch := centralWithDevice(t, "ccu-01", "DEV100", chanAddr)

	overlay, _ := newOverlay(t, nil, []string{"EXCLUDED*"})
	ctx := context.Background()
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", chanAddr, "SWITCHED_OFF", false, "test"); err != nil {
		t.Fatalf("override off: %v", err)
	}
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", chanAddr, "EXCLUDED_BUT_ON", true, "test"); err != nil {
		t.Fatalf("override on: %v", err)
	}

	live := func(param string) {
		dp := floatDP(chanAddr, param)
		ch.Put(dp)
		dp.OnEvent(1.5)
	}
	live("LIVE")
	live("EXCLUDED_BY_PATTERN")
	live("SWITCHED_OFF")
	live("EXCLUDED_BUT_ON")
	cached := floatDP(chanAddr, "CACHED")
	ch.Put(cached)
	cached.RestoreCachedValue(1.5, time.Now(), time.Now())
	ch.Put(floatDP(chanAddr, "UNOBSERVED"))
	state := boolDP(chanAddr, "STATE")
	ch.Put(state)
	state.OnEvent(true)

	want := map[string]WithheldReason{
		"LIVE":                "",
		"EXCLUDED_BUT_ON":     "",
		"EXCLUDED_BY_PATTERN": WithheldPatternExcludes,
		"SWITCHED_OFF":        WithheldOverrideOff,
		"CACHED":              WithheldNoLiveValue,
		"UNOBSERVED":          WithheldNoLiveValue,
		"STATE":               WithheldNotNumeric,
	}

	fwd := New(exp, Options{Exclude: []string{"EXCLUDED*"}, Overrides: overlay})
	view, err := NewProjectionPlane(fwd).Project(ctx, "ccu-01", nil, dev)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if view.State != projection.StateActive || len(view.Entries) != len(want) {
		t.Fatalf("view: state %s, %d entries; want active, %d", view.State, len(view.Entries), len(want))
	}
	published := map[string]bool{}
	for _, e := range view.Entries {
		reason, known := want[e.Key]
		if !known {
			t.Errorf("unexpected entry %s", e.Key)
			continue
		}
		if string(reason) != e.Reason || (reason == "") != (e.Verdict == projection.VerdictPublished) {
			t.Errorf("%s: %s / %q; want reason %q", e.Key, e.Verdict, e.Reason, reason)
		}
		if e.Verdict == projection.VerdictPublished {
			published[e.Key] = true
			if e.Identity[ProjectionIdentityChannel] != chanAddr || e.Identity[ProjectionIdentityParameter] != e.Key ||
				e.Identity[ProjectionIdentityCentral] != "ccu-01" || e.Identity[ProjectionIdentityInterfaceID] != "HmIP-RF" {
				t.Errorf("%s: identity %v", e.Key, e.Identity)
			}
		}
	}

	// The same data points, now through the event handler.
	stop := fwd.Wire(reg)
	for param := range want {
		v := hmtypes.FloatValue(1.5)
		if param == "STATE" {
			v = hmtypes.BoolValue(true)
		}
		publishValueEvent(u, chanAddr, param, hmenum.ParamsetKeyValues, v)
	}
	stop()
	forwarded := map[string]bool{}
	for _, s := range exp.got() {
		forwarded[s.Parameter] = true
	}
	for param := range want {
		if published[param] != forwarded[param] {
			t.Errorf("%s: the plane says published=%v, the forwarder forwarded=%v", param, published[param], forwarded[param])
		}
	}
	if len(forwarded) == 0 {
		t.Fatal("nothing was forwarded; the comparison would hold vacuously")
	}
}

func TestProjectionOfAForwarderThatIsOffOrSkipsTheCentral(t *testing.T) {
	t.Parallel()
	_, _, dev, _ := centralWithDevice(t, "ccu-01", "DEV101", "DEV101:1")
	ctx := context.Background()

	for name, plane := range map[string]*ProjectionPlane{
		"no forwarder": NewProjectionPlane(nil),
		"no exporter":  NewProjectionPlane(New(nil, Options{})),
	} {
		view, err := plane.Project(ctx, "ccu-01", nil, dev)
		if err != nil || view.State != projection.StateInactive || view.Reason != projection.ReasonPlaneDisabled {
			t.Errorf("%s: %+v, err %v; want inactive / %s", name, view, err, projection.ReasonPlaneDisabled)
		}
	}

	skips := New(&fakeExporter{}, Options{EnabledFor: func(string) bool { return false }})
	view, err := NewProjectionPlane(skips).Project(ctx, "ccu-01", nil, dev)
	if err != nil || view.State != projection.StateInactive || view.Reason != ReasonCentralExcluded {
		t.Errorf("skipped central: %+v, err %v; want inactive / %s", view, err, ReasonCentralExcluded)
	}
}
