// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmerr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestGroupMembersNotAssignedErrorNamesTheMembers pins what the REST layer
// relies on: the typed error matches its sentinel through a wrap, keeps
// the member ids for the caller, and names them in its message.
func TestGroupMembersNotAssignedErrorNamesTheMembers(t *testing.T) {
	t.Parallel()
	var err error = &GroupMembersNotAssignedError{Members: []string{"0000000000AA01:1", "0000000000BB02:9"}}
	wrapped := fmt.Errorf("create: %w", err)

	if !errors.Is(wrapped, ErrGroupMembersNotAssigned) {
		t.Error("errors.Is(err, ErrGroupMembersNotAssigned) = false")
	}
	if errors.Is(wrapped, ErrGroupNotFound) {
		t.Error("the error matched an unrelated sentinel")
	}
	ge, ok := errors.AsType[*GroupMembersNotAssignedError](wrapped)
	if !ok || len(ge.Members) != 2 || ge.Members[1] != "0000000000BB02:9" {
		t.Fatalf("errors.AsType lost the members: %+v (ok %v)", ge, ok)
	}
	msg := wrapped.Error()
	for _, id := range ge.Members {
		if !strings.Contains(msg, id) {
			t.Errorf("message %q does not name member %s", msg, id)
		}
	}
	if errors.Is(errors.New("other"), ErrGroupMembersNotAssigned) {
		t.Error("an unrelated error matched ErrGroupMembersNotAssigned")
	}
}
