package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// runMCP serves the same backlog as MCP tools over stdio, the third front over one core: the CLI is
// for a person and a pipe, this is for an LLM host that would rather call todo_ready than shell out
// and parse. Every tool is typed in and out, so the host gets a schema for each and a structured
// result rather than text it has to interpret.
//
// The store is opened once for the life of the server and shared across calls — a stdio server
// handles one request at a time, and SQLite is happy with that.
func runMCP(st *todo.Store) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "todo", Version: "0.1.0"}, nil)

	// --- reads ---

	mcp.AddTool(s, &mcp.Tool{Name: "todo_list",
		Description: "List tasks. Filters: tag (comma = AND), epic (substring), priority (P0..P5), search (full-text), status (open|done, default open), all (both), ready (only tasks whose deps are done)."},
		func(_ context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, tasksOut, error) {
			f := todo.Filter{Status: todo.StatusOpen, Tags: splitComma(in.Tag), Epic: in.Epic, Priority: in.Priority, Search: in.Search}
			if len(in.Status) > 0 {
				f.Status = todo.Status(in.Status)
			}
			if in.All {
				f.Status = ""
			}
			var ts []todo.Task
			var err error
			if in.Ready {
				ts, err = st.Ready()
				ts = filterTasks(ts, f)
			} else {
				ts, err = st.List(f)
			}
			return result(len(ts)), tasksOut{Tasks: ts}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_ready",
		Description: "Open tasks whose every dependency is already done — the ones that can be started now. Optional tag filter."},
		func(_ context.Context, _ *mcp.CallToolRequest, in tagIn) (*mcp.CallToolResult, tasksOut, error) {
			ts, err := st.Ready()
			ts = filterTasks(ts, todo.Filter{Tags: splitComma(in.Tag)})
			return result(len(ts)), tasksOut{Tasks: ts}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_next",
		Description: "The single most urgent ready task (lowest priority rank), optionally under a tag."},
		func(_ context.Context, _ *mcp.CallToolRequest, in tagIn) (*mcp.CallToolResult, taskOut, error) {
			ts, err := st.Ready()
			ts = filterTasks(ts, todo.Filter{Tags: splitComma(in.Tag)})
			if err != nil || len(ts) == 0 {
				return result(0), taskOut{}, err
			}
			return result(1), taskOut{Task: &ts[0]}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_show", Description: "One task in full, by id, with its comment thread."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, taskOut, error) {
			t, ok, err := st.Get(in.ID)
			if err != nil || !ok {
				return notFound(in.ID), taskOut{}, err
			}
			cs, err := st.Comments(in.ID)
			if err != nil {
				return result(0), taskOut{}, err
			}
			return result(1), taskOut{Task: &t, Comments: cs}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_impact", Description: "Open tasks that directly depend on the given id — what unblocks if it is done."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, tasksOut, error) {
			ts, err := st.Impact(in.ID)
			return result(len(ts)), tasksOut{Tasks: ts}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_stats", Description: "Open and done counts per epic."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, statsOut, error) {
			ss, err := st.Stats()
			return result(len(ss)), statsOut{Stats: ss}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_suggest",
		Description: "Dependency candidates for a task, resolved from its free-text dep line via full-text search. With apply=true, links the top candidate for each fragment."},
		func(_ context.Context, _ *mcp.CallToolRequest, in suggestIn) (*mcp.CallToolResult, suggestOut, error) {
			sg, err := st.Suggest(in.ID)
			if err != nil {
				return result(0), suggestOut{}, err
			}
			if in.Apply {
				for _, x := range sg {
					if err := st.AddDep(x.TaskID, x.Candidate); err != nil {
						return result(0), suggestOut{}, err
					}
				}
			}
			return result(len(sg)), suggestOut{Suggestions: sg}, nil
		})

	// --- writes ---

	mcp.AddTool(s, &mcp.Tool{Name: "todo_add",
		Description: "Create a task. text is required; epic defaults to the project's root epic (the first of the TODO_EPICS install wrote); tags, priority, slug, dep, touchpoints optional. The id is minted and returned."},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, taskOut, error) {
			if len(in.Epic) == 0 {
				in.Epic = rootEpic()
			}
			if len(in.Epic) == 0 || len(in.Text) == 0 {
				return errText("text is required, and an epic — none given and no TODO_EPICS is set"), taskOut{}, nil
			}
			id, err := st.NextID(in.Epic)
			if err != nil {
				return result(0), taskOut{}, err
			}
			t := todo.Task{ID: id, Tags: in.Tags, Epic: in.Epic, Status: todo.StatusOpen,
				Priority: in.Priority, Slug: in.Slug, DepText: in.Dep, Text: in.Text, Touch: in.Touch}
			if err := st.Put(t); err != nil {
				return result(0), taskOut{}, err
			}
			// Returned as READ BACK, not as sent: Put derives fields the input does not carry (the
			// rank from the priority), and a caller acting on this result must see what the store
			// holds rather than the pre-write struct with a zero in it.
			saved, _, err := st.Get(id)
			if err != nil {
				return result(0), taskOut{}, err
			}
			return textResult("added " + id), taskOut{Task: &saved}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_edit",
		Description: "Change named fields of a task: priority, epic, slug, text, dep, tags (comma-separated), touch (comma-separated file touchpoints), status. Only the fields given are touched."},
		func(_ context.Context, _ *mcp.CallToolRequest, in editIn) (*mcp.CallToolResult, okOut, error) {
			fields := map[string]string{}
			for k, v := range map[string]string{"priority": in.Priority, "epic": in.Epic, "slug": in.Slug,
				"text": in.Text, "dep": in.Dep, "tags": in.Tags, "touch": in.Touch, "status": in.Status} {
				if len(v) > 0 {
					fields[k] = v
				}
			}
			ok, err := st.Update(in.ID, fields)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_dep", Description: "Link (or with del=true unlink) a dependency edge, so ready/next can act on it."},
		func(_ context.Context, _ *mcp.CallToolRequest, in depIn) (*mcp.CallToolResult, okOut, error) {
			var err error
			if in.Del {
				err = st.DelDep(in.ID, in.DependsOn)
			} else {
				err = st.AddDep(in.ID, in.DependsOn)
			}
			return okResult(err == nil, in.ID), okOut{OK: err == nil}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_done", Description: "Mark a task done."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.SetStatus(in.ID, todo.StatusDone)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_reopen", Description: "Reopen a done task."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.SetStatus(in.ID, todo.StatusOpen)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_delete",
		Description: "Soft-delete a task: it is stamped deleted_at and moved to the trash, never removed, so it can be restored."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.Delete(in.ID, time.Now().Format(time.RFC3339))
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_restore", Description: "Restore a soft-deleted task from the trash."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.Restore(in.ID)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_trash", Description: "The soft-deleted tasks, for review before they are restored."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, tasksOut, error) {
			ts, err := st.Trash()
			return result(len(ts)), tasksOut{Tasks: ts}, err
		})

	// --- wiki + mappings ---

	mcp.AddTool(s, &mcp.Tool{Name: "doc_add",
		Description: "Create or replace a wiki page. path and body are required; title and kind (design|threat-model|note|adr|reference) optional. The id is derived from the path and returned, so a later doc_link can find it."},
		func(_ context.Context, _ *mcp.CallToolRequest, in docIn) (*mcp.CallToolResult, docOut, error) {
			if len(in.Path) == 0 || len(in.Body) == 0 {
				return errText("path and body are required"), docOut{}, nil
			}
			d := todo.Doc{ID: todo.DocID(in.Path), Path: in.Path, Title: orElse(in.Title, in.Path),
				Kind: orElse(in.Kind, "note"), Body: in.Body}
			if err := st.PutDoc(d); err != nil {
				return result(0), docOut{}, err
			}
			return textResult("saved " + d.ID), docOut{Doc: &d}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_show", Description: "One wiki page in full, by id, with the pages related to it (linked either way) as metadata."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, docOut, error) {
			d, ok, err := st.GetDoc(in.ID)
			if err != nil || !ok {
				return notFound(in.ID), docOut{}, err
			}
			rel, err := st.RelatedDocs(in.ID)
			if err != nil {
				return result(0), docOut{}, err
			}
			return result(1), docOut{Doc: &d, Related: metasOf(rel)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_list", Description: "Wiki pages, narrowed by a full-text search over title and body, or by section (the path prefix before '/', e.g. 'threat-model'; its README comes first). Returns metadata only — id, path, title, kind — because a list is for choosing; doc_show returns the body."},
		func(_ context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, docMetasOut, error) {
			var ds []todo.Doc
			var err error
			if len(in.Section) > 0 {
				ds, err = st.SectionDocs(in.Section)
			} else {
				ds, err = st.ListDocs(in.Search)
			}
			return result(len(ds)), docMetasOut{Docs: metasOf(ds)}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_link",
		Description: "Map a task to a doc, both ways at once (task_docs will show it from the task, doc_tasks from the doc). task_id may also be a DOC id, relating two pages (a chapter to its README). With del=true, unlink."},
		func(_ context.Context, _ *mcp.CallToolRequest, in linkIn) (*mcp.CallToolResult, okOut, error) {
			var err error
			if in.Del {
				err = st.Unlink(in.TaskID, todo.LinkDoc, in.DocID)
			} else {
				err = st.Link(in.TaskID, todo.LinkDoc, in.DocID, in.Note)
			}
			return okResult(err == nil, in.TaskID), okOut{OK: err == nil}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_edit",
		Description: "Change named fields of a wiki page: path, title, kind (design|threat-model|note|adr|reference), body. Only the fields given are touched; the id never changes, so every link to the page survives a rename."},
		func(_ context.Context, _ *mcp.CallToolRequest, in docEditIn) (*mcp.CallToolResult, okOut, error) {
			d, ok, err := st.GetDoc(in.ID)
			if err != nil || !ok {
				return notFound(in.ID), okOut{}, err
			}
			for _, f := range []struct {
				v   string
				dst *string
			}{{in.Path, &d.Path}, {in.Title, &d.Title}, {in.Kind, &d.Kind}, {in.Body, &d.Body}} {
				if len(f.v) > 0 {
					*f.dst = f.v
				}
			}
			err = st.PutDoc(d)
			return okResult(err == nil, in.ID), okOut{OK: err == nil}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_delete",
		Description: "Soft-delete a wiki page: stamped deleted_at, restorable — the same trash the tasks have."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.DeleteDoc(in.ID, time.Now().Format(time.RFC3339))
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_restore", Description: "Restore a soft-deleted wiki page."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.RestoreDoc(in.ID)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "link_slugs",
		Description: "Link every task to the doc its slug names — the exact path, or the single page of that name inside any section. Idempotent; returns how many edges the bridge holds."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, linkSlugsOut, error) {
			n, err := st.LinkDocsBySlug()
			return result(n), linkSlugsOut{Linked: n}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "task_docs", Description: "The wiki pages a task is mapped to — metadata only; doc_show returns a body."},
		func(_ context.Context, _ *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, docMetasOut, error) {
			ds, err := st.DocsOf(in.TaskID)
			return result(len(ds)), docMetasOut{Docs: metasOf(ds)}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "doc_tasks", Description: "The tasks mapped to a wiki page — the other direction of the same mapping."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, tasksOut, error) {
			ts, err := st.TasksOf(in.ID)
			return result(len(ts)), tasksOut{Tasks: ts}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "task_commit",
		Description: "Record a commit against a task (sha, note=subject, at=ISO 8601 commit date), or with del=true unlink it. A task accretes many over its life. Pass dir to assert which repo the commit lives in — then a sha that repo (or the trailer cache) does not contain is refused, because a dependency's commit is a task comment (todo_note), not a commit link. Without dir the link is recorded and only flagged when the sha is unknown here, since one backlog may serve several repos and the server sees only its own directory."},
		func(_ context.Context, _ *mcp.CallToolRequest, in commitIn) (*mcp.CallToolResult, okOut, error) {
			if in.Del {
				err := st.Unlink(in.TaskID, todo.LinkCommit, in.SHA)
				return okResult(err == nil, in.TaskID), okOut{OK: err == nil}, err
			}
			// With an explicit dir the caller asserts the repo, so an unknown sha is a hard error. With
			// none, the server's directory may be a different project than the commit's — so record it
			// and only flag an unknown sha rather than block a legitimate cross-repo link.
			if len(in.Dir) > 0 && !commitKnown(st, in.Dir, in.SHA) {
				return errText(shortref(in.SHA) + " is not a commit in " + in.Dir + " or the trailer cache; a dependency's sha is a task comment (todo_note), not a commit"), okOut{}, nil
			}
			if err := st.Link(in.TaskID, todo.LinkCommit, in.SHA, in.Note, in.At); err != nil {
				return okResult(false, in.TaskID), okOut{OK: false}, err
			}
			if len(in.Dir) == 0 && !commitKnown(st, ".", in.SHA) {
				return textResult("recorded, but " + shortref(in.SHA) + " is not in this directory's repo or the trailer cache — if it is a dependency's commit, prefer todo_note"), okOut{OK: true}, nil
			}
			return textResult("ok"), okOut{OK: true}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_note",
		Description: "Set or edit a task's single done_note (the closing annotation from the import grammar) WITHOUT changing its status. For an ongoing discussion use the comment thread (comment_add) instead; an empty note clears it."},
		func(_ context.Context, _ *mcp.CallToolRequest, in noteIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.SetNote(in.ID, in.Note)
			return okResult(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "comment_add",
		Description: "Append a comment to a task's thread — many timestamped entries, no author. Works on a done task without reopening. Returns the new comment with its id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in commentAddIn) (*mcp.CallToolResult, commentOut, error) {
			at := time.Now().Format(time.RFC3339)
			id, err := st.AddComment(in.TaskID, in.Text, at)
			if err != nil {
				return errText(err.Error()), commentOut{}, nil
			}
			return textResult("added comment"), commentOut{Comment: &todo.Comment{ID: id, TaskID: in.TaskID, At: at, Text: in.Text}}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "comment_list", Description: "A task's comment thread, oldest first."},
		func(_ context.Context, _ *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, commentsOut, error) {
			cs, err := st.Comments(in.TaskID)
			return result(len(cs)), commentsOut{Comments: cs}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "comment_edit", Description: "Rewrite one comment's text, by id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in commentEditIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.EditComment(in.ID, in.Text)
			return okResultN(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "comment_delete", Description: "Soft-delete one comment, by id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in commentIDIn) (*mcp.CallToolResult, okOut, error) {
			ok, err := st.DeleteComment(in.ID, time.Now().Format(time.RFC3339))
			return okResultN(ok, in.ID), okOut{OK: ok}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "task_commits", Description: "The commits recorded against a task."},
		func(_ context.Context, _ *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, linksOut, error) {
			ls, err := st.CommitsOf(in.TaskID)
			return result(len(ls)), linksOut{Links: ls}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "sync_commits",
		Description: "Scan git log in dir (default '.') for commit messages naming task ids and record each as a commit link. rev limits the range (e.g. v0.13.0..HEAD)."},
		func(_ context.Context, _ *mcp.CallToolRequest, in syncIn) (*mcp.CallToolResult, countOut, error) {
			n, err := st.SyncCommits(orElse(in.Dir, "."), in.Rev)
			return textResult(fmt.Sprintf("wrote %d commit links", n)), countOut{Count: n}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "reindex",
		Description: "Rebuild the derived trailer layer from git: read the whole log of rev (default 'main') in dir (default '.') and re-fill the trailer cache, one node per commit. The authored tasks and trailer→epic bindings are untouched. repo labels the source (default: the directory's name)."},
		func(_ context.Context, _ *mcp.CallToolRequest, in reindexIn) (*mcp.CallToolResult, countOut, error) {
			dir := orElse(in.Dir, ".")
			n, err := st.Reindex(dir, reindexRepo(in.Repo, dir), orElse(in.Rev, "main"))
			return textResult(fmt.Sprintf("reindexed %d commits", n)), countOut{Count: n}, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "todo_path",
		Description: "Shortest chain of edges between two nodes. a and b are each a task id, a commit sha (full or a 7+ char prefix), a doc id, or a full-text phrase resolved to the node that mentions it most. Edges: commit (task↔trailer), parent (trailer↔trailer), dep (task↔task), doc (task/doc↔doc). epic and tag scope which nodes take part."},
		func(_ context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, pathOut, error) {
			p, ok, err := st.Path(in.A, in.B, todo.PathScope{Epic: in.Epic, Tags: splitComma(in.Tag)})
			if err != nil {
				return result(0), pathOut{}, err
			}
			if !ok {
				return textResult("no path"), pathOut{}, nil
			}
			return textResult(fmt.Sprintf("%d step(s)", len(p.Steps))), pathOut{Path: p}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "explain",
		Description: "A code symbol from the ingested graphify graph: its source (file:line), degree, and its connections — each with a relation (calls/imports_from/references/method/contains/depends_on) and confidence (EXTRACTED|INFERRED). node resolves as an exact id, an exact label (with or without '()'), or a label substring; repo restricts the search."},
		func(_ context.Context, _ *mcp.CallToolRequest, in explainIn) (*mcp.CallToolResult, explainOut, error) {
			ex, ok, err := st.Explain(in.Repo, in.Node)
			if err != nil {
				return result(0), explainOut{}, err
			}
			if !ok {
				return textResult("no such node: " + in.Node), explainOut{}, nil
			}
			return textResult(fmt.Sprintf("degree %d", ex.Degree)), explainOut{Explain: ex}, nil
		})

	// A best-effort reindex at start-up, so the server's graph reflects the history without waiting
	// for a hook or a manual call. The server runs in the project root, so "." is the repo; any
	// failure (not a git repo, no main) is a note on stderr, never a reason not to serve.
	if n, err := st.Reindex(".", reindexRepo("", "."), "main"); err != nil {
		fmt.Fprintf(os.Stderr, "todo: start-up reindex skipped: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "todo: reindexed %d commits at start-up\n", n)
	}

	return s.Run(context.Background(), &mcp.StdioTransport{})
}

// Input and output shapes. The SDK derives a JSON schema from these, so the field names and json
// tags ARE the contract the host sees — kept close to the CLI flags on purpose.

type listIn struct {
	Tag      string `json:"tag,omitempty"`
	Epic     string `json:"epic,omitempty"`
	Priority string `json:"priority,omitempty"`
	Search   string `json:"search,omitempty"`
	Status   string `json:"status,omitempty"`
	All      bool   `json:"all,omitempty"`
	Ready    bool   `json:"ready,omitempty"`
}
type tagIn struct {
	Tag string `json:"tag,omitempty"`
}
type idIn struct {
	ID string `json:"id"`
}
type suggestIn struct {
	ID    string `json:"id"`
	Apply bool   `json:"apply,omitempty"`
}
type addIn struct {
	Tags []string `json:"tags,omitempty"`
	// omitempty is load-bearing: without it the SDK derives a schema with epic REQUIRED and rejects
	// an epicless call before the handler can apply the TODO_EPICS default.
	Epic     string   `json:"epic,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Slug     string   `json:"slug,omitempty"`
	Dep      string   `json:"dep,omitempty"`
	Touch    []string `json:"touchpoints,omitempty"`
	Text     string   `json:"text"`
}
type editIn struct {
	ID       string `json:"id"`
	Priority string `json:"priority,omitempty"`
	Epic     string `json:"epic,omitempty"`
	Slug     string `json:"slug,omitempty"`
	Text     string `json:"text,omitempty"`
	Dep      string `json:"dep,omitempty"`
	Tags     string `json:"tags,omitempty"`
	Touch    string `json:"touch,omitempty"`
	Status   string `json:"status,omitempty"`
}
type depIn struct {
	ID        string `json:"id"`
	DependsOn string `json:"dependsOn"`
	Del       bool   `json:"del,omitempty"`
}
type searchIn struct {
	Search  string `json:"search,omitempty"`
	Section string `json:"section,omitempty"`
}
type taskIDIn struct {
	TaskID string `json:"taskId"`
}
type docIn struct {
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Body  string `json:"body"`
}
type linkIn struct {
	TaskID string `json:"taskId"`
	DocID  string `json:"docId"`
	Note   string `json:"note,omitempty"`
	Del    bool   `json:"del,omitempty"`
}
type docEditIn struct {
	ID    string `json:"id"`
	Path  string `json:"path,omitempty"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Body  string `json:"body,omitempty"`
}
type linkSlugsOut struct {
	Linked int `json:"linked"`
}
type commitIn struct {
	TaskID string `json:"taskId"`
	SHA    string `json:"sha"`
	Note   string `json:"note,omitempty"`
	At     string `json:"at,omitempty"`  // commit date, ISO 8601; sync_commits fills this from git
	Dir    string `json:"dir,omitempty"` // assert the repo the commit lives in; unknown sha is then refused
	Del    bool   `json:"del,omitempty"`
}
type noteIn struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}
type commentAddIn struct {
	TaskID string `json:"taskId"`
	Text   string `json:"text"`
}
type commentEditIn struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}
type commentIDIn struct {
	ID int64 `json:"id"`
}
type commentOut struct {
	Comment *todo.Comment `json:"comment,omitempty"`
}
type commentsOut struct {
	Comments []todo.Comment `json:"comments"`
}
type syncIn struct {
	Dir string `json:"dir,omitempty"`
	Rev string `json:"rev,omitempty"`
}
type reindexIn struct {
	Dir  string `json:"dir,omitempty"`
	Rev  string `json:"rev,omitempty"`
	Repo string `json:"repo,omitempty"`
}
type pathIn struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Epic string `json:"epic,omitempty"`
	Tag  string `json:"tag,omitempty"`
}
type pathOut struct {
	Path *todo.Path `json:"path,omitempty"`
}
type explainIn struct {
	Node string `json:"node"`
	Repo string `json:"repo,omitempty"`
}
type explainOut struct {
	Explain *todo.SymbolExplain `json:"explain,omitempty"`
}

type docOut struct {
	Doc     *todo.Doc `json:"doc,omitempty"`
	Related []docMeta `json:"related,omitempty"` // pages linked to this one, either direction
}

// docMeta is a doc without its body — what every LIST returns. A wiki page can be tens of
// kilobytes, and a list of them is for choosing which one to open, not for reading them all; a
// list that carried the bodies once returned 227K characters for one search and blew straight
// through the caller's result budget.
type docMeta struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}
type docMetasOut struct {
	Docs []docMeta `json:"docs"`
}

