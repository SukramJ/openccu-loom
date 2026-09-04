// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter_test

import (
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2ParQuantisePercentF reproduces what the CCU's XML-RPC and tclrpc encoders
// do to a double: snprintf with "%f", i.e. exactly six decimals, rounded
// (../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:65 with :591-594 and
// :659-664; ../OpenCCU-Base/src/tclrpc/tclrpc.cpp:222). Go's %f has the same
// six-decimal default, so the fixture is the firmware's own format string
// rather than a hand-written approximation of it.
func w2ParQuantisePercentF(t *testing.T, v float64) float64 {
	t.Helper()
	got, err := strconv.ParseFloat(fmt.Sprintf("%f", v), 64)
	if err != nil {
		t.Fatalf("re-parsing the %%f rendering of %v failed: %v", v, err)
	}
	return got
}

// w2ParQuantiseBinRPC reproduces the BIN-RPC double encoding: the mantissa is
// normalised into [0.5,1) by frexp, scaled by 2^30 and TRUNCATED to an int
// (XmlRpcValue.cpp:624-637), then decoded again with ldexp (:605-620).
func w2ParQuantiseBinRPC(v float64) float64 {
	frac, exp := math.Frexp(v)
	mantissa := int32(frac * float64(int(1)<<30))
	return math.Ldexp(float64(mantissa)/float64(int(1)<<30), exp)
}

// TestW2ParFloatToleranceCoversTransportQuantisation pins parameter.FloatTolerance
// to the thing its doc comment claims it is: the bound on what a CCU
// transport can do to a float in transit. A tolerance tightened below that
// bound reports a faithful echo as a mismatch; the cases below are the two
// encoders that quantise at all, run over magnitudes on both sides of the
// max(1, ...) denominator's hinge.
//
// The third transport, HmIP legacy XML-RPC, quantises not at all, so it needs
// no case here — it is covered by any tolerance whatsoever.
func TestW2ParFloatToleranceCoversTransportQuantisation(t *testing.T) {
	t.Parallel()

	// Values chosen so that %f actually changes them: each has more than six
	// decimals, and they straddle |v| = 1 where the denominator switches from
	// absolute to relative.
	values := []float64{0.0000004, 0.1234567891, 0.9999999, 4.9999995, 21.5000004, 1234.5678912345}

	for _, sent := range values {
		echoed := w2ParQuantisePercentF(t, sent)
		if r := parameter.Diff(hmtypes.FloatValue(sent), hmtypes.FloatValue(echoed)); !r.Match {
			t.Errorf("a faithful CCU echo of %v is reported as a mismatch: the firmware renders it as %v "+
				"with the \"%%f\" format string it hard-codes, rel=%g, but FloatTolerance is %g",
				sent, echoed, r.RelDiff, parameter.FloatTolerance)
		}

		binEchoed := w2ParQuantiseBinRPC(sent)
		if r := parameter.Diff(hmtypes.FloatValue(sent), hmtypes.FloatValue(binEchoed)); !r.Match {
			t.Errorf("a faithful BIN-RPC echo of %v is reported as a mismatch: the firmware truncates the "+
				"frexp mantissa to %v, rel=%g, but FloatTolerance is %g",
				sent, binEchoed, r.RelDiff, parameter.FloatTolerance)
		}
	}

	// The other direction, so the guard cannot pass by matching everything: a
	// difference an order of magnitude above the widest quantisation step is
	// a real disagreement and must not be absorbed.
	for _, sent := range values {
		drifted := sent + 1e-5*math.Max(1, math.Abs(sent))
		if r := parameter.Diff(hmtypes.FloatValue(sent), hmtypes.FloatValue(drifted)); r.Match {
			t.Errorf("a %v → %v difference is ten times the widest transport quantisation step and must be "+
				"reported as a mismatch, rel=%g", sent, drifted, r.RelDiff)
		}
	}
}
