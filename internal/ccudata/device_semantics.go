// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import openccudata "github.com/SukramJ/go-openccu-data"

// DoorbellModels returns the curated set of device models whose
// press/ring channel is a doorbell rather than a generic button
// (shared device-semantics classification of the data-artifact
// module; the reference stack reads the same list). Consumers map
// the ring press onto their platform's doorbell semantics (e.g. Home
// Assistant's standard `ring` event type). Empty when the document is
// missing or malformed — callers fall back to button semantics.
func DoorbellModels() map[string]struct{} {
	return openccudata.DoorbellModels()
}
