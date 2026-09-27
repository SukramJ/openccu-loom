// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestRegistryFeatureUnavailable pins the registry's feature lookup: an
// absent feature is refused with its reason, an offered one and an
// unknown central are not.
func TestRegistryFeatureUnavailable(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	u, err := New(Config{Name: "box"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("Register: %v", err)
	}
	u.SetFeatures(NewFeatures(hmenum.SystemTypeOpenCCULite, map[hmenum.Feature]FeatureState{
		hmenum.FeatureHubSysvars:   {Reason: hmenum.FeatureReasonNotSupported},
		hmenum.FeatureDeviceRename: {Available: true},
	}))
	fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](reg.FeatureUnavailable("box", hmenum.FeatureHubSysvars))
	if !ok || fe.Reason != hmenum.FeatureReasonNotSupported || fe.Central != "box" {
		t.Errorf("hub.sysvars: %v, want refused not_supported_by_system", fe)
	}
	if err := reg.FeatureUnavailable("box", hmenum.FeatureDeviceRename); err != nil {
		t.Errorf("device.rename: %v, want nil", err)
	}
	if err := reg.FeatureUnavailable("ghost", hmenum.FeatureHubSysvars); err != nil {
		t.Errorf("unknown central: %v, want nil", err)
	}
}
