// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package diagnostics_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/diagnostics"
)

func TestManagerGet_MissingID_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	if _, err := mgr.Get("does-not-exist"); !errors.Is(err, diagnostics.ErrCaptureNotFound) {
		t.Fatalf("err = %v, want ErrCaptureNotFound", err)
	}
}

func TestManagerGet_EmptyID_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	if _, err := mgr.Get(""); !errors.Is(err, diagnostics.ErrCaptureNotFound) {
		t.Fatalf("err = %v, want ErrCaptureNotFound", err)
	}
}

func TestManagerGet_ActiveCapture_Returns(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := mgr.Get(sum.ID)
	if err != nil {
		t.Fatalf("Get(active): %v", err)
	}
	if got.ID != sum.ID {
		t.Fatalf("Get returned ID=%q, want %q", got.ID, sum.ID)
	}
}

func TestManagerGet_StoppedCapture_Returns(t *testing.T) {
	t.Parallel()
	mgr := diagnostics.NewManager(nil, nil)
	sum, err := mgr.Start(diagnostics.StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Stop(sum.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, err := mgr.Get(sum.ID)
	if err != nil {
		t.Fatalf("Get(stopped): %v", err)
	}
	if got.ID != sum.ID {
		t.Fatalf("Get returned ID=%q, want %q", got.ID, sum.ID)
	}
}
