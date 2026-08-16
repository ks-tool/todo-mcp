package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PutDoc inserts or replaces a wiki page.
func (s *Store) PutDoc(d Doc) error {
	if len(d.Kind) == 0 {
		d.Kind = "note"
	}
	_, err := s.db.Exec(`
INSERT INTO docs (id, path, title, kind, body, deleted_at) VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET path=excluded.path, title=excluded.title, kind=excluded.kind,
    body=excluded.body, deleted_at=excluded.deleted_at`,
		d.ID, d.Path, d.Title, d.Kind, d.Body, d.DeletedAt)
	return err
}

// GetDoc returns one doc by id, including a soft-deleted one.
func (s *Store) GetDoc(id string) (Doc, bool, error) {
	ds, err := s.scanDocs(`WHERE id = ?`, id)
	if err != nil || len(ds) == 0 {
		return Doc{}, false, err
	}
	return ds[0], true, nil
}

// DocByPath finds a live doc by its path — the slug a task's Slug field carries, which is what lets
// the two be mapped without a human retyping the link.
func (s *Store) DocByPath(path string) (Doc, bool, error) {
	ds, err := s.scanDocs(`WHERE deleted_at = '' AND path = ?`, path)
	if err != nil || len(ds) == 0 {
		return Doc{}, false, err
	}
	return ds[0], true, nil
}

// DocBySlug resolves the doc a task's slug names: the exact path, or — because moving a page into
// a section prefixes its path without renaming the page — the single page called <slug> inside any
// section. Two sections holding the same page name make the slug ambiguous, and an ambiguous slug
// matches nothing rather than guessing.
func (s *Store) DocBySlug(slug string) (Doc, bool, error) {
	if d, ok, err := s.DocByPath(slug); ok || err != nil {
		return d, ok, err
	}
	ds, err := s.scanDocs(`WHERE deleted_at = '' AND path LIKE '%/' || ?`, slug)
	if err != nil || len(ds) != 1 {
		return Doc{}, false, err
	}
	return ds[0], true, nil
}

// ListDocs returns live docs, optionally narrowed by a full-text search over title and body.
func (s *Store) ListDocs(search string) ([]Doc, error) {
	if len(search) > 0 {
		return s.scanDocs(`WHERE deleted_at = '' AND rowid IN (SELECT rowid FROM docs_fts WHERE docs_fts MATCH ?) ORDER BY path`, search)
	}
	return s.scanDocs(`WHERE deleted_at = '' ORDER BY path`)
}

