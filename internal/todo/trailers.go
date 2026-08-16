package todo

import "encoding/json"

// The derived layer: trailers are git commits cached as graph nodes. This file is their whole
// storage surface — write one, read one, list them, and drop them all. reindex (the rebuild from
// git log) is the only writer; everything here treats the table as a cache that TRUNCATE restores,
// never as authored data, so there is no soft-delete and no trash.

// PutTrailer inserts or replaces one trailer node. reindex calls it per commit; a second call for
// the same sha overwrites, so a rebuild that re-sees a commit is idempotent.
func (s *Store) PutTrailer(t Trailer) error {
	tags, _ := json.Marshal(normTags(t.Tags))
	parents, _ := json.Marshal(t.Parents)
	_, err := s.db.Exec(`
INSERT INTO trailers (sha, repo, subject, body, tags, parents, at) VALUES (?,?,?,?,?,?,?)
ON CONFLICT(sha) DO UPDATE SET repo=excluded.repo, subject=excluded.subject, body=excluded.body,
    tags=excluded.tags, parents=excluded.parents, at=excluded.at`,
		t.SHA, t.Repo, t.Subject, t.Body, string(tags), string(parents), t.At)
	return err
}

// GetTrailer returns one trailer by sha.
func (s *Store) GetTrailer(sha string) (Trailer, bool, error) {
	ts, err := s.scanTrailers(`WHERE sha = ?`, sha)
	if err != nil || len(ts) == 0 {
		return Trailer{}, false, err
	}
	return ts[0], true, nil
}

// Trailers lists the derived nodes, optionally narrowed to one repo ("" for all), newest first.
func (s *Store) Trailers(repo string) ([]Trailer, error) {
	if len(repo) > 0 {
		return s.scanTrailers(`WHERE repo = ? ORDER BY at DESC`, repo)
	}
	return s.scanTrailers(`ORDER BY at DESC`)
}

// TruncateTrailers empties the derived layer — the first half of a reindex, which then rebuilds it
// from git log. It touches nothing authored: the tasks and the trailer→epic bindings are in other
// tables, so a rebuild can be as destructive here as it likes without risking work that git cannot
// restore.
func (s *Store) TruncateTrailers() error {
	_, err := s.db.Exec(`DELETE FROM trailers`)
	return err
}

func (s *Store) scanTrailers(where string, args ...any) ([]Trailer, error) {
	rows, err := s.db.Query(`SELECT sha, repo, subject, body, tags, parents, at FROM trailers `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Trailer
	for rows.Next() {
		var t Trailer
		var tags, parents string
		if err := rows.Scan(&t.SHA, &t.Repo, &t.Subject, &t.Body, &tags, &parents, &t.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &t.Tags)
		_ = json.Unmarshal([]byte(parents), &t.Parents)
		out = append(out, t)
	}
	return out, rows.Err()
}

// shortSHA is the seven-character handle a person reads a commit by; the full sha stays the key.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
