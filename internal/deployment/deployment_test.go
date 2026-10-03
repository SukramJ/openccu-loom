// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package deployment

import "testing"

func TestResolve(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		host     Host
		want     Kind
	}{
		{"nothing declared, plain host", "", Host{}, Standalone},
		{"nothing declared, add-on install path", "", Host{AddonInstall: true}, CCUAddon},
		{"nothing declared, lite box", "", Host{AddonInstall: true, LiteBox: true}, LiteAddon},
		{"ccu-addon declared", "ccu-addon", Host{}, CCUAddon},
		{"ccu-addon declared on a lite box", "ccu-addon", Host{LiteBox: true}, LiteAddon},
		{"ha-addon declared", "ha-addon", Host{}, HAAddon},
		{"ha-addon declared wins over host facts", "ha-addon", Host{AddonInstall: true, LiteBox: true}, HAAddon},
		{"standalone declared wins over host facts", "standalone", Host{AddonInstall: true, LiteBox: true}, Standalone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.declared, tc.host)
			if err != nil || got != tc.want {
				t.Fatalf("Resolve(%q, %+v) = %q, %v; want %q", tc.declared, tc.host, got, err, tc.want)
			}
		})
	}
}

func TestResolveRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, declared := range []string{"lite-addon", "docker", "HA-ADDON", " "} {
		if got, err := Resolve(declared, Host{LiteBox: true}); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", declared, got)
		}
	}
}

func TestIngressPathOnlyOnALiteBox(t *testing.T) {
	for _, k := range []Kind{CCUAddon, HAAddon, Standalone} {
		if p := k.IngressPath(); p != "" {
			t.Errorf("%s.IngressPath() = %q, want none", k, p)
		}
	}
	if p := LiteAddon.IngressPath(); p != "/addons/loom/" {
		t.Errorf("lite-addon ingress path = %q", p)
	}
}
