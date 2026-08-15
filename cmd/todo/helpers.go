package main

import (
	"os"
	"strings"
)

// rootEpic is the first entry of $TODO_EPICS — the comma-separated list of this project's epics
// that install writes into the server's environment. An add that names no epic lands there.
func rootEpic() string {
	if xs := splitComma(os.Getenv("TODO_EPICS")); len(xs) > 0 {
		return xs[0]
	}
	return ""
}

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
