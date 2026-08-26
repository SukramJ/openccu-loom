// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// comTestResponseMargin is the slice of the request deadline the handler
// keeps for itself so the poll can end before the request context does.
// The CCU-side poll window and the router's request timeout are both 30s;
// without the margin the request deadline always wins and the documented
// timed-out outcome is unreachable.
const comTestResponseMargin = 2 * time.Second

// comTestPollContext derives the poll context from the request context,
// reserving comTestResponseMargin for writing the response. Without a
// request deadline the poll runs against the backend's own window.
func comTestPollContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := parent.Deadline()
	if !ok {
		return context.WithCancel(parent)
	}
	budget := time.Until(deadline) - comTestResponseMargin
	if budget <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, budget)
}

// DeviceCommunicationTestPort runs the CCU's per-device communication /
// function test. *adapter.DeviceAdminDomain satisfies it.
type DeviceCommunicationTestPort interface {
	TestDeviceCommunication(ctx context.Context, address string) (hmapi.CommunicationTestResult, error)
}

// TestDeviceCommunication serves `POST /devices/{addr}/test`: it asks the
// CCU to send a radio test frame to the device and reports whether the
// device answered within the poll window. Interfaces without a radio
// (VirtualDevices, CUxD) answer 422. The call blocks until the test
// completes or the window elapses, then returns the result.
func TestDeviceCommunication(svc DeviceCommunicationTestPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device communication test unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		started := time.Now()
		ctx, cancel := comTestPollContext(r.Context())
		defer cancel()
		result, err := svc.TestDeviceCommunication(ctx, addr)
		switch {
		case err == nil:
		case errors.Is(err, backends.ErrUnsupported):
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Communication test not supported on this interface", ""))
			return
		case errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
			// The poll budget elapsed while the request was still alive:
			// the CCU ran the test and the device did not answer in time.
			// That is the documented timed-out outcome, not an upstream
			// failure.
			result = hmapi.CommunicationTestResult{
				StartedAt:  started,
				DurationMs: time.Since(started).Milliseconds(),
				TimedOut:   true,
			}
		default:
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Communication test failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceCommunicationTest,
				DeviceAddress: addr,
				Note:          "communication test",
			})
		}
		JSON(w, http.StatusOK, result)
	}
}
