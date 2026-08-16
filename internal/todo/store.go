package todo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Store is the backlog. One SQLite file, opened for the life of a command.
type Store struct{ db *sql.DB }

// Open opens or creates the database at path and brings its schema up to date.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate is the whole schema, applied every open. It is idempotent — IF NOT EXISTS throughout — so
// there is no version table to keep: the create statements ARE the current shape, and running them
// against a database already in that shape does nothing.
//
// The task text lives in a normal table; a separate FTS5 virtual table mirrors the searchable
// columns and is kept in step by triggers, so a full-text query never scans the prose column by
// hand. depends_on is its own table rather than a JSON blob because "what depends on X" — the
// reverse question — is the one that pays for a backlog, and it is a join, not a scan.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS tasks (
    id        TEXT PRIMARY KEY,
    tags      TEXT NOT NULL DEFAULT '[]',
    epic      TEXT NOT NULL,
    status    TEXT NOT NULL DEFAULT 'open',
    priority  TEXT NOT NULL DEFAULT '',
    rank      INTEGER NOT NULL DEFAULT 99,
    slug      TEXT NOT NULL DEFAULT '',
    touch     TEXT NOT NULL DEFAULT '[]',
    dep_text  TEXT NOT NULL DEFAULT '',
    text      TEXT NOT NULL,
    done_sha  TEXT NOT NULL DEFAULT '',
    done_note TEXT NOT NULL DEFAULT '',
    ord       INTEGER NOT NULL DEFAULT 0,
    deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS tasks_by_status   ON tasks(status);
CREATE INDEX IF NOT EXISTS tasks_by_epic     ON tasks(epic);
CREATE INDEX IF NOT EXISTS tasks_by_rank     ON tasks(rank);

CREATE TABLE IF NOT EXISTS deps (
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on TEXT NOT NULL,
    PRIMARY KEY (task_id, depends_on)
);
CREATE INDEX IF NOT EXISTS deps_by_target ON deps(depends_on);

CREATE TABLE IF NOT EXISTS docs (
    id         TEXT PRIMARY KEY,
    path       TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL DEFAULT 'note',
    body       TEXT NOT NULL DEFAULT '',
    deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS docs_by_path ON docs(path);

CREATE TABLE IF NOT EXISTS links (
    task_id TEXT NOT NULL,
    kind    TEXT NOT NULL,
    ref     TEXT NOT NULL,
    note    TEXT NOT NULL DEFAULT '',
    at      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (task_id, kind, ref)
);
CREATE INDEX IF NOT EXISTS links_by_ref ON links(kind, ref);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
    id UNINDEXED, title, body, content='docs', content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON docs BEGIN
    INSERT INTO docs_fts(rowid, id, title, body) VALUES (new.rowid, new.id, new.title, new.body);
END;
CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, id, title, body) VALUES ('delete', old.rowid, old.id, old.title, old.body);
END;
CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, id, title, body) VALUES ('delete', old.rowid, old.id, old.title, old.body);
    INSERT INTO docs_fts(rowid, id, title, body) VALUES (new.rowid, new.id, new.title, new.body);
END;

