// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
)

// TestHmBkRegaBackedOperationsMatchTheRunnerSchemas ties the field sets this
// package decodes out of three ReGa scripts to the schemas the live reader
// declares in internal/client/rega.
//
// Each of the three scripts has two readers: rega.Runner's exported structs,
// which the hub path uses, and a function-local struct here. They read the
// same script output, so a field added or renamed in the script has to land in
// both — the copy that is not edited silently decodes the new field as the
// zero value, with no compile error anywhere.
//
// The production code deliberately keeps internal/client/rega out of this
// package's imports (see [ScriptRunner]); this is a test-only import, so the
// seam is unaffected.
func TestHmBkRegaBackedOperationsMatchTheRunnerSchemas(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		schema any
		raw    string
		invoke func(*CcuBackend) ([]map[string]any, error)
	}{
		{
			name:   "service_messages",
			schema: rega.ServiceMessage{},
			raw: `[{"id":"1","name":"K%FCche","timestamp":1,"type":2,"address":"ABC:1",` +
				`"device_name":"D","last_timestamp":3,"counter":4,"rooms":["R"],` +
				`"functions":["F"],"quittable":true}]`,
			invoke: func(b *CcuBackend) ([]map[string]any, error) {
				return b.GetServiceMessages(context.Background(), "")
			},
		},
		{
			name:   "alarm_messages",
			schema: rega.AlarmMessage{},
			raw: `[{"id":"1","name":"K%FCche","description":"D","timestamp":1,` +
				`"last_timestamp":2,"counter":3}]`,
			invoke: func(b *CcuBackend) ([]map[string]any, error) {
				return b.GetAlarmMessages(context.Background())
			},
		},
		{
			name:   "inbox_devices",
			schema: rega.InboxDevice{},
			raw:    `[{"id":"1","address":"ABC","name":"N","type":"HmIP-PS","interface":"HmIP-RF"}]`,
			invoke: func(b *CcuBackend) ([]map[string]any, error) {
				return b.GetInboxDevices(context.Background(), "")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := NewCcuBackend(nil, nil, nil)
			b.SetScriptRunner(&fakeScriptRunner{rawJSON: tc.raw})

			got, err := tc.invoke(b)
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d entries, want 1", len(got))
			}

			want := hmBkJSONTags(tc.schema)
			have := hmBkMapKeys(got[0])
			if !reflect.DeepEqual(have, want) {
				t.Fatalf("field set drift for %s:\n  backends emits: %v\n  %T declares:   %v\n"+
					"both read the same ReGa script; the sets must match",
					tc.name, have, tc.schema, want)
			}
		})
	}
}

// hmBkJSONTags returns the sorted json tag names of v's exported fields.
func hmBkJSONTags(v any) []string {
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func hmBkMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
