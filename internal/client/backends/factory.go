// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// FactoryInput bundles the transports the factory can wire. The
// factory picks the right ones based on the interface's [Kind].
type FactoryInput struct {
	XMLRPC    Caller
	BINRPC    Caller
	JSONRPC   Caller
	Announcer Announcer
}

// Factory returns the backend suited for iface. The Homegear variant is
// selected via [FactoryWithKind] — it cannot be derived from the interface
// ID alone (Homegear looks like a CCU interface to openccu-loom).
func Factory(iface hmenum.Interface, in FactoryInput) (Operations, error) {
	return FactoryWithKind(iface, KindFor(iface), in)
}

// FactoryWithKind constructs the backend for a known kind. Used by the
// detection layer (see [DetermineBackendKind]) and in tests.
func FactoryWithKind(iface hmenum.Interface, kind Kind, in FactoryInput) (Operations, error) {
	switch kind {
	case KindCCU:
		if in.XMLRPC == nil {
			return nil, fmt.Errorf("backends: %s requires XML-RPC caller", iface)
		}
		return NewCcuBackendForInterface(iface, in.XMLRPC, in.JSONRPC, in.Announcer), nil
	case KindCUxD:
		if in.BINRPC == nil {
			return nil, fmt.Errorf("backends: %s requires BIN-RPC caller", iface)
		}
		return NewCuxdBackend(in.BINRPC, in.Announcer), nil
	case KindHomegear:
		if in.XMLRPC == nil {
			return nil, fmt.Errorf("backends: %s requires XML-RPC caller", iface)
		}
		// Homegear is XML-RPC-only — JSON-RPC is not forwarded.
		return NewHomegearBackend(in.XMLRPC, in.Announcer), nil
	}
	return nil, fmt.Errorf("backends: no backend for %s", iface)
}

// MaybeInitialize calls [Initializer.Initialize] when the backend
// implements [Initializer], and returns nil otherwise. A soft probe
// failure is logged but not returned — conservative static defaults
// remain in effect. Callers should invoke this once after construction
// and before the first operation so probed capabilities are available.
func MaybeInitialize(ctx context.Context, b Operations) error {
	if init, ok := b.(Initializer); ok {
		return init.Initialize(ctx)
	}
	return nil
}
