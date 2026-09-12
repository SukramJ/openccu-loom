// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"encoding/json"
	"testing"

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// haBody flattens a typed discovery component into the object Home Assistant
// receives, and returns it in the shape the assertions below were written
// against.
//
// The tests keep asserting on the flat body on purpose: that is what goes on
// the wire, so a typed field that stops reaching it — a wrong `json` tag, a
// zero value swallowed by omitempty — still fails a test. Asserting on the
// struct fields instead would pass in exactly that case.
func haBody(t *testing.T, comp hadiscovery.Component) (platform string, body map[string]any) {
	t.Helper()
	if comp.Platform == "" {
		return "", nil
	}
	raw, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("marshal component: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}
	delete(out, "platform")
	return string(comp.Platform), out
}

// haTestDevice is the placeholder identity [hadiscovery.RenderComponent]
// insists on. Nothing it carries reaches an assertion: [haComponent] strips
// the frame again.
var haTestDevice = &hamodel.Device{
	Identity: hamodel.Identity{IDs: []hamodel.Identifier{{Value: "test"}}},
}

// haComponent renders a custom data point's entity the way the bridge does,
// with the bridge's frame stripped off again.
//
// A model-side test asserts on what the data point itself contributes — its
// platform, its description and its platform vocabulary. The unique id, the
// availability list, the device block and the entity-id seed are all derived
// by the bridge from the channel event, which this package has none of, and
// they are pinned where they are produced: the golden payloads in
// internal/north/mqtt/testdata.
//
// [hadiscovery.RawEncoding] is not a preference. A custom data point's
// aggregate carries a curated document, not the `{"value": …}` envelope the
// per-parameter plane publishes, so every field names its own template; the
// envelope default would project one onto entities that publish none.
func haComponent(t *testing.T, e hamodel.Entity, topics payload.HADiscoveryTopics) hadiscovery.Component {
	t.Helper()
	if e == nil {
		return hadiscovery.Component{}
	}
	comp, err := hadiscovery.RenderComponent(
		hadiscovery.StdContext{Layout: payload.SlotLayout{Topics: topics}, Enc: hadiscovery.RawEncoding},
		haTestDevice, e, hadiscovery.Origin{},
	)
	if err != nil {
		t.Fatalf("render entity: %v", err)
	}
	comp.Device, comp.UniqueID, comp.DefaultEntityID = nil, "", ""
	comp.Availability, comp.AvailabilityMode = nil, ""
	return comp
}

// haEntity is [haComponent] flattened by [haBody].
func haEntity(t *testing.T, e hamodel.Entity, topics payload.HADiscoveryTopics) (platform string, body map[string]any) {
	t.Helper()
	return haBody(t, haComponent(t, e, topics))
}
