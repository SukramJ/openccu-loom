// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// twoCentralsSharingAnAddress registers ccu-01 and ccu-02, each holding a
// device under the same address with a different model — the shape of a
// virtual-remote root, an internal device or a CUxD device, which repeat
// verbatim on every CCU.
func twoCentralsSharingAnAddress(t *testing.T, addr string) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, spec := range []struct{ name, model string }{
		{"ccu-01", "HmIP-RCV-50"},
		{"ccu-02", "HmIP-RCV-60"},
	} {
		c, err := central.New(central.Config{Name: spec.name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", spec.name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("register %s: %v", spec.name, err)
		}
		d := device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: addr, Model: spec.model,
		})
		d.AddChannel(addr+":1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
		c.ModelRegistry.Put(d)
		c.MarkSouthboundReady()
	}
	return reg
}

// TestDeviceRouteLandsOnTheNamedCentral pins the whole binding chain through
// the router with the production adapter and the production locator: the
// guard binds the request, the binding reaches the adapter's lookup, and the
// answer is the named CCU's device. The guard walk
// (TestEveryDeviceRouteHonoursCentral) proves only the refusal; this proves
// the acceptance lands where it was sent. A guard that bound the request but
// an adapter that ignored the binding answers ccu-01's model for ccu-02.
func TestDeviceRouteLandsOnTheNamedCentral(t *testing.T) {
	const addr = "HmIP-RCV-1"
	reg := twoCentralsSharingAnAddress(t, addr)
	deps := fullyWiredRouterDeps()
	deps.Devices = adapter.NewDevicesAdapter(reg)
	deps.DeviceCentrals = reg
	router := rest.NewRouter(deps)

	for _, tc := range []struct{ central, model string }{
		{"ccu-01", "HmIP-RCV-50"},
		{"ccu-02", "HmIP-RCV-60"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+addr+"?central="+tc.central, http.NoBody)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("?central=%s: status %d body %.200s", tc.central, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"`+tc.model+`"`) {
			t.Errorf("?central=%s answered %.200s, want the device of model %s", tc.central, rec.Body.String(), tc.model)
		}
	}
}
