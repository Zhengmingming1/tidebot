package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/freepool"
	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/provider"
)

func TestManagedFreePoolChildBypassesProxyForLoopback(t *testing.T) {
	skipOnWindows(t)
	before := netproxy.Get()
	t.Cleanup(func() { _ = netproxy.Set(before) })
	if err := netproxy.Set(netproxy.Settings{Mode: netproxy.ModeManual, URL: "http://proxy.example:3128", Bypass: ".corp"}); err != nil {
		t.Fatal(err)
	}
	r, _, dir := drainRunner(t, 1)
	script := filepath.Join(dir, "capture-env")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/usr/bin/env > \"$TIDEBOT_TEST_CHILD_ENV_FILE\"\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"ok\",\"total_cost_usd\":0,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		managed   bool
		inherited []string
	}{
		{"managed-absent-bypass", true, nil},
		{"managed-existing-bypass", true, []string{"NO_PROXY=.internal", "no_proxy=.private"}},
		{"ordinary", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".env")
			p := provider.Profile{Name: "test", BaseURL: "http://127.0.0.1:9876"}
			if tc.managed {
				p.ManagedBy = freepool.PluginID
			}
			base := append([]string{"TIDEBOT_TEST_CHILD_ENV_FILE=" + path}, tc.inherited...)
			res, err := r.RunOnce(context.Background(), Options{ClaudeBin: script, Profile: p, EnvBase: base, WorkspaceDir: dir, Timeout: time.Second})
			if err != nil || res.Status != "succeeded" {
				t.Fatalf("child: %s %v", res.Status, err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			for _, line := range strings.Split(string(body), "\n") {
				k, v, _ := strings.Cut(line, "=")
				env[k] = v
			}
			if env["HTTPS_PROXY"] != "http://proxy.example:3128" {
				t.Fatal("the upstream proxy setting was lost")
			}
			for _, name := range []string{"NO_PROXY", "no_proxy"} {
				if tc.managed {
					for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
						if !strings.Contains(","+env[name]+",", ","+host+",") {
							t.Fatalf("%s lacks loopback bypass %s", name, host)
						}
					}
					for _, inherited := range tc.inherited {
						_, host, _ := strings.Cut(inherited, "=")
						if !strings.Contains(env[name], host) {
							t.Fatal("an inherited bypass entry was lost")
						}
					}
				} else if env[name] != ".corp" {
					t.Fatal("ordinary provider proxy behavior changed")
				}
			}
		})
	}
}
