package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample is a few tasks in the real grammar, enough to pin what the importer must pull apart: the
// priority tag and its /EE edition, the touchpoints, the trailing slug, the dep line, a DONE
// annotation on an open box, and the body left clean of all of it.
const sample = `## Scheduler

- [ ] **P2/EE** Add the QueueSort plugin over the shared manager (internal/ee/scheduler, config) — runtime-storage-scheduler · dep: queue.Manager, ad-scheduler dep
- [ ] **P3** A plain task with no metadata at all

## Secrets

- [x] **P1** The finished one (agent/secret) — secrets **DONE abc1234:** landed as described.
`

func writeSample(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "TODO.ee.md")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportDecomposesTheGrammar(t *testing.T) {
	tasks, err := Import(writeSample(t), "CE")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}

	q := tasks[0]
	if q.Priority != "P2" {
		t.Errorf("priority: want P2, got %q", q.Priority)
	}
	if len(q.Tags) != 2 || q.Tags[0] != "ce" || q.Tags[1] != "ee" {
		t.Errorf("tags: the file tag and the /EE suffix both become tags, got %v", q.Tags)
	}
	if q.Epic != "Scheduler" {
		t.Errorf("epic: want Scheduler, got %q", q.Epic)
	}
	if q.Slug != "runtime-storage-scheduler" {
		t.Errorf("slug: want runtime-storage-scheduler, got %q", q.Slug)
	}
	if len(q.Touch) != 2 || q.Touch[0] != "internal/ee/scheduler" {
		t.Errorf("touchpoints: want [internal/ee/scheduler config], got %v", q.Touch)
	}
	if q.DepText != "queue.Manager, ad-scheduler dep" {
		t.Errorf("dep: want the raw prose, got %q", q.DepText)
	}
	// The body must be clean of every piece now in a field.
	if want := "Add the QueueSort plugin over the shared manager"; q.Text != want {
		t.Errorf("body still carries metadata:\n got %q\nwant %q", q.Text, want)
	}

	done := tasks[2]
	if done.Status != StatusDone {
		t.Errorf("a DONE annotation must mark the task done, got %q", done.Status)
	}
	if done.DoneSHA != "abc1234" {
		t.Errorf("done sha: want abc1234, got %q", done.DoneSHA)
	}
}

// TestRoundTripIsStable is the promise that makes the database a safe source of truth: import then
// render then import again must land on the same tasks, so a render can always be re-read.
func TestRoundTripIsStable(t *testing.T) {
	first, err := Import(writeSample(t), "CE")
	if err != nil {
		t.Fatal(err)
	}
	md := Render(first)
	p := filepath.Join(t.TempDir(), "again.md")
	if err := os.WriteFile(p, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Import(p, "CE")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("round-trip changed the task count: %d -> %d\n%s", len(first), len(second), md)
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.Priority != b.Priority || strings.Join(a.Tags, ",") != strings.Join(b.Tags, ",") || a.Slug != b.Slug || a.Text != b.Text {
			t.Errorf("task %d changed across round-trip:\n %+v\n %+v", i, a, b)
		}
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSoftDeleteHidesButKeeps(t *testing.T) {
	st := openTemp(t)
	if err := st.Put(Task{ID: "ce-x-01", Epic: "X", Status: StatusOpen, Text: "one"}); err != nil {
		t.Fatal(err)
	}
	if ls, _ := st.List(Filter{}); len(ls) != 1 {
		t.Fatalf("want 1 live task, got %d", len(ls))
	}
	if ok, err := st.Delete("ce-x-01", "2026-01-01T00:00:00Z"); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if ls, _ := st.List(Filter{}); len(ls) != 0 {
		t.Errorf("a deleted task must not appear in the live list, got %d", len(ls))
	}
	if tr, _ := st.Trash(); len(tr) != 1 {
		t.Errorf("a deleted task must appear in the trash, got %d", len(tr))
	}
	if _, ok, _ := st.Get("ce-x-01"); !ok {
		t.Error("a deleted task must still be gettable — nothing is lost")
	}
	if ok, _ := st.Restore("ce-x-01"); !ok {
		t.Error("restore must find it")
	}
	if ls, _ := st.List(Filter{}); len(ls) != 1 {
		t.Errorf("a restored task must be live again, got %d", len(ls))
	}
}

func TestReadyRespectsOpenDependencies(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "ce-x-01", Epic: "X", Status: StatusOpen, Text: "blocker"})
	_ = st.Put(Task{ID: "ce-x-02", Epic: "X", Status: StatusOpen, Text: "blocked", DependsOn: []string{"ce-x-01"}})

	ready, _ := st.Ready()
	if len(ready) != 1 || ready[0].ID != "ce-x-01" {
		t.Fatalf("only the blocker is ready, got %v", ids(ready))
	}
	// Close the blocker; now the blocked one is ready too.
	if _, err := st.SetStatus("ce-x-01", StatusDone); err != nil {
		t.Fatal(err)
	}
	ready, _ = st.Ready()
	if len(ready) != 1 || ready[0].ID != "ce-x-02" {
		t.Fatalf("with the blocker done, the blocked task is ready, got %v", ids(ready))
	}
}

