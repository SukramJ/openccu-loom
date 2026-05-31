// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package boundary

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func TestExecuteSuccessIncrementsCount(t *testing.T) {
	reg := metrics.NewRegistry()
	m := Metrics{Count: reg.Counter("c_total", ""), LatencySecs: reg.Gauge("lat", "")}
	if err := Execute(context.Background(), Config{Name: "ok", Metrics: m}, func(_ context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("err=%v", err)
	}
	if m.Count.Value() != 1 {
		t.Fatalf("count=%d", m.Count.Value())
	}
}

func TestExecuteErrorIncrementsErrorCount(t *testing.T) {
	reg := metrics.NewRegistry()
	m := Metrics{Count: reg.Counter("c", ""), ErrorCount: reg.Counter("e", "")}
	boom := errors.New("boom")
	err := Execute(context.Background(), Config{Metrics: m}, func(_ context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if m.Count.Value() != 1 || m.ErrorCount.Value() != 1 {
		t.Fatalf("metrics=%+v", m)
	}
}

func TestExecuteRecoversPanic(t *testing.T) {
	reg := metrics.NewRegistry()
	m := Metrics{PanicCount: reg.Counter("p", "")}
	err := Execute(context.Background(), Config{Metrics: m}, func(_ context.Context) error {
		panic("x")
	})
	if !errors.Is(err, ErrPanic) {
		t.Fatalf("err=%v", err)
	}
	if m.PanicCount.Value() != 1 {
		t.Fatalf("panic count=%d", m.PanicCount.Value())
	}
}

func TestExecuteResultReturnsValueOnSuccess(t *testing.T) {
	got, err := ExecuteResult(context.Background(), Config{}, func(_ context.Context) (int, error) {
		return 42, nil
	})
	if err != nil || got != 42 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}

func TestExecuteResultPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	_, err := ExecuteResult(context.Background(), Config{}, func(_ context.Context) (int, error) {
		return 0, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteReRaisePanicFalseSwallows(t *testing.T) {
	f := false
	err := Execute(context.Background(), Config{ReRaisePanic: &f}, func(_ context.Context) error {
		panic("swallowed")
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
