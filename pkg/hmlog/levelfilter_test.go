// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestSubsystemOverrideRaisesVerbosityOnTheStack is the operator's flow
// end to end: raise one subsystem to debug, get that subsystem's debug
// records, and get nothing extra from anywhere else.
//
// This is the whole point of the level registry — a CCU problem is
// diagnosed by turning up the transport, not the whole daemon — and it is
// invisible when broken: the endpoint reports success, the override is
// listed as active, and the output simply never changes.
func TestSubsystemOverrideRaisesVerbosityOnTheStack(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)
	stack.Levels.Set("openccu-loom.client", slog.LevelDebug, 0)

	// The override is inherited by descendants, so the transport below it
	// is raised as well.
	stack.Named("openccu-loom.client.transport.xmlrpc").Debug("xmlrpc.request")
	// A sibling subsystem keeps the default level.
	stack.Named("openccu-loom.north.mqtt").Debug("mqtt.publish")
	// So does the root logger.
	stack.Logger.Debug("daemon.tick")
	// Everything at or above the default level still comes through.
	stack.Named("openccu-loom.north.mqtt").Warn("mqtt.reconnect")

	out := buf.String()
	if !strings.Contains(out, "xmlrpc.request") {
		t.Errorf("raised subsystem produced no output — the override is inert:\n%s", out)
	}
	if strings.Contains(out, "mqtt.publish") {
		t.Errorf("a subsystem without an override was raised too:\n%s", out)
	}
	if strings.Contains(out, "daemon.tick") {
		t.Errorf("the root logger was raised by a subsystem override:\n%s", out)
	}
	if !strings.Contains(out, "mqtt.reconnect") {
		t.Errorf("a record at the default level was dropped:\n%s", out)
	}
}

// TestSubsystemOverrideReachesCaptureAndLiveLog pins that the raised
// records reach the operator-facing artefacts too. A capture requested
// with per-subsystem debug exists precisely to put that detail in the
// archive; an override that only reaches stdout would produce an archive
// without the thing it was requested for.
func TestSubsystemOverrideReachesCaptureAndLiveLog(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)
	sink := NewCaptureSink(0, false)
	stack.Tee.Attach(sink)
	stack.Levels.Set("openccu-loom.client", slog.LevelDebug, 0)

	stack.Named("openccu-loom.client").Debug("client.wire_read")

	if got := string(sink.Snapshot()); !strings.Contains(got, "client.wire_read") {
		t.Errorf("capture sink missed the raised record: %q", got)
	}
	entries := stack.Live.Snapshot(10, slog.LevelDebug)
	if !containsMessage(entries, "client.wire_read") {
		t.Errorf("live log missed the raised record: %+v", entries)
	}
}

// TestSubsystemOverrideLowersVerbosity pins the other direction: an
// override below the default silences that subsystem alone. Without it a
// noisy subsystem can only be quietened by lowering the whole daemon.
func TestSubsystemOverrideLowersVerbosity(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelDebug)
	stack.Levels.Set("openccu-loom.north.mqtt", slog.LevelError, 0)

	stack.Named("openccu-loom.north.mqtt").Info("mqtt.publish")
	stack.Named("openccu-loom.north.mqtt").Error("mqtt.publish_failed")
	stack.Named("openccu-loom.client").Info("client.call")

	out := buf.String()
	if strings.Contains(out, "mqtt.publish\"") {
		t.Errorf("a lowered subsystem still logged below its level:\n%s", out)
	}
	if !strings.Contains(out, "mqtt.publish_failed") {
		t.Errorf("a lowered subsystem dropped a record at its own level:\n%s", out)
	}
	if !strings.Contains(out, "client.call") {
		t.Errorf("lowering one subsystem lowered another:\n%s", out)
	}
}

// TestSubsystemPathIsDerivedFromTheCallSite pins that a subsystem does not
// have to name itself to be addressable. Every call site in the daemon
// logs through the root logger or slog.Default(); if the registry only
// applied to loggers somebody remembered to name, the overrides an
// operator can actually set would cover almost nothing.
func TestSubsystemPathIsDerivedFromTheCallSite(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)

	// This test compiles into pkg/hmlog, so its own call site resolves to
	// the path below.
	stack.Levels.Set("openccu-loom.hmlog", slog.LevelDebug, 0)
	stack.Logger.Debug("hmlog.derived_path")

	if out := buf.String(); !strings.Contains(out, "hmlog.derived_path") {
		t.Errorf("call-site derived path did not pick up its override:\n%s", out)
	}
}

// TestNoOverridesLeavesTheDefaultLevelInCharge guards the common case: a
// daemon without a single override must behave exactly as before, with no
// record slipping past the configured default.
func TestNoOverridesLeavesTheDefaultLevelInCharge(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)

	stack.Logger.Debug("root.debug")
	stack.Named("openccu-loom.client").Debug("client.debug")
	stack.Logger.Info("root.info")

	out := buf.String()
	if strings.Contains(out, "debug") {
		t.Errorf("a debug record escaped the configured default level:\n%s", out)
	}
	if !strings.Contains(out, "root.info") {
		t.Errorf("an info record was dropped at the default level:\n%s", out)
	}
}

// TestSubsystemPathMapsThePackageTree pins the path space the diagnostics
// endpoint documents. Operators type these paths by hand, so the mapping
// from a package to its subsystem path is part of the contract.
func TestSubsystemPathMapsThePackageTree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fn   string
		want string
	}{
		{
			fn:   "github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc.(*Client).Call",
			want: "openccu-loom.client.transport.xmlrpc",
		},
		{
			fn:   "github.com/SukramJ/openccu-loom/internal/north/matter.(*Bridge).Start",
			want: "openccu-loom.north.matter",
		},
		{
			fn:   "github.com/SukramJ/openccu-loom/pkg/hmlog.BuildFullStack",
			want: "openccu-loom.hmlog",
		},
		{
			fn:   "github.com/SukramJ/openccu-loom/cmd/openccu-loom.(*daemon).run",
			want: "openccu-loom.cmd.openccu-loom",
		},
		{
			// A closure inside a method keeps its package.
			fn:   "github.com/SukramJ/openccu-loom/internal/central.(*Unit).Start.func1",
			want: "openccu-loom.central",
		},
		{
			// Outside the module there is no subsystem to address; the
			// registry default applies.
			fn:   "github.com/lmittmann/tint.(*handler).Handle",
			want: "",
		},
		{fn: "", want: ""},
	}
	for _, tc := range cases {
		if got := subsystemPathForFunc(tc.fn); got != tc.want {
			t.Errorf("subsystemPathForFunc(%q) = %q, want %q", tc.fn, got, tc.want)
		}
	}
}

func containsMessage(entries []LogRecord, msg string) bool {
	for _, e := range entries {
		if e.Msg == msg {
			return true
		}
	}
	return false
}
