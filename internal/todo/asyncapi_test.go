package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckContractAsyncAPI covers graphify-10: an AsyncAPI document reduces to (channel, operation)
// endpoints with message-payload signatures, and the same orphan-call / schema-drift detection runs.
func TestCheckContractAsyncAPI(t *testing.T) {
	provider := `{"asyncapi":"2.6.0","channels":{"user/signedup":{"subscribe":{"message":{"payload":{"$ref":"#/components/schemas/UserSignedUp"}}}},"user/deleted":{"subscribe":{"message":{"payload":{"$ref":"#/components/schemas/UserDeleted"}}}}},"components":{"schemas":{"UserSignedUp":{"properties":{"id":{},"name":{}}},"UserDeleted":{"properties":{"id":{}}}}}}`
	consumer := `{"asyncapi":"2.6.0","channels":{"user/signedup":{"subscribe":{"message":{"payload":{"$ref":"#/components/schemas/UserSignedUp"}}}},"user/deleted":{"subscribe":{"message":{"payload":{"$ref":"#/components/schemas/UserDeleted"}}}},"user/legacy":{"subscribe":{"message":{"payload":{"properties":{"id":{}}}}}}},"components":{"schemas":{"UserSignedUp":{"properties":{"id":{},"name":{},"email":{}}},"UserDeleted":{"properties":{"id":{}}}}}}`
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
	if len(c.Matched) != 1 || c.Matched[0].key() != "SUB user/deleted" {
		t.Errorf("SUB user/deleted should match: %+v", c.Matched)
	}
	kinds := map[string]ContractBreak{}
	for _, b := range c.Breaks {
		kinds[b.Kind] = b
	}
	if len(c.Breaks) != 2 {
		t.Fatalf("want 2 breaks, got %d: %+v", len(c.Breaks), c.Breaks)
	}
	if o, ok := kinds["orphan-call"]; !ok || o.Path != "user/legacy" {
		t.Errorf("orphan-call wrong: %+v", o)
	}
	if d, ok := kinds["schema-drift"]; !ok || d.Path != "user/signedup" || !strings.Contains(d.Detail, "email") {
		t.Errorf("schema-drift wrong: %+v", d)
	}
}
