// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"encoding/json"
	"strings"
	"sync"
)

// deviceSemantics mirrors embedded/device_semantics.json: curated
// device classifications shared with the reference stack through the
// upstream data package. Keys starting with "_" are documentation.
type deviceSemantics struct {
	DoorbellModels []string `json:"doorbell_models"`
}

var (
	semanticsOnce sync.Once
	doorbellSet   map[string]struct{}
)

// DoorbellModels returns the curated set of device models whose
// press/ring channel is a doorbell rather than a generic button.
// Consumers map the ring press of these devices onto their platform's
// doorbell semantics (e.g. Home Assistant's standard `ring` event
// type). Returns an empty set when the embedded document is missing
// or malformed — callers then fall back to generic button semantics.
func DoorbellModels() map[string]struct{} {
	semanticsOnce.Do(func() {
		doorbellSet = map[string]struct{}{}
		raw, err := embedded.ReadFile("embedded/device_semantics.json")
		if err != nil {
			return
		}
		var doc deviceSemantics
		if err := json.Unmarshal(raw, &doc); err != nil {
			return
		}
		for _, m := range doc.DoorbellModels {
			if m = strings.TrimSpace(m); m != "" {
				doorbellSet[m] = struct{}{}
			}
		}
	})
	return doorbellSet
}
