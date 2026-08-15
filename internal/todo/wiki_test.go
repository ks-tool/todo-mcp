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