// DeleteDoc and RestoreDoc are soft, like tasks: a page is stamped, not dropped.
func (s *Store) DeleteDoc(id, now string) (bool, error) {
	res, err := s.db.Exec(`UPDATE docs SET deleted_at = ? WHERE id = ? AND deleted_at = ''`, now, id)
	return affected(res, err)
}
func (s *Store) RestoreDoc(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE docs SET deleted_at = '' WHERE id = ?`, id)
	return affected(res, err)
}

// Link adds an edge from a task — or from a doc — to a doc, commit or url. A doc source is what
// relates pages to each other: a chapter to its README, a design to the threat model it answers.
// An optional at time-stamps the edge — a commit's date, in ISO 8601 — for the kinds that have a
// when. Idempotent: the same edge twice is one; re-recording it with a date sets the date, and
// re-recording without one keeps the date already there rather than clearing it.
func (s *Store) Link(fromID, kind, ref, note string, at ...string) error {
	if _, ok, err := s.Get(fromID); err != nil || !ok {
		if _, ok, err := s.GetDoc(fromID); err != nil || !ok {
			return fmt.Errorf("no such task or doc: %s", fromID)
		}
	}
	ts := ""
	if len(at) > 0 {
		ts = at[0]
	}
	_, err := s.db.Exec(`INSERT INTO links (task_id, kind, ref, note, at) VALUES (?,?,?,?,?)
ON CONFLICT(task_id, kind, ref) DO UPDATE SET note=excluded.note,
    at = CASE WHEN excluded.at <> '' THEN excluded.at ELSE links.at END`, fromID, kind, ref, note, ts)
	return err
}

// Unlink removes one edge.
func (s *Store) Unlink(taskID, kind, ref string) error {
	_, err := s.db.Exec(`DELETE FROM links WHERE task_id = ? AND kind = ? AND ref = ?`, taskID, kind, ref)
	return err
}

// LinksOf returns a task's edges, optionally of one kind ("" for all) — its docs, its commits, its
// urls, in one place.
func (s *Store) LinksOf(taskID, kind string) ([]Link, error) {
	where := `WHERE task_id = ?`
	args := []any{taskID}
	if len(kind) > 0 {
		where += ` AND kind = ?`
		args = append(args, kind)
	}
	// Commits sort newest first (their at is an ISO date that sorts lexically); other kinds have no
	// date and fall back to ref order.
	rows, err := s.db.Query(`SELECT task_id, kind, ref, note, at FROM links `+where+` ORDER BY kind, at DESC, ref`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.TaskID, &l.Kind, &l.Ref, &l.Note, &l.At); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DocsOf is one direction of the two-way map: the live docs a task is linked to.
func (s *Store) DocsOf(taskID string) ([]Doc, error) {
	return s.scanDocs(`WHERE deleted_at = '' AND id IN (SELECT ref FROM links WHERE task_id = ? AND kind = 'doc') ORDER BY path`, taskID)
}

// TasksOf is the other direction: the live tasks linked to a doc. Same edges, read from the doc
// end; a doc-sourced edge simply never matches a task, so the two kinds of source need no flag.
func (s *Store) TasksOf(docID string) ([]Task, error) {
	return s.query(`t.id IN (SELECT task_id FROM links WHERE kind = 'doc' AND ref = ?)`, docID)
}

// SplitSection reads the one-level hierarchy out of a path: "threat-model/02-node" is page
// "02-node" in section "threat-model"; a bare path is a page in no section.
func SplitSection(path string) (section, page string) {
	if before, after, ok := strings.Cut(path, "/"); ok {
		return before, after
	}
	return "", path
}

// SectionDocs is one section's pages, the README first — the index reads before its chapters, even
// though a plain path sort would file it after the numbered ones.
func (s *Store) SectionDocs(section string) ([]Doc, error) {
	return s.scanDocs(`WHERE deleted_at = '' AND path LIKE ? || '/%'
	    ORDER BY (path = ? || '/README') DESC, path`, section, section)
}

// RelatedDocs is a doc's neighbourhood among docs: the pages it links to and the pages linking to
// it, one list — a reader following a relation does not care which end holds the edge. Section
// membership is NOT an edge: it lives in the path, and SectionDocs answers it.
func (s *Store) RelatedDocs(id string) ([]Doc, error) {
	return s.scanDocs(`WHERE deleted_at = '' AND id != ? AND (
	       id IN (SELECT ref FROM links WHERE task_id = ? AND kind = 'doc')
	    OR id IN (SELECT task_id FROM links WHERE kind = 'doc' AND ref = ?)) ORDER BY path`, id, id, id)
}

// CommitsOf is a task's commit edges, ready for a "history" view. Ref is the sha, Note the subject.
func (s *Store) CommitsOf(taskID string) ([]Link, error) { return s.LinksOf(taskID, LinkCommit) }

// LinkDocsBySlug bridges the backlog that exists to the wiki: for every live task whose Slug names a
// doc path, it creates the doc edge. It is how the slug field already in the markdown becomes a real
// two-way mapping without a human re-linking hundreds of tasks. Returns how many edges it made.
func (s *Store) LinkDocsBySlug() (int, error) {
	tasks, err := s.List(Filter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		for _, slug := range splitList(t.Slug) {
			d, ok, err := s.DocBySlug(slug)
			if err != nil {
				return n, err
			}
			if !ok {
				continue
			}
			if err := s.Link(t.ID, LinkDoc, d.ID, ""); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// ImportDoc reads a markdown file into a wiki page: the path is the file's base name without its
// extension (so a task's slug matching that name maps onto it), the title is the file's first `# `
// heading if it has one, and the body is the file verbatim. It is how the docs a project already
// keeps as files become the wiki without being retyped.
func ImportDoc(path, kind string) (Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	base := filepath.Base(path)
	slug := strings.TrimSuffix(base, filepath.Ext(base))
	body := string(b)
	return Doc{
		ID:    DocID(slug),
		Path:  slug,
		Title: firstHeading(body, slug),
		Kind:  orDefault(kind, "reference"),
		Body:  body,
	}, nil
}

func firstHeading(body, fallback string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return fallback
}

func orDefault(v, def string) string {
	if len(v) > 0 {
		return v
	}
	return def
}

func (s *Store) scanDocs(where string, args ...any) ([]Doc, error) {
	rows, err := s.db.Query(`SELECT id, path, title, kind, body, deleted_at FROM docs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.Path, &d.Title, &d.Kind, &d.Body, &d.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DocID mints a readable, stable id from a doc's path: the whole path kebab-cased, so a doc keyed on
// the same slug a task carries lines up with it. Exported so the CLI mints the same id the store
// would for a given path.
func DocID(path string) string {
	var b strings.Builder
	prevDash := true // avoid a leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(path)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	id := strings.TrimRight(b.String(), "-")
	if len(id) == 0 {
		return "doc-untitled"
	}
	return "doc-" + id
}

func affected(res interface{ RowsAffected() (int64, error) }, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
