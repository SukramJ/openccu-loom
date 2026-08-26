// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeGroupProvider implements both the flat and the grouped candidate
// surface, as the production adapter does.
type fakeGroupProvider struct {
	flat   map[string][]string
	groups map[string][]visibility.CandidateGroup
	// lastParamsets records what the handler asked for.
	lastParamsets []hmenum.ParamsetKey
}

func (f *fakeGroupProvider) UnIgnoreCandidates(centralName string, paramset hmenum.ParamsetKey) []string {
	if paramset == hmenum.ParamsetKeyMaster {
		return nil
	}
	return append([]string(nil), f.flat[centralName]...)
}

func (f *fakeGroupProvider) UnIgnoreCandidateGroups(centralName string, paramsets []hmenum.ParamsetKey) []visibility.CandidateGroup {
	f.lastParamsets = paramsets
	return append([]visibility.CandidateGroup(nil), f.groups[centralName]...)
}

// fakeLabeler resolves a single parameter so the label plumbing is
// observable without the embedded translation catalogue.
type fakeLabeler struct{ byParameter map[string]string }

func (f *fakeLabeler) ParameterLabel(parameter string) string {
	return f.byParameter[parameter]
}

func valuesGroup(parameter, model string, channels []int, devices int, reasons ...visibility.HiddenReason) visibility.CandidateGroup {
	scope := visibility.CandidateModelScope{
		Model:           model,
		Channels:        channels,
		Devices:         devices,
		WildcardPattern: visibility.WildcardPattern(parameter, hmenum.ParamsetKeyValues, model),
		ChannelPatterns: map[int]string{},
	}
	for _, ch := range channels {
		scope.ChannelPatterns[ch] = visibility.ChannelPattern(parameter, hmenum.ParamsetKeyValues, model, ch)
	}
	return visibility.CandidateGroup{
		Parameter:     parameter,
		Paramset:      hmenum.ParamsetKeyValues,
		Reason:        reasons[0],
		Reasons:       reasons,
		SimplePattern: parameter,
		Models:        []visibility.CandidateModelScope{scope},
		Devices:       devices,
		Channels:      len(channels),
	}
}

func getCandidates(t *testing.T, h http.Handler, query string) handlers.UnIgnoreCandidateListDTO {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore/candidates"+query, http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body handlers.UnIgnoreCandidateListDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// TestCandidatesEndpointReturnsGroupsAndReasonVocabulary pins the
// grouped payload the picker consumes: the scopes, the pattern forms
// and the reason vocabulary all arrive in one response.
func TestCandidatesEndpointReturnsGroupsAndReasonVocabulary(t *testing.T) {
	t.Parallel()

	lister := &fakeCentralLister{names: []string{"ccu-01"}}
	provider := &fakeGroupProvider{
		flat: map[string][]string{"ccu-01": {"LOW_BAT"}},
		groups: map[string][]visibility.CandidateGroup{
			"ccu-01": {
				valuesGroup("LOW_BAT", "HmIP-eTRV-2", []int{0, 1}, 3, visibility.ReasonHidden),
			},
		},
	}
	labels := &fakeLabeler{byParameter: map[string]string{"LOW_BAT": "Batteriezustand"}}
	h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider, labels)

	body := getCandidates(t, h, "")
	if len(body.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(body.Groups))
	}
	g := body.Groups[0]
	if g.Parameter != "LOW_BAT" || g.Paramset != "VALUES" {
		t.Errorf("group = %+v", g)
	}
	if g.Label != "Batteriezustand" {
		t.Errorf("label = %q, want the resolved translation", g.Label)
	}
	if g.Reason != "hidden" {
		t.Errorf("reason = %q", g.Reason)
	}
	if g.SimplePattern != "LOW_BAT" {
		t.Errorf("simple_pattern = %q", g.SimplePattern)
	}
	if g.DeviceCount != 3 || g.ChannelCount != 2 {
		t.Errorf("device_count = %d, channel_count = %d, want 3/2", g.DeviceCount, g.ChannelCount)
	}
	if len(g.Models) != 1 || g.Models[0].Model != "HmIP-eTRV-2" {
		t.Fatalf("models = %+v", g.Models)
	}
	m := g.Models[0]
	if m.WildcardPattern != "LOW_BAT:VALUES@HmIP-eTRV-2:all" {
		t.Errorf("wildcard_pattern = %q", m.WildcardPattern)
	}
	if len(m.Channels) != 2 || m.Channels[0].Pattern != "LOW_BAT:VALUES@HmIP-eTRV-2:0" {
		t.Errorf("channels = %+v", m.Channels)
	}
	if len(body.Reasons) == 0 {
		t.Error("reasons vocabulary is empty; the UI builds its chips from it")
	}
	if slices.Contains(body.Reasons, "unknown") {
		t.Error("reasons vocabulary contains 'unknown'; it is a drift signal, not a chip")
	}
}

