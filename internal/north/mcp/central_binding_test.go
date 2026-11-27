// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
)

// adminIdentity is the caller of the binding tests: some of the device tools
// are admin-only, and the binding must hold for every one of them.
func adminIdentity() auth.Identity {
	return auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic}
}

// toolErrorText joins the text content of a tool result.
func toolErrorText(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// repeatedAddressDeps builds a two-CCU fixture where ADDR001 exists on both
// ccu1 and ccu2 — the virtual-remote buses, the INT000* devices and CUxD
// devices repeat verbatim across CCUs. CentralOf names ccu1 for it, the
// first central by name, so a check against CentralOf alone could never
// reach ccu2's device.
func repeatedAddressDeps(writer *fakeWriter, ps *fakeParamsets, cache *fakeConfigCache) mcp.Deps {
	devs, _, _ := makeDeviceFixture()
	devs.alsoOn("ADDR001", "ccu2")
	return mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Writer:      writer,
		Paramsets:   ps,
		ConfigCache: cache,
		Audit:       audit.NewBuffer(100),
		AllowWrites: true,
	}
}

// TestDeviceTools_BindTheNamedCentral pins that a device tool naming a
// central acts on that central's device: the call reaches the domain service
// with its context bound to the named central, for the second CCU that
// repeats an address as much as for the first. Binding is what makes the
// domain service resolve the address on that CCU; a check that only compares
// central_name against the first holder refuses ccu2 and leaves the ccu1 call
// unbound.
func TestDeviceTools_BindTheNamedCentral(t *testing.T) {
	for _, central := range []string{"ccu1", "ccu2"} {
		t.Run(central, func(t *testing.T) {
			writer := &fakeWriter{}
			ps := newFakeParamsets()
			cache := &fakeConfigCache{}
			cs := serveMCPAs(t, adminIdentity(), repeatedAddressDeps(writer, ps, cache))
			defer cs.Close()

			res := callTool(t, cs, "set_datapoint", map[string]any{
				"central_name": central,
				"address":      "ADDR001:3",
				"parameter":    "STATE",
				"value":        true,
			})
			if res.IsError {
				t.Fatalf("set_datapoint on %s: %v", central, res.Content)
			}
			if writer.last.scope != central {
				t.Errorf("set_datapoint reached the writer bound to %q, want %q", writer.last.scope, central)
			}

			res = callTool(t, cs, "write_paramset", map[string]any{
				"central_name": central,
				"address":      "ADDR001:3",
				"key":          "VALUES",
				"values":       map[string]any{"STATE": true},
			})
			if res.IsError {
				t.Fatalf("write_paramset on %s: %v", central, res.Content)
			}
			if len(ps.putCalls) != 1 || ps.putCalls[0].scope != central {
				t.Errorf("write_paramset calls = %+v, want one bound to %q", ps.putCalls, central)
			}

			res = callTool(t, cs, "clear_device_config_cache", map[string]any{
				"central_name": central,
				"address":      "ADDR001",
			})
			if res.IsError {
				t.Fatalf("clear_device_config_cache on %s: %v", central, res.Content)
			}
			if len(cache.scopes) != 1 || cache.scopes[0] != central {
				t.Errorf("clear_device_config_cache scopes = %v, want [%s]", cache.scopes, central)
			}
		})
	}
}

// TestDeviceTools_RefuseUnknownOrNonHoldingCentral pins the two refusals of
// the binding, the MCP twin of the REST device-route 404s: a central that is
// not configured, and a configured one that does not hold the address. The
// domain service is never reached.
func TestDeviceTools_RefuseUnknownOrNonHoldingCentral(t *testing.T) {
	cases := []struct {
		name, central, address, want string
	}{
		{"unknown central", "ccu9", "ADDR001", `no central named "ccu9"`},
		{"central without the device", "ccu1", "ADDR002", `central "ccu1" holds no device ADDR002`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeConfigCache{}
			cs := serveMCPAs(t, adminIdentity(), repeatedAddressDeps(&fakeWriter{}, newFakeParamsets(), cache))
			defer cs.Close()

			res := callTool(t, cs, "clear_device_config_cache", map[string]any{
				"central_name": tc.central,
				"address":      tc.address,
			})
			if !res.IsError {
				t.Fatalf("expected an error, got %v", res.Content)
			}
			if got := toolErrorText(res); !strings.Contains(got, tc.want) {
				t.Errorf("error = %q, want it to contain %q", got, tc.want)
			}
			if len(cache.cleared) != 0 {
				t.Errorf("domain service reached: %v", cache.cleared)
			}
		})
	}
}
