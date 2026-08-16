package todo

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossServicePath covers todomcpgraphify-01: with two services' symbols ingested and their API
// endpoints bound by operationId, a path runs from a function in one service, through the call it
// makes, across the network boundary (a contract match), to the handler in the other.
func TestCrossServicePath(t *testing.T) {
	st := openTemp(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// svc-a: Caller() calls the generated client createUser().
	graphA := `{"nodes":[
	  {"id":"a_caller","label":"Caller()","file_type":"code","source_file":"a/main.go","source_location":"L1"},
	  {"id":"a_create","label":"createUser()","file_type":"code","source_file":"a/client.go","source_location":"L5"}
	],"links":[{"source":"a_caller","target":"a_create","relation":"calls","confidence":"EXTRACTED"}]}`
	// svc-b: the handler.
	graphB := `{"nodes":[{"id":"b_handle","label":"handleCreate()","file_type":"code","source_file":"b/server.go","source_location":"L9"}],"links":[]}`
	if _, err := st.IngestGraph("svc-a", write("a.json", graphA)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IngestGraph("svc-b", write("b.json", graphB)); err != nil {
		t.Fatal(err)
	}
	// The same endpoint on both sides, bound to each side's symbol.
	if err := st.SetEndpoints("svc-a", []StoredEndpoint{{Repo: "svc-a", Method: "POST", Path: "/users", Symbol: "a_create"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEndpoints("svc-b", []StoredEndpoint{{Repo: "svc-b", Method: "POST", Path: "/users", Symbol: "b_handle"}}); err != nil {
		t.Fatal(err)
	}

	p, ok, err := st.Path("svc-a:Caller", "svc-b:handleCreate", PathScope{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a cross-service path must exist from Caller to handleCreate")
	}
	if p.Start.ID != "a_caller" {
		t.Errorf("start resolved wrong: %+v", p.Start)
	}
	crossed := false
	for _, s := range p.Steps {
		if s.Edge == edgeBoundary {
			crossed = true
		}
	}
	if !crossed {
		t.Errorf("the path must cross a boundary edge: %+v", p.Steps)
	}
	if p.Steps[len(p.Steps)-1].Node.ID != "b_handle" {
		t.Errorf("end resolved wrong: %+v", p.Steps[len(p.Steps)-1].Node)
	}
}
