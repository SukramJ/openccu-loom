// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Proxy limits and timings of the lite-rpc XML-RPC proxy.
const (
	proxyRequestLimit  = 4 << 20
	proxyResponseLimit = 16 << 20
	proxyTimeout       = 30 * time.Second
)

// initRefusal is the exact fault string the box answers init with,
// alone or inside system.multicall. The document it cites does not
// exist; the text is reproduced verbatim anyway because clients match
// on it.
const initRefusal = "init is not available remotely on openccu-lite: " +
	"subscribe to /api/rpc/v1/events - see docs/rpc-remote.md"

// interfaceInfo is one row of GET /api/rpc/v1/interfaces.
type interfaceInfo struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	URLPath  string `json:"url_path"`
	Running  bool   `json:"running"`
}

// handleInterfaces answers GET /api/rpc/v1/interfaces, sorted by name.
// running is false only for an interface marked down.
func (f *Fake) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, scopeRPCRead, true); !ok {
		return
	}
	names := f.interfaceNames()
	out := make([]interfaceInfo, 0, len(names))
	for _, n := range names {
		st, _ := f.ifaceSnapshot(n)
		out = append(out, interfaceInfo{
			Name:     n,
			Protocol: "xmlrpc",
			URLPath:  "/api/rpc/v1/xmlrpc/" + n,
			Running:  !st.down,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// methodTier returns the scope a method needs, from the lite-rpc tier
// table. Every method not listed needs rpc:admin.
func methodTier(method string, params []xmlrpc.Value) string {
	switch method {
	case "system.listMethods", "system.methodHelp", "system.methodSignature",
		"listDevices", "getDeviceDescription", "getParamsetDescription",
		"getParamset", "getParamsetId", "getValue", "getLinks", "getLinkInfo",
		"getLinkPeers", "getMetadata", "getAllMetadata", "listBidcosInterfaces",
		"getInstallMode", "getKeyMismatchDevice", "getServiceMessages",
		"listReplaceableDevices", "getVersion", "ping", "rssiInfo",
		"getLGWStatus", "listTeams", "getDeviceStatus", "getMasterValue",
		"clientServerInitialized", "refreshDeployedDeviceFirmwareList",
		"getRFLGWInfoLED", "getParamsetsInfo", "listAllDevices",
		"getBackgroundBackupState", "getCurrentDutyCycle":
		return scopeRPCRead
	case "logLevel":
		if len(params) == 0 {
			return scopeRPCRead
		}
		return scopeRPCConfigure
	case "setValue":
		return scopeRPCOperate
	case "putParamset":
		if len(params) > 1 {
			if key, err := xmlrpc.AsString(params[1]); err == nil && strings.EqualFold(key, "VALUES") {
				return scopeRPCOperate
			}
		}
		return scopeRPCConfigure
	case "setInstallMode", "addLink", "removeLink", "setLinkInfo", "setMetadata",
		"deleteMetadata", "setBidcosInterface", "setTeam", "addDevice",
		"activateLinkParamset", "reportValueUsage", "abortDeleteDevice",
		"setRFLGWInfoLED", "setInterfaceClock", "addVirtualDevice",
		"setMasterValue", "determineParameter", "searchDevices", "setTempKey":
		return scopeRPCConfigure
	default:
		return scopeRPCAdmin
	}
}

// innerCall is one method invocation of a request, the sub-calls of a
// multicall unpacked.
type innerCall struct {
	method string
	params []xmlrpc.Value
}

// unpackCalls lists the invocations a request makes. A system.multicall
// whose parameter is not an array of {methodName, params} structs counts
// as the single method system.multicall.
func unpackCalls(call *xmlrpc.MethodCall) []innerCall {
	whole := []innerCall{{method: call.Method, params: call.Params}}
	if call.Method != "system.multicall" || len(call.Params) != 1 {
		return whole
	}
	arr, err := xmlrpc.AsArray(call.Params[0])
	if err != nil {
		return whole
	}
	out := make([]innerCall, 0, len(arr))
	for _, item := range arr {
		name, err := xmlrpc.StructField[xmlrpc.StringValue](item, "methodName")
		if err != nil {
			return whole
		}
		var params []xmlrpc.Value
		if s, err := xmlrpc.AsStruct(item); err == nil {
			if p, ok := s.Get("params"); ok {
				if a, err := xmlrpc.AsArray(p); err == nil {
					params = a
				}
			}
		}
		out = append(out, innerCall{method: string(name), params: params})
	}
	return out
}

// handleXMLRPC is POST /api/rpc/v1/xmlrpc/{iface}: route auth, the init
// refusal and the tier check (both as faults over HTTP 200), then the
// forward to the interface process. An interface that is marked down or
// does not answer yields 503 {"error":"down"}, JSON rather than a fault.
func (f *Fake) handleXMLRPC(w http.ResponseWriter, r *http.Request) {
	entry, _, ok := f.authorize(w, r, scopeRPCRead, true)
	if !ok {
		return
	}
	iface := r.PathValue("iface")
	st, known := f.ifaceSnapshot(iface)
	if !known {
		writeError(w, http.StatusNotFound, "unknown-interface", "no such interface: "+iface)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method-not-allowed", "use POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, proxyRequestLimit+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad-request", "cannot read the request: "+err.Error())
		return
	}
	if len(body) > proxyRequestLimit {
		writeError(w, http.StatusBadRequest, "bad-request", "the request exceeds 4 MiB")
		return
	}
	call, err := xmlrpc.DecodeCall(bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad-request", "not an XML-RPC call: "+err.Error())
		return
	}
	calls := unpackCalls(call)
	methods := make([]string, 0, len(calls))
	for _, c := range calls {
		methods = append(methods, c.method)
	}
	recordRPCMethods(r, methods)

	// init is refused before any tier check, wherever it appears.
	for _, c := range calls {
		if c.method == "init" {
			writeFault(w, -1, initRefusal)
			return
		}
	}
	for _, c := range calls {
		tier := methodTier(c.method, c.params)
		if !hasScope(entry.scopes, tier) {
			writeFault(w, -1, "not permitted: "+c.method+" needs "+tier)
			return
		}
	}
	if st.down {
		writeDown(w, iface, "marked down")
		return
	}

	f.mu.Lock()
	if s, ok := f.ifaces[iface]; ok {
		s.calls++
	}
	f.mu.Unlock()

	resp, err := forward(r.Context(), st.daemonURL, call)
	if err != nil {
		var local *localError
		if errors.As(err, &local) {
			writeFault(w, -1, local.Error())
			return
		}
		writeDown(w, iface, err.Error())
		return
	}
	if resp.Fault != nil {
		writeFault(w, resp.Fault.Code, resp.Fault.Message)
		return
	}
	if len(resp.Params) == 0 {
		// A void answer becomes an empty string value.
		resp.Params = []xmlrpc.Value{xmlrpc.StringValue("")}
	}
	writeXMLRPC(w, &xmlrpc.MethodResponse{Params: resp.Params[:1]})
}

// localError is a failure on the box's side of the forward (the request
// could not be re-encoded); it becomes fault -1, not 503.
type localError struct{ err error }

func (e *localError) Error() string { return e.err.Error() }
func (e *localError) Unwrap() error { return e.err }

// forward sends call to the interface process re-encoded as ISO-8859-1
// and decodes its answer. Every failure to get a decodable answer
// (refused, timeout, non-2xx, undecodable) is an error: the process
// does not answer.
func forward(ctx context.Context, url string, call *xmlrpc.MethodCall) (*xmlrpc.MethodResponse, error) {
	payload, err := encodeLatin1Call(call)
	if err != nil {
		return nil, &localError{err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, proxyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &localError{err: err}
	}
	req.Header.Set("Content-Type", "text/xml; charset=ISO-8859-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, proxyResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > proxyResponseLimit {
		return nil, errors.New("answer exceeds 16 MiB")
	}
	mr, err := xmlrpc.DecodeResponse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("undecodable answer: %w", err)
	}
	return mr, nil
}

// ncrMarkOpen and ncrMarkClose bracket a code point that ISO-8859-1
// cannot carry while the call passes through the Latin-1 encoder; the
// markers are then rewritten into numeric character references.
const (
	ncrMarkOpen  = "¤¤NCR"
	ncrMarkClose = "¤¤"
)

// encodeLatin1Call encodes call as ISO-8859-1, turning every character
// outside Latin-1 into a numeric character reference the way the box
// re-encodes a UTF-8 request before forwarding it.
func encodeLatin1Call(call *xmlrpc.MethodCall) ([]byte, error) {
	marked := false
	params := make([]xmlrpc.Value, len(call.Params))
	for i, p := range call.Params {
		params[i] = markNonLatin1(p, &marked)
	}
	var buf bytes.Buffer
	if err := xmlrpc.EncodeCall(&buf, &xmlrpc.MethodCall{Method: call.Method, Params: params}); err != nil {
		return nil, err
	}
	if !marked {
		return buf.Bytes(), nil
	}
	return rewriteMarks(buf.Bytes()), nil
}

// markNonLatin1 replaces every rune above U+00FF in string carriers
// with a marker the Latin-1 encoder passes through.
func markNonLatin1(v xmlrpc.Value, marked *bool) xmlrpc.Value {
	switch t := v.(type) {
	case xmlrpc.StringValue:
		return xmlrpc.StringValue(markString(string(t), marked))
	case xmlrpc.StructValue:
		members := make([]xmlrpc.Member, len(t.Members))
		for i, m := range t.Members {
			members[i] = xmlrpc.Member{Name: markString(m.Name, marked), Value: markNonLatin1(m.Value, marked)}
		}
		return xmlrpc.StructValue{Members: members}
	case xmlrpc.ArrayValue:
		out := make(xmlrpc.ArrayValue, len(t))
		for i, item := range t {
			out[i] = markNonLatin1(item, marked)
		}
		return out
	default:
		return v
	}
}

func markString(s string, marked *bool) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0xFF {
			*marked = true
			b.WriteString(ncrMarkOpen + strconv.Itoa(int(r)) + ncrMarkClose)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// rewriteMarks replaces the Latin-1 bytes of every marker with the
// numeric character reference it stands for.
func rewriteMarks(latin1 []byte) []byte {
	open := []byte{0xA4, 0xA4, 'N', 'C', 'R'}
	closeMark := []byte{0xA4, 0xA4}
	var out bytes.Buffer
	for {
		i := bytes.Index(latin1, open)
		if i < 0 {
			out.Write(latin1)
			return out.Bytes()
		}
		out.Write(latin1[:i])
		rest := latin1[i+len(open):]
		j := bytes.Index(rest, closeMark)
		if j < 0 {
			out.Write(latin1[i:])
			return out.Bytes()
		}
		out.WriteString("&#" + string(rest[:j]) + ";")
		latin1 = rest[j+len(closeMark):]
	}
}

// writeDown answers 503 {"error":"down"} for an interface process that
// does not answer.
func writeDown(w http.ResponseWriter, iface, detail string) {
	writeError(w, http.StatusServiceUnavailable, "down",
		"the interface process does not answer: "+iface+": "+detail)
}

// writeFault answers an XML-RPC fault over HTTP 200.
func writeFault(w http.ResponseWriter, code int, msg string) {
	writeXMLRPC(w, &xmlrpc.MethodResponse{Fault: &hmerr.XMLRPCFault{Code: code, Message: msg}})
}

// utf8Preamble is the declaration of every proxy answer.
const utf8Preamble = `<?xml version="1.0" encoding="UTF-8"?>`

// writeXMLRPC writes mr re-encoded as UTF-8. Loom's encoder emits
// ISO-8859-1, whose bytes map one-to-one onto the first 256 code points,
// so the transcoding is a byte-to-rune widening plus a new declaration.
func writeXMLRPC(w http.ResponseWriter, mr *xmlrpc.MethodResponse) {
	var buf bytes.Buffer
	if err := xmlrpc.EncodeResponse(&buf, mr); err != nil {
		buf.Reset()
		fault := &xmlrpc.MethodResponse{Fault: &hmerr.XMLRPCFault{Code: -1, Message: "cannot encode the answer"}}
		if err := xmlrpc.EncodeResponse(&buf, fault); err != nil {
			http.Error(w, "encode failed", http.StatusInternalServerError)
			return
		}
	}
	var out strings.Builder
	out.Grow(buf.Len() + 8)
	for _, b := range buf.Bytes() {
		out.WriteRune(rune(b))
	}
	text := out.String()
	if i := strings.Index(text, "?>"); strings.HasPrefix(text, "<?xml") && i >= 0 {
		text = utf8Preamble + text[i+2:]
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, text)
}

// stateAnswer is the GET /api/rpc/v1/state answer.
type stateAnswer struct {
	Entries     []stateEntry `json:"entries"`
	Total       int          `json:"total"`
	Unconfirmed int          `json:"unconfirmed"`
	Next        string       `json:"next,omitempty"`
	EventID     string       `json:"event_id"`
}

// Paging bounds of /api/rpc/v1/state.
const (
	stateLimitDefault = 1000
	stateLimitMax     = 5000
)

// handleState answers GET /api/rpc/v1/state. event_id is read before the
// values, so a client that resumes the stream from it misses nothing
// that happened after its seed. next is an opaque cursor.
func (f *Fake) handleState(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, scopeRPCRead, true); !ok {
		return
	}
	boot, seq := f.ring.position()
	q := r.URL.Query()
	limit := stateLimitDefault
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > stateLimitMax {
			writeError(w, http.StatusBadRequest, "bad-request", "limit must be 1-5000")
			return
		}
		limit = n
	}
	offset := 0
	if s := q.Get("after"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad-request", "invalid after cursor")
			return
		}
		offset = n
	}
	flt := parseFilter(q)
	var matched []stateEntry
	for _, e := range f.values.snapshot() {
		if flt.matchState(e.Interface, e.Address, e.Datapoint) {
			matched = append(matched, e)
		}
	}
	ans := stateAnswer{Entries: []stateEntry{}, Total: len(matched), EventID: boot + "-" + strconv.FormatUint(seq, 10)}
	for _, e := range matched {
		if !e.Confirmed {
			ans.Unconfirmed++
		}
	}
	if offset < len(matched) {
		end := min(offset+limit, len(matched))
		ans.Entries = matched[offset:end]
		if end < len(matched) {
			ans.Next = strconv.Itoa(end)
		}
	}
	writeJSON(w, http.StatusOK, ans)
}
