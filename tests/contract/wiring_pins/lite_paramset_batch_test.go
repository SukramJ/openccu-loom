// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// liteDescriptionCalls counts how a lite central read its paramset
// descriptions: requests to the box's description route, and
// getParamsetDescription calls through the XML-RPC proxy.
func liteDescriptionCalls(fake *litefake.Fake) (batch, single int) {
	calls := fake.Calls()
	for i := range calls {
		if calls[i].Method == http.MethodPost && strings.HasPrefix(calls[i].Path, "/api/rpc/v1/paramsets/") && calls[i].Status == http.StatusOK {
			batch++
		}
		for _, m := range calls[i].RPCMethods {
			if m == "getParamsetDescription" {
				single++
			}
		}
	}
	return batch, single
}

// liteDescriptions renders every paramset description a central holds,
// one line per parameter, so two centrals can be compared and a
// difference names its place. Numbers are compared by value: a bound of
// 5 and one of 5.0 are the same description.
func liteDescriptions(t *testing.T, unit *central.Unit) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dev := range unit.ModelRegistry.List() {
		iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		addresses := make([]string, 0, 1+len(dev.Channels()))
		addresses = append(addresses, dev.Address)
		for _, ch := range dev.Channels() {
			addresses = append(addresses, ch.Address)
		}
		for _, addr := range addresses {
			for _, key := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
				ps, ok := unit.ParamsetReg.Get(iface, addr, key)
				if !ok {
					continue
				}
				for param := range ps {
					out[fmt.Sprintf("%s %s %s", addr, key, param)] = canonicalParameter(t, ps[param])
				}
			}
		}
	}
	return out
}

// canonicalParameter renders a parameter description with its numbers
// normalised, so the JSON text a bound happened to arrive in does not
// count as a difference.
func canonicalParameter(t *testing.T, pd hmproto.ParameterData) string {
	t.Helper()
	raw, err := json.Marshal(pd)
	if err != nil {
		t.Fatalf("marshal parameter: %v", err)
	}
	var v any // decoded JSON of unknown shape, re-encoded below
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal parameter: %v", err)
	}
	canon, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal canonical parameter: %v", err)
	}
	return string(canon)
}

// TestLiteHydrationReadsDescriptionsInOneRequestPerParamset pins the box's
// paramset description route through the lite bring-up: a central reads
// its descriptions in a handful of requests instead of one XML-RPC call
// per channel, and what it holds afterwards is what a central holds that
// read them one by one from a box without the route.
func TestLiteHydrationReadsDescriptionsInOneRequestPerParamset(t *testing.T) {
	batched := startFake(t, litefake.Options{})
	viaBatch := startLiteCentral(t, batched, litefake.DefaultToken)
	waitLiteReady(t, viaBatch)

	plain := startFake(t, litefake.Options{})
	plain.Deviate(litefake.DeviateNoParamsetsRoute, true)
	oneByOne := startLiteCentral(t, plain, litefake.DefaultToken)
	waitLiteReady(t, oneByOne)

	batch, single := liteDescriptionCalls(batched)
	_, plainSingle := liteDescriptionCalls(plain)
	if plainSingle == 0 {
		t.Fatal("the central on the box without the route read no description through the proxy: the comparison has no baseline")
	}
	// Two interfaces, two paramsets each.
	if batch == 0 || batch > 4 {
		t.Errorf("requests to the description route = %d, want 1 to 4", batch)
	}
	if single != 0 {
		t.Errorf("getParamsetDescription calls beside the batch = %d (a box without the route needs %d), want 0", single, plainSingle)
	}

	got, want := liteDescriptions(t, viaBatch), liteDescriptions(t, oneByOne)
	if len(want) == 0 {
		t.Fatal("the reference central holds no description")
	}
	if len(got) != len(want) {
		t.Errorf("descriptions held = %d parameters, want %d", len(got), len(want))
	}
	shown := 0
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			shown++
			if shown <= 5 {
				t.Errorf("%s:\n  batch      %s\n  one by one %s", k, g, w)
			}
		}
	}
	if shown > 5 {
		t.Errorf("… and %d more differences", shown-5)
	}
}
