// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
)

// TestMessageDisplayNameSeparatorEdges pins the "." split without catalogs.
func TestMessageDisplayNameSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a.b.CODE": "CODE",
		"CODE":     "CODE",
		"x.":       "",
		".C":       "C",
	} {
		if got := messageDisplayName(nil, "en", in); got != want {
			t.Errorf("messageDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestServiceMessageParameterSeparatorEdges pins the "." split edges.
func TestServiceMessageParameterSeparatorEdges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"AL-000A:1.LOWBAT": "LOWBAT",
		"AL-000A:1":        "",
		"x.":               "",
		".P":               "P",
		"a.b.P":            "P",
	} {
		if got := serviceMessageParameter(in); got != want {
			t.Errorf("serviceMessageParameter(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveChannelDeviceAddressSeparatorEdges pins which device address
// resolveChannel derives from the channel address; the error names it.
func TestResolveChannelDeviceAddressSeparatorEdges(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-sep"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	s := NewMQTTCommandSink(reg, nil)
	for in, want := range map[string]string{
		"000A:1": "000A",
		"000A":   "000A",
		":1":     ":1",
		"000A:":  "000A",
		"A:B:1":  "A:B",
	} {
		_, err := s.resolveChannel("ccu-sep", "", in)
		if err == nil || !strings.Contains(err.Error(), "unknown device "+strconv.Quote(want)) {
			t.Errorf("resolveChannel(%q) error = %v, want unknown device %q", in, err, want)
		}
	}
}
