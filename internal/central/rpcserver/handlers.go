// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Handlers is implemented by an object that wants to receive CCU
// callbacks. Both the XML-RPC and BIN-RPC listeners dispatch through
// this interface so the concrete listener is transport-agnostic.
//
// Every method returns an optional [*hmerr.XMLRPCFault] that the
// listener serialises back to the CCU when non-nil.
type Handlers interface {
	// Event is called once per parameter change.
	Event(ctx context.Context, interfaceID, channelAddress, parameter string, value xmlrpc.Value) error

	// NewDevices is called when the CCU announces a set of devices.
	NewDevices(ctx context.Context, interfaceID string, descriptions xmlrpc.ArrayValue) error

	// DeleteDevices is called when devices are removed.
	DeleteDevices(ctx context.Context, interfaceID string, addresses []string) error

	// UpdateDevice is called when an existing device's metadata changes.
	UpdateDevice(ctx context.Context, interfaceID, address string, hint int) error

	// ReplaceDevice is called when a device is swapped.
	ReplaceDevice(ctx context.Context, interfaceID, oldAddress, newAddress string) error

	// ReaddedDevice signals a device that was removed and is now back.
	ReaddedDevice(ctx context.Context, interfaceID string, addresses []string) error

	// ListDevices is the CCU's request for our current device list.
	ListDevices(ctx context.Context, interfaceID string) (xmlrpc.ArrayValue, error)

	// Error is the CCU's notification that a wire-level error occurred while
	// reaching one of our devices.
	//
	// `errorCode` is the CCU-side error class (negative integers follow the
	// XML-RPC fault convention). `msg` is the operator- readable explanation.
	// The handler is expected to log the error at WARNING and publish a
	// SystemEvent of type ERROR so health dashboards reflect the problem; the
	// CCU does not require a non-nil response.
	Error(ctx context.Context, interfaceID string, errorCode int, msg string) error
}

// ErrNoHandlers is returned when a callback arrives for an unknown
// central/interface.
var ErrNoHandlers = errors.New("rpcserver: no handlers registered")

// asFault collapses an arbitrary error into an XML-RPC fault.
func asFault(err error) *hmerr.XMLRPCFault {
	if fault, ok := errors.AsType[*hmerr.XMLRPCFault](err); ok {
		return fault
	}
	return &hmerr.XMLRPCFault{Code: -1, Message: err.Error()}
}
