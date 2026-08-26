// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Writer is the outbound-command contract every combined data point
// needs. Identical in shape to the custom / generic writers — callers
// can pass the same implementation.
type Writer interface {
	SetValue(
		ctx context.Context,
		channelAddress string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}
