// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// wireFwMethodDoc returns the doc comment of method `method` declared on
// receiver type `recv` in the given file of this package.
func wireFwMethodDoc(t *testing.T, file, recv, method string) string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != method {
			continue
		}
		var name string
		switch rt := fn.Recv.List[0].Type.(type) {
		case *ast.Ident:
			name = rt.Name
		case *ast.StarExpr:
			if id, ok := rt.X.(*ast.Ident); ok {
				name = id.Name
			}
		}
		if name != recv {
			continue
		}
		if fn.Doc == nil {
			t.Fatalf("%s: (%s).%s has no doc comment", file, recv, method)
		}
		return fn.Doc.Text()
	}
	t.Fatalf("%s: method (%s).%s not found", file, recv, method)
	return ""
}

// TestWireFwValueDocDoesNotBlameFaultMinus5 pins what the two write-side
// formatting rules in value.go are allowed to claim.
//
// Neither rule is owed to a fault code. On every CCU-side XML-RPC parser this
// transport reaches, <i4> and <int> are synonyms — OpenCCU-Base
// src/libXmlRpc/src/XmlRpcValue.cpp:260 dispatches I4_TAG and INT_TAG to the
// same intFromXml, and :517 emits <i4> for the CCU's own integers — and a
// <double> literal is consumed by strtod (:577) before any validator sees the
// text, so "1" and "1.0" are indistinguishable downstream. Fault -5 in rfd is
// "Unknown parameter" (src/rfd/RFChannel.cpp:128), a parameter-identity
// failure, and ReGaHss is not on this codec's path at all.
//
// The guard is textual on purpose: the defect was a false rationale, and a
// false rationale is only visible in the doc comment.
func TestWireFwValueDocDoesNotBlameFaultMinus5(t *testing.T) {
	t.Parallel()

	// The bans target the *claim*, not the fault number: a corrected comment
	// is expected to name -5 in order to say what it really is.
	required := []string{
		"xmlrpcvalue.cpp",
		"unverified",
	}

	for _, tc := range []struct {
		recv, method string
		banned       []string
	}{
		{
			recv:   "IntValue",
			method: "MarshalXML",
			banned: []string{
				"accepts only this form",
				"silently rejected",
			},
		},
		{
			recv:   "DoubleValue",
			method: "MarshalXML",
			banned: []string{
				"requires a literal decimal point",
				"is rejected with xml-rpc fault",
			},
		},
	} {
		t.Run(tc.recv, func(t *testing.T) {
			t.Parallel()
			doc := strings.ToLower(wireFwMethodDoc(t, "value.go", tc.recv, tc.method))
			for _, b := range tc.banned {
				if strings.Contains(doc, b) {
					t.Errorf("value.go: (%s).%s doc still claims %q.\n"+
						"  The CCU-side parsers contradict it: OpenCCU-Base"+
						" src/libXmlRpc/src/XmlRpcValue.cpp:260 maps <i4> and <int> to the"+
						" same branch and :517 EMITS <i4>; doubles go through strtod (:577)"+
						" before any validator. Fault -5 in rfd is \"Unknown parameter\""+
						" (src/rfd/RFChannel.cpp:128).",
						tc.recv, tc.method, b)
				}
			}
			for _, r := range required {
				if !strings.Contains(doc, r) {
					t.Errorf("value.go: (%s).%s doc does not mention %q — the rule must"+
						" name the firmware it rests on and say in those words what is"+
						" unverified.", tc.recv, tc.method, r)
				}
			}
		})
	}
}

// TestWireFwWriteFormattingIsUnchanged pins the emitted wire shape.
//
// Both forms below are accepted by every CCU-side parser, so this is a
// determinism choice rather than a firmware requirement — which is exactly why
// it needs a pin: nothing else would notice the encoder changing.
func TestWireFwWriteFormattingIsUnchanged(t *testing.T) {
	t.Parallel()

	if got := marshalToString(t, IntValue(1)); got != "<value><int>1</int></value>" {
		t.Errorf("IntValue(1) encoded as %q, want <value><int>1</int></value>", got)
	}
	if got := marshalToString(t, DoubleValue(1)); got != "<value><double>1.0</double></value>" {
		t.Errorf("DoubleValue(1) encoded as %q, want <value><double>1.0</double></value>", got)
	}
}
