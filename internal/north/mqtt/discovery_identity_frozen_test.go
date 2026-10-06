// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// frozenIdentity is what Home Assistant keys an entity on, as the discovery
// goldens recorded it before ADR 0083 moved every state and command topic:
// the discovery topic (and with it the node id and object id), the
// `unique_id`, the `default_entity_id` seed and the device block's
// identifiers.
type frozenIdentity struct {
	Topic            string                       `json:"topic"`
	UniqueID         string                       `json:"unique_id,omitempty"`
	DefaultEntityID  string                       `json:"default_entity_id,omitempty"`
	ObjectID         string                       `json:"object_id,omitempty"`
	DeviceIDs        []string                     `json:"device_identifiers,omitempty"`
	DeviceViaDevice  string                       `json:"device_via_device,omitempty"`
	BundleComponents map[string]map[string]string `json:"components,omitempty"`
}

// identityOf projects one golden entry onto the fields [frozenIdentity]
// records, exactly the way testdata/discovery_identity_frozen.json was
// extracted from the pre-change goldens.
func identityOf(t *testing.T, entry map[string]json.RawMessage) frozenIdentity {
	t.Helper()
	var id frozenIdentity
	if raw, ok := entry["topic"]; ok {
		if err := json.Unmarshal(raw, &id.Topic); err != nil {
			t.Fatalf("topic: %v", err)
		}
	}
	var payload map[string]json.RawMessage
	if raw, ok := entry["payload"]; ok {
		// A retraction entry pins an empty payload; it has no identity
		// fields beyond its topic.
		_ = json.Unmarshal(raw, &payload)
	}
	str := func(key string) string {
		var s string
		if raw, ok := payload[key]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		return s
	}
	id.UniqueID = str("unique_id")
	id.DefaultEntityID = str("default_entity_id")
	id.ObjectID = str("object_id")
	if raw, ok := payload["device"]; ok {
		var dev struct {
			Identifiers []string `json:"identifiers"`
			ViaDevice   string   `json:"via_device"`
		}
		if err := json.Unmarshal(raw, &dev); err != nil {
			t.Fatalf("device: %v", err)
		}
		id.DeviceIDs = dev.Identifiers
		id.DeviceViaDevice = dev.ViaDevice
	}
	if raw, ok := payload["components"]; ok {
		var comps map[string]map[string]json.RawMessage
		if err := json.Unmarshal(raw, &comps); err != nil {
			t.Fatalf("components: %v", err)
		}
		id.BundleComponents = map[string]map[string]string{}
		for name, comp := range comps {
			fields := map[string]string{}
			for _, key := range []string{"unique_id", "default_entity_id", "object_id"} {
				if v, ok := comp[key]; ok {
					var s string
					_ = json.Unmarshal(v, &s)
					fields[key] = s
				}
			}
			id.BundleComponents[name] = fields
		}
	}
	return id
}

// TestDiscoveryIdentityUnchangedByTopicConvention is the ADR 0068 proof for
// ADR 0083: moving every state and command topic to the mqtt-smarthome
// grammar must not move a single identity Home Assistant keys its registry
// on, because Home Assistant has no migration for a `unique_id` and re-points
// an entity to new topics only when its identity stands still.
//
// testdata/discovery_identity_frozen.json was extracted from the discovery
// goldens as they stood before the change — the identity fields only, one
// entry per golden case — and is never regenerated. Every current golden is
// compared against it, case by case: a device entity (discovery_golden.json),
// the hub plane (discovery_golden_hub.json), the alarm plane
// (discovery_golden_alarm.json) and the Security & Safety plane
// (discovery_golden_security_combined.json), plus every other plane's golden.
// The goldens themselves are pinned against the builders by their own tests,
// so a builder that changed an identity fails there first and here second.
//
// Falsifiability: change one `unique_id` in a current golden, or rename the
// security entity key `state` to `severity` (its item was renamed; its key
// must not be), and this test names the case.
func TestDiscoveryIdentityUnchangedByTopicConvention(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "discovery_identity_frozen.json"))
	if err != nil {
		t.Fatalf("read frozen identities: %v", err)
	}
	var frozen map[string]map[string]frozenIdentity
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatalf("decode frozen identities: %v", err)
	}
	for _, plane := range []string{
		"discovery_golden.json", "discovery_golden_hub.json",
		"discovery_golden_alarm.json", "discovery_golden_security_combined.json",
	} {
		if len(frozen[plane]) == 0 {
			t.Errorf("frozen identities carry no case for %s — the proof would cover nothing there", plane)
		}
	}
	files := make([]string, 0, len(frozen))
	for f := range frozen {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		want := frozen[file]
		cur, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		var golden map[string]map[string]json.RawMessage
		if err := json.Unmarshal(cur, &golden); err != nil {
			t.Errorf("%s: decode: %v", file, err)
			continue
		}
		if len(golden) != len(want) {
			t.Errorf("%s: %d cases now, %d before the topic move — a case was added or dropped", file, len(golden), len(want))
		}
		for name, wantID := range want {
			entry, ok := golden[name]
			if !ok {
				t.Errorf("%s: case %q is gone", file, name)
				continue
			}
			got := identityOf(t, entry)
			if !reflect.DeepEqual(got, wantID) {
				t.Errorf("%s: case %q moved an identity Home Assistant keys its registry on:\n got  %+v\n want %+v",
					file, name, got, wantID)
			}
			if strings.Contains(got.Topic, "/status/") || strings.Contains(got.Topic, "/set/") {
				t.Errorf("%s: case %q: discovery topic %q took on the state grammar", file, name, got.Topic)
			}
		}
	}
}
