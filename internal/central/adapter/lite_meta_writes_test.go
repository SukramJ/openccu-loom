// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// concurrentEditor sits in front of the fake and edits the store right
// before the first `edits` PATCHes reach it, as another client writing in
// between our read and our write would.
type concurrentEditor struct {
	fake    *litefake.Fake
	next    http.Handler
	edits   int32
	patches atomic.Int32
}

func (c *concurrentEditor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		if n := c.patches.Add(1); n <= c.edits {
			_ = c.fake.Meta().Rename("HmIP-RF.VCU2128127", "edited elsewhere "+strconv.Itoa(int(n)))
		}
	}
	c.next.ServeHTTP(w, r)
}

func startEditedWriter(t *testing.T, edits int32) (*liteMetaWriter, *litefake.Fake, *concurrentEditor) {
	t.Helper()
	f := startTestFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
	target, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	editor := &concurrentEditor{fake: f, next: httputil.NewSingleHostReverseProxy(target), edits: edits}
	proxy := httptest.NewServer(editor)
	t.Cleanup(proxy.Close)
	client, err := occulited.New(occulited.Config{BaseURL: proxy.URL, Token: litefake.DefaultToken, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.SetFeatures(liteFeatures(occulited.ExpandScopes([]string{"*"})))
	p := &liteProfile{cc: *liteCentralFor(t, f, litefake.DefaultToken), client: client}
	if err := newLiteMetadata(p, unit, slog.New(slog.DiscardHandler)).resnapshot(context.Background()); err != nil {
		t.Fatalf("resnapshot: %v", err)
	}
	return &liteMetaWriter{client: client, unit: unit}, f, editor
}

// TestLiteIfMatchConflictRetriesOnce pins the conflict handling of an
// assignment write: a store that moved on between the read and the write
// answers 409, the write is redone once on a fresh read and lands; a
// second conflict in a row is the answer and nothing is written.
func TestLiteIfMatchConflictRetriesOnce(t *testing.T) {
	t.Parallel()
	t.Run("one conflict is retried", func(t *testing.T) {
		t.Parallel()
		w, f, editor := startEditedWriter(t, 1)
		if err := w.SetDeviceRooms(context.Background(), "VCU0000321:1", []string{"Erdgeschoss"}); err != nil {
			t.Fatalf("SetDeviceRooms after one conflict: %v", err)
		}
		if got := editor.patches.Load(); got != 2 {
			t.Errorf("%d PATCHes, want 2 (the conflicting one and the retry)", got)
		}
		got := slices.Sorted(slices.Values(f.Meta().Snapshot().Objects["BidCos-RF.VCU0000321:1"].Enums))
		if want := []string{"function/licht", "room/eg"}; !slices.Equal(got, want) {
			t.Errorf("store enums = %v, want %v", got, want)
		}
	})
	t.Run("a second conflict is returned", func(t *testing.T) {
		t.Parallel()
		w, f, editor := startEditedWriter(t, 2)
		err := w.SetDeviceRooms(context.Background(), "VCU0000321:1", []string{"Erdgeschoss"})
		if !errors.Is(err, occulited.ErrRevisionConflict) {
			t.Fatalf("err = %v, want ErrRevisionConflict", err)
		}
		if got := editor.patches.Load(); got != 2 {
			t.Errorf("%d PATCHes, want 2 — the write must stop after one retry", got)
		}
		if got := f.Meta().Snapshot().Objects["BidCos-RF.VCU0000321:1"].Enums; slices.Contains(got, "room/eg") {
			t.Errorf("store enums = %v: a conflicting write landed", got)
		}
	})
}

// TestLiteAssignCreatesAMissingObject pins the path for a channel the
// store holds no object for (the store keeps only named objects): the
// write creates it, carrying a name, instead of failing on the 404.
func TestLiteAssignCreatesAMissingObject(t *testing.T) {
	t.Parallel()
	w, f, _ := startEditedWriter(t, 0)
	const ref = "HmIP-RF.VCU2128127:1"
	if _, ok := f.Meta().Snapshot().Objects[ref]; ok {
		t.Fatalf("fixture drift: %s already exists, the test no longer reaches the create path", ref)
	}
	if err := w.SetDeviceFunctions(context.Background(), "VCU2128127:1", []string{"Licht"}); err != nil {
		t.Fatalf("SetDeviceFunctions: %v", err)
	}
	obj, ok := f.Meta().Snapshot().Objects[ref]
	if !ok {
		t.Fatalf("%s was not created", ref)
	}
	if obj.Name == "" || !slices.Equal(obj.Enums, []string{"function/licht"}) {
		t.Errorf("created object = name %q, enums %v; want a name and [function/licht]", obj.Name, obj.Enums)
	}
}
