// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package linkprofile

import (
	"errors"
	"testing"
)

// TestLoad_UnknownReceiverTypeIsStableAcrossCalls pins that load answers the
// same way on every call for a receiver type with no archive. The first
// call misses the archive and populates the negative-result cache; a
// second call must read that cache and produce the identical (bucket,
// error) pair rather than a cache hit that silently drops the error.
//
// This is a white-box test: the inconsistency lives entirely inside load,
// and every exported caller happens to normalise a nil bucket to the same
// externally-observable result regardless of whether err is set, which
// would hide a regression here.
func TestLoad_UnknownReceiverTypeIsStableAcrossCalls(t *testing.T) {
	t.Parallel()
	s := New()

	bucket1, err1 := s.load("NONEXISTENT_RECEIVER_TYPE_XYZ")
	if !errors.Is(err1, ErrUnsupported) {
		t.Fatalf("first call: expected ErrUnsupported, got %v", err1)
	}
	if bucket1 != nil {
		t.Fatalf("first call: expected nil bucket, got %v", bucket1)
	}

	bucket2, err2 := s.load("NONEXISTENT_RECEIVER_TYPE_XYZ")
	if !errors.Is(err2, ErrUnsupported) {
		t.Fatalf("second call: expected ErrUnsupported, got %v", err2)
	}
	if bucket2 != nil {
		t.Fatalf("second call: expected nil bucket, got %v", bucket2)
	}
}
