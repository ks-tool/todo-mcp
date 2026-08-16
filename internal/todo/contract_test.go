package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckContract covers graphify-06: two OpenAPI specs compare to the endpoints that still line
// up, an orphan-call for one the provider dropped, and a schema-drift for a response field the
// provider stopped returning — with $ref schemas resolved.
func TestCheckContract(t *testing.T) {
	// The provider's User response is {id, name}; the consumer expects {id, name, email} and also
	// calls a /legacy the provider no longer has.
	provider := `{"paths":{"/users/{id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}},"/users":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/NewUser"}}}},"responses":{"201":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Created"}}}}}}}},"components":{"schemas":{"User":{"properties":{"id":{},"name":{}}},"NewUser":{"properties":{"name":{},"email":{}}},"Created":{"properties":{"id":{}}}}}}`
	consumer := `{"paths":{"/users/{id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}},"/users":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/NewUser"}}}},"responses":{"201":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Created"}}}}}}},"/legacy":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}},"components":{"schemas":{"User":{"properties":{"id":{},"name":{},"email":{}}},"NewUser":{"properties":{"name":{},"email":{}}},"Created":{"properties":{"id":{}}}}}}`

	dir := t.TempDir()
	pp := filepath.Join(dir, "provider.json")
	cp := filepath.Join(dir, "consumer.json")
	if err := os.WriteFile(pp, []byte(provider), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := CheckContractFiles(cp, pp)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Matched) != 1 || c.Matched[0].key() != "POST /users" {
		t.Errorf("matched wrong: %+v", c.Matched)
	}
	kinds := map[string]ContractBreak{}
	for _, b := range c.Breaks {
		kinds[b.Kind] = b
	}
	if len(c.Breaks) != 2 {
		t.Fatalf("want 2 breaks, got %d: %+v", len(c.Breaks), c.Breaks)
	}
	if o, ok := kinds["orphan-call"]; !ok || o.Path != "/legacy" {
		t.Errorf("orphan-call wrong: %+v", o)
	}
	if d, ok := kinds["schema-drift"]; !ok || d.Path != "/users/{id}" || !strings.Contains(d.Detail, "email") {
		t.Errorf("schema-drift wrong: %+v", d)
	}

	// A spec against itself is intact.
	same, err := CheckContractFiles(pp, pp)
	if err != nil {
		t.Fatal(err)
	}
	if len(same.Breaks) != 0 || len(same.Matched) != 2 {
		t.Errorf("a spec against itself must be intact: matched=%d breaks=%d", len(same.Matched), len(same.Breaks))
	}
}

// TestCheckContractYAML covers graphify-07: the same check works on YAML specs — loadSpec detects
// the format by extension and decodes YAML through the same struct.
func TestCheckContractYAML(t *testing.T) {
	providerYAML := "" +
		"paths:\n" +
		"  /users/{id}:\n" +
		"    get:\n" +
		"      responses:\n" +
		"        '200':\n" +
		"          content:\n" +
		"            application/json:\n" +
		"              schema:\n" +
		"                properties:\n" +
		"                  id: {}\n" +
		"                  name: {}\n"
	consumerYAML := providerYAML +
		"                  email: {}\n" +
		"  /legacy:\n" +
		"    get:\n" +
		"      responses:\n" +
		"        '200':\n" +
		"          content:\n" +
		"            application/json:\n" +
		"              schema:\n" +
		"                properties:\n" +
		"                  id: {}\n"

	dir := t.TempDir()
	pp := filepath.Join(dir, "provider.yaml")
	cp := filepath.Join(dir, "consumer.yaml")
	if err := os.WriteFile(pp, []byte(providerYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, []byte(consumerYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := CheckContractFiles(cp, pp)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]ContractBreak{}
	for _, b := range c.Breaks {
		kinds[b.Kind] = b
	}
	if len(c.Breaks) != 2 {
		t.Fatalf("want 2 breaks from YAML specs, got %d: %+v", len(c.Breaks), c.Breaks)
	}
	if o, ok := kinds["orphan-call"]; !ok || o.Path != "/legacy" {
		t.Errorf("orphan-call wrong: %+v", o)
	}
	if d, ok := kinds["schema-drift"]; !ok || !strings.Contains(d.Detail, "email") {
		t.Errorf("schema-drift wrong: %+v", d)
	}
}
