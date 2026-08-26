// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestBucketStringValues pins the topic-segment names. Consumers
// (REST exporters, the bridge URL builder, downstream test fixtures)
// depend on these strings — changing them is a breaking change.
func TestBucketStringValues(t *testing.T) {
	t.Parallel()
	cases := map[payload.Bucket]string{
		payload.BucketValues:     "values",
		payload.BucketMaster:     "master",
		payload.BucketCalculated: "calculated",
		payload.BucketCustom:     "custom",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("Bucket %q != %q", string(got), want)
		}
	}
}

// fakeSource exercises the full ADR-0011 declarative surface from
// payload-package-internal tests, without pulling in any custom-DP
// package. Mirrors the real custom-DP shape closely enough to pin
// the interface contracts.
type fakeSource struct {
	addr   string
	chNo   int
	bucket payload.Bucket
	param  string
	comp   string
}

func (f *fakeSource) HAComponent() string { return f.comp }
func (f *fakeSource) TopicSlot() payload.TopicSlot {
	return payload.TopicSlot{Address: f.addr, Channel: f.chNo, Bucket: f.bucket, Parameter: f.param}
}

// TestSlottedAndHAEntityCompose pins that a single source can satisfy
// both interfaces independently — bridges should be able to query
// each capability on its own without forcing the source to implement
// the other.
func TestSlottedAndHAEntityCompose(t *testing.T) {
	t.Parallel()
	src := &fakeSource{addr: "000C9709AEF157", chNo: 1, bucket: payload.BucketCustom, param: "climate", comp: "climate"}

	if _, ok := any(src).(payload.HAEntity); !ok {
		t.Fatal("source must satisfy payload.HAEntity")
	}
	if _, ok := any(src).(payload.Slotted); !ok {
		t.Fatal("source must satisfy payload.Slotted")
	}

	if got := src.HAComponent(); got != "climate" {
		t.Errorf("HAComponent=%q want climate", got)
	}
	slot := src.TopicSlot()
	if slot.Address != "000C9709AEF157" || slot.Channel != 1 || slot.Bucket != payload.BucketCustom || slot.Parameter != "climate" {
		t.Errorf("TopicSlot=%+v unexpected", slot)
	}
}

// TestHAComponentEmptyOptsOut documents the contract that a source
// returning the empty string for HAComponent opts out of HA
// MQTT-Discovery. The bridge must respect that — phase 1b/2 will
// add a corresponding bridge-side test.
func TestHAComponentEmptyOptsOut(t *testing.T) {
	t.Parallel()
	src := &fakeSource{comp: ""}
	if got := src.HAComponent(); got != "" {
		t.Errorf("HAComponent=%q want \"\" (opt-out sentinel)", got)
	}
}
