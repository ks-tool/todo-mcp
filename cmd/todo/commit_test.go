package main

import (
	"path/filepath"
	"testing"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// TestCommitKnownViaTrailer covers todomcp-16: a sha is "known" — and so allowed as a commit link —
// when it is a trailer in the cache, even if the given directory is not the repo that holds it. That
// is what stops the guard from refusing a legitimate commit in a multi-project backlog; a sha in no
// tracked history stays unknown.
func TestCommitKnownViaTrailer(t *testing.T) {
	st, err := todo.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	notARepo := t.TempDir()
	if commitKnown(st, notARepo, "0123456789abcdef0123456789abcdef01234567") {
		t.Error("a sha in no repo and no trailer must be unknown")
	}

	if err := st.PutTrailer(todo.Trailer{SHA: "0123456789abcdef0123456789abcdef01234567", Subject: "x"}); err != nil {
		t.Fatal(err)
	}
	if !commitKnown(st, notARepo, "0123456789abcdef0123456789abcdef01234567") {
		t.Error("a sha that is a trailer must be known even outside its repo")
	}
	if !commitKnown(st, notARepo, "0123456") {
		t.Error("a prefix of a trailer sha must be known")
	}
}
