// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"net/http"
	"net/url"
)

// Add-on notices: what an add-on says on the box's Status page, beside
// the system's own warnings. The routes are open and take the add-on's
// control token as a Bearer — not an API token and not a session. The
// token names the add-on, and reaches that add-on's notices alone. The
// box mints it anew at every start of its service, so each call here
// carries the token it is given and nothing is kept.

const noticesBase = systemBase + "/addon-notices"

// Notice severities.
const (
	NoticeError   = "error"
	NoticeWarning = "warning"
	NoticeInfo    = "info"
)

// CodeTooManyNotices answers a new notice past the number an add-on may
// hold (409).
const CodeTooManyNotices = "too-many"

// NoticeText is a notice's title or text. The box requires both
// languages.
type NoticeText struct {
	DE string `json:"de"`
	EN string `json:"en"`
}

// Notice is one add-on notice as the box holds it.
type Notice struct {
	// ID is the add-on's own name for the notice: a-z, 0-9, ".", "_" and
	// "-", starting with a letter or digit, at most 64 characters.
	ID       string      `json:"id"`
	Severity string      `json:"severity"`
	Title    NoticeText  `json:"title"`
	Text     *NoticeText `json:"text,omitempty"`
	// Link is an in-app path of the box's shell; empty leads to the
	// add-on's settings page.
	Link string `json:"link,omitempty"`
}

// noticeWrite is the body of a notice post: a [Notice] without its id,
// which is the path's.
type noticeWrite struct {
	Severity string      `json:"severity"`
	Title    NoticeText  `json:"title"`
	Text     *NoticeText `json:"text,omitempty"`
	Link     string      `json:"link,omitempty"`
}

// NoticeList is GET /addon-notices: the add-on the token names, how many
// notices it may hold, and the ones it holds in posting order.
type NoticeList struct {
	Addon   string   `json:"addon"`
	Max     int      `json:"max"`
	Notices []Notice `json:"notices"`
}

// controlRequest is a request that carries an add-on control token in
// place of the client's own credential.
func controlRequest(ctx context.Context, controlToken string, r request) (context.Context, request) {
	r.header = http.Header{"Authorization": {"Bearer " + controlToken}}
	return withoutCredential(ctx), r
}

// AddonNotices lists the notices of the add-on controlToken names.
func (c *Client) AddonNotices(ctx context.Context, controlToken string) (NoticeList, error) {
	ctx, r := controlRequest(ctx, controlToken, request{method: http.MethodGet, path: noticesBase})
	return call[NoticeList](ctx, c, r)
}

// PutAddonNotice posts n, or replaces the notice of the same id. The box
// keeps a replaced notice's first posting time and an administrator's
// silence of it.
func (c *Client) PutAddonNotice(ctx context.Context, controlToken string, n Notice) error {
	raw, err := marshal(noticeWrite{Severity: n.Severity, Title: n.Title, Text: n.Text, Link: n.Link})
	if err != nil {
		return err
	}
	ctx, r := controlRequest(ctx, controlToken, request{
		method: http.MethodPut, path: noticesBase + "/" + url.PathEscape(n.ID), body: raw,
	})
	_, err = call[Notice](ctx, c, r)
	return err
}

// DeleteAddonNotice clears the notice id. Clearing one the add-on does
// not hold is an [*APIError] with status 404.
func (c *Client) DeleteAddonNotice(ctx context.Context, controlToken, id string) error {
	raw, err := marshal(emptyBody{})
	if err != nil {
		return err
	}
	ctx, r := controlRequest(ctx, controlToken, request{
		method: http.MethodDelete, path: noticesBase + "/" + url.PathEscape(id), body: raw,
	})
	resp, err := c.send(ctx, c.calls, r)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
