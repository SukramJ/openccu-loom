// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

import (
	"math"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DiffResult summarises the comparison of an optimistic-sent value
// against what the CCU actually reports back.
type DiffResult struct {
	// Match is true when the values agree (typed, modulo float tolerance).
	Match bool
	// Kind is the kind of the compared values when both sides agree on
	// kind; ValueKindNone when Mismatch is caused by kind divergence.
	Kind hmtypes.ValueKind
	// RelDiff is the relative numeric difference for float kinds.
	// Zero for non-numeric kinds or when values match exactly.
	RelDiff float64
}

// FloatTolerance is the epsilon [Diff] applies to float values: two floats
// agree when |sent-got| / max(1, |sent|, |got|) <= FloatTolerance. It is the
// bound on what a round trip through a CCU transport can change, not a
// tuning knob:
//
//   - BidCos rfd XML-RPC and ReGa/tclrpc render a double with "%f", i.e.
//     exactly six decimals. ../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:65
//     declares `_doubleFormat("%f")`, consumed by doubleToXml (:591-594) and
//     doubleToText (:659-664); the only way to widen it, setDoubleFormat
//     (XmlRpcValue.h:151), has no caller anywhere under src/, so the default
//     is what ships. ../OpenCCU-Base/src/tclrpc/tclrpc.cpp:222 hard-codes the
//     same "%f". Absolute step 1e-6, rounding error at most 5e-7.
//   - BIN-RPC sends `int(frexp(v,&e) * 2^30)` (XmlRpcValue.cpp:624-637), a
//     truncating binary-mantissa form. frexp normalises the mantissa into
//     [0.5,1), so the error is bounded by 2^-29 ≈ 1.9e-9 relative — a
//     constant relative step, not a growing one.
//   - HmIP legacy XML-RPC does not quantise at all: the <double> element is
//     written with Java's String.valueOf, the shortest round-trip form
//     (HMIPServer de.eq3.cbcs.legacy.communication.rpc.internal.format.xml.XmlRpcSerializer;
//     symbol-level citation, not re-measured against a local source tree).
//
// The max(1, ...) denominator is why one epsilon covers all three: for
// |v| <= 1 it degrades to an absolute 1e-6, which covers %f's 5e-7; for
// |v| > 1 it stays relative, and %f's relative error only shrinks as |v|
// grows. The bounds are pinned by TestW2ParFloatToleranceCoversTransportQuantisation.
//
// What this constant is NOT: the daemon's optimistic-confirmation test. That
// decision is taken in internal/model/generic (valuesClose), which rounds
// both operands to two decimals before comparing — a much wider band for a
// different question, and it does not consult this constant. [Diff] and
// [FloatTolerance] are a comparison helper offered to callers that want the
// wire-quantisation bound; measured against the tree, no non-test .go file
// references either today, so tuning this value changes no shipped
// behaviour.
const FloatTolerance = 1e-6

// Diff compares sent against got and reports whether they effectively
// agree. For floats it uses [FloatTolerance]; for lists it does an
// element-wise comparison — over [hmtypes.ParamValue.List], which is
// []string, so no float ever reaches that branch.
func Diff(sent, got hmtypes.ParamValue) DiffResult {
	if sent.Kind != got.Kind {
		return DiffResult{Match: false, Kind: hmtypes.ValueKindNone}
	}
	switch sent.Kind {
	case hmtypes.ValueKindNone:
		return DiffResult{Match: true, Kind: hmtypes.ValueKindNone}
	case hmtypes.ValueKindBool:
		return DiffResult{Match: sent.Bool == got.Bool, Kind: hmtypes.ValueKindBool}
	case hmtypes.ValueKindInt:
		return DiffResult{Match: sent.Int == got.Int, Kind: hmtypes.ValueKindInt}
	case hmtypes.ValueKindFloat:
		return diffFloat(sent.Float, got.Float)
	case hmtypes.ValueKindString:
		return DiffResult{Match: sent.String == got.String, Kind: hmtypes.ValueKindString}
	case hmtypes.ValueKindList:
		if len(sent.List) != len(got.List) {
			return DiffResult{Match: false, Kind: hmtypes.ValueKindList}
		}
		for i := range sent.List {
			if sent.List[i] != got.List[i] {
				return DiffResult{Match: false, Kind: hmtypes.ValueKindList}
			}
		}
		return DiffResult{Match: true, Kind: hmtypes.ValueKindList}
	}
	return DiffResult{Match: false}
}

func diffFloat(sent, got float64) DiffResult {
	if sent == got {
		return DiffResult{Match: true, Kind: hmtypes.ValueKindFloat}
	}
	denom := math.Max(1, math.Max(math.Abs(sent), math.Abs(got)))
	rel := math.Abs(sent-got) / denom
	return DiffResult{
		Match:   rel <= FloatTolerance,
		Kind:    hmtypes.ValueKindFloat,
		RelDiff: rel,
	}
}