func TestNextIDContinuesTheSequence(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "secrets-03", Epic: "Secrets", Text: "x"})
	id, err := st.NextID("Secrets")
	if err != nil {
		t.Fatal(err)
	}
	if id != "secrets-04" {
		t.Errorf("NextID must continue past the highest, want secrets-04, got %q", id)
	}
}

func ids(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// TestBackupIsConsistentAndRefusesOverwrite pins what backup promises: the copy opens, holds the
// same rows, and an existing destination is refused rather than clobbered.
func TestBackupIsConsistentAndRefusesOverwrite(t *testing.T) {
	st := openTemp(t)
	_ = st.Put(Task{ID: "x-01", Epic: "X", Status: StatusOpen, Text: "a"})
	_ = st.PutDoc(Doc{ID: "doc-y", Path: "y", Title: "Y", Body: "b"})

	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := st.BackupTo(dest); err != nil {
		t.Fatal(err)
	}
	copyStore, err := Open(dest)
	if err != nil {
		t.Fatalf("the snapshot does not open: %v", err)
	}
	defer func() { _ = copyStore.Close() }()
	tasks, docs, err := copyStore.Counts()
	if err != nil || tasks != 1 || docs != 1 {
		t.Fatalf("the snapshot disagrees: tasks=%d docs=%d err=%v", tasks, docs, err)
	}
	if err := st.BackupTo(dest); err == nil {
		t.Fatal("an existing destination must be refused, not overwritten")
	}
}

// TestSetNoteOnDoneTaskKeepsStatus covers todomcp-07: a comment can be set or cleared on a task
// after it is done, without reopening it.
func TestSetNoteOnDoneTaskKeepsStatus(t *testing.T) {
	st := openTemp(t)
	if err := st.Put(Task{ID: "n-01", Epic: "n", Status: StatusDone, Text: "shipped"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.SetNote("n-01", "resolved by bumping the dep"); err != nil || !ok {
		t.Fatalf("set note: ok=%v err=%v", ok, err)
	}
	got, _, _ := st.Get("n-01")
	if got.Status != StatusDone {
		t.Errorf("setting a note must not change status, got %q", got.Status)
	}
	if got.DoneNote != "resolved by bumping the dep" {
		t.Errorf("note not stored: %q", got.DoneNote)
	}
	if ok, _ := st.SetNote("n-01", ""); !ok {
		t.Error("clearing a note must succeed")
	}
	got, _, _ = st.Get("n-01")
	if len(got.DoneNote) != 0 || got.Status != StatusDone {
		t.Errorf("clear left note=%q status=%q", got.DoneNote, got.Status)
	}
	if ok, _ := st.SetNote("missing", "x"); ok {
		t.Error("a note on a missing task must report not-found")
	}
}
