package todo

import "testing"

// TestPathBFS covers todomcp-05: a shortest path crosses every edge kind — a dependency between two
// tasks, the commit that closed one, and that commit's parent — and the endpoints resolve by id, by
// sha and by full-text phrase.
func TestPathBFS(t *testing.T) {
	st := openTemp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.Put(Task{ID: "a-01", Epic: "a", Text: "the origin task"}))
	must(st.Put(Task{ID: "a-02", Epic: "a", Text: "closed by the first commit"}))
	must(st.AddDep("a-01", "a-02")) // a-01 depends on a-02
	must(st.PutTrailer(Trailer{SHA: "1111111aaa", Repo: "a", Subject: "feat: root commit"}))
	must(st.PutTrailer(Trailer{SHA: "2222222bbb", Repo: "a", Subject: "feat: builds on root", Parents: []string{"1111111aaa"}}))
	must(st.Link("a-02", LinkCommit, "1111111aaa", "feat: root commit"))

	// a-01 —dep— a-02 —commit— 1111111 —parent— 2222222
	p, ok, err := st.Path("a-01", "2222222bbb", PathScope{})
	must(err)
	if !ok {
		t.Fatal("a path must exist across dep, commit and parent")
	}
	if p.Start.Kind != KindTask || p.Start.ID != "a-01" {
		t.Errorf("start node wrong: %+v", p.Start)
	}
	if len(p.Steps) != 3 {
		t.Fatalf("want a 3-step path, got %d: %+v", len(p.Steps), p.Steps)
	}
	wantEdges := []string{edgeDep, edgeCommit, edgeParent}
	for i, e := range wantEdges {
		if p.Steps[i].Edge != e {
			t.Errorf("step %d edge = %q, want %q", i, p.Steps[i].Edge, e)
		}
	}
	if p.Steps[2].Node.Kind != KindTrailer || p.Steps[2].Node.ID != "2222222bbb" {
		t.Errorf("end node wrong: %+v", p.Steps[2].Node)
	}

	// A 7-char sha prefix resolves the same trailer.
	if _, ok, err := st.Path("a-01", "2222222", PathScope{}); err != nil || !ok {
		t.Errorf("a sha prefix must resolve: ok=%v err=%v", ok, err)
	}
	// A full-text phrase resolves to the node that mentions it.
	if p2, ok, err := st.Path("origin", "builds on root", PathScope{}); err != nil || !ok {
		t.Errorf("a text phrase must resolve both ends: ok=%v err=%v", ok, err)
	} else if p2.Start.ID != "a-01" {
		t.Errorf("'origin' should resolve to a-01, got %s", p2.Start.ID)
	}

	// No path: an unrelated task in another epic, unreachable.
	must(st.Put(Task{ID: "b-01", Epic: "b", Text: "an island"}))
	if _, ok, _ := st.Path("b-01", "2222222bbb", PathScope{}); ok {
		t.Error("an unconnected node must have no path")
	}

	// Scope keeps the path inside one epic: restricting to epic b excludes the a-side nodes.
	if _, ok, _ := st.Path("a-01", "2222222bbb", PathScope{Epic: "b"}); ok {
		t.Error("scoping to another epic must drop the path")
	}
}
