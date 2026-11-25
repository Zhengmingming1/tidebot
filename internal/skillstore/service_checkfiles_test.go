package skillstore

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

func TestCheckFiles(t *testing.T) {
	got, err := CheckFiles(map[string][]byte{"SKILL.md": []byte("# A"), "./ref/a.md": []byte("a"), ".git/HEAD": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got["ref/a.md"]) != "a" || got["SKILL.md"] == nil {
		t.Fatalf("cleaned set: %v", got)
	}
	for name, files := range map[string]map[string][]byte{
		"parent":    {"../x": nil},
		"absolute":  {"/etc/x": nil},
		"drive":     {"C:/x": nil},
		"device":    {"aux.md": nil},
		"duplicate": {"a": nil, "./a": nil},
		"too big":   {"a": make([]byte, skillpkg.MaxFileBytes+1)},
	} {
		if _, err := CheckFiles(files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := map[string][]byte{}
	for i := 0; i <= skillpkg.MaxEntries; i++ {
		many["f/"+strconv.Itoa(i)] = nil
	}
	if _, err := CheckFiles(many); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("too many files: %v", err)
	}
}
