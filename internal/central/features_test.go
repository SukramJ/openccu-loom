// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func TestFeaturesZeroValueIsNotReady(t *testing.T) {
	t.Parallel()
	var f Features
	if f.Known() {
		t.Error("zero Features reports Known")
	}
	s := f.State(hmenum.FeatureHubSysvars)
	if s.Available || s.Reason != hmenum.FeatureReasonNotReady {
		t.Errorf("zero Features state = %+v, want unavailable/not_ready", s)
	}
}

func TestFeaturesUnlistedKeyIsNotSupported(t *testing.T) {
	t.Parallel()
	f := NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]FeatureState{
		hmenum.FeatureHubSysvars: {Available: true},
	})
	if !f.Available(hmenum.FeatureHubSysvars) {
		t.Error("listed feature not available")
	}
	if s := f.State(hmenum.FeatureTaxonomyTree); s.Available || s.Reason != hmenum.FeatureReasonNotSupported {
		t.Errorf("unlisted feature state = %+v, want not_supported_by_system", s)
	}
}

func TestFeaturesRequireWrapsTheLegacyError(t *testing.T) {
	t.Parallel()
	legacy := errors.New("unsupported")
	f := NewFeatures(hmenum.SystemTypeOpenCCULite, map[hmenum.Feature]FeatureState{
		hmenum.FeatureSystemReboot: {Reason: hmenum.FeatureReasonMissingScope, Scope: "power"},
	})
	err := f.Require("lite", hmenum.FeatureSystemReboot, legacy)
	var fe *hmerr.FeatureUnavailableError
	if !errors.As(err, &fe) {
		t.Fatalf("Require = %v, want a FeatureUnavailableError", err)
	}
	if fe.Scope != "power" || fe.Reason != hmenum.FeatureReasonMissingScope || fe.Central != "lite" {
		t.Errorf("refusal = %+v", fe)
	}
	if !errors.Is(err, legacy) {
		t.Error("Require lost the legacy error")
	}
	if err := f.Require("lite", hmenum.FeatureSystemReboot, nil); err == nil {
		t.Error("Require returned nil for an unavailable feature")
	}
	ok := NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]FeatureState{hmenum.FeatureSystemReboot: {Available: true}})
	if err := ok.Require("ccu", hmenum.FeatureSystemReboot, legacy); err != nil {
		t.Errorf("Require for an available feature = %v, want nil", err)
	}
}

func TestSetFeaturesPublishesOnlyOnChange(t *testing.T) {
	t.Parallel()
	u, err := New(Config{Name: "feat"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var got int
	unsub := u.EventBus.Subscribe(func(e hmevent.CentralFeaturesChangedEvent) {
		if e.CentralName == "feat" {
			got++
		}
	})
	defer unsub()

	set := NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]FeatureState{hmenum.FeatureHubSysvars: {Available: true}})
	u.SetFeatures(set)
	u.SetFeatures(NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]FeatureState{hmenum.FeatureHubSysvars: {Available: true}}))
	if got != 1 {
		t.Fatalf("events after an unchanged re-set = %d, want 1", got)
	}
	u.SetFeatures(NewFeatures(hmenum.SystemTypeCCU, map[hmenum.Feature]FeatureState{hmenum.FeatureHubSysvars: {Reason: hmenum.FeatureReasonNotSupported}}))
	if got != 2 {
		t.Fatalf("events after a change = %d, want 2", got)
	}
	if u.Features().Available(hmenum.FeatureHubSysvars) {
		t.Error("Features() does not return the latest set")
	}
}
