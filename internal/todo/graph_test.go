package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

// TestPathReachesFiles covers todomcp-06: reindex records the files a commit changed, and a path
// can cross from a commit into a file and on to a task that lists the same file as a touchpoint.
func TestPathReachesFiles(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "app.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "feat: add the app")

	st := openTemp(t)
	if _, err := st.Reindex(dir, "app", "main"); err != nil {
		t.Fatal(err)
	}
	ts, _ := st.Trailers("app")
	if len(ts) != 1 {
		t.Fatalf("want 1 trailer, got %d", len(ts))
	}
	sha := ts[0].SHA

	// A commit reaches the file it changed.
	if _, ok, err := st.Path(sha, "cmd/app.go", PathScope{}); err != nil || !ok {
		t.Fatalf("a commit must reach the file it changed: ok=%v err=%v", ok, err)
	}
	// And a task touching the same file joins to the commit through it.
	if err := st.Put(Task{ID: "app-01", Epic: "app", Text: "owns the file", Touch: []string{"cmd/app.go"}}); err != nil {
		t.Fatal(err)
	}
	p, ok, err := st.Path("app-01", sha, PathScope{})
	if err != nil || !ok {
		t.Fatalf("task→file→commit path missing: ok=%v err=%v", ok, err)
	}
	if len(p.Steps) != 2 || p.Steps[0].Node.Kind != KindFile || p.Steps[1].Node.Kind != KindTrailer {
		t.Errorf("want task -file- file -file- trailer, got %+v", p.Steps)
	}
}
