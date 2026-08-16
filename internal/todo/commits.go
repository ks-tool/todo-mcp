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

// RepoHasCommit reports whether dir is a git repository and, if so, whether it contains sha as a
// commit. It is how task_commit refuses a foreign sha: a dependency's or library's commit is not in
// this repo's history, so recording it as a commit link would point the provenance graph at a ref
// that goes nowhere — that belongs in a task comment instead. When dir is not a repo the membership
// cannot be judged, so the guard steps aside (isRepo false) rather than block a legitimate record.
func RepoHasCommit(dir, sha string) (isRepo, has bool) {
	if err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run(); err != nil {
		return false, false
	}
	err := exec.Command("git", "-C", dir, "cat-file", "-e", sha+"^{commit}").Run()
	return true, err == nil
}

// Commit is one commit read whole from the log — everything a trailer node needs. Parents are the
// git edges the graph walks; Message is the commit message as-is, subject and body together.
type Commit struct {
	SHA     string
	Parents []string
	Date    string // committer date, ISO 8601 (%cI)
	Subject string
	Message string // %B — the raw message, subject and body
}

// LogCommits reads the whole log of rev in dir — every commit as a node, with its parents as edges.
// It is what reindex projects into the derived layer; it only READS git, so the projection is
// testable without a repository of the right shape. rev is a ref or range ("main", "v1..HEAD").
func LogCommits(dir, rev string) ([]Commit, error) {
	args := []string{"-C", dir, "log", "--format=%H%x1f%P%x1f%cI%x1f%s%x1f%B%x1e"}
	if len(rev) > 0 {
		args = append(args, rev)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		rec = strings.Trim(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 5)
		if len(parts) < 4 {
			continue
		}
		c := Commit{SHA: parts[0], Date: parts[2], Subject: parts[3]}
		if len(parts[1]) > 0 {
			c.Parents = strings.Fields(parts[1])
		}
		if len(parts) == 5 {
			c.Message = strings.TrimRight(parts[4], "\n")
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// LogFiles reads which files each commit of rev changed — the trailer→file edges of the optional
// code layer. Merges are skipped (a merge lists no files of its own), so a file is attributed to
// the commit that actually changed it. Returns sha → paths.
func LogFiles(dir, rev string) (map[string][]string, error) {
	args := []string{"-C", dir, "log", "--no-merges", "--name-only", "--format=%x1e%H"}
	if len(rev) > 0 {
		args = append(args, rev)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	files := map[string][]string{}
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		lines := strings.Split(strings.Trim(rec, "\n"), "\n")
		if len(lines) == 0 || len(lines[0]) == 0 {
			continue
		}
		sha := lines[0]
		for _, f := range lines[1:] {
			if f = strings.TrimSpace(f); len(f) > 0 {
				files[sha] = append(files[sha], f)
			}
		}
	}
	return files, nil
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
