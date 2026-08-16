package todo

import "testing"

// TestCommentThread covers todomcp-14: a task's thread accretes timestamped comments (oldest first),
// they can be edited and soft-deleted by id, adding one never changes the task's status, and a
// comment on a missing task is refused.
func TestCommentThread(t *testing.T) {
	st := openTemp(t)
	if err := st.Put(Task{ID: "c-01", Epic: "c", Status: StatusDone, Text: "shipped"}); err != nil {
		t.Fatal(err)
	}

	id1, err := st.AddComment("c-01", "first", "2026-08-16T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := st.AddComment("c-01", "second", "2026-08-16T11:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	cs, _ := st.Comments("c-01")
	if len(cs) != 2 || cs[0].Text != "first" || cs[1].Text != "second" {
		t.Fatalf("thread must read oldest first: %+v", cs)
	}
	// Adding a comment must not reopen or change a done task.
	if got, _, _ := st.Get("c-01"); got.Status != StatusDone {
		t.Errorf("commenting must not change status, got %q", got.Status)
	}

	if ok, _ := st.EditComment(id1, "first edited"); !ok {
		t.Error("edit must find the comment")
	}
	if cs, _ := st.Comments("c-01"); cs[0].Text != "first edited" {
		t.Errorf("edit did not take: %q", cs[0].Text)
	}

	if ok, _ := st.DeleteComment(id2, "2026-08-16T12:00:00Z"); !ok {
		t.Error("delete must find the comment")
	}
	if cs, _ := st.Comments("c-01"); len(cs) != 1 {
		t.Errorf("a soft-deleted comment must drop from the thread, got %d", len(cs))
	}

	if _, err := st.AddComment("nope", "x", "2026-08-16T10:00:00Z"); err == nil {
		t.Error("a comment on a missing task must be refused")
	}
}
