// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import "testing"

// TestNoMethodIsBothReadAndWrite pins that the two tables stay disjoint. A
// method listed in both is classified by whichever lookup runs first, which
// makes the stated contract of each table unenforceable — and the contract is
// the whole point: readMethods is what the project's own rule for the
// developer's live CCU treats as free of consequence.
func TestNoMethodIsBothReadAndWrite(t *testing.T) {
	t.Parallel()

	for m := range readMethods {
		if _, both := writeMethods[m]; both {
			t.Errorf("%q is listed as both a read and a write", m)
		}
		if _, both := controlMethods[m]; both {
			t.Errorf("%q is listed as both a read and a control method", m)
		}
	}
	for m := range writeMethods {
		if _, both := controlMethods[m]; both {
			t.Errorf("%q is listed as both a write and a control method", m)
		}
	}
}
