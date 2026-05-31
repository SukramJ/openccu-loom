// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func TestRetryClassifiesXMLRPCFaultCodes(t *testing.T) {
	// The local -2 (timeout) is also kept retryable because some transports
	// surface generic timeouts as a -2 fault.
	cases := []struct {
		name     string
		code     int
		retryAtt int
	}{
		{"unreach is retryable", -1, 3},
		{"timeout is retryable", -2, 3},
		{"duty_cycle is retryable", -8, 3},
		{"device_out_of_range is retryable", -9, 3},
		{"transmission_pending is retryable", -10, 3},
		{"unknown code is permanent", -42, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRetrier(RetryConfig{
				MaxAttempts:              3,
				Initial:                  1 * time.Millisecond,
				Max:                      2 * time.Millisecond,
				Multiplier:               2,
				DutyCycleDelay:           1 * time.Millisecond,
				TransmissionPendingDelay: 1 * time.Millisecond,
			})
			attempts := 0
			err := r.Do(context.Background(), func(_ context.Context, _ int) error {
				attempts++
				return &hmerr.XMLRPCFault{Code: c.code, Message: "test"}
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if attempts != c.retryAtt {
				t.Fatalf("attempts = %d, want %d", attempts, c.retryAtt)
			}
		})
	}
}

func TestRetrySpecialDelayDutyCycle(t *testing.T) {
	// Verify the duty-cycle path takes the *fixed* delay and ignores
	// the exponential schedule. We use a marker delay that is far
	// from any plausible exponential value.
	r := NewRetrier(RetryConfig{
		MaxAttempts:    2,
		Initial:        1 * time.Microsecond,
		Max:            1 * time.Microsecond,
		Multiplier:     2,
		DutyCycleDelay: 100 * time.Millisecond,
	})
	start := time.Now()
	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return &hmerr.XMLRPCFault{Code: -8, Message: "duty cycle"}
	})
	elapsed := time.Since(start)
	if elapsed < 80*time.Millisecond {
		t.Fatalf("duty-cycle delay not honoured: elapsed=%v want >=80ms", elapsed)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("duty-cycle delay too generous: elapsed=%v want <=300ms", elapsed)
	}
}

func TestRetrySpecialDelayTransmissionPending(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts:              2,
		Initial:                  1 * time.Microsecond,
		Max:                      1 * time.Microsecond,
		Multiplier:               2,
		TransmissionPendingDelay: 50 * time.Millisecond,
	})
	start := time.Now()
	_ = r.Do(context.Background(), func(_ context.Context, _ int) error {
		return &hmerr.XMLRPCFault{Code: -10, Message: "transmission pending"}
	})
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("transmission-pending delay not honoured: elapsed=%v want >=40ms", elapsed)
	}
}

func TestRetryStillSurfacesAuthFailureNonRetryable(t *testing.T) {
	r := NewRetrier(RetryConfig{
		MaxAttempts: 3,
		Initial:     1 * time.Millisecond,
		Max:         2 * time.Millisecond,
		Multiplier:  2,
	})
	attempts := 0
	err := r.Do(context.Background(), func(_ context.Context, _ int) error {
		attempts++
		return hmerr.ErrAuthFailure
	})
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("unexpected err: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("auth failure must short-circuit; got %d attempts", attempts)
	}
}

func TestXMLRPCFaultIsRetryableHelpers(t *testing.T) {
	f := &hmerr.XMLRPCFault{Code: -8}
	if !f.IsRetryable() {
		t.Fatal("DUTY_CYCLE must be retryable")
	}
	if f.FaultCode() != hmerr.XMLRPCFaultDutyCycle {
		t.Fatalf("FaultCode mismatch: got %v", f.FaultCode())
	}
	g := &hmerr.XMLRPCFault{Code: -9}
	if !g.IsRetryable() {
		t.Fatal("DEVICE_OUT_OF_RANGE must be retryable (aiohomematic parity)")
	}
	if g.FaultCode() != hmerr.XMLRPCFaultDeviceOutOfRange {
		t.Fatalf("FaultCode mismatch: got %v", g.FaultCode())
	}
	h := &hmerr.XMLRPCFault{Code: -10}
	if !h.IsRetryable() {
		t.Fatal("TRANSMISSION_PENDING must be retryable (aiohomematic parity)")
	}
	if h.FaultCode() != hmerr.XMLRPCFaultTransmissionPending {
		t.Fatalf("FaultCode mismatch: got %v", h.FaultCode())
	}
}

func TestXMLRPCFaultUnknownCodeDefaultsPermanent(t *testing.T) {
	for _, code := range []int{-3, -4, -5, -7, 1, 99} {
		f := &hmerr.XMLRPCFault{Code: code}
		if f.IsRetryable() {
			t.Fatalf("code %d should default to permanent", code)
		}
	}
}
