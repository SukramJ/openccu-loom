// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func TestParameterDataSignature_StableAcrossCalls(t *testing.T) {
	t.Parallel()
	p := &hmproto.ParameterData{
		Type:       hmenum.ParameterTypeBool,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Unit:       "",
	}
	a := p.Signature()
	b := p.Signature()
	if a == "" {
		t.Fatal("Signature must not return the empty fallback for a well-formed ParameterData")
	}
	if len(a) != 16 {
		t.Fatalf("Signature length = %d, want 16 hex chars", len(a))
	}
	if a != b {
		t.Fatalf("Signature unstable across calls: %q vs %q", a, b)
	}
}

func TestParameterDataSignature_DetectsFieldChange(t *testing.T) {
	t.Parallel()
	a := (&hmproto.ParameterData{Type: hmenum.ParameterTypeBool}).Signature()
	b := (&hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}).Signature()
	if a == b {
		t.Fatalf("Signature must differ when fields differ; both = %q", a)
	}
}

func TestParameterDataSignature_HexShape(t *testing.T) {
	t.Parallel()
	sig := (&hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}).Signature()
	for _, c := range sig {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("Signature contains non-hex byte %q in %q", c, sig)
		}
	}
}