-- The derived layer: git commits projected as read-only trailer nodes. This table is a CACHE, not
-- authored data — reindex TRUNCATEs and rebuilds it from git log, so nothing here is soft-deleted
-- and losing it costs only a reindex. It is kept apart from tasks precisely so that rebuild touches
-- only the derived side and never the authored tasks, which are never hard-deleted. A trailer's
-- project is the repo it came from; its cross-cutting tags are parsed from the commit message.
CREATE TABLE IF NOT EXISTS trailers (
    sha     TEXT PRIMARY KEY,
    repo    TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    body    TEXT NOT NULL DEFAULT '',
    tags    TEXT NOT NULL DEFAULT '[]',
    parents TEXT NOT NULL DEFAULT '[]',
    at      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS trailers_by_repo ON trailers(repo);

-- The optional code layer, also derived: which files each commit changed, so a path can reach from
-- a commit into the files it touched. Rebuilt with the trailers on every reindex.
CREATE TABLE IF NOT EXISTS trailer_files (
    sha  TEXT NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (sha, path)
);
CREATE INDEX IF NOT EXISTS trailer_files_by_path ON trailer_files(path);

-- The trailer→epic binding is AUTHORED, not derived: it is the local decision to file a commit from
-- the history under one of your own epics, and it must survive the reindex that rebuilds the trailer
-- cache. So it lives in its own table, keyed by sha, and reindex never touches it. Epics are local —
-- never written to a commit, never synced between databases; this is where that locality is kept.
CREATE TABLE IF NOT EXISTS trailer_epics (
    sha  TEXT PRIMARY KEY,
    epic TEXT NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS trailers_fts USING fts5(
    sha UNINDEXED, subject, body, content='trailers', content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS trailers_ai AFTER INSERT ON trailers BEGIN
    INSERT INTO trailers_fts(rowid, sha, subject, body) VALUES (new.rowid, new.sha, new.subject, new.body);
END;
CREATE TRIGGER IF NOT EXISTS trailers_ad AFTER DELETE ON trailers BEGIN
    INSERT INTO trailers_fts(trailers_fts, rowid, sha, subject, body) VALUES ('delete', old.rowid, old.sha, old.subject, old.body);
END;
CREATE TRIGGER IF NOT EXISTS trailers_au AFTER UPDATE ON trailers BEGIN
    INSERT INTO trailers_fts(trailers_fts, rowid, sha, subject, body) VALUES ('delete', old.rowid, old.sha, old.subject, old.body);
    INSERT INTO trailers_fts(rowid, sha, subject, body) VALUES (new.rowid, new.sha, new.subject, new.body);
END;

CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
    id UNINDEXED, epic, text, done_note, content='tasks', content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS tasks_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, id, epic, text, done_note)
    VALUES (new.rowid, new.id, new.epic, new.text, new.done_note);
END;
CREATE TRIGGER IF NOT EXISTS tasks_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, id, epic, text, done_note)
    VALUES ('delete', old.rowid, old.id, old.epic, old.text, old.done_note);
END;
CREATE TRIGGER IF NOT EXISTS tasks_au AFTER UPDATE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, id, epic, text, done_note)
    VALUES ('delete', old.rowid, old.id, old.epic, old.text, old.done_note);
    INSERT INTO tasks_fts(rowid, id, epic, text, done_note)
    VALUES (new.rowid, new.id, new.epic, new.text, new.done_note);
END;
`
	// No migrations, deliberately: pre-1.0 there is no installed base, so the create statements
	// above ARE the shape and an older database is re-imported rather than converted. A schema
	// change edits them outright instead of accreting ALTERs behind them.
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// The one exception: a column added to a table that already holds data worth keeping — commit
	// links carry real history that a re-import would lose. This is a forward-only add of a column
	// with a default (no row is reshaped), not a back-compat conversion, so it is safe to run every
	// open and does nothing once the column exists.
	return s.ensureColumn("links", "at", "TEXT NOT NULL DEFAULT ''")
}

// ensureColumn adds a column to a table when it is absent, and does nothing when it is present, so
// a database created before the column gains it without losing its rows.
func (s *Store) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}

// Put inserts or replaces a task and rewrites its dependency edges. Used by both the importer and
// the CLI's add, so there is one write path and one place the FTS mirror and the deps table stay in
// agreement with the row.
func (s *Store) Put(t Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	touch, _ := json.Marshal(t.Touch)
	// Tags are stored lower-cased and de-duplicated, so a filter never misses on case and the
	// LIKE the filter uses cannot be fooled by doubles.
	tags, _ := json.Marshal(normTags(t.Tags))
	if t.Rank == 0 {
		t.Rank = rankOf(t.Priority)
	}
	if len(t.Status) == 0 {
		t.Status = StatusOpen
	}
	_, err = tx.Exec(`
