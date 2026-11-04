package app

import "testing"

func TestNPMVersionRanges(t *testing.T) {
	for _, tc := range []struct {
		v, r string
		want bool
	}{
		{"1.2.3", "^1.2.0", true}, {"2.0.0", "^1.2.0", false}, {"0.2.9", "^0.2.3", true}, {"0.3.0", "^0.2.3", false}, {"0.0.4", "^0.0.3", false}, {"0.9.9", "^0", true}, {"1.0.0", "^0", false},
		{"1.2.9", "~1.2.3", true}, {"1.3.0", "~1.2.3", false}, {"1.9.0", "~1", true}, {"1.2.3", "1.2.x", true}, {"1.3.0", "1.2.x", false},
		{"2.0.0", ">= 1.5 < 3", true}, {"3.0.0", ">=1.5 <3", false}, {"1.9.0", "1 || 2", true}, {"2.3.4", "1.2 - 2.3", true}, {"2.4.0", "1.2 - 2.3", false},
		{"1.2.3", ">1.2", false}, {"1.3.0", ">1.2", true}, {"1.2.9", "<=1.2", true}, {"1.3.0", "<=1.2", false}, {"1.2.3", "*", true}, {"1.2.3", ">*", false},
	} {
		t.Run(tc.v+tc.r, func(t *testing.T) {
			got, e := matchNPMRange(tc.v, tc.r)
			if e != nil || got != tc.want {
				t.Fatalf("match=%v err=%v want=%v", got, e, tc.want)
			}
		})
	}
	for _, r := range []string{"latest", "npm:other@^1", "https://evil.example/x", "file:../x", "1.2.3-beta", "1.2.*.4", "01.2.3", "+1.2.3", "^1.2 || garbage", "1.*.2"} {
		if _, e := matchNPMRange("1.2.3", r); e == nil {
			t.Fatalf("unsupported expression accepted: %s", r)
		}
	}
}
