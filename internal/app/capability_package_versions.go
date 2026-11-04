package app

// A bounded, explicit subset of npm SemVer and Python stable version specifiers.
// Unsupported syntax is an error, never an unconstrained match.
import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

type packageVersion [3]int

var stablePackageVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var rangeOperatorSpace = regexp.MustCompile(`(>=|<=|>|<|=|\^|~)\s+`)

func parsePackageVersion(s string) (packageVersion, error) {
	var out packageVersion
	m := stablePackageVersion.FindStringSubmatch(s)
	if m == nil {
		return out, errors.New("requires stable numeric version")
	}
	for i := range out {
		v, e := strconv.Atoi(m[i+1])
		if e != nil || v > 1_000_000_000 {
			return out, errors.New("version component too large")
		}
		out[i] = v
	}
	return out, nil
}
func comparePackageVersion(a, b packageVersion) int {
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
func partialPackageVersion(s string) (packageVersion, int, error) {
	var out packageVersion
	if s == "*" || s == "x" || s == "X" || s == "" {
		return out, 0, nil
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, 0, errors.New("unsupported version syntax")
	}
	n := 0
	wild := false
	for i, p := range parts {
		if p == "x" || p == "X" || p == "*" {
			wild = true
			continue
		}
		if wild || p == "" || len(p) > 1 && p[0] == '0' {
			return out, 0, errors.New("invalid version wildcard")
		}
		if strings.Trim(p, "0123456789") != "" {
			return out, 0, errors.New("invalid numeric version")
		}
		v, e := strconv.Atoi(p)
		if e != nil || v < 0 || v > 1_000_000_000 {
			return out, 0, errors.New("invalid numeric version")
		}
		out[i] = v
		n++
	}
	return out, n, nil
}
func matchNPMRange(version, expression string) (bool, error) {
	actual, e := parsePackageVersion(version)
	if e != nil {
		return false, e
	}
	expression = strings.TrimSpace(expression)
	if len(expression) > 256 {
		return false, errors.New("version range too long")
	}
	alternatives := strings.Split(expression, "||")
	if len(alternatives) > 8 {
		return false, errors.New("too many version alternatives")
	}
	result := false
	for _, alt := range alternatives {
		alt = strings.TrimSpace(alt)
		if strings.Contains(alt, " - ") {
			parts := strings.Split(alt, " - ")
			if len(parts) != 2 {
				return false, errors.New("invalid hyphen range")
			}
			lo, _, e := partialPackageVersion(strings.TrimSpace(parts[0]))
			if e != nil {
				return false, e
			}
			hi, n, e := partialPackageVersion(strings.TrimSpace(parts[1]))
			if e != nil {
				return false, e
			}
			upper := comparePackageVersion(actual, hi) <= 0
			if n < 3 {
				if n == 0 {
					upper = true
				} else {
					hi[n-1]++
					upper = comparePackageVersion(actual, hi) < 0
				}
			}
			result = result || (comparePackageVersion(actual, lo) >= 0 && upper)
			continue
		}
		alt = rangeOperatorSpace.ReplaceAllString(alt, "$1")
		terms := strings.Fields(alt)
		if len(terms) == 0 {
			terms = []string{"*"}
		}
		if len(terms) > 8 {
			return false, errors.New("too many version comparisons")
		}
		matches := true
		for _, term := range terms {
			op := ""
			for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
				if strings.HasPrefix(term, prefix) {
					op = prefix
					term = strings.TrimPrefix(term, prefix)
					break
				}
			}
			lo, n, e := partialPackageVersion(term)
			if e != nil {
				return false, e
			}
			cmp := comparePackageVersion(actual, lo)
			ok := false
			switch op {
			case "", "=":
				if n == 3 {
					ok = cmp == 0
				} else if n == 0 {
					ok = true
				} else {
					hi := lo
					hi[n-1]++
					ok = cmp >= 0 && comparePackageVersion(actual, hi) < 0
				}
			case "^":
				hi := lo
				if n == 0 {
					ok = true
				} else {
					index := 0
					if lo[0] == 0 && n > 1 {
						index = 1
						if lo[1] == 0 && n > 2 {
							index = 2
						}
					}
					hi[index]++
					for k := index + 1; k < 3; k++ {
						hi[k] = 0
					}
					ok = cmp >= 0 && comparePackageVersion(actual, hi) < 0
				}
			case "~":
				hi := lo
				if n == 0 {
					ok = true
				} else {
					index := 0
					if n > 1 {
						index = 1
					}
					hi[index]++
					for k := index + 1; k < 3; k++ {
						hi[k] = 0
					}
					ok = cmp >= 0 && comparePackageVersion(actual, hi) < 0
				}
			case ">=":
				ok = cmp >= 0
			case "<":
				ok = cmp < 0
			case ">":
				if n == 0 {
					ok = false
				} else if n < 3 {
					hi := lo
					hi[n-1]++
					ok = comparePackageVersion(actual, hi) >= 0
				} else {
					ok = cmp > 0
				}
			case "<=":
				if n == 0 {
					ok = true
				} else if n < 3 {
					hi := lo
					hi[n-1]++
					ok = comparePackageVersion(actual, hi) < 0
				} else {
					ok = cmp <= 0
				}
			}
			matches = matches && ok
		}
		result = result || matches
	}
	return result, nil
}

func intersectNPMRanges(expressions ...string) (string, error) {
	alternatives := []string{""}
	for _, expression := range expressions {
		if _, e := matchNPMRange("1.0.0", expression); e != nil {
			return "", e
		}
		next := []string{}
		for _, left := range alternatives {
			for _, right := range strings.Split(expression, "||") {
				if strings.Contains(right, " - ") {
					return "", errors.New("peer intersection with hyphen range needs explicit comparisons")
				}
				next = append(next, strings.TrimSpace(left+" "+strings.TrimSpace(right)))
			}
		}
		if len(next) > 8 {
			return "", errors.New("too many peer range alternatives")
		}
		alternatives = next
	}
	combined := strings.Join(alternatives, " || ")
	if _, e := matchNPMRange("1.0.0", combined); e != nil {
		return "", e
	}
	return combined, nil
}
