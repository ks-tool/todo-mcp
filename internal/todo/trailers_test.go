package todo

import (
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
