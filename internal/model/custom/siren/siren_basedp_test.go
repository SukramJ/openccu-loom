// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestSirenBaseDPMethodsExist verifies that Siren embeds custom.BaseDP and
// exposes its observability methods without panicking.
func TestSirenBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:1", &stubWriter{}, custom.SirenCapabilities{})

	// Must compile and return zero values before any event.
	_, _ = r.siren.ModifiedAt()
	_, _ = r.siren.RefreshedAt()
	_ = r.siren.UnconfirmedLastValuesSend()

	r.siren.MarkModified()
	r.siren.MarkRefreshed()

	if _, ok := r.siren.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := r.siren.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
