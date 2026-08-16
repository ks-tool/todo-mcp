package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCommitCount covers todomcp-22: the count install uses to decide whether to suggest a reindex —
// zero outside a git repo, the real number inside one.
func TestCommitCount(t *testing.T) {
	if n := CommitCount(t.TempDir()); n != 0 {
		t.Errorf("a non-git dir must count 0, got %d", n)
	}
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
	for _, f := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-m", f)
	}
	if n := CommitCount(dir); n != 2 {
		t.Errorf("want 2 commits, got %d", n)
	}
}
