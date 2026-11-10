// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/go-fabric/mdns"
	"github.com/SukramJ/go-fabric/node"
)

func TestLogMatterMDNSInterfaces_WritesEachInterfaceFinding(t *testing.T) {
	t.Parallel()

	findings := []node.MDNSFinding{
		{Severity: mdns.SeverityError, Code: "interface_not_found", Message: "eth9 does not exist"},
		{Severity: mdns.SeverityWarning, Code: "no_operational_records", Message: "not an interface finding"},
		{Severity: mdns.SeverityWarning, Code: "interface_filtered", Message: "docker0 is a container bridge"},
		{Severity: mdns.SeverityWarning, Code: "interface_no_addresses", Message: "eth1 has no address"},
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logMatterMDNSInterfaces(func() []node.MDNSFinding { return findings }, []string{"eth9", "docker0", "eth1"}, logger)

	type line struct {
		Level, Msg, Code, Severity, Message string
	}
	raws := strings.Split(strings.TrimSpace(buf.String()), "\n")
	got := make([]line, 0, len(raws))
	for _, raw := range raws {
		var l line
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		got = append(got, l)
	}
	want := []line{
		{"WARN", "matter.bridge.mdns.interface", "interface_not_found", "error", "eth9 does not exist"},
		{"WARN", "matter.bridge.mdns.interface", "interface_filtered", "warning", "docker0 is a container bridge"},
		{"WARN", "matter.bridge.mdns.interface", "interface_no_addresses", "warning", "eth1 has no address"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLogMatterMDNSInterfaces_WithoutTheListDiagnosesNothing(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	called := false
	logMatterMDNSInterfaces(func() []node.MDNSFinding {
		called = true
		return []node.MDNSFinding{{Severity: mdns.SeverityError, Code: "interface_not_found"}}
	}, nil, logger)

	if called {
		t.Error("diagnosed without an interface list")
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q without an interface list", buf.String())
	}
}
