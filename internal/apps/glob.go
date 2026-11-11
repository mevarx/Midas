package apps

import (
	"path"
	"strings"
)

// MatchGlob matches a slash-separated relative path against a glob where **
// matches any number of path segments and each other segment follows
// path.Match semantics. Both arguments are interpreted with / separators.
func MatchGlob(pattern, rel string) bool {
	p := splitGlob(pattern)
	r := splitGlob(rel)
	return matchSegs(p, r)
}

func splitGlob(s string) []string {
	s = strings.ReplaceAll(s, `\`, "/")
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "/")
	if s == "" || s == "." {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegs(pat, rel []string) bool {
	if len(pat) == 0 {
		return len(rel) == 0
	}
	if pat[0] == "**" {
		if matchSegs(pat[1:], rel) {
			return true
		}
		for i := 0; i < len(rel); i++ {
			if matchSegs(pat[1:], rel[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], rel[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], rel[1:])
}

// MatchAny reports whether rel matches any of the patterns.
func MatchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if MatchGlob(p, rel) {
			return true
		}
	}
	return false
}
