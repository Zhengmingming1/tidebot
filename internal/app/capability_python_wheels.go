package app

import (
	"strconv"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
)

func wheelPlatformMatches(tag string, target *capability.RecipePythonTarget) bool {
	if target == nil {
		return false
	}
	machine := strings.ToLower(target.Machine)
	arch := ""
	switch machine {
	case "amd64", "x86_64":
		arch = "x86_64"
	case "arm64", "aarch64":
		arch = "aarch64"
	default:
		return false
	}
	switch target.SysPlatform {
	case "win32":
		return tag == "win_amd64" && arch == "x86_64" || tag == "win_arm64" && arch == "aarch64"
	case "linux":
		libc, ok := strings.CutPrefix(target.Libc, "glibc ")
		if !ok {
			return false
		}
		have, _, e := pythonVersionNumber(libc)
		if e != nil {
			return false
		}
		if !strings.HasSuffix(tag, "_"+arch) {
			return false
		}
		tag = strings.TrimSuffix(tag, "_"+arch)
		var floor string
		switch tag {
		case "manylinux1":
			floor = "2.5"
		case "manylinux2010":
			floor = "2.12"
		case "manylinux2014":
			floor = "2.17"
		default:
			if !strings.HasPrefix(tag, "manylinux_") {
				return false
			}
			floor = strings.ReplaceAll(strings.TrimPrefix(tag, "manylinux_"), "_", ".")
		}
		want, count, e := pythonVersionNumber(floor)
		return e == nil && count == 2 && comparePythonVersion(have, want) >= 0
	case "darwin":
		parts := strings.Split(tag, "_")
		if len(parts) < 4 || parts[0] != "macosx" {
			return false
		}
		wheelArch := strings.Join(parts[3:], "_")
		if wheelArch != "universal2" && !(wheelArch == "arm64" && arch == "aarch64") && !(wheelArch == "x86_64" && arch == "x86_64") {
			return false
		}
		actual := strings.Split(target.Platform, "-")
		if len(actual) != 3 || actual[0] != "macosx" {
			return false
		}
		have, _, e := pythonVersionNumber(actual[1])
		if e != nil {
			return false
		}
		want, _, e := pythonVersionNumber(parts[1] + "." + parts[2])
		return e == nil && comparePythonVersion(have, want) >= 0
	}
	return false
}
func pythonWheelRank(filename string, target *capability.RecipePythonTarget) int {
	if !strings.HasSuffix(filename, ".whl") || strings.ContainsAny(filename, "/\\\x00") {
		return 0
	}
	parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
	if len(parts) != 5 && len(parts) != 6 {
		return 0
	}
	py, abi, platform := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	for _, p := range strings.Split(py, ".") {
		if p == "py3" && abi == "none" && platform == "any" {
			return 1000
		}
	}
	if target == nil || target.Implementation != "cpython" || target.GILDisabled {
		return 0
	}
	v, n, e := pythonVersionNumber(target.Version)
	if e != nil || n < 2 || v[0] != 3 {
		return 0
	}
	current := "cp" + strconv.Itoa(v[0]) + strconv.Itoa(v[1])
	rank := 0
	for _, p := range strings.Split(py, ".") {
		if p == current && (abi == current || abi == "none") {
			rank = 100
		}
		if abi == "abi3" && strings.HasPrefix(p, "cp3") {
			minor, e := strconv.Atoi(strings.TrimPrefix(p, "cp3"))
			if e == nil && minor <= v[1] && minor >= 2 && rank < 50 {
				rank = 50
			}
		}
	}
	if rank == 0 {
		return 0
	}
	for _, p := range strings.Split(platform, ".") {
		if wheelPlatformMatches(p, target) {
			return rank
		}
	}
	return 0
}
