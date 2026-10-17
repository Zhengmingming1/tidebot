package netproxy

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const scutilOut = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : <local>
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 7891
  SOCKSProxy : 127.0.0.1
}
`

func TestParseScutil(t *testing.T) {
	s := ParseScutil(scutilOut)
	if s.HTTP != "127.0.0.1:7890" || s.HTTPS != "127.0.0.1:7890" || s.SOCKS != "127.0.0.1:7891" || s.PAC != "" {
		t.Fatalf("%+v", s)
	}
	for _, want := range []string{".local", "169.254.0.0/16", "localhost", "127.0.0.1", "::1"} {
		if !contains(s.Bypass, want) {
			t.Fatalf("bypass %v missing %s", s.Bypass, want)
		}
	}
	c := s.Config()
	if c.HTTPSProxy != "http://127.0.0.1:7890" {
		t.Fatal(c.HTTPSProxy)
	}
	// SOCKS only
	so := ParseScutil("<dictionary> {\n  SOCKSEnable : 1\n  SOCKSPort : 1080\n  SOCKSProxy : 10.0.0.2\n  HTTPEnable : 0\n}\n")
	if c := so.Config(); c == nil || c.HTTPSProxy != "socks5://10.0.0.2:1080" {
		t.Fatalf("%+v", c)
	}
	// disabled -> no proxy; PAC reported
	pac := ParseScutil("<dictionary> {\n  HTTPEnable : 0\n  HTTPProxy : 1.2.3.4\n  HTTPPort : 8080\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : http://wpad/proxy.pac\n}\n")
	if pac.Config() != nil || pac.PAC != "http://wpad/proxy.pac" {
		t.Fatalf("%+v", pac)
	}
	if ParseScutil("").Config() != nil {
		t.Fatal("empty output must be direct")
	}
}

func TestParseWindowsRegistry(t *testing.T) {
	out := `
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    127.0.0.1:10809
    ProxyOverride    REG_SZ    localhost;127.*;10.*;*.corp.example;<local>
