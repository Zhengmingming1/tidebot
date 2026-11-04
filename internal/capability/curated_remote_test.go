package capability

import (
	"strings"
	"testing"
)

func TestCuratedRemoteCatalogPinnedOfficialSources(t *testing.T) {
	for _, id := range []string{"notion", "linear", "linear-readonly"} {
		entries := SearchCatalog(id)
		if len(entries) == 0 || entries[0].ID != id {
			t.Fatalf("missing curated %s", id)
		}
		source := entries[0].Source
		if source.Trust != "official" || source.Kind != "curated-remote" || len(source.Commit) != 64 {
			t.Fatalf("invalid source %+v", source)
		}
		files, err := CuratedRemoteSnapshot(source)
		if err != nil {
			t.Fatal(err)
		}
		_, hash, err := HashFiles(files)
		if err != nil || hash != source.Commit {
			t.Fatalf("unlocked configuration: %s %v", hash, err)
		}
		files["server.json"][0] = '!'
		fresh, err := CuratedRemoteSnapshot(source)
		if err != nil || fresh["server.json"][0] != '{' {
			t.Fatal("mutable shared catalog snapshot")
		}
		for _, mutate := range []func(*Source){
			func(s *Source) { s.URL += "/evil" }, func(s *Source) { s.Ref = "latest" },
			func(s *Source) { s.Commit = strings.Repeat("a", 64) }, func(s *Source) { s.Trust = "unknown" },
			func(s *Source) { s.Publisher = "impostor" }, func(s *Source) { s.Adapter = "skill-dir" },
			func(s *Source) { s.Kind = "git" }, func(s *Source) { s.Registry = &Registry{Name: "other", Entry: id} },
			func(s *Source) { s.Subpaths = []string{"."} },
		} {
			changed := source
			mutate(&changed)
			if _, err := CuratedRemoteSnapshot(changed); err == nil {
				t.Fatalf("accepted modified official source %+v", changed)
			}
		}
	}
}
