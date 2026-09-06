// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestNoTopicBuilderMethodProducesLegacyAggregateStateShape pins the
// retirement of the `<base>/<central>/<iface>/<address>/<channel>/state`
// shape.
//
// [LegacyAggregateStateMatcher] deletes retained topics of that shape at
// boot. That is only safe while nothing publishes it. A publisher can
// only reach the shape through a [TopicBuilder] method, so the guard
// walks every exported builder method by reflection — a newly added one
// is covered without touching this test — and asserts none of them
// returns a topic the cleanup matcher would claim.
//
// Bites when a builder method resurrects the shape: before the removal
// of `AggregatedState` this test failed naming that method.
func TestNoTopicBuilderMethodProducesLegacyAggregateStateShape(t *testing.T) {
	const base = "openccu-loom"
	tb := NewTopicBuilder(base)

	slots := map[string]payload.TopicSlot{
		"values": {Bucket: payload.BucketValues, Address: "0001ABCD", Channel: 1, Parameter: "STATE"},
		"custom": {Bucket: payload.BucketCustom, Address: "0001ABCD", Channel: 1, Parameter: "switch"},
	}

	slotType := reflect.TypeOf(payload.TopicSlot{})
	rv := reflect.ValueOf(tb)
	rt := rv.Type()

	for i := range rt.NumMethod() {
		m := rt.Method(i)
		if !m.IsExported() {
			continue
		}
		// A builder method that returns something other than a single
		// topic string is not a publish target for this shape.
		mt := m.Type
		if mt.NumOut() != 1 || mt.Out(0).Kind() != reflect.String {
			continue
		}
		for slotName, slot := range slots {
			args := make([]reflect.Value, 0, mt.NumIn()-1)
			strIdx := 0
			for p := 1; p < mt.NumIn(); p++ {
				pt := mt.In(p)
				switch {
				case pt == slotType:
					args = append(args, reflect.ValueOf(slot))
				case pt.Kind() == reflect.String:
					args = append(args, reflect.ValueOf(sampleStringArg(strIdx)))
					strIdx++
				case pt.Kind() == reflect.Int:
					args = append(args, reflect.ValueOf(1))
				default:
					// Do not silently skip: an unhandled parameter type
					// means the guard stopped covering a method.
					t.Fatalf("%s: unhandled parameter type %s — extend the guard", m.Name, pt)
				}
			}
			got := rv.Method(i).Call(args)[0].String()
			if got == "" {
				continue
			}
			if LegacyAggregateStateMatcher(base, got) {
				t.Errorf("TopicBuilder.%s (slot %s) produces the retired aggregate-state shape %q; "+
					"RetainCleanup deletes that shape at boot, so publishing it loses retained state",
					m.Name, slotName, got)
			}
		}
	}
}

// sampleStringArg returns a plausible value for the n-th string
// parameter of a [TopicBuilder] method. The builders take their scoping
// arguments in the order central, interface, address; everything after
// that is a leaf-ish segment.
func sampleStringArg(n int) string {
	switch n {
	case 0:
		return "ccu"
	case 1:
		return "HmIP-RF"
	case 2:
		return "0001ABCD"
	default:
		return "seg"
	}
}
