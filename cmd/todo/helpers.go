package main

import (
	"os"
	"strings"
	"time"
)

// whenShort renders an ISO 8601 timestamp as "YYYY-MM-DD HH:MM" — the day and the time to the
// minute, which is what a commit list wants at a glance; the seconds and the zone offset are noise
// there. An unparseable value is returned as-is rather than dropped.
func whenShort(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.Format("2006-01-02 15:04")
}

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
