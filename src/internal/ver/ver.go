package ver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var core = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?`)

func split(v string) []string {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '+' || r == '_' || r == '~'
	})
}

func isNum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func Compare(a, b string) int {
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		if i >= len(pa) {
			if isNum(pb[i]) {
				return -1
			}
			return 1
		}
		if i >= len(pb) {
			if isNum(pa[i]) {
				return 1
			}
			return -1
		}
		x, y := pa[i], pb[i]
		if isNum(x) && isNum(y) {
			nx, _ := strconv.ParseUint(x, 10, 64)
			ny, _ := strconv.ParseUint(y, 10, 64)
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
			continue
		}
		if isNum(x) != isNum(y) {
			if isNum(x) {
				return 1
			}
			return -1
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func Bump(v, kind string) (string, error) {
	m := core.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", fmt.Errorf("cannot bump %q: expected a version like 1.2 or 1.2.3", v)
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	pat := 0
	if m[3] != "" {
		pat, _ = strconv.Atoi(m[3])
	}
	switch kind {
	case "major":
		maj, mnr, pat = maj+1, 0, 0
	case "minor":
		mnr, pat = mnr+1, 0
	case "patch":
		pat++
	default:
		return "", fmt.Errorf("unknown bump kind %q (patch, minor or major)", kind)
	}
	return fmt.Sprintf("%d.%d.%d", maj, mnr, pat), nil
}
