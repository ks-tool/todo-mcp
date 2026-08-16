package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestTrailersDerivedLayer covers todomcp-02: the derived layer stores trailer nodes apart from
// tasks, round-trips their fields, and TRUNCATE clears it without touching the authored tasks —
// which is what lets a reindex rebuild the cache without risking work git cannot restore.
func TestTrailersDerivedLayer(t *testing.T) {
	st := openTemp(t)

	if err := st.Put(Task{ID: "p-01", Epic: "proj", Text: "authored, must survive a truncate"}); err != nil {
		t.Fatal(err)
	}
	tr := Trailer{SHA: "abc123", Repo: "proj", Subject: "feat: a thing",
		Body: "feat: a thing\n\nbody", Tags: []string{"Auth", "auth", "api"},
		Parents: []string{"def456"}, At: "2026-08-16T10:00:00Z"}
	if err := st.PutTrailer(tr); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.GetTrailer("abc123")
	if err != nil || !ok {
		t.Fatalf("get trailer: ok=%v err=%v", ok, err)
	}
	if got.Subject != "feat: a thing" || got.Repo != "proj" || got.At != "2026-08-16T10:00:00Z" {
		t.Errorf("trailer round-trip lost fields: %+v", got)
	}
	if !slices.Equal(got.Tags, []string{"auth", "api"}) {
		t.Errorf("trailer tags must be lower-cased and de-duplicated: %v", got.Tags)
	}
	if !slices.Equal(got.Parents, []string{"def456"}) {
		t.Errorf("trailer parents lost: %v", got.Parents)
	}

	if ts, _ := st.Trailers("proj"); len(ts) != 1 {
		t.Errorf("Trailers(repo) must find the node, got %d", len(ts))
	}
	if ts, _ := st.Trailers("other"); len(ts) != 0 {
		t.Errorf("a different repo must not match, got %d", len(ts))
	}

	// Truncate is the reindex's first half: the derived layer empties, the authored task stays.
	if err := st.TruncateTrailers(); err != nil {
		t.Fatal(err)
	}
	if ts, _ := st.Trailers(""); len(ts) != 0 {
		t.Errorf("truncate must clear the derived layer, got %d", len(ts))
	}
	if ls, _ := st.List(Filter{}); len(ls) != 1 {
		t.Errorf("truncating trailers must not touch authored tasks, got %d", len(ls))
	}
}

