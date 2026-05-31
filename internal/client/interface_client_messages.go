// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// MessageAcknowledger is the optional capability a backend can expose to
// acknowledge a CCU service/alarm message without going through the ReGa
// layer. Only backends with a JSON-RPC channel (CcuBackend) implement this.
// used as fallback when [Config.RegaRunner] is nil.
type MessageAcknowledger interface {
	AcknowledgeMessage(ctx context.Context, messageID string) (bool, error)
}

// AcknowledgeMessage acknowledges a CCU service or alarm message identified
// by messageID (the ReGa ISE-ID). Returns true when the CCU confirmed the
// acknowledgement, false otherwise.
//
// Resolution order : 1. [Config.RegaRunner] — preferred: ReGa-based
// acknowledgement via the rega.Runner wired in by the coordinator. 2. Backend
// fallback — when RegaRunner is nil but the backend implements
// [MessageAcknowledger] (i.e. CcuBackend with a JSON-RPC caller), delegates
// to the backend directly. 3. Returns (false, [hmerr.ErrUnsupported]) when
// neither path is available.
func (c *InterfaceClient) AcknowledgeMessage(ctx context.Context, messageID string, b backends.Operations) (bool, error) {
	if c.cfg.RegaRunner != nil {
		return c.cfg.RegaRunner.AcknowledgeMessage(ctx, messageID)
	}
	if b != nil {
		if ack, ok := b.(MessageAcknowledger); ok {
			return ack.AcknowledgeMessage(ctx, messageID)
		}
	}
	return false, hmerr.ErrUnsupported
}
