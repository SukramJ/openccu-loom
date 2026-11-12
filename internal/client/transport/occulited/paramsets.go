// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
)

// MaxParamsetAddresses is how many addresses one paramset description
// request may name; [Client.ParamsetDescriptions] splits a longer list.
const MaxParamsetAddresses = 2000

// ParamsetDescriptions is the box's answer about the paramset
// descriptions of many addresses. The box keeps each description once per
// device model, firmware and channel type: Keys maps an address to the
// key of its description, Paramsets holds each description under its key
// (parameter id to the description, as the interface process gave it),
// and Errors names every address the process refused with its fault. An
// address is in Keys or in Errors.
type ParamsetDescriptions struct {
	Keys      map[string]string                     `json:"keys"`
	Paramsets map[string]map[string]json.RawMessage `json:"paramsets"`
	Errors    map[string]string                     `json:"errors"`
}

type paramsetsRequest struct {
	Addresses []string `json:"addresses"`
	Paramset  string   `json:"paramset"`
}

// ErrNoParamsetRoute reports a box that does not offer the paramset
// description route; the caller reads descriptions one by one instead.
var ErrNoParamsetRoute = errors.New("occulited: the box has no paramset description route")

// ParamsetDescriptions reads the descriptions of the paramset named
// paramset ("VALUES", "MASTER") for addresses on one interface, in as
// few requests as the box's limit allows (scope rpc:read). A box from
// before the route answers [ErrNoParamsetRoute].
func (c *Client) ParamsetDescriptions(ctx context.Context, iface string, addresses []string, paramset string) (ParamsetDescriptions, error) {
	out := ParamsetDescriptions{
		Keys: map[string]string{}, Paramsets: map[string]map[string]json.RawMessage{}, Errors: map[string]string{},
	}
	for len(addresses) > 0 {
		n := min(len(addresses), MaxParamsetAddresses)
		raw, err := marshal(paramsetsRequest{Addresses: addresses[:n], Paramset: paramset})
		if err != nil {
			return ParamsetDescriptions{}, err
		}
		part, err := call[ParamsetDescriptions](ctx, c, request{
			method: http.MethodPost, path: "/api/rpc/v1/paramsets/" + url.PathEscape(iface), body: raw,
		})
		if err != nil {
			// The box answers an unknown interface with its own code; a
			// 404 without it is a box that has no such route at all.
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code != CodeUnknownInterface {
				return ParamsetDescriptions{}, ErrNoParamsetRoute
			}
			return ParamsetDescriptions{}, err
		}
		maps.Copy(out.Keys, part.Keys)
		maps.Copy(out.Paramsets, part.Paramsets)
		maps.Copy(out.Errors, part.Errors)
		addresses = addresses[n:]
	}
	return out, nil
}
