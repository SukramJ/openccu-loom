// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func key() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(hmenum.ParameterState),
	}
}

func TestDataCachePutGet(t *testing.T) {
	c := NewDataCache()
	c.Put(key(), true, time.Time{})
	e, ok := c.Get(key())
	if !ok || e.Value != true || e.ModifiedAt.IsZero() {
		t.Fatalf("entry=%+v", e)
	}
	if c.Len() != 1 {
		t.Fatalf("len=%d", c.Len())
	}
	c.Forget(key())
	if _, ok := c.Get(key()); ok {
		t.Fatal("forget must remove")
	}
}

func TestCommandCacheEchoSuppression(t *testing.T) {
	c := NewCommandCache()
	now := time.Now()
	c.now = func() time.Time { return now }

	c.Record(key(), true)
	if !c.IsEcho(key(), true) {
		t.Fatal("matching value within TTL must be echo")
	}
	if c.IsEcho(key(), true) {
		t.Fatal("second lookup must miss (entry consumed)")
	}

	c.Record(key(), true)
	c.now = func() time.Time { return now.Add(10 * time.Second) }
	if c.IsEcho(key(), true) {
		t.Fatal("expired entry must not be echo")
	}
}

func TestPingPongJournalLatest(t *testing.T) {
	j := NewPingPongJournal(10)
	sent := time.Now()
	j.RecordSent("HmIP-RF", sent)
	if _, ok := j.Latest("HmIP-RF"); ok {
		t.Fatal("unacked must not be latest")
	}
	if !j.RecordAck("HmIP-RF", sent.Add(50*time.Millisecond)) {
		t.Fatal("ack should pair")
	}
	e, ok := j.Latest("HmIP-RF")
	if !ok || e.Latency != 50*time.Millisecond {
		t.Fatalf("latest=%+v", e)
	}
}

func TestPingPongJournalCapacity(t *testing.T) {
	j := NewPingPongJournal(2)
	for range 5 {
		j.RecordSent("x", time.Now())
	}
	if len(j.Snapshot()) != 2 {
		t.Fatalf("snapshot len=%d", len(j.Snapshot()))
	}
}
