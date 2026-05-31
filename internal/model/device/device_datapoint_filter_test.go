// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// fakeGetDP satisfies CategorisedDataPoint + noCreateDP + registeredDP for
// GetDataPoints filter tests.
type fakeGetDP struct {
	parameterKey string
	category     hmenum.DataPointCategory
	usage        hmenum.DataPointUsage
	registered   bool
}

func (f *fakeGetDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: f.parameterKey}
}
func (f *fakeGetDP) Category() hmenum.DataPointCategory { return f.category }
func (f *fakeGetDP) Usage() hmenum.DataPointUsage       { return f.usage }
func (f *fakeGetDP) IsRegistered() bool                 { return f.registered }

// ─── A1.P1.1: GetDataPoints filter params ────────────────────────────────────

// TestGetDataPointsNoFilter verifies that with zero-value filter args all
// custom + calculated DPs are returned.
func TestGetDataPointsNoFilter(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "GD1", Model: "M"})
	ch := d.AddChannel("GD1:1", 1, "T", hmenum.ParamsetKeyValues)

	dp1 := &fakeGetDP{parameterKey: "A", category: "SENSOR"}
	dp2 := &fakeGetDP{parameterKey: "B", category: "SENSOR"}
	ch.SetCustomDataPoint(dp1)
	ch.AttachCalculatedDataPoint(dp2)

	got := d.GetDataPoints("", false, nil)
	if len(got) != 2 {
		t.Fatalf("GetDataPoints() len=%d, want 2", len(got))
	}
}

// TestGetDataPointsCategoryFilter verifies that only DPs matching the
// requested category are returned.
func TestGetDataPointsCategoryFilter(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "GD2", Model: "M"})
	ch := d.AddChannel("GD2:1", 1, "T", hmenum.ParamsetKeyValues)

	sensor := &fakeGetDP{parameterKey: "S", category: "SENSOR"}
	actor := &fakeGetDP{parameterKey: "A", category: "ACTOR"}
	ch.SetCustomDataPoint(sensor)
	ch.AttachCalculatedDataPoint(actor)

	got := d.GetDataPoints("SENSOR", false, nil)
	if len(got) != 1 {
		t.Fatalf("GetDataPoints(SENSOR) len=%d, want 1", len(got))
	}
	if got[0].DataPointKey().Parameter != "S" {
		t.Errorf("unexpected DP key: %v", got[0].DataPointKey())
	}
}

// TestGetDataPointsExcludeNoCreate verifies that DPs with UsageNoCreate are
// excluded when excludeNoCreate is true.
func TestGetDataPointsExcludeNoCreate(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "GD3", Model: "M"})
	ch := d.AddChannel("GD3:1", 1, "T", hmenum.ParamsetKeyValues)

	normal := &fakeGetDP{parameterKey: "N", category: "SENSOR", usage: hmenum.DataPointUsageDataPoint}
	noCreate := &fakeGetDP{parameterKey: "X", category: "SENSOR", usage: hmenum.DataPointUsageNoCreate}
	ch.AttachCalculatedDataPoint(normal)
	ch.AttachCalculatedDataPoint(noCreate)

	got := d.GetDataPoints("", true, nil)
	for _, dp := range got {
		if dp.DataPointKey().Parameter == "X" {
			t.Error("noCreate DP must be excluded when excludeNoCreate=true")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dp after noCreate exclusion, got %d", len(got))
	}
}

// TestGetDataPointsRegisteredFilter verifies that the registered *bool filter
// returns only DPs that match the desired IsRegistered() state.
func TestGetDataPointsRegisteredFilter(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "GD4", Model: "M"})
	ch := d.AddChannel("GD4:1", 1, "T", hmenum.ParamsetKeyValues)

	regDP := &fakeGetDP{parameterKey: "R", registered: true}
	notRegDP := &fakeGetDP{parameterKey: "NR", registered: false}
	ch.AttachCalculatedDataPoint(regDP)
	ch.AttachCalculatedDataPoint(notRegDP)

	wantTrue := true
	got := d.GetDataPoints("", false, &wantTrue)
	if len(got) != 1 || got[0].DataPointKey().Parameter != "R" {
		t.Fatalf("registered=true filter: got %v, want [R]", got)
	}

	wantFalse := false
	got2 := d.GetDataPoints("", false, &wantFalse)
	if len(got2) != 1 || got2[0].DataPointKey().Parameter != "NR" {
		t.Fatalf("registered=false filter: got %v, want [NR]", got2)
	}
}

// ─── A1.P1.2: HasLinkSourceCategory / HasLinkTargetCategory ──────────────────

