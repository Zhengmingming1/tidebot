package app

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var registryPythonStableVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)
var pythonRequirementHead = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[([A-Za-z0-9_., -]+)\])?\s*(.*)$`)

type pythonNumericVersion [4]int

func pythonVersionNumber(s string) (pythonNumericVersion, int, error) {
	var v pythonNumericVersion
	if !registryPythonStableVersion.MatchString(s) {
		return v, 0, errors.New("unsupported Python version")
	}
	parts := strings.Split(s, ".")
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil || n > 1_000_000_000 {
			return v, 0, errors.New("Python version component too large")
		}
		v[i] = n
	}
	return v, len(parts), nil
}
func comparePythonVersion(a, b pythonNumericVersion) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
func matchPythonVersion(version, spec string) (bool, error) {
	v, _, e := pythonVersionNumber(version)
	if e != nil {
		return false, e
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true, nil
	}
	if len(spec) > 256 {
		return false, errors.New("Python version specifier too long")
	}
	terms := strings.Split(spec, ",")
	if len(terms) > 16 {
		return false, errors.New("too many Python comparisons")
	}
	matches := true
	for _, term := range terms {
		term = strings.TrimSpace(term)
		op := ""
		for _, prefix := range []string{"~=", "==", "!=", ">=", "<=", ">", "<"} {
			if strings.HasPrefix(term, prefix) {
				op = prefix
				term = strings.TrimSpace(strings.TrimPrefix(term, prefix))
				break
			}
		}
		if op == "" {
			return false, errors.New("unsupported Python version specifier")
		}
		wildcard := strings.HasSuffix(term, ".*")
		if wildcard {
			if op != "==" && op != "!=" {
				return false, errors.New("invalid Python wildcard")
			}
			term = strings.TrimSuffix(term, ".*")
		}
		want, n, e := pythonVersionNumber(term)
		if e != nil {
			return false, e
		}
		cmp := comparePythonVersion(v, want)
		ok := false
		if wildcard {
			ok = true
			for i := 0; i < n; i++ {
				ok = ok && v[i] == want[i]
			}
			if op == "!=" {
				ok = !ok
			}
		} else {
			switch op {
			case "==":
				ok = cmp == 0
			case "!=":
				ok = cmp != 0
			case ">=":
				ok = cmp >= 0
			case "<=":
				ok = cmp <= 0
			case ">":
				ok = cmp > 0
			case "<":
				ok = cmp < 0
			case "~=":
				if n < 2 {
					return false, errors.New("compatible Python release requires two components")
				}
				upper := want
				upper[n-2]++
				for i := n - 1; i < 4; i++ {
					upper[i] = 0
				}
				ok = cmp >= 0 && comparePythonVersion(v, upper) < 0
			}
		}
		matches = matches && ok
	}
	return matches, nil
}

type pythonRequirement struct {
	name, spec, marker string
	extras             []string
}

func parsePythonRequirement(s string) (pythonRequirement, error) {
	var out pythonRequirement
	if len(s) > 2048 {
		return out, errors.New("Python requirement too long")
	}
	head, marker, _ := strings.Cut(s, ";")
	m := pythonRequirementHead.FindStringSubmatch(strings.TrimSpace(head))
	if m == nil {
		return out, errors.New("unsupported Python requirement")
	}
	out.name = normalizePythonPackage(m[1])
	out.spec = strings.TrimSpace(m[3])
	out.marker = strings.TrimSpace(marker)
	if strings.HasPrefix(out.spec, "(") {
		if !strings.HasSuffix(out.spec, ")") {
			return out, errors.New("invalid requirement parentheses")
		}
		out.spec = strings.TrimSpace(out.spec[1 : len(out.spec)-1])
	}
	if _, e := matchPythonVersion("1.0.0", out.spec); e != nil {
		return out, e
	}
	if m[2] != "" {
		for _, extra := range strings.Split(m[2], ",") {
			extra = normalizePythonPackage(strings.TrimSpace(extra))
			if !registryPythonName.MatchString(extra) {
				return out, errors.New("invalid Python extra")
			}
			out.extras = append(out.extras, extra)
		}
	}
	return out, nil
}
func tokenizePythonMarker(s string) ([]string, error) {
	if len(s) > 1024 {
		return nil, errors.New("Python marker too long")
	}
	out := []string{}
	for i := 0; i < len(s); {
		c := s[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '\'' || c == '"' {
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' || s[j] == '\n' || s[j] == '\r' {
					return nil, errors.New("unsupported marker string")
				}
				j++
			}
			if j == len(s) {
				return nil, errors.New("unterminated marker string")
			}
			out = append(out, s[i:j+1])
			i = j + 1
		} else if strings.ContainsRune("()", rune(c)) {
			out = append(out, s[i:i+1])
			i++
		} else if strings.ContainsRune("=!<>~", rune(c)) {
			j := i + 1
			if j < len(s) && s[j] == '=' {
				j++
			}
			out = append(out, s[i:j])
			i = j
		} else {
			j := i
			for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] == '_') {
				j++
			}
			if j == i {
				return nil, errors.New("unsupported marker token")
			}
			out = append(out, s[i:j])
			i = j
		}
		if len(out) > 128 {
			return nil, errors.New("too many marker tokens")
		}
	}
	return out, nil
}
func evaluatePythonMarker(s string, env map[string]string) (bool, error) {
	if strings.TrimSpace(s) == "" {
		return true, nil
	}
	tokens, e := tokenizePythonMarker(s)
	if e != nil {
		return false, e
	}
	pos := 0
	depth := 0
	var expression, conjunction, atom func() (bool, error)
	operand := func() (string, string, error) {
		if pos >= len(tokens) {
			return "", "", errors.New("missing marker operand")
		}
		t := tokens[pos]
		pos++
		if strings.HasPrefix(t, "'") || strings.HasPrefix(t, `"`) {
			return t[1 : len(t)-1], "", nil
		}
		value, ok := env[t]
		if !ok {
			return "", "", errors.New("unknown interpreter marker: " + t)
		}
		return value, t, nil
	}
	atom = func() (bool, error) {
		depth++
		defer func() { depth-- }()
		if depth > 16 {
			return false, errors.New("marker nesting exceeded")
		}
		if pos < len(tokens) && tokens[pos] == "(" {
			pos++
			v, e := expression()
			if e != nil {
				return false, e
			}
			if pos >= len(tokens) || tokens[pos] != ")" {
				return false, errors.New("unclosed marker group")
			}
			pos++
			return v, nil
		}
		left, lk, e := operand()
		if e != nil {
			return false, e
		}
		if pos >= len(tokens) {
			return false, errors.New("missing marker operator")
		}
		op := tokens[pos]
		pos++
		if op == "not" {
			if pos >= len(tokens) || tokens[pos] != "in" {
				return false, errors.New("invalid marker operator")
			}
			pos++
			op = "not in"
		}
		right, rk, e := operand()
		if e != nil {
			return false, e
		}
		switch op {
		case "in":
			return strings.Contains(right, left), nil
		case "not in":
			return !strings.Contains(right, left), nil
		}
		if strings.HasPrefix(lk, "python_") || strings.HasPrefix(rk, "python_") || lk == "implementation_version" || rk == "implementation_version" {
			return matchPythonVersion(left, op+right)
		}
		switch op {
		case "==":
			return left == right, nil
		case "!=":
			return left != right, nil
		default:
			return false, errors.New("unsupported non-version marker comparison")
		}
	}
	conjunction = func() (bool, error) {
		v, e := atom()
		if e != nil {
			return false, e
		}
		for pos < len(tokens) && tokens[pos] == "and" {
			pos++
			next, e := atom()
			if e != nil {
				return false, e
			}
			v = v && next
		}
		return v, nil
	}
	expression = func() (bool, error) {
		v, e := conjunction()
		if e != nil {
			return false, e
		}
		for pos < len(tokens) && tokens[pos] == "or" {
			pos++
			next, e := conjunction()
			if e != nil {
				return false, e
			}
			v = v || next
		}
		return v, nil
	}
	result, e := expression()
	if e != nil {
		return false, e
	}
	if pos != len(tokens) {
		return false, errors.New("trailing marker tokens")
	}
	return result, nil
}
