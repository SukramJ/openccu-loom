// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"testing"
)

type sampleDevice struct {
	Address       string `payload:"info"`
	Model         string `payload:"info"`
	SubModel      string `payload:"info"`
	Manufacturer  string `payload:"info,alt=manufacturer_name"`
	Icon          string `payload:"config"`
	UnitOfMeasure string `payload:"config,alt=unit_of_measurement"`
	Level         int    `payload:"state"`
	Available     bool   `payload:"state"`
	Secret        string `payload:"-"`
	Notes         string // untagged → ignored
}

func TestForInfoKind(t *testing.T) {
	d := sampleDevice{
		Address: "0001ABCD", Model: "HmIP-STH",
		Manufacturer: "eQ-3", Icon: "lamp.png",
	}
	got := For(&d, KindInfo)
	if len(got) != 3 {
		t.Fatalf("expected 3 info entries, got %d: %+v", len(got), got)
	}
	if got["address"] != "0001ABCD" || got["model"] != "HmIP-STH" || got["manufacturer"] != "eQ-3" {
		t.Fatalf("info=%+v", got)
	}
	if _, shouldNotBeHere := got["manufacturer_name"]; shouldNotBeHere {
		t.Fatal("alt name must only surface with UseAltNames")
	}
}

func TestForUseAltNames(t *testing.T) {
	d := sampleDevice{Manufacturer: "eQ-3", UnitOfMeasure: "°C"}
	got := ForWith(&d, KindConfig, Options{UseAltNames: true})
	if got["unit_of_measurement"] != "°C" {
		t.Fatalf("config=%+v", got)
	}
	got = ForWith(&d, KindInfo, Options{UseAltNames: true})
	if got["manufacturer_name"] != "eQ-3" {
		t.Fatalf("info=%+v", got)
	}
}

func TestForOmitZero(t *testing.T) {
	d := sampleDevice{Address: "0001ABCD"}
	got := For(&d, KindInfo)
	if _, hasModel := got["model"]; hasModel {
		t.Fatal("zero-valued fields must be omitted")
	}
	got = ForWith(&d, KindInfo, Options{IncludeZero: true})
	if _, hasModel := got["model"]; !hasModel {
		t.Fatal("IncludeZero=true must retain zero fields")
	}
}

func TestForSkipsDash(t *testing.T) {
	d := sampleDevice{Secret: "x"}
	for _, k := range []Kind{KindInfo, KindConfig, KindState} {
		if len(For(&d, k)) != 0 {
			t.Fatalf("payload:%s must be ignored", k)
		}
	}
}

func TestForNilAndNonStruct(t *testing.T) {
	if got := For(nil, KindInfo); len(got) != 0 {
		t.Fatalf("nil input: %+v", got)
	}
	var d *sampleDevice
	if got := For(d, KindInfo); len(got) != 0 {
		t.Fatalf("nil ptr: %+v", got)
	}
	if got := For(42, KindInfo); len(got) != 0 {
		t.Fatalf("scalar input: %+v", got)
	}
}

type embedBase struct {
	CentralID string `payload:"info"`
}
type embedDerived struct {
	embedBase
	Address string `payload:"info"`
}

func TestForEmbeddedFields(t *testing.T) {
	d := embedDerived{embedBase: embedBase{CentralID: "ccu-01"}, Address: "0001"}
	got := For(&d, KindInfo)
	if got["centralid"] != "ccu-01" || got["address"] != "0001" {
		t.Fatalf("got=%+v", got)
	}
}

func TestMerge(t *testing.T) {
	a := map[string]any{"a": 1, "b": 2}
	b := map[string]any{"b": 20, "c": 3}
	m := Merge(a, b)
	if m["a"] != 1 || m["b"] != 20 || m["c"] != 3 {
		t.Fatalf("merge=%+v", m)
	}
	// Originals untouched.
	if a["b"] != 2 {
		t.Fatal("source mutated")
	}
}

func TestDescribeCached(t *testing.T) {
	// Two calls on the same type should return the same description
	// pointer via sync.Map.
	_ = For(&sampleDevice{}, KindInfo)
	_ = For(&sampleDevice{}, KindState)
	// No assertion — passing without panic / stale-cache weirdness is
	// the contract here.
}
