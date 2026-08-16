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
