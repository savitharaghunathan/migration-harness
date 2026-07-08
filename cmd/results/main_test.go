package main

import "testing"

func TestStatusForExitCode(t *testing.T) {
	cases := []struct {
		name string
		code int
		want string
	}{
		{name: "zero is succeeded", code: 0, want: "succeeded"},
		{name: "one is failed", code: 1, want: "failed"},
		{name: "negative one is failed", code: -1, want: "failed"},
		{name: "large positive code is failed", code: 137, want: "failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusForExitCode(tc.code)
			if got != tc.want {
				t.Errorf("statusForExitCode(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}
