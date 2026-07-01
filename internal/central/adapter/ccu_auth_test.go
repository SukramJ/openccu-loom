// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// stubResolver builds a CentralConfigResolver backed by a fixed name→config
// map plus an optional "default" entry returned for an empty name — this
// mirrors the shape of the production resolver without touching SQLite.
func stubResolver(byName map[string]config.CentralConfig, defaultName string) CentralConfigResolver {
	return func(_ context.Context, name string) (config.CentralConfig, bool) {
		if name == "" {
			name = defaultName
		}
		cc, ok := byName[name]
		return cc, ok
	}
}

// TestCCUAuthDomainCentralConfigRouting verifies the three resolution
// outcomes centralConfig must produce: a named hit, an empty name
// selecting the resolver's notion of "default", and a miss.
func TestCCUAuthDomainCentralConfigRouting(t *testing.T) {
	t.Parallel()

	byName := map[string]config.CentralConfig{
		"ccu1": {Name: "ccu1", Host: "192.0.2.1"},
		"ccu2": {Name: "ccu2", Host: "192.0.2.2"},
	}
	d := NewCCUAuthDomain(nil, stubResolver(byName, "ccu1"), nil)

	t.Run("named_hit", func(t *testing.T) {
		t.Parallel()
		cc, ok := d.centralConfig(context.Background(), "ccu2")
		if !ok || cc.Host != "192.0.2.2" {
			t.Fatalf("expected ccu2 hit, got %+v ok=%v", cc, ok)
		}
	})

	t.Run("empty_selects_default", func(t *testing.T) {
		t.Parallel()
		cc, ok := d.centralConfig(context.Background(), "")
		if !ok || cc.Name != "ccu1" {
			t.Fatalf("expected default ccu1, got %+v ok=%v", cc, ok)
		}
	})

	t.Run("miss", func(t *testing.T) {
		t.Parallel()
		_, ok := d.centralConfig(context.Background(), "unknown")
		if ok {
			t.Fatal("expected miss (ok=false) for unknown central")
		}
	})
}

// TestCCUAuthDomainCentralConfigNilResolver verifies a nil resolver fails
// closed rather than panicking.
func TestCCUAuthDomainCentralConfigNilResolver(t *testing.T) {
	t.Parallel()
	d := NewCCUAuthDomain(nil, nil, nil)
	_, ok := d.centralConfig(context.Background(), "ccu1")
	if ok {
		t.Fatal("expected nil resolver to fail closed (ok=false)")
	}
}

// TestCCUAuthDomainRuntimeAdoptedCentralResolves verifies that a resolver
// returning a central absent from any boot-time snapshot (simulating a
// runtime-adopted central backed by the SQLite centrals store) is usable
// by ValidateCredentials/UserLevel's centralConfig lookup — the point of
// PR4's store-backed resolver migration.
func TestCCUAuthDomainRuntimeAdoptedCentralResolves(t *testing.T) {
	t.Parallel()

	runtimeOnly := config.CentralConfig{Name: "runtime-ccu", Host: "192.0.2.50"}
	resolve := func(_ context.Context, name string) (config.CentralConfig, bool) {
		if name == "runtime-ccu" {
			return runtimeOnly, true
		}
		return config.CentralConfig{}, false
	}
	d := NewCCUAuthDomain(nil, resolve, nil)

	cc, ok := d.centralConfig(context.Background(), "runtime-ccu")
	if !ok {
		t.Fatal("expected runtime-adopted central to resolve")
	}
	if cc.Host != "192.0.2.50" {
		t.Errorf("expected host 192.0.2.50, got %q", cc.Host)
	}
}

// TestCCUAuthDomainValidateCredentialsNotFound verifies ValidateCredentials
// returns ErrCCUAuthCentralNotFound (without attempting any network call)
// when the resolver cannot find the named central — the fail-closed path.
func TestCCUAuthDomainValidateCredentialsNotFound(t *testing.T) {
	t.Parallel()
	d := NewCCUAuthDomain(nil, stubResolver(nil, ""), nil)

	err := d.ValidateCredentials(context.Background(), "missing", "user", "pass")
	if !errors.Is(err, ErrCCUAuthCentralNotFound) {
		t.Fatalf("expected ErrCCUAuthCentralNotFound, got %v", err)
	}
}

// TestCCUAuthDomainUserLevelNotFound verifies UserLevel fails closed with
// ErrCCUAuthCentralNotFound (and level -1) when the resolver misses.
func TestCCUAuthDomainUserLevelNotFound(t *testing.T) {
	t.Parallel()
	d := NewCCUAuthDomain(central.NewRegistry(), stubResolver(nil, ""), nil)

	level, err := d.UserLevel(context.Background(), "missing", "user")
	if !errors.Is(err, ErrCCUAuthCentralNotFound) {
		t.Fatalf("expected ErrCCUAuthCentralNotFound, got %v", err)
	}
	if level != -1 {
		t.Errorf("expected level -1, got %d", level)
	}
}

// TestCCUAuthDomainUserLevelNoLiveUnit verifies that a resolvable
// central config with no matching live registry entry still fails
// closed with ErrCCUAuthCentralNotFound rather than panicking.
func TestCCUAuthDomainUserLevelNoLiveUnit(t *testing.T) {
	t.Parallel()
	byName := map[string]config.CentralConfig{"ccu1": {Name: "ccu1", Host: "192.0.2.1"}}
	d := NewCCUAuthDomain(central.NewRegistry(), stubResolver(byName, "ccu1"), nil)

	level, err := d.UserLevel(context.Background(), "ccu1", "user")
	if !errors.Is(err, ErrCCUAuthCentralNotFound) {
		t.Fatalf("expected ErrCCUAuthCentralNotFound, got %v", err)
	}
	if level != -1 {
		t.Errorf("expected level -1, got %d", level)
	}
}
