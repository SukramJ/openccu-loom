// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type writeJSONMarshaler struct{ v int }

func (m writeJSONMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{ "custom" : ` + string(rune('0'+m.v)) + ` }`), nil
}

type writeJSONOmit struct {
	A string             `json:"a,omitempty"`
	B []int              `json:"b,omitempty"`
	C map[string]int     `json:"c,omitempty"`
	D *int               `json:"d,omitempty"`
	E time.Time          `json:"e"`
	F json.RawMessage    `json:"f,omitempty"`
	G any                `json:"g"`
	H []byte             `json:"h"`
	I writeJSONMarshaler `json:"i"`
}

// TestJSON_BytesMatchTheV1Encoder pins the REST response body byte for byte
// against json.NewEncoder(w).Encode(v), the form every endpoint answered
// with first: HTML escaping, map-key order, nil collections, raw messages,
// MarshalJSON output (which v1 compacts) and the trailing newline.
func TestJSON_BytesMatchTheV1Encoder(t *testing.T) {
	t.Parallel()
	seven := 7
	cases := map[string]any{
		"html":        map[string]string{"q": `<a href="x">&amp;</a>`, "u": "  "},
		"map order":   map[string]int{"zeta": 1, "alpha": 2, "Mid": 3, "mid": 4},
		"nil slice":   struct{ S []string }{},
		"empty slice": struct{ S []string }{S: []string{}},
		"nil map":     struct{ M map[string]int }{},
		"omitempty":   writeJSONOmit{},
		"filled": writeJSONOmit{
			A: "x", B: []int{1}, C: map[string]int{"b": 2, "a": 1}, D: &seven,
			E: time.Date(2026, 10, 1, 12, 0, 0, 5, time.UTC),
			F: json.RawMessage(`{ "raw" : [1, 2] }`), G: 1.5, H: []byte("hi"),
			I: writeJSONMarshaler{v: 3},
		},
		"floats":   []float64{0, 1e21, 1e-7, 123456789.125, -0.5},
		"strings":  []string{"tab\tquote\"backslash\\", "ünïcödé", "\x01ctrl"},
		"nil":      nil,
		"pointer":  &writeJSONOmit{A: "p"},
		"snapshot": SnapshotEnvelope{GeneratedAt: "t", Devices: []DeviceSummary{{Address: "A<B"}}},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var want bytes.Buffer
			if err := json.NewEncoder(&want).Encode(v); err != nil {
				t.Fatalf("v1 encode: %v", err)
			}
			w := httptest.NewRecorder()
			JSON(w, 200, v)
			if got := w.Body.Bytes(); !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("response bytes changed\n got: %s\nwant: %s", got, want.Bytes())
			}
		})
	}
}
