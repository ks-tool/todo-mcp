package todo

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
)

// The derived layer: trailers are git commits cached as graph nodes. This file is their whole
// storage surface — write one, read one, list them, and drop them all. reindex (the rebuild from
// git log) is the only writer; everything here treats the table as a cache that TRUNCATE restores,
// never as authored data, so there is no soft-delete and no trash.

// execer is *sql.DB or *sql.Tx — so one insert body serves both a single PutTrailer and the batched
// rebuild inside a Reindex transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// PutTrailer inserts or replaces one trailer node. reindex calls it per commit; a second call for
// the same sha overwrites, so a rebuild that re-sees a commit is idempotent.
func (s *Store) PutTrailer(t Trailer) error { return putTrailer(s.db, t) }

func putTrailer(x execer, t Trailer) error {
	tags, _ := json.Marshal(normTags(t.Tags))
	parents, _ := json.Marshal(t.Parents)
	_, err := x.Exec(`
INSERT INTO trailers (sha, repo, subject, body, tags, parents, at) VALUES (?,?,?,?,?,?,?)
ON CONFLICT(sha) DO UPDATE SET repo=excluded.repo, subject=excluded.subject, body=excluded.body,
    tags=excluded.tags, parents=excluded.parents, at=excluded.at`,
		t.SHA, t.Repo, t.Subject, t.Body, string(tags), string(parents), t.At)
	return err
}

// Reindex rebuilds the derived layer from git: it reads the whole log of rev in dir (main, by
// convention — the integrated history whose conflicts are already resolved), then in ONE
// transaction empties the trailer cache and re-fills it, one node per commit, its parents the git
// edges. The authored side — tasks and the trailer→epic bindings — is in other tables and is never
// touched, so this destructive rebuild can never lose work git cannot restore. repo is the source
// label a trailer resolves its epic through when nothing local overrides it. Returns the node count.
//
// It is whole-history each time, not incremental: correct is cheaper to keep than clever, and a
// rebuild cannot drift the way a patched cache can. The rebuild is scoped to THIS repo — only its
// trailers and their files are dropped and re-inserted — so one database can hold several projects
// and reindexing one never wipes another's graph.
func (s *Store) Reindex(dir, repo, rev string) (int, error) {
	commits, err := LogCommits(dir, rev)
	if err != nil {
		return 0, err
	}
	files, err := LogFiles(dir, rev)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Files first: they are keyed by sha, so the shas to drop must still be resolvable from the
	// trailers table when this runs.
	if _, err := tx.Exec(`DELETE FROM trailer_files WHERE sha IN (SELECT sha FROM trailers WHERE repo = ?)`, repo); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM trailers WHERE repo = ?`, repo); err != nil {
		return 0, err
	}
	for _, c := range commits {
		t := Trailer{SHA: c.SHA, Repo: repo, Subject: c.Subject, Body: c.Message,
			Tags: tagsFromMessage(c.Message), Parents: c.Parents, At: c.Date}
		if err := putTrailer(tx, t); err != nil {
			return 0, err
		}
		for _, path := range files[c.SHA] {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO trailer_files (sha, path) VALUES (?,?)`, c.SHA, path); err != nil {
				return 0, err
			}
		}
	}
	return len(commits), tx.Commit()
}

