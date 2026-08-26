// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve

import (
	"testing"
)

// TestIrrigationBaseDPMethodsExist verifies that Irrigation embeds custom.BaseDP
// and exposes its observability methods without panicking.
func TestIrrigationBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-FALMOT:1", nil)
	if v == nil {
		t.Skip("newTestIrrigation returned nil")
	}

	_, _ = v.ModifiedAt()
	_, _ = v.RefreshedAt()
	_ = v.UnconfirmedLastValuesSend()

	v.MarkModified()
	v.MarkRefreshed()

	if _, ok := v.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := v.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}

// TestModulatingBaseDPMethodsExist verifies that Modulating embeds custom.BaseDP
// and exposes its observability methods without panicking.
func TestModulatingBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "HmIP-FALMOT:1", nil)
	if v == nil {
		t.Skip("newTestModulating returned nil")
	}

	_, _ = v.ModifiedAt()
	_, _ = v.RefreshedAt()
	_ = v.UnconfirmedLastValuesSend()

	v.MarkModified()
	v.MarkRefreshed()

	if _, ok := v.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := v.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
