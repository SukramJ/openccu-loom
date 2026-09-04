// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// decodeErrorCode reads the error_code argument of the CCU's error()
// callback, which arrives as either an integer or a stringified integer
// depending on the firmware. Both callback transports (XML-RPC over HTTP
// and CUxD's BIN-RPC channel) carry the same argument, so both decode it
// here. A value that is neither yields 0.
func decodeErrorCode(v xmlrpc.Value) int {
	if i, err := xmlrpc.AsInt(v); err == nil {
		return i
	}
	var code int
	if s, err := xmlrpc.AsString(v); err == nil {
		_, _ = fmt.Sscanf(s, "%d", &code)
	}
	return code
}
