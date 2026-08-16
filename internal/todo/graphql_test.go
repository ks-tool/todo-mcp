package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckContractGraphQL covers graphify-09: a GraphQL SDL reduces to Query/Mutation operations
// with argument + return-field signatures, and the same orphan-call / schema-drift detection runs.
func TestCheckContractGraphQL(t *testing.T) {
	provider := `
type Query { user(id: ID!): User }
type Mutation { createUser(name: String!, email: String!): ID }
type User { id: ID! name: String }
`
	consumer := `
type Query { user(id: ID!): User  legacy: String }
type Mutation { createUser(name: String!, email: String!): ID }
type User { id: ID! name: String email: String }
`
	dir := t.TempDir()
	pp := filepath.Join(dir, "provider.graphql")
	cp := filepath.Join(dir, "consumer.graphql")
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
	if len(c.Matched) != 1 || c.Matched[0].key() != "Mutation createUser" {
		t.Errorf("Mutation createUser should match: %+v", c.Matched)
	}
	kinds := map[string]ContractBreak{}
	for _, b := range c.Breaks {
		kinds[b.Kind] = b
	}
	if len(c.Breaks) != 2 {
		t.Fatalf("want 2 breaks, got %d: %+v", len(c.Breaks), c.Breaks)
	}
	if o, ok := kinds["orphan-call"]; !ok || o.Path != "legacy" {
		t.Errorf("orphan-call wrong: %+v", o)
	}
	if d, ok := kinds["schema-drift"]; !ok || d.Path != "user" || !strings.Contains(d.Detail, "email") {
		t.Errorf("schema-drift wrong: %+v", d)
	}
}
