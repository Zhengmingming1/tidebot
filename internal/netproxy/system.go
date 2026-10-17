package netproxy

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
)

func run(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// ParseScutil parses `scutil --proxy` output (macOS).
//
//	<dictionary> {
//	  ExceptionsList : <array> { 0 : *.local  1 : 169.254/16 }
//	  HTTPEnable : 1
//	  HTTPPort : 7890
//	  HTTPProxy : 127.0.0.1
//	  ...
//	}
func ParseScutil(out string) *System {
	kv := map[string]string{}
	var exc []string
	inExc := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if inExc {
			if line == "}" {
				inExc = false
				continue
			}
			if _, v, ok := strings.Cut(line, " : "); ok {
				exc = append(exc, strings.TrimSpace(v))
			}
			continue
		}
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "ExceptionsList" {
			inExc = strings.HasPrefix(v, "<array>")
			continue
		}
		kv[k] = v
	}
	s := &System{Source: "scutil", Bypass: exc}
	hp := func(pfx string) string {
		if kv[pfx+"Enable"] != "1" || kv[pfx+"Proxy"] == "" {
			return ""
		}
		if p := kv[pfx+"Port"]; p != "" && p != "0" {
			return kv[pfx+"Proxy"] + ":" + p
		}
		return ""
	}
	s.HTTP, s.HTTPS, s.SOCKS = hp("HTTP"), hp("HTTPS"), hp("SOCKS")
	if kv["ProxyAutoConfigEnable"] == "1" {
		s.PAC = kv["ProxyAutoConfigURLString"]
		if s.PAC == "" {
			s.PAC = "(PAC)"
		}
	}
	if kv["ExcludeSimpleHostnames"] == "1" {
		s.Bypass = append(s.Bypass, "<local>")
	}
	s.Bypass = normBypass(s.Bypass)
	return s
}

// ParseWinINet turns the IE/WinINET registry values (HKCU\Software\Microsoft\
// Windows\CurrentVersion\Internet Settings) into a System.
// ProxyServer is "host:port" (all protocols) or "http=h:p;https=h:p;socks=h:p".
// ProxyOverride is ";"-separated, "<local>" means plain host names.
func ParseWinINet(enable uint32, server, override, autoConfigURL string) *System {
	s := &System{Source: "registry", PAC: autoConfigURL}
	if enable != 0 && strings.TrimSpace(server) != "" {
		if !strings.Contains(server, "=") {
			v := stripScheme(server)
			s.HTTP, s.HTTPS = v, v
		} else {
			for _, part := range strings.Split(server, ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
				if !ok {
					continue
				}
				v = stripScheme(v)
				switch strings.ToLower(k) {
				case "http":
					s.HTTP = v
				case "https":
					s.HTTPS = v
				case "socks":
					s.SOCKS = v
				}
			}
		}
		s.Bypass = normBypass(strings.Split(override, ";"))
	}
	return s
}

// ParseRegQuery reads `reg query "...\Internet Settings"` output (used where the
// registry API isn't linked; also handy for tests).
func ParseRegQuery(out string) (enable uint32, server, override, pac string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		val := strings.Join(f[2:], " ")
		switch f[0] {
		case "ProxyEnable":
			n, _ := strconv.ParseUint(strings.TrimPrefix(val, "0x"), 16, 32)
			enable = uint32(n)
		case "ProxyServer":
			server = val
		case "ProxyOverride":
			override = val
		case "AutoConfigURL":
			pac = val
		}
	}
	return
}

// ParseGsettings builds a System from `gsettings get org.gnome.system.proxy...` values.
func ParseGsettings(get func(schema, key string) string) *System {
	s := &System{Source: "gsettings"}
	unq := func(v string) string { return strings.Trim(strings.TrimSpace(v), "'") }
	switch unq(get("org.gnome.system.proxy", "mode")) {
	case "manual":
		hp := func(schema string) string {
			h, p := unq(get(schema, "host")), unq(get(schema, "port"))
			if h == "" || p == "" || p == "0" {
				return ""
			}
			return h + ":" + p
		}
		s.HTTP = hp("org.gnome.system.proxy.http")
		s.HTTPS = hp("org.gnome.system.proxy.https")
		s.SOCKS = hp("org.gnome.system.proxy.socks")
		lst := strings.Trim(strings.TrimSpace(get("org.gnome.system.proxy", "ignore-hosts")), "[]@as ")
		var hosts []string
		for _, h := range strings.Split(lst, ",") {
			hosts = append(hosts, unq(h))
		}
		s.Bypass = normBypass(hosts)
	case "auto":
		s.PAC = unq(get("org.gnome.system.proxy", "autoconfig-url"))
		if s.PAC == "" {
			s.PAC = "(PAC)"
		}
	}
	return s
}

func stripScheme(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	return strings.TrimRight(v, "/")
}

// normBypass maps OS exception syntax to NO_PROXY syntax: "*.corp" -> ".corp",
// "<local>" -> localhost + loopback, "169.254/16" -> "169.254.0.0/16".
func normBypass(in []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range in {
		v = strings.TrimSpace(v)
		switch {
		case v == "":
		case v == "<local>":
			add("localhost")
			add("127.0.0.1")
			add("::1")
		case strings.HasPrefix(v, "*."):
			add(v[1:])
		case strings.HasPrefix(v, "*"):
			add(strings.TrimPrefix(v, "*"))
		case strings.Contains(v, "/"):
			ip, bits, _ := strings.Cut(v, "/")
			for strings.Count(ip, ".") < 3 && !strings.Contains(ip, ":") {
				ip += ".0"
			}
			add(ip + "/" + bits)
		default:
			add(v)
		}
	}
	add("localhost")
	add("127.0.0.1")
	return out
}
