// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproperty"
)

func TestKind_StringValues(t *testing.T) {
	cases := []struct {
		k    hmproperty.Kind
		want string
	}{
		{hmproperty.KindConfig, "config"},
		{hmproperty.KindInfo, "info"},
		{hmproperty.KindSimple, "simple"},
		{hmproperty.KindState, "state"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%q).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestAllKinds_HasFourMembers(t *testing.T) {
	if len(hmproperty.AllKinds) != 4 {
		t.Errorf("AllKinds has %d members, want 4", len(hmproperty.AllKinds))
	}
}

// ---- GetPropertyByKind / GetPropertyByLogContext ( / ) ----

type testPayloadStruct struct {
	Alpha string  `payload:"config,log_context"`
	Beta  float64 `payload:"state"`
	Gamma int     `payload:"info"`
	Delta string  `payload:"-"`
	plain string  //nolint:unused // unexported field kept to verify the reflection path skips it
}

func TestGetPropertyByKind_Config(t *testing.T) {
	obj := testPayloadStruct{Alpha: "hello", Beta: 1.5, Gamma: 3}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindConfig, false)
	if len(got) != 1 {
		t.Fatalf("GetPropertyByKind(config) len = %d, want 1; got %v", len(got), got)
	}
	if got["Alpha"] != "hello" {
		t.Errorf("Alpha = %v, want hello", got["Alpha"])
	}
}

func TestGetPropertyByKind_State(t *testing.T) {
	obj := testPayloadStruct{Alpha: "x", Beta: 2.7}
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindState, false)
	if len(got) != 1 {
		t.Fatalf("GetPropertyByKind(state) len = %d, want 1; got %v", len(got), got)
	}
	if got["Beta"] != 2.7 {
		t.Errorf("Beta = %v, want 2.7", got["Beta"])
	}
}

func TestGetPropertyByKind_LogContextOnly(t *testing.T) {
	obj := testPayloadStruct{Alpha: "a", Beta: 1.0, Gamma: 7}
	// Alpha is config+log_context; Beta and Gamma do not have log_context.
	got := hmproperty.GetPropertyByKind(obj, hmproperty.KindConfig, true)
	if len(got) != 1 {
		t.Fatalf("GetPropertyByKind(config, logCtxOnly) len = %d, want 1; got %v", len(got), got)
	}
	if got["Alpha"] != "a" {
		t.Errorf("Alpha = %v, want a", got["Alpha"])
	}
}

func TestGetPropertyByLogContext_CollectsLogContextFields(t *testing.T) {
	obj := testPayloadStruct{Alpha: "ctx", Beta: 0.0, Gamma: 0}
	got := hmproperty.GetPropertyByLogContext(obj)
	// Only Alpha has log_context=true.
	if len(got) != 1 {
		t.Fatalf("GetPropertyByLogContext len = %d, want 1; got %v", len(got), got)
	}
	if got["Alpha"] != "ctx" {
		t.Errorf("Alpha = %v, want ctx", got["Alpha"])
	}
}

func TestGetPropertyByKind_IgnoreDashTag(t *testing.T) {
	obj := testPayloadStruct{Delta: "should-not-appear"}
	// Delta is tagged "-" and must not appear in any kind.
	for _, k := range hmproperty.AllKinds {
		got := hmproperty.GetPropertyByKind(obj, k, false)
		if _, found := got["Delta"]; found {
			t.Errorf("Delta appeared under kind %s despite '-' tag", k)
		}
	}
}
