package todo

import (
	"os"
	"regexp"
	"strings"
)

// gRPC / Protobuf contract extraction. A .proto is reduced to the same Endpoint model as an OpenAPI
// path: each rpc is an endpoint keyed by Service/Method, its request and response signatures the
// field names of the request and response messages. This is a minimal proto3 reader — services,
// rpcs and flat message fields — not a full compiler, which is all a contract diff needs.

var (
	reProtoMessage = regexp.MustCompile(`(?s)\bmessage\s+(\w+)\s*\{(.*?)\}`)
	reProtoField   = regexp.MustCompile(`(\w+)\s*=\s*\d+\s*;`)
	reProtoService = regexp.MustCompile(`(?s)\bservice\s+(\w+)\s*\{(.*?)\}`)
	reProtoRPC     = regexp.MustCompile(`\brpc\s+(\w+)\s*\(\s*(?:stream\s+)?([\w.]+)\s*\)\s*returns\s*\(\s*(?:stream\s+)?([\w.]+)\s*\)`)
	reLineComment  = regexp.MustCompile(`//[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func protoEndpointsFile(path string) (map[string]Endpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return protoEndpoints(string(b)), nil
}

func protoEndpoints(src string) map[string]Endpoint {
	src = reBlockComment.ReplaceAllString(src, "")
	src = reLineComment.ReplaceAllString(src, "")

	msg := map[string][]string{} // message name → field names
	for _, m := range reProtoMessage.FindAllStringSubmatch(src, -1) {
		var fields []string
		for _, f := range reProtoField.FindAllStringSubmatch(m[2], -1) {
			fields = append(fields, f[1])
		}
		msg[m[1]] = sortUniq(fields)
	}

	out := map[string]Endpoint{}
	for _, s := range reProtoService.FindAllStringSubmatch(src, -1) {
		service := s[1]
		for _, r := range reProtoRPC.FindAllStringSubmatch(s[2], -1) {
			e := Endpoint{Method: "RPC", Path: service + "/" + r[1], OpID: r[1],
				Request: msg[shortType(r[2])], Response: msg[shortType(r[3])]}
			out[e.key()] = e
		}
	}
	return out
}

// shortType drops a package qualifier, so foo.CreateUserRequest resolves to the message
// CreateUserRequest.
func shortType(t string) string {
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}
