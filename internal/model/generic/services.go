// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import "github.com/SukramJ/openccu-loom/internal/payload"

// Service-method param decoders are unexported aliases to the public
// helpers in [internal/payload]. The aliases keep the per-wrapper
// constructors short (e.g. `paramBool` instead of `payload.ParamBool`)
// while sharing the implementation across the codebase.
var (
	paramBool    = payload.ParamBool
	paramFloat64 = payload.ParamFloat64
	paramInt32   = payload.ParamInt32
	paramString  = payload.ParamString
)
