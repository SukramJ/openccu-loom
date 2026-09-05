// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

// CombineUnsubs folds several change-subscription unsubscribers into a
// single closure that releases all of them. nil entries are skipped, so
// callers can pass the result of a data point's OnMatterValueChanged
// directly even when the underlying DP is absent (those return a no-op
// unsubscribe). Custom device types that project onto more than one Matter
// attribute use this to fan every state data point into one
// [contract.ChangeNotifier] and hand the bridge a single
// unsubscribe.
func CombineUnsubs(unsubs ...func()) func() {
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}
