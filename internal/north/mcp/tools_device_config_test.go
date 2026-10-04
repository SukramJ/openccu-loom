// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeConfigRepairTool records its call and answers from fields.
type fakeConfigRepairTool struct {
	outcomes []interfaces.ConfigRepairOutcome
	calls    int
	address  string
	channels []string
	dryRun   bool
}

func (f *fakeConfigRepairTool) RepairDeviceConfig(
	_ context.Context, address string, channels []string, dryRun bool,
) ([]interfaces.ConfigRepairOutcome, error) {
	f.calls++
	f.address, f.channels, f.dryRun = address, channels, dryRun
	return f.outcomes, nil
}

// fakeConfigCache records the addresses it was asked to clear and answers
// err when set.
type fakeConfigCache struct {
	cleared []string
	err     error
}

func (f *fakeConfigCache) ClearConfigCache(_ context.Context, address string) error {
	f.cleared = append(f.cleared, address)
	return f.err
}

func deviceConfigDeps(repair *fakeConfigRepairTool, cache *fakeConfigCache, rec audit.Recorder) mcp.Deps {
	devs, _, _ := makeDeviceFixture()
	return mcp.Deps{
		Centrals:     &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:      devs,
		ConfigRepair: repair,
		ConfigCache:  cache,
		Audit:        rec,
		AllowWrites:  true,
	}
}

// TestRepairDeviceConfigToolDefaultsToDryRun pins the safe default, the
// explicit write, the owner check and the outcome projection.
func TestRepairDeviceConfigToolDefaultsToDryRun(t *testing.T) {
	repair := &fakeConfigRepairTool{outcomes: []interfaces.ConfigRepairOutcome{
		{Channel: "ADDR001:1", Status: interfaces.RepairWouldRepair, Corrections: []interfaces.ConfigRepairCorrection{
			{Parameter: "TX", Stored: 99, Corrected: 10, Reason: "clamped"},
		}},
	}}
	cs := serveMCPAs(t, auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic},
		deviceConfigDeps(repair, &fakeConfigCache{}, nil))

	res := callTool(t, cs, "repair_device_config", map[string]any{"central_name": "ccu1", "address": "ADDR001"})
	if res.IsError {
		t.Fatalf("repair_device_config: %v", res.Content)
	}
	if !repair.dryRun || repair.address != "ADDR001" {
		t.Fatalf("call=%+v, want a dry run for ADDR001", repair)
	}
	if res := callTool(t, cs, "repair_device_config", map[string]any{
		"address": "ADDR001", "dry_run": false, "channels": []string{"ADDR001:1"},
	}); res.IsError {
		t.Fatalf("explicit write: %v", res.Content)
	}
	if repair.dryRun || len(repair.channels) != 1 {
		t.Fatalf("call=%+v, want dry_run=false with one channel", repair)
	}

	calls := repair.calls
	if res := callTool(t, cs, "repair_device_config", map[string]any{"central_name": "ccu2", "address": "ADDR001"}); !res.IsError {
		t.Fatal("a device of another central was accepted")
	}
	if repair.calls != calls {
		t.Fatal("the owner check ran after the domain call")
	}
}

// TestDeviceConfigToolsAreAdminOnly pins the role re-check: both tools'
// REST twins are admin-gated, and an operator identity must not reach the
// domain through MCP either.
func TestDeviceConfigToolsAreAdminOnly(t *testing.T) {
	repair := &fakeConfigRepairTool{}
	cache := &fakeConfigCache{}
	cs := serveMCPAs(t, auth.Identity{Subject: "op", Role: auth.RoleOperator, Scheme: auth.SchemeBasic},
		deviceConfigDeps(repair, cache, nil))

	if res := callTool(t, cs, "repair_device_config", map[string]any{"address": "ADDR001"}); !res.IsError {
		t.Error("repair_device_config accepted an operator")
	}
	if res := callTool(t, cs, "clear_device_config_cache", map[string]any{"address": "ADDR001"}); !res.IsError {
		t.Error("clear_device_config_cache accepted an operator")
	}
	if repair.calls != 0 || len(cache.cleared) != 0 {
		t.Fatalf("domain reached: repair=%d clear=%v", repair.calls, cache.cleared)
	}
}

// TestClearDeviceConfigCacheToolRecordsAudit pins the call and the audit row
// carrying the caller and the shared cache-clear note.
func TestClearDeviceConfigCacheToolRecordsAudit(t *testing.T) {
	cache := &fakeConfigCache{}
	buf := audit.NewBuffer(4)
	cs := serveMCPAs(t, auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic},
		deviceConfigDeps(&fakeConfigRepairTool{}, cache, buf))

	if res := callTool(t, cs, "clear_device_config_cache", map[string]any{"central_name": "ccu1", "address": "ADDR001"}); res.IsError {
		t.Fatalf("clear_device_config_cache: %v", res.Content)
	}
	if len(cache.cleared) != 1 || cache.cleared[0] != "ADDR001" {
		t.Fatalf("cleared=%v, want [ADDR001]", cache.cleared)
	}
	rows := buf.List(4)
	if len(rows) != 1 || rows[0].User != "admin" || rows[0].DeviceAddress != "ADDR001" ||
		rows[0].Action != audit.ActionDeviceConfigCacheClear {
		t.Fatalf("audit rows=%+v", rows)
	}
}

// TestClearDeviceConfigCacheToolReportsAnUnknownDeviceAsNotFound pins what
// an MCP client sees for a device that is gone from the daemon's model:
// MCP tools carry no error code, so the not-found answer is the tool error
// whose text says so and names the address — not a "no backend" text that
// reads like an unreachable CCU. No change-log row is written for a clear
// that never happened.
func TestClearDeviceConfigCacheToolReportsAnUnknownDeviceAsNotFound(t *testing.T) {
	cache := &fakeConfigCache{err: fmt.Errorf("%w: GONE000000001", interfaces.ErrDeviceNotFound)}
	buf := audit.NewBuffer(4)
	cs := serveMCPAs(t, auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic},
		deviceConfigDeps(&fakeConfigRepairTool{}, cache, buf))

	res := callTool(t, cs, "clear_device_config_cache", map[string]any{"address": "GONE000000001"})
	if !res.IsError {
		t.Fatal("an unknown device was reported as cleared")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	text := b.String()
	if !strings.Contains(text, "device not found") || !strings.Contains(text, "GONE000000001") {
		t.Fatalf("tool error %q does not say the device was not found", text)
	}
	if rows := buf.List(4); len(rows) != 0 {
		t.Fatalf("audit rows for a failed clear: %+v", rows)
	}
}
