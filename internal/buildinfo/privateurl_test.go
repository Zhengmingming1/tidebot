package buildinfo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// Shipped clients must never point users at the private source repo (it 404s for
// them); everything user-facing goes to the public tidebot-releases repo.
// Package paths like github.com/Zhengmingming1/tidebot/internal/... are allowed.
var privateURL = regexp.MustCompile(`https?://(?:api\.)?github\.com/(?:repos/)?Zhengmingming1/tidebot(?:$|[^-\w/]|/[^ic t])|Zhengmingming1/tidebot/(?:releases|issues|archive)\b|Zhengmingming1/tidebot\.git`)

func TestPatternSelf(t *testing.T) {
	for _, bad := range []string{"https://github.com/Zhengmingming1/tidebot/releases/latest", "https://api.github.com/repos/Zhengmingming1/tidebot/releases/latest", "https://github.com/Zhengmingming1/tidebot\"", "git clone https://github.com/Zhengmingming1/tidebot.git"} {
		if !privateURL.MatchString(bad) {
			t.Fatal("pattern misses", bad)
		}
	}
	for _, ok := range []string{"https://github.com/Zhengmingming1/tidebot-releases/releases/latest", "github.com/Zhengmingming1/tidebot/internal/app.(*App).Start", "mod\tgithub.com/Zhengmingming1/tidebot\t(devel)"} {
		if privateURL.MatchString(ok) {
			t.Fatal("pattern flags", ok)
		}
	}
}

func TestBinariesHaveNoPrivateRepoURL(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	dir := t.TempDir()
	for _, pkg := range []string{"agentd", "agentctl"} {
		out := filepath.Join(dir, pkg)
		cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/"+pkg)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, b)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if m := privateURL.FindAll(data, 5); len(m) > 0 {
			t.Fatalf("%s references the private repo: %q", pkg, m)
		}
	}
}
