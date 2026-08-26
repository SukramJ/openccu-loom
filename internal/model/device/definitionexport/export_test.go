// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package definitionexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/orderedjson"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeWire replays recorded godevccu descriptions as if they came off the wire,
// preserving member order so the export reproduces the Python reference's bytes.
type fakeWire struct {
	devices  map[string]*orderedjson.Object            // address → device description
	paramset map[string]map[string]*orderedjson.Object // address → paramset key → description
}

func (w *fakeWire) CallOrdered(_ context.Context, method string, params []any, _ hmenum.CommandPriority) (any, error) {
	switch method {
	case "getDeviceDescription":
		addr := params[0].(string)
		d, ok := w.devices[addr]
		if !ok {
			return nil, fmt.Errorf("no device description for %s", addr)
		}
		return d, nil
	case "getParamsetDescription":
		addr := params[0].(string)
		key := params[1].(string)
		ps, ok := w.paramset[addr][key]
		if !ok {
			// Mirrors a CCU that has no such paramset: the export skips it.
			return nil, fmt.Errorf("no paramset %s/%s", addr, key)
		}
		return ps, nil
	}
	return nil, fmt.Errorf("unexpected method %s", method)
}

// TestExportMatchesReferenceGolden drives the export with recorded godevccu
// wire data and asserts the two zipped JSON members are byte-for-byte equal to
// the golden produced by orjson over the Python reference's anonymisation algorithm
// (testdata/expected_*.json, fixed VCU id "VCU1000000"). This is the
// end-to-end byte-parity guard: traversal order, paramset enumeration,
// anonymisation and orjson formatting all have to line up.
func TestExportMatchesReferenceGolden(t *testing.T) {
	wire := loadWire(t)
	res, err := Export(context.Background(), wire, "VCU0000237", func() (string, error) {
		return "VCU1000000", nil
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Model != "HM-WDS30-T-O" {
		t.Fatalf("model = %q, want HM-WDS30-T-O", res.Model)
	}

	got := unzip(t, res.Zip)
	wantDevice := readFile(t, "testdata/expected_device_descriptions.json")
	wantParamset := readFile(t, "testdata/expected_paramset_descriptions.json")

	assertEqualJSON(t, "device_descriptions/HM-WDS30-T-O.json", got["device_descriptions/HM-WDS30-T-O.json"], wantDevice)
	assertEqualJSON(t, "paramset_descriptions/HM-WDS30-T-O.json", got["paramset_descriptions/HM-WDS30-T-O.json"], wantParamset)
}

func assertEqualJSON(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s: byte mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func loadWire(t *testing.T) *fakeWire {
	t.Helper()
	var rawDevices []json.RawMessage
	if err := json.Unmarshal(readFile(t, "testdata/wire_device_descriptions.json"), &rawDevices); err != nil {
		t.Fatalf("decode wire devices: %v", err)
	}
	w := &fakeWire{
		devices:  map[string]*orderedjson.Object{},
		paramset: map[string]map[string]*orderedjson.Object{},
	}
	for _, rd := range rawDevices {
		obj := decodeOrderedObject(t, rd)
		addr, _ := obj.Get("ADDRESS")
		w.devices[addr.(string)] = obj
	}

	psRoot := decodeOrderedObject(t, readFile(t, "testdata/wire_paramset_descriptions.json"))
	for i := range psRoot.Members {
		addr := psRoot.Members[i].Key
		keys := psRoot.Members[i].Value.(*orderedjson.Object)
		w.paramset[addr] = map[string]*orderedjson.Object{}
		for j := range keys.Members {
			w.paramset[addr][keys.Members[j].Key] = keys.Members[j].Value.(*orderedjson.Object)
		}
	}
	return w
}

func unzip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("zip entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("zip read %s: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// --- test-only order-preserving JSON decoder ---------------------------------
// Reproduces Python's json int/float split so the round-tripped numbers match
// the orjson golden.

func decodeOrderedObject(t *testing.T, data []byte) *orderedjson.Object {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrderedValue(dec)
	if err != nil {
		t.Fatalf("decode ordered: %v", err)
	}
	obj, ok := v.(*orderedjson.Object)
	if !ok {
		t.Fatalf("decode ordered: top-level is %T, want object", v)
	}
	return obj
}

func decodeOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := orderedjson.NewObject(0)
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Set(keyTok.(string), val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := orderedjson.Array{}
			for dec.More() {
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delim %v", t)
	case string:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	case json.Number:
		s := string(t)
		if !strings.ContainsAny(s, ".eE") {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, nil
			}
		}
		return strconv.ParseFloat(s, 64)
	}
	return nil, fmt.Errorf("unexpected token %T", tok)
}
