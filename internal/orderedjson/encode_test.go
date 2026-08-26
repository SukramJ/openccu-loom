// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package orderedjson

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestFormatFloatMatchesOrjson checks the float repr against a golden table
// captured from orjson 3.11 (OPT_INDENT_2). Each row is "<float64 bits as
// 16-hex>\t<orjson output>", so the exact bit pattern — not a lossy decimal —
// drives the comparison.
func TestFormatFloatMatchesOrjson(t *testing.T) {
	f, err := os.Open("testdata/float_golden.tsv")
	if err != nil {
		t.Fatalf("open golden: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			t.Fatalf("malformed golden row: %q", line)
		}
		bits, err := strconv.ParseUint(line[:tab], 16, 64)
		if err != nil {
			t.Fatalf("parse bits %q: %v", line[:tab], err)
		}
		want := line[tab+1:]
		got, err := formatFloat(math.Float64frombits(bits))
		if err != nil {
			t.Fatalf("formatFloat(%x): %v", bits, err)
		}
		if got != want {
			t.Errorf("formatFloat(%x): got %q, want %q", bits, got, want)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n < 100 {
		t.Fatalf("golden table too small (%d rows) — regenerate testdata", n)
	}
}

func TestMarshalScalarsAndStructure(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"empty_obj", NewObject(0), "{}"},
		{"empty_arr", Array{}, "[]"},
		{"null", nil, "null"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"int", int64(65535), "65535"},
		{"neg_int", int(-5), "-5"},
		{
			"floats_obj",
			NewObject(0).Set("MIN", 0.0).Set("MAX", 1.0).Set("STEP", 0.1).Set("SCI", 1e-7),
			"{\n  \"MIN\": 0.0,\n  \"MAX\": 1.0,\n  \"STEP\": 0.1,\n  \"SCI\": 1e-7\n}",
		},
		{
			"empty_members",
			NewObject(0).Set("M", NewObject(0)).Set("L", Array{}).Set("S", ""),
			"{\n  \"M\": {},\n  \"L\": [],\n  \"S\": \"\"\n}",
		},
		{
			"special_chars",
			NewObject(0).Set("name", "a<b>&c").Set("u", "café").Set("tab", "x\ty").Set("nl", "a\nb"),
			"{\n  \"name\": \"a<b>&c\",\n  \"u\": \"café\",\n  \"tab\": \"x\\ty\",\n  \"nl\": \"a\\nb\"\n}",
		},
		{
			"toplevel_list",
			Array{
				NewObject(0).Set("TYPE", "X").Set("CHILDREN", Array{"a", "b"}),
				NewObject(0).Set("TYPE", "Y").Set("CHILDREN", Array{}),
			},
			"[\n  {\n    \"TYPE\": \"X\",\n    \"CHILDREN\": [\n      \"a\",\n      \"b\"\n    ]\n  },\n  {\n    \"TYPE\": \"Y\",\n    \"CHILDREN\": []\n  }\n]",
		},
		{
			"control_escapes",
			NewObject(0).Set("c", "\x00\x01\x07\x08\x0c\x1f"),
			"{\n  \"c\": \"\\u0000\\u0001\\u0007\\b\\f\\u001f\"\n}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestMarshalRejectsNonFinite(t *testing.T) {
	for _, f := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		if _, err := Marshal(f); err == nil {
			t.Errorf("Marshal(%v): expected error, got nil", f)
		}
	}
}
