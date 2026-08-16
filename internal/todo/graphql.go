package todo

import (
	"os"
	"regexp"
	"strings"
)

// GraphQL (SDL) contract extraction. A schema is reduced to the same Endpoint model: each field of
// Query / Mutation / Subscription is an endpoint keyed by that operation and the field name, its
// request signature the field's argument names, its response signature the field names of the return
// type (one level). A minimal SDL reader — object types and their fields — not a full validator.

var (
	reGQLType  = regexp.MustCompile(`(?s)\btype\s+(\w+)[^{]*\{(.*?)\}`)
	reGQLField = regexp.MustCompile(`(\w+)\s*(\(([^)]*)\))?\s*:\s*([\[\]\w!]+)`)
	reGQLArg   = regexp.MustCompile(`(\w+)\s*:`)
)

type gqlField struct {
	name string
	args []string
	ret  string
}

func graphqlEndpointsFile(path string) (map[string]Endpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return graphqlEndpoints(string(b)), nil
}

func graphqlEndpoints(src string) map[string]Endpoint {
	src = stripHashComments(src)

	types := map[string][]gqlField{}
	for _, tm := range reGQLType.FindAllStringSubmatch(src, -1) {
		var fs []gqlField
		for _, fm := range reGQLField.FindAllStringSubmatch(tm[2], -1) {
			fs = append(fs, gqlField{name: fm[1], args: gqlArgNames(fm[3]), ret: gqlBaseType(fm[4])})
		}
		types[tm[1]] = fs
	}

	fieldNames := func(typeName string) []string {
		var out []string
		for _, f := range types[typeName] {
			out = append(out, f.name)
		}
		return sortUniq(out)
	}

	out := map[string]Endpoint{}
	for _, op := range []string{"Query", "Mutation", "Subscription"} {
		for _, f := range types[op] {
			e := Endpoint{Method: op, Path: f.name, Request: sortUniq(f.args), Response: fieldNames(f.ret)}
			out[e.key()] = e
		}
	}
	return out
}

func gqlArgNames(args string) []string {
	var out []string
	for _, m := range reGQLArg.FindAllStringSubmatch(args, -1) {
		out = append(out, m[1])
	}
	return out
}

// gqlBaseType strips list and non-null decoration, so [User!]! resolves to the type User.
func gqlBaseType(t string) string {
	return strings.Trim(t, "[]! ")
}

func stripHashComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
