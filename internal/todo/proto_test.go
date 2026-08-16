package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckContractProto covers graphify-08: a .proto reduces to rpc endpoints (Service/Method) with
// request/response message field signatures, and the same orphan-call / schema-drift detection runs.
func TestCheckContractProto(t *testing.T) {
	provider := `syntax = "proto3";
package u;
message CreateReq { string name = 1; string email = 2; }
message CreateResp { string id = 1; }
message GetReq { string id = 1; }
message User { string id = 1; string name = 2; }
service Users {
  rpc Create(CreateReq) returns (CreateResp);
  rpc Get(GetReq) returns (User);
}`
	consumer := `syntax = "proto3";
package u;
message CreateReq { string name = 1; string email = 2; }
message CreateResp { string id = 1; }
message GetReq { string id = 1; }
message User { string id = 1; string name = 2; string email = 3; }
message LegReq {}
message LegResp {}
service Users {
  rpc Create(CreateReq) returns (CreateResp);
  rpc Get(GetReq) returns (User);
  rpc Legacy(LegReq) returns (LegResp);
}`
	dir := t.TempDir()
	pp := filepath.Join(dir, "provider.proto")
	cp := filepath.Join(dir, "consumer.proto")
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
	if len(c.Matched) != 1 || c.Matched[0].Path != "Users/Create" {
		t.Errorf("Users/Create should match: %+v", c.Matched)
	}
	kinds := map[string]ContractBreak{}
	for _, b := range c.Breaks {
		kinds[b.Kind] = b
	}
	if len(c.Breaks) != 2 {
		t.Fatalf("want 2 breaks, got %d: %+v", len(c.Breaks), c.Breaks)
	}
	if o, ok := kinds["orphan-call"]; !ok || o.Path != "Users/Legacy" {
		t.Errorf("orphan-call wrong: %+v", o)
	}
	if d, ok := kinds["schema-drift"]; !ok || d.Path != "Users/Get" || !strings.Contains(d.Detail, "email") {
		t.Errorf("schema-drift wrong: %+v", d)
	}
}