// TestCandidatesEndpointAlwaysCollectsBothParamsets pins the decision
// that `include_master` governs only the legacy flat list. After
// grouping, MASTER costs ~10 rows rather than ~940 patterns, so the
// picker gets both paramsets in one round-trip and filters client-side.
func TestCandidatesEndpointAlwaysCollectsBothParamsets(t *testing.T) {
	t.Parallel()

	lister := &fakeCentralLister{names: []string{"ccu-01"}}
	provider := &fakeGroupProvider{
		groups: map[string][]visibility.CandidateGroup{
			"ccu-01": {valuesGroup("LOW_BAT", "HmIP-eTRV-2", []int{0}, 1, visibility.ReasonHidden)},
		},
	}
	h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider, nil)

	body := getCandidates(t, h, "")
	if body.IncludeMaster {
		t.Error("include_master = true, want the query default to stay false")
	}
	want := []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster}
	if !slices.Equal(provider.lastParamsets, want) {
		t.Errorf("requested paramsets = %v, want %v", provider.lastParamsets, want)
	}
}

// TestCandidatesEndpointMergesGroupsAcrossCentrals pins the multi-CCU
// fold: one row per parameter, with the device counts and the model
// scopes of every central added together.
func TestCandidatesEndpointMergesGroupsAcrossCentrals(t *testing.T) {
	t.Parallel()

	lister := &fakeCentralLister{names: []string{"ccu-01", "ccu-02"}}
	provider := &fakeGroupProvider{
		groups: map[string][]visibility.CandidateGroup{
			"ccu-01": {
				valuesGroup("LOW_BAT", "HmIP-eTRV-2", []int{0}, 2, visibility.ReasonHidden),
			},
			"ccu-02": {
				// Same model, a further channel — plus a second model.
				valuesGroup("LOW_BAT", "HmIP-eTRV-2", []int{1}, 3, visibility.ReasonReadOnly),
				valuesGroup("LOW_BAT", "HmIP-SWDO", []int{0}, 1, visibility.ReasonReadOnly),
			},
		},
	}
	h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider, nil)

	body := getCandidates(t, h, "")
	if len(body.Groups) != 1 {
		t.Fatalf("groups = %d, want 1 merged group", len(body.Groups))
	}
	g := body.Groups[0]
	if g.DeviceCount != 6 {
		t.Errorf("device_count = %d, want 2+3+1", g.DeviceCount)
	}
	if len(g.Models) != 2 {
		t.Fatalf("models = %+v, want HmIP-SWDO + HmIP-eTRV-2", g.Models)
	}
	if g.Models[0].Model != "HmIP-SWDO" {
		t.Errorf("models not sorted: %+v", g.Models)
	}
	etrv := g.Models[1]
	if len(etrv.Channels) != 2 {
		t.Errorf("HmIP-eTRV-2 channels = %+v, want channels 0 and 1 merged", etrv.Channels)
	}
	if etrv.Channels[0].Channel != 0 || etrv.Channels[1].Channel != 1 {
		t.Errorf("channels not sorted: %+v", etrv.Channels)
	}
	if etrv.Channels[1].Pattern != "LOW_BAT:VALUES@HmIP-eTRV-2:1" {
		t.Errorf("merged channel lost its pattern: %+v", etrv.Channels[1])
	}
	want := []string{"hidden", "read_only"}
	if !slices.Equal(g.Reasons, want) {
		t.Errorf("reasons = %v, want the union %v", g.Reasons, want)
	}
	if g.Reason != "hidden" {
		t.Errorf("reason = %q, want the highest-precedence of the union", g.Reason)
	}
}

// TestCandidatesEndpointWithoutGroupSupportStillServesTheFlatList pins
// the degradation path: a provider that implements only the flat
// surface must not break the endpoint, and must emit an empty array
// rather than a JSON null.
func TestCandidatesEndpointWithoutGroupSupportStillServesTheFlatList(t *testing.T) {
	t.Parallel()

	lister := &fakeCentralLister{names: []string{"ccu-01"}}
	provider := &fakeCandidateProvider{
		values: map[string][]string{"ccu-01": {"LOW_BAT"}},
	}
	h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider, nil)

	body := getCandidates(t, h, "")
	if len(body.Candidates) != 1 {
		t.Errorf("candidates = %v, want the flat list to survive", body.Candidates)
	}
	if body.Groups == nil {
		t.Error("groups = null, want an empty array")
	}
	if len(body.Groups) != 0 {
		t.Errorf("groups = %v, want empty", body.Groups)
	}
}
