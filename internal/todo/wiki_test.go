package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTwoWayDocMapping(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "ee-sched-01", Epic: "Scheduler", Text: "a", Slug: "scheduler-design"})
	_ = st.Put(Task{ID: "ee-sched-02", Epic: "Scheduler", Text: "b"})
	if err := st.PutDoc(Doc{ID: "doc-scheduler-design", Path: "scheduler-design", Title: "Scheduler", Kind: "design", Body: "queues"}); err != nil {
		t.Fatal(err)
	}

	if err := st.Link("ee-sched-02", LinkDoc, "doc-scheduler-design", ""); err != nil {
		t.Fatal(err)
	}
	// task -> docs
	ds, _ := st.DocsOf("ee-sched-02")
	if len(ds) != 1 || ds[0].ID != "doc-scheduler-design" {
		t.Fatalf("task->doc: want the design doc, got %v", ds)
	}
	// doc -> tasks
	ts, _ := st.TasksOf("doc-scheduler-design")
	if len(ts) != 1 || ts[0].ID != "ee-sched-02" {
		t.Fatalf("doc->task: want ee-sched-02, got %v", ids(ts))
	}
}

// TestDocToDocRelations pins the doc-sourced edge: a chapter links to its README, both ends see the
// relation through RelatedDocs, the doc-sourced edge never leaks into TasksOf, and an id that is
// neither a task nor a doc is refused.
func TestDocToDocRelations(t *testing.T) {
	st := openTemp(t)
	for _, d := range []Doc{
		{ID: "doc-readme", Path: "readme", Title: "Threat models", Kind: "threat-model", Body: "index"},
		{ID: "doc-01-boundaries", Path: "01-boundaries", Title: "The map", Kind: "threat-model", Body: "x"},
		{ID: "doc-02-node", Path: "02-node", Title: "The node", Kind: "threat-model", Body: "y"},
	} {
		if err := st.PutDoc(d); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.Link("doc-01-boundaries", LinkDoc, "doc-readme", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Link("doc-02-node", LinkDoc, "doc-readme", ""); err != nil {
		t.Fatal(err)
	}

	// The chapter sees its README; the README sees every chapter — same edges, both ends.
	rel, _ := st.RelatedDocs("doc-01-boundaries")
	if len(rel) != 1 || rel[0].ID != "doc-readme" {
		t.Fatalf("chapter's related docs: want the readme, got %v", rel)
	}
	rel, _ = st.RelatedDocs("doc-readme")
	if len(rel) != 2 {
		t.Fatalf("readme's related docs: want both chapters, got %v", rel)
	}

	// A doc-sourced edge is not a task and must not surface as one.
	if ts, _ := st.TasksOf("doc-readme"); len(ts) != 0 {
		t.Fatalf("a doc-sourced edge leaked into TasksOf: %v", ids(ts))
	}

	if err := st.Link("neither-task-nor-doc", LinkDoc, "doc-readme", ""); err == nil {
		t.Fatal("a source that exists nowhere must be refused")
	}
}

// TestSections pins the one-level hierarchy: a section is a path prefix, SectionDocs returns its
// pages with the README first (a plain path sort would file it after the numbered chapters), a page
// outside the section stays outside, and SplitSection reads both shapes of path.
func TestSections(t *testing.T) {
	st := openTemp(t)
	for _, d := range []Doc{
		{ID: "doc-tm-readme", Path: "threat-model/README", Title: "Threat models", Kind: "threat-model", Body: "i"},
		{ID: "doc-tm-02", Path: "threat-model/02-node", Title: "The node", Kind: "threat-model", Body: "n"},
		{ID: "doc-tm-01", Path: "threat-model/01-map", Title: "The map", Kind: "threat-model", Body: "m"},
		{ID: "doc-loose", Path: "loose", Title: "Loose", Kind: "note", Body: "l"},
	} {
		if err := st.PutDoc(d); err != nil {
			t.Fatal(err)
		}
	}

	ds, err := st.SectionDocs("threat-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 || ds[0].Path != "threat-model/README" || ds[1].Path != "threat-model/01-map" {
		t.Fatalf("want README first then the chapters, got %v", ds)
	}

	if sec, page := SplitSection("threat-model/02-node"); sec != "threat-model" || page != "02-node" {
		t.Fatalf("SplitSection sectioned path: got %q %q", sec, page)
	}
	if sec, page := SplitSection("loose"); len(sec) != 0 || page != "loose" {
		t.Fatalf("SplitSection bare path: got %q %q", sec, page)
	}

	// A slug keeps finding its page after the page moves into a section: the prefix groups, it does
	// not rename. The bare-path doc is still found exactly.
	if d, ok, _ := st.DocBySlug("02-node"); !ok || d.ID != "doc-tm-02" {
		t.Fatalf("a sectioned page must resolve by its page name, got %v %v", d, ok)
	}
	if d, ok, _ := st.DocBySlug("loose"); !ok || d.ID != "doc-loose" {
		t.Fatalf("a bare path must resolve exactly, got %v %v", d, ok)
	}
	// The same page name in a second section makes the slug ambiguous — no match, no guessing.
	if err := st.PutDoc(Doc{ID: "doc-other-02", Path: "other/02-node", Title: "x", Kind: "note", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.DocBySlug("02-node"); ok {
		t.Fatal("an ambiguous slug must match nothing")
	}
}

func TestLinkDocsBySlugBridgesTheBacklog(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "ee-sched-01", Epic: "Scheduler", Text: "a", Slug: "scheduler-design"})
	_ = st.Put(Task{ID: "ee-sched-02", Epic: "Scheduler", Text: "b", Slug: "scheduler-design, tenancy"})
	_ = st.PutDoc(Doc{ID: "doc-scheduler-design", Path: "scheduler-design", Title: "S"})
	_ = st.PutDoc(Doc{ID: "doc-tenancy", Path: "tenancy", Title: "T"})

	n, err := st.LinkDocsBySlug()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 { // sched-01→design, sched-02→design, sched-02→tenancy
		t.Fatalf("want 3 slug edges, got %d", n)
	}
	if ts, _ := st.TasksOf("doc-tenancy"); len(ts) != 1 {
		t.Errorf("the tenancy doc should map to one task, got %d", len(ts))
	}
}

func TestSoftDeleteHidesDocsFromMapping(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "ee-x-01", Epic: "X", Text: "a"})
	_ = st.PutDoc(Doc{ID: "doc-x", Path: "x", Title: "X"})
	_ = st.Link("ee-x-01", LinkDoc, "doc-x", "")

	if _, err := st.DeleteDoc("doc-x", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if ds, _ := st.DocsOf("ee-x-01"); len(ds) != 0 {
		t.Errorf("a soft-deleted doc must drop out of the mapping, got %d", len(ds))
	}
	if _, ok, _ := st.GetDoc("doc-x"); !ok {
		t.Error("but it must still be gettable — nothing is lost")
	}
}

// TestScanCommitsFindsTaskIDs builds a tiny real git repo whose commit messages name tasks, and
// checks the scan pairs each commit with the ids it mentions — subject and body and trailer alike.
func TestScanCommitsFindsTaskIDs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write := func(name, msg string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", msg)
	}
	write("a", "feat: do the thing for ee-scheduler-07")
	write("b", "unrelated commit with no id")
	write("c", "fix (x)\n\nTask: ce-storage-ha-03 and also ee-scheduler-07")

	got, err := ScanCommits(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// Expect: ee-scheduler-07 (commit a), and ce-storage-ha-03 + ee-scheduler-07 (commit c).
	byTask := map[string]int{}
	for _, cl := range got {
		byTask[cl.TaskID]++
	}
	if byTask["ee-scheduler-07"] != 2 {
		t.Errorf("ee-scheduler-07 should appear in 2 commits, got %d", byTask["ee-scheduler-07"])
	}
	if byTask["ce-storage-ha-03"] != 1 {
		t.Errorf("ce-storage-ha-03 should appear in 1 commit, got %d", byTask["ce-storage-ha-03"])
	}
	if len(byTask) != 2 {
		t.Errorf("no other ids should be found, got %v", byTask)
	}
}
