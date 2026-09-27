// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmerr

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestFeatureUnavailableErrorPreservesLegacySentinel(t *testing.T) {
	t.Parallel()
	legacy := errors.New("no inbox accepter")
	var err error = &FeatureUnavailableError{
		Central: "c1",
		Feature: hmenum.FeatureHubInbox,
		Reason:  hmenum.FeatureReasonNotSupported,
		Legacy:  legacy,
	}
	wrapped := fmt.Errorf("accept: %w", err)

	if !errors.Is(wrapped, ErrFeatureUnavailable) {
		t.Error("errors.Is(err, ErrFeatureUnavailable) = false")
	}
	if !errors.Is(wrapped, legacy) {
		t.Error("the legacy sentinel no longer matches; callers branching on it would break")
	}
	var fe *FeatureUnavailableError
	if !errors.As(wrapped, &fe) || fe.Feature != hmenum.FeatureHubInbox {
		t.Errorf("errors.As lost the typed error: %+v", fe)
	}
	if errors.Is(errors.New("other"), ErrFeatureUnavailable) {
		t.Error("an unrelated error matched ErrFeatureUnavailable")
	}
}

func TestFeatureUnavailableErrorNamesTheMissingScope(t *testing.T) {
	t.Parallel()
	err := &FeatureUnavailableError{
		Central: "lite",
		Feature: hmenum.FeatureSystemReboot,
		Reason:  hmenum.FeatureReasonMissingScope,
		Scope:   "power",
	}
	msg := err.Error()
	for _, want := range []string{"lite", "system.reboot", "missing_scope", "power"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to mention %q", msg, want)
		}
	}
	if errors.Unwrap(err) != nil {
		t.Error("an error without a legacy cause must unwrap to nil")
	}
}

func TestScopeMissingErrorMatchesItsSentinel(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("call: %w", &ScopeMissingError{Scope: "rpc:operate", Operation: "setValue"})
	if !errors.Is(err, ErrScopeMissing) {
		t.Fatal("errors.Is(err, ErrScopeMissing) = false")
	}
	var sm *ScopeMissingError
	if !errors.As(err, &sm) || sm.Scope != "rpc:operate" {
		t.Fatalf("errors.As lost the scope: %+v", sm)
	}
	if !strings.Contains(err.Error(), "setValue") || !strings.Contains(err.Error(), "rpc:operate") {
		t.Errorf("Error() = %q", err.Error())
	}
	if errors.Is(&FeatureUnavailableError{}, ErrScopeMissing) {
		t.Error("an unrelated refusal matched ErrScopeMissing")
	}
}
