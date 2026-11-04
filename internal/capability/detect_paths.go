package capability

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Login startup files can execute arbitrary commands, so discovery never starts
// a shell. Only files consisting entirely of comments and literal PATH
// assignments are understood; richer profiles must use an explicit tool path.
func loginPathHints(home, shell, inherited string) []string {
	if runtime.GOOS == "windows" || !filepath.IsAbs(home) {
		return nil
	}
	names := []string{".profile"}
	switch filepath.Base(shell) {
	case "bash":
		names = []string{".bash_profile", ".bash_login", ".profile"}
	case "zsh":
		names = []string{".zprofile"}
	case "sh", "dash", "":
	default:
		return nil
	}
	for _, name := range names {
		p := filepath.Join(home, name)
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
		f.Close()
		if err != nil || len(raw) > 64<<10 {
			return nil
		}
		return literalProfilePATH(string(raw), home, inherited)
	}
	return nil
}

var literalPathVariable = regexp.MustCompile(`\$(?:\{(?:HOME|PATH)\}|(?:HOME|PATH)\b)`)

func literalProfilePATH(profile, home, inherited string) []string {
	value := inherited
	changed := false
	for _, line := range strings.Split(profile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		if !strings.HasPrefix(line, "PATH=") {
			return nil
		}
		expression := strings.TrimPrefix(line, "PATH=")
		if len(expression) >= 2 && expression[0] == '"' && expression[len(expression)-1] == '"' {
			expression = expression[1 : len(expression)-1]
		} else if strings.ContainsAny(expression, " \t\"'") {
			return nil
		}
		if strings.ContainsAny(expression, "\x00\r\n`\\;|&<>()") {
			return nil
		}
		expression = literalPathVariable.ReplaceAllStringFunc(expression, func(v string) string {
			if v == "$HOME" || v == "${HOME}" {
				return home
			}
			return value
		})
		if strings.ContainsAny(expression, "$\"'") {
			return nil
		}
		dirs := filepath.SplitList(expression)
		for _, dir := range dirs {
			if dir != "" && !filepath.IsAbs(dir) {
				return nil
			}
		}
		value = expression
		changed = true
	}
	if !changed {
		return nil
	}
	return filepath.SplitList(value)
}

func commonToolDirectories(home string) []string {
	if runtime.GOOS == "windows" {
		dirs := []string{}
		if filepath.IsAbs(home) {
			dirs = append(dirs, filepath.Join(home, "scoop", "shims"), filepath.Join(home, ".local", "bin"))
		}
		for _, key := range []string{"LOCALAPPDATA", "APPDATA", "ProgramFiles"} {
			root := os.Getenv(key)
			if !filepath.IsAbs(root) {
				continue
			}
			switch key {
			case "LOCALAPPDATA":
				dirs = append(dirs, filepath.Join(root, "Programs", "nodejs"), filepath.Join(root, "Microsoft", "WinGet", "Links"))
			case "APPDATA":
				dirs = append(dirs, filepath.Join(root, "npm"))
			case "ProgramFiles":
				dirs = append(dirs, filepath.Join(root, "nodejs"), filepath.Join(root, "GitHub CLI"))
			}
		}
		return dirs
	}
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".cargo", "bin"))
	}
	return dirs
}