// HasTrailer reports whether a sha (full, or a prefix) is a trailer in the cache — a commit reindex
// loaded from SOME repo's history. It is the cross-repo signal that a sha is a known commit even when
// it is not in the current directory's repo, so a commit link is not refused just because the server
// runs in a different project's checkout than the one the commit belongs to.
func (s *Store) HasTrailer(sha string) (bool, error) {
	if len(sha) == 0 {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM trailers WHERE sha = ? OR sha LIKE ? || '%' LIMIT 1`, sha, sha).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
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
func (s *Store) Trailers(repo string) ([]Trailer, error) { return s.TrailersFiltered(repo, nil) }

// TrailersFiltered narrows the derived nodes by repo and by tags — every tag must match, the way a
// task list narrows. Trailer tags are the ones reindex parsed from the commit message, so this is
// how the git-derived, shared slices become a filter.
func (s *Store) TrailersFiltered(repo string, tags []string) ([]Trailer, error) {
	var where []string
	var args []any
	if len(repo) > 0 {
		where = append(where, "repo = ?")
		args = append(args, repo)
	}
	for _, tag := range tags {
		where = append(where, `tags LIKE '%"' || ? || '"%'`)
		args = append(args, strings.ToLower(tag))
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ") + " "
	}
	return s.scanTrailers(cond+"ORDER BY at DESC", args...)
}

// tagRef matches a #tag anywhere a word boundary allows — the loose form a commit author reaches
// for. The dash and underscore let kebab and snake tags through; a leading digit is fine.
var tagRef = regexp.MustCompile(`(?:^|\s)#([A-Za-z0-9][\w-]*)`)

// tagsFromMessage pulls the cross-cutting tags a commit declares: a `Tags:` trailer line
// (comma- or space-separated) and any `#tag` in the message. These are git-derived and shared —
// unlike a task's locally-authored tags — so everyone reindexing the same history filters on the
// same slices. Lower-casing and de-duplication happen on store (normTags).
func tagsFromMessage(msg string) []string {
	var out []string
	for line := range strings.SplitSeq(msg, "\n") {
		rest, ok := cutTagsTrailer(line)
		if !ok {
			continue
		}
		for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			out = append(out, strings.TrimPrefix(f, "#"))
		}
	}
	for _, m := range tagRef.FindAllStringSubmatch(msg, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		return nil
	}
	return normTags(out)
}

// cutTagsTrailer returns the value of a `Tags:` line (case-insensitive key at the line start), and
// whether the line was one.
func cutTagsTrailer(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if i := strings.IndexByte(line, ':'); i >= 0 && strings.EqualFold(line[:i], "Tags") {
		return line[i+1:], true
	}
	return "", false
}

// TruncateTrailers empties the derived layer — the first half of a reindex, which then rebuilds it
// from git log. It touches nothing authored: the tasks and the trailer→epic bindings are in other
// tables, so a rebuild can be as destructive here as it likes without risking work that git cannot
// restore.
func (s *Store) TruncateTrailers() error {
	if _, err := s.db.Exec(`DELETE FROM trailers`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM trailer_files`)
	return err
}

// BindTrailerEpic files a trailer under one of your local epics. The binding is authored and
// survives reindex — it is how a commit from someone else's history (a fork loaded as trailers)
// becomes part of your own project's graph. Idempotent; a second call re-files it. An empty epic
// clears the binding, the same as UnbindTrailerEpic.
func (s *Store) BindTrailerEpic(sha, epic string) error {
	if len(epic) == 0 {
		return s.UnbindTrailerEpic(sha)
	}
	_, err := s.db.Exec(`INSERT INTO trailer_epics (sha, epic) VALUES (?,?)
ON CONFLICT(sha) DO UPDATE SET epic=excluded.epic`, sha, epic)
	return err
}

// UnbindTrailerEpic drops the explicit binding, so the trailer falls back to an inherited or its
// repo's epic.
func (s *Store) UnbindTrailerEpic(sha string) error {
	_, err := s.db.Exec(`DELETE FROM trailer_epics WHERE sha = ?`, sha)
	return err
}

// TrailerEpic resolves which epic a trailer belongs to, in one fixed order: the explicit local
// binding first; then, with none, the epic of the task the commit closed — inherited through the
// commit link a task carries, computed here rather than stored so it tracks the task without a
// second source of truth; and finally the repo the trailer came from. It is a local, mutable view:
// nothing here is written to git or shared.
func (s *Store) TrailerEpic(sha string) (string, error) {
	var epic string
	err := s.db.QueryRow(`SELECT epic FROM trailer_epics WHERE sha = ?`, sha).Scan(&epic)
	if err == nil {
		return epic, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// The task the commit closed — the first task that records this sha as a commit — lends its epic.
	err = s.db.QueryRow(`SELECT t.epic FROM tasks t
JOIN links l ON l.task_id = t.id
WHERE l.kind = ? AND l.ref = ? AND t.deleted_at = '' ORDER BY t.rank, t.ord LIMIT 1`, LinkCommit, sha).Scan(&epic)
	if err == nil {
		return epic, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// Neither bound nor inherited: the repo the reindex filed it under.
	err = s.db.QueryRow(`SELECT repo FROM trailers WHERE sha = ?`, sha).Scan(&epic)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return epic, err
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