// TestHasLinkSourceCategoryMatch verifies true when category is present.
func TestHasLinkSourceCategoryMatch(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LS1", Model: "M"})
	ch := d.AddChannel("LS1:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetLinkPeerSourceCategories([]string{"SWITCH", "DIMMER"})

	if !ch.HasLinkSourceCategory("SWITCH") {
		t.Error("HasLinkSourceCategory(SWITCH) want true")
	}
	if !ch.HasLinkSourceCategory("DIMMER") {
		t.Error("HasLinkSourceCategory(DIMMER) want true")
	}
}

// TestHasLinkSourceCategoryNoMatch verifies false when category is absent.
func TestHasLinkSourceCategoryNoMatch(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LS2", Model: "M"})
	ch := d.AddChannel("LS2:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetLinkPeerSourceCategories([]string{"SWITCH"})

	if ch.HasLinkSourceCategory("SENSOR") {
		t.Error("HasLinkSourceCategory(SENSOR) want false")
	}
}

// TestHasLinkSourceCategoryEmpty verifies false when no categories are recorded.
func TestHasLinkSourceCategoryEmpty(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LS3", Model: "M"})
	ch := d.AddChannel("LS3:1", 1, "T", hmenum.ParamsetKeyValues)

	if ch.HasLinkSourceCategory("SWITCH") {
		t.Error("HasLinkSourceCategory on channel with no source categories must return false")
	}
}

// TestHasLinkSourceCategoryNilChannel verifies nil-safe behaviour.
func TestHasLinkSourceCategoryNilChannel(t *testing.T) {
	t.Parallel()

	var ch *Channel
	if ch.HasLinkSourceCategory("X") {
		t.Error("nil channel HasLinkSourceCategory must return false")
	}
}

// TestHasLinkTargetCategoryMatch verifies true when target category is present.
func TestHasLinkTargetCategoryMatch(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LT1", Model: "M"})
	ch := d.AddChannel("LT1:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetLinkPeerTargetCategories([]string{"CLIMATE", "VALVE"})

	if !ch.HasLinkTargetCategory("CLIMATE") {
		t.Error("HasLinkTargetCategory(CLIMATE) want true")
	}
	if !ch.HasLinkTargetCategory("VALVE") {
		t.Error("HasLinkTargetCategory(VALVE) want true")
	}
}

// TestHasLinkTargetCategoryNoMatch verifies false when target category absent.
func TestHasLinkTargetCategoryNoMatch(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LT2", Model: "M"})
	ch := d.AddChannel("LT2:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetLinkPeerTargetCategories([]string{"CLIMATE"})

	if ch.HasLinkTargetCategory("VALVE") {
		t.Error("HasLinkTargetCategory(VALVE) want false")
	}
}

// TestHasLinkTargetCategoryNilChannel verifies nil-safe behaviour.
func TestHasLinkTargetCategoryNilChannel(t *testing.T) {
	t.Parallel()

	var ch *Channel
	if ch.HasLinkTargetCategory("Y") {
		t.Error("nil channel HasLinkTargetCategory must return false")
	}
}

// ─── A1.P1.3: LinkPeerChannels ───────────────────────────────────────────────

// TestLinkPeerChannelsNoPeers verifies nil is returned when no channel has
// link peers.
func TestLinkPeerChannelsNoPeers(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LP1", Model: "M"})
	d.AddChannel("LP1:1", 1, "T", hmenum.ParamsetKeyValues)
	d.AddChannel("LP1:2", 2, "T", hmenum.ParamsetKeyValues)

	if got := d.LinkPeerChannels(); got != nil {
		t.Errorf("LinkPeerChannels() with no peers = %v, want nil", got)
	}
}

// TestLinkPeerChannelsWithPeers verifies the map contains peer lists for
// channels that have peers set while omitting peer-less channels.
func TestLinkPeerChannelsWithPeers(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LP2", Model: "M"})
	ch1 := d.AddChannel("LP2:1", 1, "T", hmenum.ParamsetKeyValues)
	_ = d.AddChannel("LP2:2", 2, "T", hmenum.ParamsetKeyValues)
	ch1.SetLinkPeers([]string{"VALVE:1", "VALVE:2"})

	got := d.LinkPeerChannels()
	if got == nil {
		t.Fatal("LinkPeerChannels() must not be nil when a channel has peers")
	}
	peers, ok := got["LP2:1"]
	if !ok {
		t.Fatalf("map missing key LP2:1; got %v", got)
	}
	if len(peers) != 2 {
		t.Errorf("expected 2 peers for LP2:1, got %d", len(peers))
	}
	if _, ok2 := got["LP2:2"]; ok2 {
		t.Error("channel with no peers must not appear in map")
	}
}

// TestLinkPeerChannelsReturnsCopy verifies that mutating the returned map
// does not affect subsequent calls.
func TestLinkPeerChannelsReturnsCopy(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "LP3", Model: "M"})
	ch := d.AddChannel("LP3:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetLinkPeers([]string{"X:1"})

	got1 := d.LinkPeerChannels()
	got1["EXTRA"] = []string{"MUTATED"}

	got2 := d.LinkPeerChannels()
	if _, found := got2["EXTRA"]; found {
		t.Error("mutation of returned map must not affect second call")
	}
}
