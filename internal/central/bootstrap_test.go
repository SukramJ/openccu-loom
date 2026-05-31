// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

func TestBootstrapBuildsEveryCentral(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu-01", Host: "h1"},
		{Name: "ccu-02", Host: "h2"},
	}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer teardown()
	if got := reg.Names(); len(got) != 2 || got[0] != "ccu-01" || got[1] != "ccu-02" {
		t.Fatalf("names=%+v", got)
	}
}

func TestBootstrapDetectsDuplicateName(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu", Host: "h1"},
		{Name: "ccu", Host: "h2"},
	}}
	_, _, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryStartStopAll(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "a", Host: "h"}}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	teardown()
}

func TestQueryFacadeDelegates(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "q", Host: "h"}}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer teardown()
	c, _ := reg.Get("q")
	qf := c.QueryFacade()
	if qf.Name() != "q" || qf.DeviceCount() != 0 {
		t.Fatalf("facade=%+v", qf)
	}
}
