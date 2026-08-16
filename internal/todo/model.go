// Package todo is the store and the model behind a personal backlog: tasks with an epic, a
// priority, any number of tags, the files they touch, and the dependencies between them. The database is the
// source of truth; markdown is one thing it can render.
package todo

import "strings"

// Status is where a task is. Two values, because a backlog answers one question — is this still to
// do — and a third state is always someone's private meaning leaking into a shared field.
type Status string

const (
	StatusOpen Status = "open"
	StatusDone Status = "done"
)

// Task is one line of the backlog, decomposed into the fields the markdown grammar already carries.
type Task struct {
	ID string `json:"id"` // stable, human- and LLM-referable, e.g. scheduler-07
	// Tags are free labels, lower-cased, any number of them. They replaced a dedicated "edition"
	// field: an edition was one project's delivery split promoted into everybody's schema, and a
	// tag says the same thing without the tool holding an opinion about what projects ship.
	Tags      []string `json:"tags,omitempty"`
	Epic      string   `json:"epic"`           // the ## heading it lived under
	Status    Status   `json:"status"`         // open | done
	Priority  string   `json:"priority"`       // P0..P5, E4, v2, or EE — verbatim as written
	Rank      int      `json:"rank"`           // priority as a number, for ordering; see rankOf
	Slug      string   `json:"slug,omitempty"` // the design-doc it points at (the "— slug" suffix)
	Touch     []string `json:"touchpoints,omitempty"`
	DependsOn []string `json:"dependsOn,omitempty"` // task IDs this waits on (resolved where possible)
	DepText   string   `json:"depText,omitempty"`   // the raw "· dep: ..." text, kept because not every dep is an ID
	Text      string   `json:"text"`                // the task itself, prose and all
	DoneSHA   string   `json:"doneSha,omitempty"`   // the commit that closed it, from **DONE <sha>:**
	DoneNote  string   `json:"doneNote,omitempty"`  // what the DONE annotation said
	Order     int      `json:"order"`               // position within its file, so render is stable
	DeletedAt string   `json:"deletedAt,omitempty"` // RFC3339 when soft-deleted; empty means live
}

// A node's kind tells a task apart from a trailer wherever the two share a view — the graph `todo
// path` walks, and the list filters that show tasks only. Tasks and trailers live in separate
// tables (one authored, one a rebuildable cache), so the kind is the discriminator that names which
// side a node came from without a column that has to be kept in step.
const (
	KindTask    = "task"
	KindTrailer = "trailer"
)

// Trailer is a git commit projected into the graph as a read-only node. reindex loads it from the
// history — the sha is its name, the commit message its body — and nothing edits it afterwards. A
// trailer is NOT a task: it never appears in the backlog list or the TUI, and exists only so `todo
// path` can walk the provenance behind the tasks. Its project is the repo it came from; its tags,
// unlike a task's locally-authored ones, are parsed from the commit message and so are shared by
// everyone who reindexes the same history.
type Trailer struct {
	SHA     string   `json:"sha"`  // the commit sha — the node's name
	Repo    string   `json:"repo,omitempty"`
	Subject string   `json:"subject"`
	Body    string   `json:"body,omitempty"`
	Tags    []string `json:"tags,omitempty"`    // from the commit message; git-derived, shared
	Parents []string `json:"parents,omitempty"` // parent shas — the git edges
	At      string   `json:"at,omitempty"`      // committer date, ISO 8601
}

// Symbol is a code node ingested from graphify's extraction: a class, function, file or package,
// with where it lives in the source. It is derived (rebuilt per repo on each ingest) and joins the
// same graph as the tasks and trailers, bridged by File to the provenance side.
type Symbol struct {
	Repo  string `json:"repo"`
	SID   string `json:"sid"` // graphify node id
	Label string `json:"label"`
	Kind  string `json:"kind"` // file | func | package | doc | symbol
	File  string `json:"file"`
	Line  string `json:"line"`
}

// Doc is a wiki page: a title, a stable path, a kind, and a markdown body. The path may carry ONE
// level of hierarchy — "<section>/<page>", e.g. threat-model/02-node — where "<section>/README" is
// the section's index and the pages beside it stay flat: a section groups pages that together
// describe one thing, it does not nest. Tasks map onto docs through Links, both ways — a task's
// docs and a doc's tasks are the same edges read from two ends.
type Doc struct {
	ID        string `json:"id"`   // stable handle, e.g. doc-runtime-storage-scheduler
	Path      string `json:"path"` // the slug a task's Slug field can match; optionally "<section>/<page>"
	Title     string `json:"title"`
	Kind      string `json:"kind"` // design | threat-model | note | adr | reference
	Body      string `json:"body"`
	DeletedAt string `json:"deletedAt,omitempty"`
}

// Link is an edge FROM a task or a doc to something else: a doc, a commit, or a URL. One table
// carries all of it because the questions are the same shape — "what does this point at" and, for
// docs, "what points at this doc" — and a kind keeps them apart. Commits land here rather than in a
// field because a task accretes many of them over its life.
type Link struct {
	TaskID string `json:"taskId"` // the source: a task id, or a doc id for doc↔doc relations
	Kind   string `json:"kind"`   // doc | commit | url
	Ref    string `json:"ref"`    // doc id | commit sha | url
	Note   string `json:"note,omitempty"`
	At     string `json:"at,omitempty"` // when the edge is time-stamped: a commit's date (ISO 8601)
}

const (
	LinkDoc    = "doc"
	LinkCommit = "commit"
	LinkURL    = "url"
)

// Comment is one entry in a task's comment thread: a timestamp and text, and nothing more. There is
// deliberately NO author — the backlog has no notion of a user, and the work is distributed (each
// maintainer keeps their own database), so authorship would be meaningless. A thread accretes many
// comments over a task's life, oldest first; each keeps its own id so it can be edited or removed.
type Comment struct {
	ID     int64  `json:"id"`
	TaskID string `json:"taskId"`
	At     string `json:"at"` // RFC3339, supplied by the caller — the store stays clock-free
	Text   string `json:"text"`
}

// rankOf turns a priority label into a sort key. Lower is more urgent. Everything the grammar uses
// maps onto one line; an unknown label sorts last rather than crashing, because a backlog with a
// typo in it is still a backlog you want to read.
func rankOf(priority string) int {
	switch strings.ToUpper(strings.TrimSpace(priority)) {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	case "P4":
		return 4
	case "P5":
		return 5
	case "E4":
		return 6 // the networking "E" scale sits just past the P scale
	case "V2":
		return 8 // explicitly a later cut
	case "EE":
		return 7 // a bare edition tag with no number — after the P scale, before v2
	default:
		return 99
	}
}
