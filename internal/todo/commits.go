package todo

import (
	"os/exec"
	"regexp"
	"strings"
)

// taskRef matches a task id wherever it appears in a commit message — the subject, the body, a
// trailer like "Task: ee-scheduler-07". It is the same shape the ids are minted in.
var taskRef = regexp.MustCompile(`\b((?:ce|ee)-[a-z0-9]+(?:-[a-z0-9]+)*-\d{2,})\b`)

// CommitLink is one discovered mapping, before it is written.
type CommitLink struct {
	TaskID  string
	SHA     string
	Subject string
	Date    string // committer date, ISO 8601 (%cI)
}

// ScanCommits reads `git log` in dir and returns every (task id, commit) pair it finds — a commit
// that names a task in its message is a commit that belongs to that task. rev limits the range
// ("" scans all history; "v0.13.0..HEAD" scans since a tag). It only READS git; writing the edges
// is SyncCommits, so the discovery is testable without a store.
func ScanCommits(dir, rev string) ([]CommitLink, error) {
	args := []string{"-C", dir, "log", "--no-merges", "--format=%H%x1f%cI%x1f%s%x1f%b%x1e"}
	if len(rev) > 0 {
		args = append(args, rev)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	var links []CommitLink
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		rec = strings.Trim(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 4)
		if len(parts) < 3 {
			continue
		}
		sha, date, subject := parts[0], parts[1], parts[2]
		msg := subject
		if len(parts) == 4 {
			msg += "\n" + parts[3]
		}
		seen := map[string]bool{}
		for _, m := range taskRef.FindAllString(msg, -1) {
			if seen[m] {
				continue
			}
			seen[m] = true
			links = append(links, CommitLink{TaskID: m, SHA: sha, Subject: subject, Date: date})
		}
	}
	return links, nil
}

// SyncCommits discovers commit→task mappings and writes them as commit links. A commit that names a
// task the store does not have is skipped rather than failed — a message can mention an id that was
// renamed or never existed, and one bad reference must not stop the sweep. Returns how many edges
// were written.
func (s *Store) SyncCommits(dir, rev string) (int, error) {
	found, err := ScanCommits(dir, rev)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, cl := range found {
		if _, ok, err := s.Get(cl.TaskID); err != nil {
			return n, err
		} else if !ok {
			continue
		}
		if err := s.Link(cl.TaskID, LinkCommit, cl.SHA, cl.Subject, cl.Date); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
