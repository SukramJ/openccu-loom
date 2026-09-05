// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
)

// validEndpointConfig is the minimal config [endpoint.New] accepts.
func validEndpointConfig() endpoint.Config {
	return endpoint.Config{
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "TestBridge",
	}
}

func TestConfigValidate_ZeroVendorID(t *testing.T) {
	t.Parallel()
	cfg := validEndpointConfig()
	cfg.VendorID = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero VendorID, got nil")
	}
}

func TestConfigValidate_ZeroProductID(t *testing.T) {
	t.Parallel()
	cfg := validEndpointConfig()
	cfg.ProductID = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero ProductID, got nil")
	}
}

func TestConfigValidate_EmptyNodeLabel(t *testing.T) {
	t.Parallel()
	for _, lbl := range []string{"", "   ", "\t"} {
		t.Run("label="+lbl, func(t *testing.T) {
			t.Parallel()
			cfg := validEndpointConfig()
			cfg.NodeLabel = lbl
			if err := cfg.Validate(); err == nil {
				t.Error("expected error for empty NodeLabel, got nil")
			}
		})
	}
}

func TestConfigValidate_OK(t *testing.T) {
	t.Parallel()
	if err := validEndpointConfig().Validate(); err != nil {
		t.Errorf("expected nil for valid config, got %v", err)
	}
}