INSERT INTO tasks (id, tags, epic, status, priority, rank, slug, touch, dep_text, text, done_sha, done_note, ord, deleted_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    tags=excluded.tags, epic=excluded.epic, status=excluded.status, priority=excluded.priority,
    rank=excluded.rank, slug=excluded.slug, touch=excluded.touch, dep_text=excluded.dep_text,
    text=excluded.text, done_sha=excluded.done_sha, done_note=excluded.done_note, ord=excluded.ord,
    deleted_at=excluded.deleted_at`,
		t.ID, string(tags), t.Epic, string(t.Status), t.Priority, t.Rank, t.Slug, string(touch),
		t.DepText, t.Text, t.DoneSHA, t.DoneNote, t.Order, t.DeletedAt)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM deps WHERE task_id = ?`, t.ID); err != nil {
		return err
	}
	for _, d := range t.DependsOn {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO deps (task_id, depends_on) VALUES (?,?)`, t.ID, d); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Get returns one task by id, including a soft-deleted one — restore and the trash view need to
// reach it, and callers that must not act on a deleted task check DeletedAt.
func (s *Store) Get(id string) (Task, bool, error) {
	ts, err := s.scan(`WHERE t.id = ?`, id)
	if err != nil || len(ts) == 0 {
		return Task{}, false, err
	}
	return ts[0], true, nil
}

// Filter is what list narrows on. A zero value matches everything.
type Filter struct {
	Status Status
	// Tags a task must ALL carry to match — the multi-tag filter narrows, it does not widen.
	Tags     []string
	Epic     string
	Priority string
	Search   string // full-text query over epic/text/done_note
}

// List returns tasks matching the filter, ordered by rank then file position — the order you would
// work them in, and the same order every time so a diff of two runs means something.
func (s *Store) List(f Filter) ([]Task, error) {
	var where []string
	var args []any
	if len(f.Status) > 0 {
		where = append(where, "t.status = ?")
		args = append(args, string(f.Status))
	}
	for _, tag := range f.Tags {
		where = append(where, `t.tags LIKE '%"' || ? || '"%'`)
		args = append(args, strings.ToLower(tag))
	}
	if len(f.Epic) > 0 {
		where = append(where, "t.epic LIKE ?")
		args = append(args, "%"+f.Epic+"%")
	}
	if len(f.Priority) > 0 {
		where = append(where, "t.priority = ?")
		args = append(args, f.Priority)
	}
	if len(f.Search) > 0 {
		where = append(where, "t.rowid IN (SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH ?)")
		args = append(args, f.Search)
	}
	return s.query(strings.Join(where, " AND "), args...)
}

// Ready is the question a backlog exists to answer: which OPEN tasks have every dependency already
// done. A dep that names something not in the store (free text, or a reference never imported) is
// treated as satisfied — it is not a blocker this database can act on, and pretending otherwise
// would hide every task behind a dependency nobody tracks here.
func (s *Store) Ready() ([]Task, error) {
	return s.query(`
t.status = 'open'
  AND NOT EXISTS (
        SELECT 1 FROM deps d
        JOIN tasks bt ON bt.id = d.depends_on
        WHERE d.task_id = t.id AND bt.status = 'open' AND bt.deleted_at = ''
  )`)
}

// Impact is the reverse traversal: the OPEN tasks that name id among their dependencies, directly.
// One level, because "what unblocks if I do this" is a decision the reader makes a step at a time,
// and a transitive closure buries the immediate answer under everything downstream of it.
func (s *Store) Impact(id string) ([]Task, error) {
	return s.query(`t.status = 'open' AND t.id IN (SELECT task_id FROM deps WHERE depends_on = ?)`, id)
}

// Delete soft-deletes a task: it stamps deleted_at rather than removing the row, so nothing is ever
// lost to a wrong keystroke and the trash can be reviewed and restored. now is passed in (the CLI
// supplies time.Now) so the store stays clock-free and testable.
func (s *Store) Delete(id, now string) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET deleted_at = ? WHERE id = ? AND deleted_at = ''`, now, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Restore clears the deletion stamp, bringing a task back.
func (s *Store) Restore(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET deleted_at = '' WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Trash lists the soft-deleted tasks — the review before anything is discarded for good.
func (s *Store) Trash() ([]Task, error) {
	return s.scan(`WHERE t.deleted_at != ''`)
}

// NextID mints the next unused id for an epic: <slug>-<NN>, continuing the
// sequence the import left off at rather than restarting it, so an id is never reused. It reads the
// current maximum for that prefix and adds one — a gap left by a deleted task is not refilled, which
// keeps an id a permanent handle to a task rather than a slot that changes meaning.
func (s *Store) NextID(epic string) (string, error) {
	prefix := slugFor(epic) + "-"
	var max int
	rows, err := s.db.Query(`SELECT id FROM tasks WHERE id LIKE ?`, prefix+"%")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if n := trailingNum(id); n > max {
			max = n
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%02d", prefix, max+1), nil
}

// Update applies a set of field changes to an existing task and returns whether it existed. Only the
// fields named in the map are touched; the rest of the row is left as it was. Status and rank are
// derived here when priority moves, so a caller never has to keep the two in step.
func (s *Store) Update(id string, fields map[string]string) (bool, error) {
	t, ok, err := s.Get(id)
	if err != nil || !ok {
		return false, err
	}
	for k, v := range fields {
		switch k {
		case "epic":
			t.Epic = v
		case "priority":
			t.Priority = v
			t.Rank = rankOf(v)
		case "tags":
			t.Tags = splitList(v)
		case "slug":
			t.Slug = v
		case "text":
			t.Text = v
		case "dep":
			t.DepText = v
		case "touch":
			t.Touch = splitList(v)
		case "status":
			t.Status = Status(v)
		case "note":
			t.DoneNote = v
		}
	}
	return true, s.Put(t)
}

// SetNote sets or clears a task's comment (the done_note) without touching its status — so a note
// can be added or corrected AFTER a task is closed, no reopen required. An empty note clears it.
func (s *Store) SetNote(id, note string) (bool, error) {
	return s.Update(id, map[string]string{"note": note})
}

// AddDep and DelDep manage one dependency edge by hand, which is how the free-text `dep:` prose gets
// turned into a graph the Ready query can act on.
func (s *Store) AddDep(id, dependsOn string) error {
	if _, ok, err := s.Get(id); err != nil || !ok {
		return fmt.Errorf("no such task: %s", id)
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO deps (task_id, depends_on) VALUES (?,?)`, id, dependsOn)
	return err
}

func (s *Store) DelDep(id, dependsOn string) error {
	_, err := s.db.Exec(`DELETE FROM deps WHERE task_id = ? AND depends_on = ?`, id, dependsOn)
	return err
}

// Suggestion is a candidate edge: a fragment of a task's free-text dep line, and the task the
// full-text index thinks it names.
type Suggestion struct {
	TaskID    string `json:"taskId"`
	Fragment  string `json:"fragment"`
	Candidate string `json:"candidate"`
	CandText  string `json:"candidateText"`
}

// Suggest turns the free-text `dep:` prose of a task into candidate edges. The import kept that
// prose because it names dependencies by phrase, not by id ("SPNEGO HTTP authenticator", "Project
// Kind") — the full-text index is exactly the tool for phrase-to-task, so each comma-separated
// fragment becomes a query and the best-matching OTHER task is the candidate.
//
// It proposes, it does not decide: a phrase can match the wrong task, and a wrong edge is worse
// than a missing one because Ready acts on it. The caller (a person, or an LLM) confirms with
// `dep`, or passes --apply to accept the top candidate for each fragment at once.
func (s *Store) Suggest(id string) ([]Suggestion, error) {
	t, ok, err := s.Get(id)
	if err != nil || !ok {
		return nil, err
	}
	var out []Suggestion
	for _, frag := range splitList(t.DepText) {
		q := ftsQuery(frag)
		if len(q) == 0 {
			continue
		}
		rows, err := s.db.Query(`
SELECT t.id, t.text FROM tasks_fts f JOIN tasks t ON t.rowid = f.rowid
WHERE f.tasks_fts MATCH ? AND t.id != ?
ORDER BY bm25(f.tasks_fts) LIMIT 1`, q, id)
		if err != nil {
			return nil, err
		}
		var candID, candText string
		if rows.Next() {
			_ = rows.Scan(&candID, &candText)
		}
		_ = rows.Close()
		if len(candID) > 0 {
			out = append(out, Suggestion{TaskID: id, Fragment: frag, Candidate: candID, CandText: candText})
		}
	}
	return out, nil
}

// ftsQuery turns a free-text fragment into a safe FTS5 OR-query: the words of three or more letters,
// joined by OR. Dropping punctuation is what keeps "queue.Manager" or "spec.project" from being read
// as FTS operators, and OR rather than AND is deliberate — a fragment is a hint, and the best single
// match under bm25 is wanted, not only rows that contain every word.
func ftsQuery(frag string) string {
	var words []string
	for _, w := range strings.FieldsFunc(frag, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		if len(w) >= 3 {
			words = append(words, w)
		}
	}
	return strings.Join(words, " OR ")
}

// normTags lower-cases, trims and de-duplicates, preserving first-seen order.
func normTags(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) == 0 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func trailingNum(id string) int {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	n := 0
	for _, c := range id[i:] {
		n = n*10 + int(c-'0')
	}
	return n
}

// SetStatus closes or reopens a task and returns whether it existed.
func (s *Store) SetStatus(id string, st Status) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, string(st), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Tags is every distinct tag the live backlog uses, sorted — what a UI offers as filters,
// discovered rather than hard-coded, because the labels are each project's own convention.
func (s *Store) Tags() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT je.value FROM tasks t, json_each(t.tags) je
WHERE t.deleted_at = '' ORDER BY je.value`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Epics is every distinct epic the live backlog uses, sorted — the existing projects a UI offers to
// file a task under, so an epic is chosen from what exists rather than retyped and misspelled.
func (s *Store) Epics() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT epic FROM tasks WHERE deleted_at = '' AND epic != '' ORDER BY epic`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BackupTo writes a consistent snapshot of the whole database to path, via VACUUM INTO: a
// transactional copy made by SQLite itself, safe while the database is open — a plain file copy
// can catch the middle of a write. It refuses an existing destination rather than overwriting a
// previous backup, which is the property that makes keeping several of them trivial.
func (s *Store) BackupTo(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; a backup never overwrites", path)
	}
	// The path travels as a bound parameter, so a quote in a directory name cannot break out.
	_, err := s.db.Exec(`VACUUM INTO ?`, path)
	return err
}

// Counts is the one-line inventory — what a backup verification reports.
func (s *Store) Counts() (tasks, docs int, err error) {
	if err = s.db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT count(*) FROM docs`).Scan(&docs)
	return
}

// Stat is one row of the stats report.
type Stat struct {
	Epic string `json:"epic"`
	Open int    `json:"open"`
	Done int    `json:"done"`
}

// Stats counts open and done per epic, so "where does the work stand" is one command.
func (s *Store) Stats() ([]Stat, error) {
	rows, err := s.db.Query(`
SELECT epic,
       SUM(CASE WHEN status='open' THEN 1 ELSE 0 END),
       SUM(CASE WHEN status='done' THEN 1 ELSE 0 END)
FROM tasks WHERE deleted_at = '' GROUP BY epic ORDER BY MIN(ord)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Stat
	for rows.Next() {
		var st Stat
		if err := rows.Scan(&st.Epic, &st.Open, &st.Done); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// query is the one SELECT every LIVE read goes through — soft-deleted tasks are filtered here, in
// one place, so no caller has to remember to. The caller supplies extra conditions (without the
// word WHERE) and their arguments; the projection, the deleted filter and the ordering are fixed.
func (s *Store) query(cond string, args ...any) ([]Task, error) {
	return s.scan(injectLive(cond), args...)
}

// injectLive prefixes the always-on "not deleted" condition to a caller's conditions.
func injectLive(cond string) string {
	if len(cond) == 0 {
		return "WHERE t.deleted_at = ''"
	}
	return "WHERE t.deleted_at = '' AND " + cond
}

// scan is the raw read without the deleted filter, so the trash view can reach what query hides.
func (s *Store) scan(where string, args ...any) ([]Task, error) {
	rows, err := s.db.Query(`
SELECT t.id, t.tags, t.epic, t.status, t.priority, t.rank, t.slug, t.touch, t.dep_text,
       t.text, t.done_sha, t.done_note, t.ord, t.deleted_at
FROM tasks t `+where+` ORDER BY t.rank, t.ord`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Task
	for rows.Next() {
		var t Task
		var touch, tags, status string
		if err := rows.Scan(&t.ID, &tags, &t.Epic, &status, &t.Priority, &t.Rank, &t.Slug,
			&touch, &t.DepText, &t.Text, &t.DoneSHA, &t.DoneNote, &t.Order, &t.DeletedAt); err != nil {
			return nil, err
		}
		t.Status = Status(status)
		_ = json.Unmarshal([]byte(touch), &t.Touch)
		_ = json.Unmarshal([]byte(tags), &t.Tags)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The dependency edges in one extra query, keyed back onto the tasks — cheaper than a join that
	// multiplies every task row by its edge count and then de-duplicates in Go.
	if err := s.attachDeps(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) attachDeps(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	byID := make(map[string]*Task, len(tasks))
	ph := make([]string, len(tasks))
	args := make([]any, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
		ph[i] = "?"
		args[i] = tasks[i].ID
	}
	rows, err := s.db.Query(`SELECT task_id, depends_on FROM deps WHERE task_id IN (`+
		strings.Join(ph, ",")+`) ORDER BY depends_on`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, dep string
		if err := rows.Scan(&id, &dep); err != nil {
			return err
		}
		if t := byID[id]; t != nil {
			t.DependsOn = append(t.DependsOn, dep)
		}
	}
	return rows.Err()
}

// All returns every task, for render and for a full export.
func (s *Store) All() ([]Task, error) { return s.query("") }
