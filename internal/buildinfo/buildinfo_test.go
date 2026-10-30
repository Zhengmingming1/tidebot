package buildinfo

import "testing"

func TestSourceRefAndOfficial(t *testing.T) {
	defer func(v, c, o string) { Version, Commit, Official = v, c, o }(Version, Commit, Official)
	Version, Commit = "0.1.9", "abc"
	if g := SourceRef(); g != "https://github.com/Zhengmingming1/tidebot/tree/v0.1.9" {
		t.Fatal(g)
	}
	Version = "dev"
	if g := SourceRef(); g != "https://github.com/Zhengmingming1/tidebot/tree/abc" {
		t.Fatal(g)
	}
	Commit = ""
	if g := SourceRef(); g != "https://github.com/Zhengmingming1/tidebot" {
		t.Fatal(g)
	}
	for o, want := range map[string]bool{"": false, "0": false, "1": true, "true": true} {
		Official = o
		if IsOfficial() != want {
			t.Fatalf("Official=%q", o)
		}
	}
}
