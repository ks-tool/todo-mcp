package todo

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIngestGraph covers graphify-01: a graphify graph.json loads into the symbol layer, node kinds
// are derived (package, file, func, doc), and the ingest is scoped per repo so one project's symbols
// do not wipe another's.
func TestIngestGraph(t *testing.T) {
	st := openTemp(t)
	graph := `{"nodes":[
	  {"id":"pkg_x","label":"example.com/x","file_type":"code","type":"package","source_file":"go.mod","source_location":"L1"},
	  {"id":"a_helpers","label":"helpers.go","file_type":"code","source_file":"a/helpers.go","source_location":"L1"},
	  {"id":"a_helpers_do","label":"Do()","file_type":"code","source_file":"a/helpers.go","source_location":"L11"},
	  {"id":"doc_readme","label":"README.md","file_type":"document","source_file":"README.md","source_location":"L1"}
	],"links":[]}`
	p := filepath.Join(t.TempDir(), "graph.json")
	if err := os.WriteFile(p, []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := st.IngestGraph("mine", p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 nodes ingested, got %d", n)
	}

	ss, _ := st.Symbols("mine")
	kind := map[string]string{}
	for _, s := range ss {
		kind[s.SID] = s.Kind
	}
	for sid, want := range map[string]string{
		"pkg_x": "package", "a_helpers": "file", "a_helpers_do": "func", "doc_readme": "doc",
	} {
		if kind[sid] != want {
			t.Errorf("kind[%s] = %q, want %q", sid, kind[sid], want)
		}
	}
	if sym, ok, _ := st.GetSymbol("mine", "a_helpers_do"); !ok || sym.File != "a/helpers.go" || sym.Line != "L11" {
		t.Errorf("symbol source lost: %+v", sym)
	}

	// A second repo's graph ingests alongside, not over.
	other := `{"nodes":[{"id":"b_main","label":"main.go","file_type":"code","source_file":"b/main.go","source_location":"L1"}],"links":[]}`
	po := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(po, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IngestGraph("other", po); err != nil {
		t.Fatal(err)
	}
	if ss, _ := st.Symbols("mine"); len(ss) != 4 {
		t.Errorf("ingesting 'other' must not touch 'mine', got %d", len(ss))
	}
	if ss, _ := st.Symbols("other"); len(ss) != 1 {
		t.Errorf("'other' repo must have its 1 symbol, got %d", len(ss))
	}

	// Re-ingesting 'mine' with fewer nodes replaces, not accumulates.
	small := `{"nodes":[{"id":"a_helpers","label":"helpers.go","file_type":"code","source_file":"a/helpers.go","source_location":"L1"}],"links":[]}`
	ps := filepath.Join(t.TempDir(), "small.json")
	if err := os.WriteFile(ps, []byte(small), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IngestGraph("mine", ps); err != nil {
		t.Fatal(err)
	}
	if ss, _ := st.Symbols("mine"); len(ss) != 1 {
		t.Errorf("re-ingest must replace, got %d", len(ss))
	}
}
