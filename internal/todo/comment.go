package todo

import "fmt"

// The comment thread: an authored, timestamped stream on a task, apart from the single done_note (the
// import DONE-annotation). It accretes — a comment is added, edited or soft-deleted, never an author
// recorded — and it works on a done task without reopening, because a comment is not a state change.

// AddComment appends a comment to a task's thread and returns its id. at is the caller's timestamp
// (the store keeps no clock). The task must exist — a comment with no task is a dangling note.
func (s *Store) AddComment(taskID, text, at string) (int64, error) {
	if _, ok, err := s.Get(taskID); err != nil || !ok {
		return 0, fmt.Errorf("no such task: %s", taskID)
	}
	res, err := s.db.Exec(`INSERT INTO comments (task_id, at, text) VALUES (?,?,?)`, taskID, at, text)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Comments returns a task's live thread, oldest first — the order it was written and reads in.
func (s *Store) Comments(taskID string) ([]Comment, error) {
	rows, err := s.db.Query(`SELECT id, task_id, at, text FROM comments
WHERE task_id = ? AND deleted_at = '' ORDER BY at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.At, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EditComment rewrites one comment's text, by id; the timestamp is left as it was.
func (s *Store) EditComment(id int64, text string) (bool, error) {
	res, err := s.db.Exec(`UPDATE comments SET text = ? WHERE id = ? AND deleted_at = ''`, text, id)
	return affected(res, err)
}

// DeleteComment soft-deletes one comment, by id — stamped, not dropped, like everything else here.
func (s *Store) DeleteComment(id int64, now string) (bool, error) {
	res, err := s.db.Exec(`UPDATE comments SET deleted_at = ? WHERE id = ? AND deleted_at = ''`, now, id)
	return affected(res, err)
}
