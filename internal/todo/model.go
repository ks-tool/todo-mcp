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

// Doc is a wiki page: a title, a stable path, a kind, and a markdown body. Tasks map onto docs
// through Links, both ways — a task's docs and a doc's tasks are the same edges read from two ends.
type Doc struct {
	ID        string `json:"id"`   // stable handle, e.g. doc-runtime-storage-scheduler
	Path      string `json:"path"` // the slug a task's Slug field can match, e.g. runtime-storage-scheduler
	Title     string `json:"title"`
	Kind      string `json:"kind"` // design | note | adr | reference
	Body      string `json:"body"`
	DeletedAt string `json:"deletedAt,omitempty"`
}

// Link is an edge FROM a task to something else: a doc, a commit, or a URL. One table carries all
// three because the questions are the same shape — "what does this task point at" and, for docs,
// "what points at this doc" — and a kind keeps them apart. Commits land here rather than in a field
// because a task accretes many of them over its life.
type Link struct {
	TaskID string `json:"taskId"`
	Kind   string `json:"kind"` // doc | commit | url
	Ref    string `json:"ref"`  // doc id | commit sha | url
	Note   string `json:"note,omitempty"`
}

const (
	LinkDoc    = "doc"
	LinkCommit = "commit"
	LinkURL    = "url"
)

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
