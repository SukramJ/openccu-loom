// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

// TestAddonNoticesRoundTrip pins the notice calls against the box's
// rules: a notice is posted, read back, replaced and cleared with the
// add-on's control token, and clearing one the add-on does not hold is a
// 404.
func TestAddonNoticesRoundTrip(t *testing.T) {
	f := startFake(t, litefake.Options{})
	// The client's own token is an API token; the notice calls must not
	// send it.
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	tok := litefake.DefaultAddonControlToken

	n := occulited.Notice{
		ID: "health.mqtt", Severity: occulited.NoticeWarning,
		Title: occulited.NoticeText{DE: "MQTT eingeschränkt", EN: "MQTT degraded"},
	}
	if err := c.PutAddonNotice(ctx, tok, n); err != nil {
		t.Fatalf("PutAddonNotice: %v", err)
	}
	list, err := c.AddonNotices(ctx, tok)
	if err != nil {
		t.Fatalf("AddonNotices: %v", err)
	}
	if list.Addon != litefake.DefaultAddonID || list.Max != 20 || len(list.Notices) != 1 ||
		list.Notices[0].ID != n.ID || list.Notices[0].Severity != n.Severity || list.Notices[0].Title != n.Title {
		t.Errorf("list = %+v, want the one notice as posted", list)
	}

	n.Severity = occulited.NoticeError
	if err := c.PutAddonNotice(ctx, tok, n); err != nil {
		t.Fatalf("replacing PutAddonNotice: %v", err)
	}
	if got := f.AddonNotices(); len(got) != 1 || got[0].Severity != occulited.NoticeError {
		t.Errorf("box holds %+v, want the one notice replaced", got)
	}

	if err := c.DeleteAddonNotice(ctx, tok, n.ID); err != nil {
		t.Fatalf("DeleteAddonNotice: %v", err)
	}
	var apiErr *occulited.APIError
	if err := c.DeleteAddonNotice(ctx, tok, n.ID); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("second delete: %v, want a 404", err)
	}
}

// TestAddonNoticesCarryTheControlTokenOnly pins the credential: the
// client's own API token does not open the notices, and a control token
// the box replaced is refused.
func TestAddonNoticesCarryTheControlTokenOnly(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	var apiErr *occulited.APIError
	if _, err := c.AddonNotices(ctx, litefake.DefaultToken); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("listing with an API token: %v, want a 401", err)
	}
	f.SetAddonControlToken("after-restart")
	if _, err := c.AddonNotices(ctx, litefake.DefaultAddonControlToken); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("listing with a replaced control token: %v, want a 401", err)
	}
	if _, err := c.AddonNotices(ctx, "after-restart"); err != nil {
		t.Errorf("listing with the new control token: %v", err)
	}
}
