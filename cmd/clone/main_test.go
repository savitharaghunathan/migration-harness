package main

import "testing"

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		wantURL string
		wantDst string
	}{
		{name: "zero args", args: []string{}, wantErr: true},
		{name: "one arg", args: []string{"https://example.com/repo.git"}, wantErr: true},
		{name: "two args", args: []string{"https://example.com/repo.git", "/tmp/dest"}, wantErr: false, wantURL: "https://example.com/repo.git", wantDst: "/tmp/dest"},
		{name: "three args", args: []string{"a", "b", "c"}, wantErr: true},
		{name: "four args", args: []string{"a", "b", "c", "d"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, dest, err := parseArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for args %v, got none", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for args %v: %v", tc.args, err)
			}
			if url != tc.wantURL {
				t.Errorf("expected url %q, got %q", tc.wantURL, url)
			}
			if dest != tc.wantDst {
				t.Errorf("expected dest %q, got %q", tc.wantDst, dest)
			}
		})
	}
}
