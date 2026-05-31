package hmerr

import "testing"

func TestXMLRPCFault_NotFoundMessageBlocksRetry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		code    int
		message string
		want    bool
	}{
		{"retryable-unreach", -1, "device unreachable", true},
		{"permanent-paramset-not-found", -1, "rpc error: paramset \"MASTER\" not found on \"VCU1769958\"", false},
		{"permanent-uppercase-marker", -1, "Method does not exist", false},
		{"permanent-unknown-method", -1, "Unknown method foo", false},
		{"retryable-duty-cycle", -8, "DUTY_CYCLE", true},
		{"permanent-unknown-code", -999, "device unreachable", false},
		{"permanent-not-found-on-permanent-code", -999, "not found", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &XMLRPCFault{Code: tc.code, Message: tc.message}
			if got := f.IsRetryable(); got != tc.want {
				t.Fatalf("IsRetryable() = %v, want %v (code=%d msg=%q)", got, tc.want, tc.code, tc.message)
			}
		})
	}
}