`
	en, srv, ovr, pac := ParseRegQuery(out)
	if en != 1 || srv != "127.0.0.1:10809" || pac != "" {
		t.Fatal(en, srv, pac)
	}
	s := ParseWinINet(en, srv, ovr, pac)
	if s.HTTP != "127.0.0.1:10809" || s.HTTPS != "127.0.0.1:10809" {
		t.Fatalf("%+v", s)
	}
	if !contains(s.Bypass, ".corp.example") || !contains(s.Bypass, "localhost") {
		t.Fatal(s.Bypass)
	}
	per := ParseWinINet(1, "http=proxy:80;https=proxy:443;socks=sock:1080", "", "")
	if per.HTTP != "proxy:80" || per.HTTPS != "proxy:443" || per.SOCKS != "sock:1080" {
		t.Fatalf("%+v", per)
	}
	if ParseWinINet(0, "127.0.0.1:1", "", "").Config() != nil {
		t.Fatal("ProxyEnable=0 must be direct")
	}
	if s := ParseWinINet(1, "http://127.0.0.1:7890/", "", ""); s.HTTP != "127.0.0.1:7890" {
		t.Fatal(s.HTTP)
	}
}

func TestParseGsettings(t *testing.T) {
	vals := map[string]string{
		"org.gnome.system.proxy mode":         "'manual'",
		"org.gnome.system.proxy ignore-hosts": "['localhost', '127.0.0.0/8', '*.lan']",
		"org.gnome.system.proxy.http host":    "'10.1.1.1'",
		"org.gnome.system.proxy.http port":    "3128",
		"org.gnome.system.proxy.https host":   "'10.1.1.1'",
		"org.gnome.system.proxy.https port":   "3128",
		"org.gnome.system.proxy.socks host":   "''",
		"org.gnome.system.proxy.socks port":   "0",
	}
	s := ParseGsettings(func(a, b string) string { return vals[a+" "+b] })
	if s.HTTPS != "10.1.1.1:3128" || s.SOCKS != "" || !contains(s.Bypass, ".lan") {
		t.Fatalf("%+v", s)
	}
	vals["org.gnome.system.proxy mode"] = "'none'"
	if ParseGsettings(func(a, b string) string { return vals[a+" "+b] }).Config() != nil {
		t.Fatal("mode none must be direct")
	}
}

func withEnv(t *testing.T, env map[string]string, sys *System) {
	t.Helper()
	og, ol := Getenv, LookupSystem
	Getenv = func(k string) string { return env[k] }
	LookupSystem = func(context.Context) *System { return sys }
	t.Cleanup(func() { Getenv, LookupSystem = og, ol; _ = Set(Settings{Mode: ModeAuto}) })
}

func proxyOf(t *testing.T, raw string) string {
	req, _ := http.NewRequest("GET", raw, nil)
	u, err := ProxyFunc(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		return ""
	}
	return u.String()
}

func TestPrecedence(t *testing.T) {
	sys := ParseScutil(scutilOut)
	env := map[string]string{"HTTPS_PROXY": "http://env-proxy:3128"}
	withEnv(t, env, sys)

	// auto: env beats system
	_ = Set(Settings{Mode: ModeAuto})
	if got := proxyOf(t, "https://github.com/x"); got != "http://env-proxy:3128" {
		t.Fatal(got)
	}
	if e := Resolve(); e.Source != "env" {
		t.Fatal(e)
	}
	// manual beats env
	if err := Set(Settings{Mode: ModeManual, URL: "socks5://user:pw@127.0.0.1:1080", Bypass: "internal.example"}); err != nil {
		t.Fatal(err)
	}
	if got := proxyOf(t, "https://github.com/x"); got != "socks5://user:pw@127.0.0.1:1080" {
		t.Fatal(got)
	}
	if got := proxyOf(t, "https://internal.example/x"); got != "" {
		t.Fatal("bypass ignored:", got)
	}
	if e := Resolve(); e.Source != "settings" || strings.Contains(e.HTTPS, "pw") {
		t.Fatalf("credentials must be redacted: %+v", e)
	}
	// off beats everything
	_ = Set(Settings{Mode: ModeOff})
	if got := proxyOf(t, "https://github.com/x"); got != "" || Env() != nil {
		t.Fatal(got)
	}
	// auto with no env falls back to the system proxy, honouring its exceptions
	delete(env, "HTTPS_PROXY")
	_ = Set(Settings{Mode: ModeAuto})
	if got := proxyOf(t, "https://github.com/x"); got != "http://127.0.0.1:7890" {
		t.Fatal(got)
	}
	if got := proxyOf(t, "http://printer.local/"); got != "" {
		t.Fatal("system exception ignored:", got)
	}
	if e := Resolve(); e.Source != "system:scutil" {
		t.Fatal(e)
	}
	ev := Env()
	if !contains(ev, "HTTPS_PROXY=http://127.0.0.1:7890") || !contains(ev, "https_proxy=http://127.0.0.1:7890") {
		t.Fatal(ev)
	}
}

func TestPACNoteAndValidate(t *testing.T) {
	withEnv(t, map[string]string{}, &System{Source: "scutil", PAC: "http://wpad/p.pac"})
	_ = Set(Settings{Mode: ModeAuto})
	if e := Resolve(); e.Source != "none" || e.Note != "pac" {
		t.Fatalf("%+v", e)
	}
	for _, bad := range []string{"", "ftp://x:1", "http://host", "::::"} {
		if Set(Settings{Mode: ModeManual, URL: bad}) == nil {
			t.Fatal("accepted", bad)
		}
	}
	if Set(Settings{Mode: "weird"}) == nil {
		t.Fatal("accepted bad mode")
	}
	if u, err := ParseProxyURL("127.0.0.1:7890"); err != nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatal(u, err)
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
