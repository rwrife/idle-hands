package main

import "testing"

func TestParseWatchFlagsJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		wantJS  bool
		wantFD  *int
		restLen int
	}{
		{"bare --json", []string{"--json", "--", "claude"}, true, nil, 2},
		{"--json=true", []string{"--json=true", "echo", "hi"}, true, nil, 2},
		{"--json=false", []string{"--json=false", "echo"}, false, nil, 1},
		{"--json-fd 2 implies enable", []string{"--json-fd", "2", "--", "aider"}, true, intp(2), 2},
		{"--json-fd=2", []string{"--json-fd=2", "echo"}, true, intp(2), 1},
		{"no json flag", []string{"echo", "hi"}, false, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, rest, err := parseWatchFlags(tc.in)
			if err != nil {
				t.Fatalf("parseWatchFlags(%v) error: %v", tc.in, err)
			}
			if flags.json != tc.wantJS {
				t.Errorf("json = %v, want %v", flags.json, tc.wantJS)
			}
			if (flags.jsonFD == nil) != (tc.wantFD == nil) {
				t.Fatalf("jsonFD = %v, want %v", flags.jsonFD, tc.wantFD)
			}
			if flags.jsonFD != nil && *flags.jsonFD != *tc.wantFD {
				t.Errorf("jsonFD = %d, want %d", *flags.jsonFD, *tc.wantFD)
			}
			if len(rest) != tc.restLen {
				t.Errorf("rest = %v (len %d), want len %d", rest, len(rest), tc.restLen)
			}
		})
	}
}

func TestParseWatchFlagsJSONErrors(t *testing.T) {
	bad := [][]string{
		{"--json=maybe", "echo"},
		{"--json-fd", "notanumber", "echo"},
		{"--json-fd"}, // missing value
	}
	for _, in := range bad {
		if _, _, err := parseWatchFlags(in); err == nil {
			t.Errorf("parseWatchFlags(%v) = nil error, want an error", in)
		}
	}
}

func intp(i int) *int { return &i }