func metasOf(ds []todo.Doc) []docMeta {
	out := make([]docMeta, len(ds))
	for i, d := range ds {
		out[i] = docMeta{ID: d.ID, Path: d.Path, Title: d.Title, Kind: d.Kind}
	}
	return out
}

type linksOut struct {
	Links []todo.Link `json:"links"`
}
type countOut struct {
	Count int `json:"count"`
}

type tasksOut struct {
	Tasks []todo.Task `json:"tasks"`
}
type taskOut struct {
	Task     *todo.Task     `json:"task,omitempty"`
	Comments []todo.Comment `json:"comments,omitempty"`
}
type statsOut struct {
	Stats []todo.Stat `json:"stats"`
}
type suggestOut struct {
	Suggestions []todo.Suggestion `json:"suggestions"`
}
type okOut struct {
	OK bool `json:"ok"`
}

// Small helpers for the human-readable Content half of a result. The structured output is the data;
// this line is what a host shows when it renders the call rather than the payload.

func result(n int) *mcp.CallToolResult { return textResult(fmt.Sprintf("%d result(s)", n)) }
func notFound(id string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no such task: " + id}}}
}
func errText(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
func okResult(ok bool, id string) *mcp.CallToolResult {
	if !ok {
		return notFound(id)
	}
	return textResult("ok")
}

func okResultN(ok bool, id int64) *mcp.CallToolResult {
	if !ok {
		return errText(fmt.Sprintf("no such comment: %d", id))
	}
	return textResult("ok")
}

func orElse(v, def string) string {
	if len(v) > 0 {
		return v
	}
	return def
}

// filterTasks narrows an already-computed slice (Ready returns the graph result; this applies the
// tag/epic/priority filter in memory rather than re-querying).
func filterTasks(in []todo.Task, f todo.Filter) []todo.Task {
	var out []todo.Task
	for _, t := range in {
		if !hasAllTags(t, f.Tags) {
			continue
		}
		if len(f.Priority) > 0 && t.Priority != f.Priority {
			continue
		}
		out = append(out, t)
	}
	return out
}
