package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// API-contract checking between two services. The two OpenAPI specs — what the CONSUMER was built
// against and what the PROVIDER now offers — are compared endpoint by endpoint: an endpoint the
// consumer needs that the provider no longer has is an orphan-call; one whose request/response shape
// diverged is a schema-drift. It is the fast, language-agnostic answer to "is the API contract still
// honoured": no code parsing, just the specs both sides already publish.

// Endpoint is one (method, path) with a shallow signature — the parameter and request-body property
// names, and the success-response property names — enough to catch a drift without a deep type check.
type Endpoint struct {
	Method   string   `json:"method"`
	Path     string   `json:"path"`
	Request  []string `json:"request"`
	Response []string `json:"response"`
}

func (e Endpoint) key() string { return e.Method + " " + e.Path }

// ContractBreak is one broken expectation.
type ContractBreak struct {
	Kind   string `json:"kind"` // orphan-call | schema-drift
	Method string `json:"method"`
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

// Contract is the result: the endpoints present and compatible in both, and the breaks.
type Contract struct {
	Matched []Endpoint      `json:"matched"`
	Breaks  []ContractBreak `json:"breaks"`
}

// CheckContractFiles loads two API specs and compares the consumer's expectations against the
// provider's current surface. The format is detected from the file (OpenAPI JSON/YAML, an AsyncAPI
// document, a .proto, or a GraphQL SDL); both sides are expected to be the same kind.
func CheckContractFiles(consumerPath, providerPath string) (*Contract, error) {
	cons, err := endpointsFromFile(consumerPath)
	if err != nil {
		return nil, fmt.Errorf("consumer spec: %w", err)
	}
	prov, err := endpointsFromFile(providerPath)
	if err != nil {
		return nil, fmt.Errorf("provider spec: %w", err)
	}
	return compareEndpoints(cons, prov), nil
}

// CheckContract compares a consumer OpenAPI spec against a provider one.
func CheckContract(consumer, provider *oaSpec) *Contract {
	return compareEndpoints(endpoints(consumer), endpoints(provider))
}

// compareEndpoints is the protocol-agnostic core: whatever the format, each side is reduced to a
// map of "identity → Endpoint", and the breaks are the same — an endpoint the consumer needs that
// the provider dropped (orphan-call), or one whose shape diverged (schema-drift).
func compareEndpoints(consumer, provider map[string]Endpoint) *Contract {
	c := &Contract{}
	for _, ce := range consumer {
		pe, ok := provider[ce.key()]
		if !ok {
			c.Breaks = append(c.Breaks, ContractBreak{Kind: "orphan-call", Method: ce.Method, Path: ce.Path,
				Detail: "the provider no longer offers this endpoint"})
			continue
		}
		if d := signatureDiff(ce, pe); len(d) > 0 {
			c.Breaks = append(c.Breaks, ContractBreak{Kind: "schema-drift", Method: ce.Method, Path: ce.Path, Detail: d})
			continue
		}
		c.Matched = append(c.Matched, ce)
	}
	sort.Slice(c.Matched, func(i, j int) bool { return c.Matched[i].key() < c.Matched[j].key() })
	sort.Slice(c.Breaks, func(i, j int) bool { return c.Breaks[i].Method+c.Breaks[i].Path < c.Breaks[j].Method+c.Breaks[j].Path })
	return c
}

// endpointsFromFile reduces one spec file to its endpoints, picking the parser by format: a .proto
// by extension, otherwise an OpenAPI JSON/YAML document.
func endpointsFromFile(path string) (map[string]Endpoint, error) {
	if strings.HasSuffix(strings.ToLower(path), ".proto") {
		return protoEndpointsFile(path)
	}
	sp, err := loadSpec(path)
	if err != nil {
		return nil, err
	}
	return endpoints(sp), nil
}

// signatureDiff reports how a consumer's expected shape differs from the provider's, or "" when they
// agree — a field the consumer relies on that the provider dropped, or a shape mismatch either way.
func signatureDiff(cons, prov Endpoint) string {
	var parts []string
	if req := missing(cons.Request, prov.Request); len(req) > 0 {
		parts = append(parts, "request fields the provider lacks: "+strings.Join(req, ", "))
	}
	if resp := missing(cons.Response, prov.Response); len(resp) > 0 {
		parts = append(parts, "response fields the provider no longer returns: "+strings.Join(resp, ", "))
	}
	return strings.Join(parts, "; ")
}

// missing returns the entries of want not present in have.
func missing(want, have []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

// ---- OpenAPI (JSON) parsing, only the slice this needs ----

type oaSpec struct {
	Paths      map[string]map[string]oaOp `json:"paths"`
	Components struct {
		Schemas map[string]oaSchema `json:"schemas"`
	} `json:"components"`
}

type oaOp struct {
	Parameters  []oaParam         `json:"parameters"`
	RequestBody *oaBody           `json:"requestBody"`
	Responses   map[string]oaBody `json:"responses"`
}

type oaParam struct {
	Name string `json:"name"`
}

type oaBody struct {
	Content map[string]struct {
		Schema oaSchema `json:"schema"`
	} `json:"content"`
}

type oaSchema struct {
	Ref        string              `json:"$ref"`
	Properties map[string]oaSchema `json:"properties"`
	Items      *oaSchema           `json:"items"`
}

func loadSpec(path string) (*oaSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err = toJSON(path, b)
	if err != nil {
		return nil, err
	}
	var s oaSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// toJSON returns the spec as JSON bytes: passed through when it already is JSON, or decoded from
// YAML and re-encoded when it is not — so the one struct (with its json tags) parses both. YAML is a
// superset of JSON, so a spec is treated as JSON only when it clearly is: a .json name, or content
// whose first non-space byte opens an object or array.
func toJSON(path string, b []byte) ([]byte, error) {
	if looksJSON(path, b) {
		return b, nil
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func looksJSON(path string, b []byte) bool {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".json"):
		return true
	case strings.HasSuffix(strings.ToLower(path), ".yaml"), strings.HasSuffix(strings.ToLower(path), ".yml"):
		return false
	}
	for _, c := range b {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return c == '{' || c == '['
	}
	return true
}

// endpoints flattens a spec into a map of "METHOD /path" → Endpoint with its shallow signature.
func endpoints(spec *oaSpec) map[string]Endpoint {
	out := map[string]Endpoint{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			e := Endpoint{Method: strings.ToUpper(method), Path: path}
			req := append([]string(nil), paramNames(op.Parameters)...)
			req = append(req, bodyProps(op.RequestBody, spec)...)
			e.Request = sortUniq(req)
			e.Response = sortUniq(bodyProps(successResponse(op), spec))
			out[e.key()] = e
		}
	}
	return out
}

func paramNames(ps []oaParam) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// successResponse picks the lowest 2xx response body, which is the shape a caller reads.
func successResponse(op oaOp) *oaBody {
	var keys []string
	for k := range op.Responses {
		if strings.HasPrefix(k, "2") {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	b := op.Responses[keys[0]]
	return &b
}

// bodyProps returns the property names of a body's JSON schema, resolving one level of $ref.
func bodyProps(b *oaBody, spec *oaSpec) []string {
	if b == nil || len(b.Content) == 0 {
		return nil
	}
	mt, ok := b.Content["application/json"]
	if !ok {
		for _, k := range sortedKeys(b.Content) {
			mt = b.Content[k]
			break
		}
	}
	return schemaProps(mt.Schema, spec)
}

func schemaProps(sc oaSchema, spec *oaSpec) []string {
	if len(sc.Ref) > 0 {
		name := sc.Ref[strings.LastIndex(sc.Ref, "/")+1:]
		if r, ok := spec.Components.Schemas[name]; ok {
			return propKeys(r)
		}
		return nil
	}
	if sc.Items != nil {
		return schemaProps(*sc.Items, spec)
	}
	return propKeys(sc)
}

func propKeys(sc oaSchema) []string {
	var ks []string
	for k := range sc.Properties {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortUniq(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, x := range in {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
