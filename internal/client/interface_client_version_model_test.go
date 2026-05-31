// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for InterfaceClient.GetVersion / SetVersion / Model properties.

package client

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func newVersionModelClient(t *testing.T, version string, kind backends.Kind) *InterfaceClient {
	t.Helper()
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		Version:     version,
		BackendKind: kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ---------------------------------------------------------------------------
// GetVersion / SetVersion
// ---------------------------------------------------------------------------

func TestGetVersionReturnsConfiguredValue(t *testing.T) {
	c := newVersionModelClient(t, "3.65.10", backends.KindCCU)
	if got := c.GetVersion(); got != "3.65.10" {
		t.Errorf("GetVersion() = %q; want %q", got, "3.65.10")
	}
}

func TestGetVersionDefaultsToEmpty(t *testing.T) {
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.GetVersion(); got != "" {
		t.Errorf("GetVersion() = %q; want empty string for uninitialised client", got)
	}
}

func TestSetVersionUpdatesValue(t *testing.T) {
	c := newVersionModelClient(t, "", backends.KindCCU)
	c.SetVersion("4.0.0.0")
	if got := c.GetVersion(); got != "4.0.0.0" {
		t.Errorf("GetVersion() after SetVersion = %q; want %q", got, "4.0.0.0")
	}
}

func TestSetVersionOverwritesPrevious(t *testing.T) {
	c := newVersionModelClient(t, "3.65.10", backends.KindCCU)
	c.SetVersion("3.79.5")
	if got := c.GetVersion(); got != "3.79.5" {
		t.Errorf("GetVersion() after overwrite = %q; want %q", got, "3.79.5")
	}
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

func TestModelCCU(t *testing.T) {
	c := newVersionModelClient(t, "", backends.KindCCU)
	if got := c.Model(); got != "ccu" {
		t.Errorf("Model() for KindCCU = %q; want %q", got, "ccu")
	}
}

func TestModelHomegear(t *testing.T) {
	c := newVersionModelClient(t, "", backends.KindHomegear)
	if got := c.Model(); got != "homegear" {
		t.Errorf("Model() for KindHomegear = %q; want %q", got, "homegear")
	}
}

func TestModelCUxD(t *testing.T) {
	c := newVersionModelClient(t, "", backends.KindCUxD)
	if got := c.Model(); got != "cuxd" {
		t.Errorf("Model() for KindCUxD = %q; want %q", got, "cuxd")
	}
}

func TestModelDefaultsToKindCCUWhenBackendKindNotSet(t *testing.T) {
	c, err := New(Config{
		CentralName: "test-central",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The zero value of backends.Kind is KindCCU (iota = 0), so Model()
	// should return "ccu" when BackendKind is left unset.
	if got := c.Model(); got != "ccu" {
		t.Errorf("Model() for zero-value BackendKind = %q; want %q", got, "ccu")
	}
}
