// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"
)

// TestChannelGroupsSortedByGroupNumber verifies that ChannelGroups returns
// entries in ascending GroupNumber order regardless of insertion order.
func TestChannelGroupsSortedByGroupNumber(t *testing.T) {
	d := newTestDevice(t)

	// Insert groups out of order.
	d.AddChannelToGroup(5, 5)
	d.AddChannelToGroup(5, 6)
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(3, 3)

	groups := d.ChannelGroups()
	if len(groups) != 3 {
		t.Fatalf("want 3 groups, got %d", len(groups))
	}

	wantNos := []struct {
		groupNo  int
		channels []int
	}{
		{1, []int{1, 2}},
		{3, []int{3}},
		{5, []int{5, 6}},
	}
	for i, w := range wantNos {
		g := groups[i]
		if g.GroupNumber != w.groupNo {
			t.Errorf("groups[%d].GroupNumber = %d, want %d", i, g.GroupNumber, w.groupNo)
		}
		if len(g.ChannelNumbers) != len(w.channels) {
			t.Errorf("groups[%d].ChannelNumbers = %v, want %v", i, g.ChannelNumbers, w.channels)
			continue
		}
		for j, cn := range w.channels {
			if g.ChannelNumbers[j] != cn {
				t.Errorf("groups[%d].ChannelNumbers[%d] = %d, want %d", i, j, g.ChannelNumbers[j], cn)
			}
		}
	}
}

// TestChannelGroupsChannelNumbersAreSorted verifies that ChannelNumbers within
// each group are sorted in ascending order regardless of insertion order.
func TestChannelGroupsChannelNumbersAreSorted(t *testing.T) {
	d := newTestDevice(t)

	// Insert channel numbers out of order within the same group.
	d.AddChannelToGroup(2, 9)
	d.AddChannelToGroup(2, 4)
	d.AddChannelToGroup(2, 7)

	groups := d.ChannelGroups()
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	want := []int{4, 7, 9}
	got := groups[0].ChannelNumbers
	if len(got) != len(want) {
		t.Fatalf("ChannelNumbers = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("ChannelNumbers[%d] = %d, want %d", i, got[i], v)
		}
	}
}