// TestTrailerEpicResolution covers todomcp-03: a trailer's epic is its explicit local binding
// first, then the epic of the task the commit closed, then the repo it came from — and the binding
// is authored, so it survives a truncate of the derived cache.
func TestTrailerEpicResolution(t *testing.T) {
	st := openTemp(t)
	if err := st.PutTrailer(Trailer{SHA: "sha-1", Repo: "from-repo", Subject: "x"}); err != nil {
		t.Fatal(err)
	}

	// With nothing bound and no closing task, the repo answers.
	if e, _ := st.TrailerEpic("sha-1"); e != "from-repo" {
		t.Errorf("unbound trailer must fall back to its repo, got %q", e)
	}

	// A task that records the commit lends its epic — inherited, not stored.
	if err := st.Put(Task{ID: "svc-01", Epic: "service", Text: "closed by sha-1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Link("svc-01", LinkCommit, "sha-1", "x"); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.TrailerEpic("sha-1"); e != "service" {
		t.Errorf("a closing task must lend its epic, got %q", e)
	}

	// An explicit binding wins over both, and survives a rebuild of the derived layer.
	if err := st.BindTrailerEpic("sha-1", "mine"); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.TrailerEpic("sha-1"); e != "mine" {
		t.Errorf("an explicit binding must win, got %q", e)
	}
	if err := st.TruncateTrailers(); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.TrailerEpic("sha-1"); e != "mine" {
		t.Errorf("the binding is authored and must survive a truncate, got %q", e)
	}

	// Clearing it falls back to the inherited epic (the closing task, still present).
	if err := st.UnbindTrailerEpic("sha-1"); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.TrailerEpic("sha-1"); e != "service" {
		t.Errorf("after unbind the inherited epic answers, got %q", e)
	}
}

// TestTagsFromMessage covers todomcp-12's parser: a Tags: trailer line and loose #hashtags both
// contribute, lower-cased and de-duplicated, and a bare "# heading" (space after #) is not a tag.
func TestTagsFromMessage(t *testing.T) {
	msg := "feat: a thing #Auth\n\nsome body #api and a #api again\n# not-a-tag heading\nTags: contract, Auth, billing"
	got := tagsFromMessage(msg)
	slices.Sort(got)
	want := []string{"api", "auth", "billing", "contract"}
	if !slices.Equal(got, want) {
		t.Errorf("tagsFromMessage = %v, want %v", got, want)
	}
	if len(tagsFromMessage("no tags here")) != 0 {
		t.Error("a message with no tags must yield none")
	}
}

// TestTrailerTagFilter covers todomcp-12's filter: reindex loads message tags onto the trailer, and
// TrailersFiltered narrows by them, every tag having to match.
func TestTrailerTagFilter(t *testing.T) {
	st := openTemp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.PutTrailer(Trailer{SHA: "s1", Repo: "r", Subject: "one", Tags: []string{"auth", "api"}}))
	must(st.PutTrailer(Trailer{SHA: "s2", Repo: "r", Subject: "two", Tags: []string{"auth"}}))
	must(st.PutTrailer(Trailer{SHA: "s3", Repo: "r", Subject: "three", Tags: []string{"billing"}}))

	if ts, _ := st.TrailersFiltered("", []string{"auth"}); len(ts) != 2 {
		t.Errorf("one tag must match two, got %d", len(ts))
	}
	if ts, _ := st.TrailersFiltered("", []string{"auth", "api"}); len(ts) != 1 {
		t.Errorf("two tags AND to one, got %d", len(ts))
	}
	if ts, _ := st.TrailersFiltered("", []string{"nope"}); len(ts) != 0 {
		t.Errorf("an unused tag matches none, got %d", len(ts))
	}
}

// TestReindexFromGit covers todomcp-04: reindex reads the log of main and rebuilds the trailer
// cache — one node per commit, parents as edges — leaving the authored tasks alone, and a second
// run is idempotent because it TRUNCATEs first.
func TestReindexFromGit(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "first: the root")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "second: builds on the first")

	st := openTemp(t)
	if err := st.Put(Task{ID: "z-01", Epic: "z", Text: "authored, untouched by reindex"}); err != nil {
		t.Fatal(err)
	}
	n, err := st.Reindex(dir, "myrepo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reindex must project both commits, got %d", n)
	}
	ts, err := st.Trailers("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("want 2 trailers, got %d", len(ts))
	}
	// Newest first: the second commit, and it names the first as a parent — the git edge.
	if ts[0].Subject != "second: builds on the first" {
		t.Errorf("newest-first order broken: %q", ts[0].Subject)
	}
	if len(ts[0].Parents) != 1 || ts[0].Parents[0] != ts[1].SHA {
		t.Errorf("the second commit must edge to the first: parents=%v first=%s", ts[0].Parents, ts[1].SHA)
	}
	if len(ts[1].Parents) != 0 {
		t.Errorf("the root commit has no parent, got %v", ts[1].Parents)
	}

	// Idempotent: a second reindex TRUNCATEs and rebuilds to the same two, and the task still stands.
	if _, err := st.Reindex(dir, "myrepo", "main"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := st.Trailers(""); len(ts) != 2 {
		t.Errorf("a second reindex must not double the cache, got %d", len(ts))
	}
	if ls, _ := st.List(Filter{}); len(ls) != 1 {
		t.Errorf("reindex must leave the authored task alone, got %d", len(ls))
	}
}
