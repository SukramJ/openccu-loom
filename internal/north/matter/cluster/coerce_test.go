// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cluster

import "testing"

func TestAsUint8AcceptsDecoderWidths(t *testing.T) {
	for _, v := range []any{uint8(4), uint16(4), uint32(4), uint64(4), int(4), int64(4), float64(4)} {
		if got, ok := AsUint8(v); !ok || got != 4 {
			t.Errorf("AsUint8(%T %v) = (%d, %v); want (4, true)", v, v, got, ok)
		}
	}
	if _, ok := AsUint8("nope"); ok {
		t.Error("AsUint8(string) must return false")
	}
}

func TestAsInt16AcceptsDecoderWidths(t *testing.T) {
	for _, v := range []any{int16(2100), int32(2100), int64(2100), int(2100), uint64(2100), float64(2100)} {
		if got, ok := AsInt16(v); !ok || got != 2100 {
			t.Errorf("AsInt16(%T %v) = (%d, %v); want (2100, true)", v, v, got, ok)
		}
	}
	if _, ok := AsInt16([]byte{1}); ok {
		t.Error("AsInt16([]byte) must return false")
	}
}
