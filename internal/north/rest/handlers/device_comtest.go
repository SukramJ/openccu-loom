// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

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
		result, err := svc.TestDeviceCommunication(r.Context(), addr)
		if err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Communication test not supported on this interface", ""))
				return
			}
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
