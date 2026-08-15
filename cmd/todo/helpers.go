package main

import "strings"

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if len(a) > 0 {
		return a
	}
	return b
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func oneLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return trunc(s, n)
}
