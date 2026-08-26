// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestValidateStartupPasses(t *testing.T) {
	if err := ValidateStartup(); err != nil {
		t.Fatalf("ValidateStartup() unexpected error: %v", err)
	}
}

func TestAllDataPointCategoriesCoversKnownCount(t *testing.T) {
	// AllDataPointCategories must not include Undefined (that's in
	// BlockedDataPointCategories and intentionally excluded from the
	// exhaustive slice because it is a sentinel, not a real category).
	for _, c := range AllDataPointCategories {
		if c == DataPointCategoryUndefined {
			t.Error("AllDataPointCategories must not include DataPointCategoryUndefined")
		}
	}
}

func TestHubDataPointCategoriesAreSubset(t *testing.T) {
	// Every hub category must also be present in CategoryToType because
	// hub DPs still carry a type for north-bound routing.
	for c := range HubDataPointCategories {
		if _, ok := CategoryToType[c]; !ok {
			t.Errorf("HubDataPointCategories member %s missing from CategoryToType", c)
		}
	}
}

func TestBlockedCategoryUndefinedPresent(t *testing.T) {
	if _, ok := BlockedDataPointCategories[DataPointCategoryUndefined]; !ok {
		t.Error("BlockedDataPointCategories must contain DataPointCategoryUndefined")
	}
}
