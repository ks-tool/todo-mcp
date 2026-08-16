package todo

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindEndpointsNormalizesNames covers todomcpgraphify-02: an operationId binds a code symbol on a
// normalized name — case and separators dropped — so createUser binds a Python create_user and a Go
// CreateUser alike.
func TestBindEndpointsNormalizesNames(t *testing.T) {
	for _, a := range []struct{ x, y string }{
		{"createUser", "create_user()"}, {"createUser", "CreateUser"}, {"create-user", "createUser()"},
	} {
		if normName(a.x) != normName(a.y) {
			t.Errorf("normName(%q)=%q != normName(%q)=%q", a.x, normName(a.x), a.y, normName(a.y))
		}
	}

	st := openTemp(t)
	graph := `{"nodes":[{"id":"c_snake","label":"create_user()","file_type":"code","source_file":"c.py","source_location":"L1"}],"links":[]}`
	p := filepath.Join(t.TempDir(), "g.json")
	if err := os.WriteFile(p, []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IngestGraph("svc", p); err != nil {
		t.Fatal(err)
	}
	rows, err := st.BindEndpoints("svc", []Endpoint{{Method: "POST", Path: "/users", OpID: "createUser"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Symbol != "c_snake" {
		t.Errorf("camelCase operationId must bind the snake_case symbol: %+v", rows)
	}
}
