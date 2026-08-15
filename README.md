# todo

A backlog and a wiki in one SQLite file, reachable three ways: as a command-line tool for a person
and a pipe, as an interactive terminal UI, and as an MCP server for an LLM. The three are fronts over
one core, so what you add at the command line an assistant sees over MCP, and what an assistant
records shows up in the UI. Nothing is stored anywhere but the one database file, and the tool is a
single static binary with no cgo, so it installs and runs without a toolchain of its own.

It grew out of replacing a code-knowledge-graph indexer on a real project;
[COMPARISON.md](COMPARISON.md) is the measured account of that trade — what a graph did well, what
it cost, and why the knowledge worth indexing turned out to be the decisions rather than the code.

## Install

```
go install github.com/ks-tool/todo-mcp/cmd/todo@latest
```

That puts a `todo` binary in your `GOBIN` (usually `~/go/bin`). Every command finds its database the
same way, in one fixed order: a global `--db <path>` flag, else the `TODO_DB` environment variable
(which is how an MCP host hands the server its database), else a `backlog.db` in the current
directory — cd into a project that keeps one and every command just works — else the XDG default at
`$XDG_DATA_HOME/todo/backlog.db`, created on first use.

## The model

A **task** is one line of work with an epic, a priority, the free-text body itself, and any number
of **tags** — free labels the tool holds no opinion about. One project tags its tasks ce and ee for
the editions it ships; another tags by component or by release; a third uses none. Filters take a
tag, and the TUI cycles through whatever tags the backlog actually uses.
Priorities sort urgency — `P0` first, then `P1..P5`, with a few project-specific labels after them —
and a task can depend on others, which is what lets the tool answer the question a backlog exists to
answer: what can I start now. An epic is just a grouping string, so a new project is nothing more
than tasks under a new epic; there is no separate project to create.

A **doc** is a wiki page: a title, a stable path, a kind (`design`, `note`, `adr`, `reference`), and
a markdown body. Tasks and docs map onto each other both ways through **links** — a task's docs and a
doc's tasks are the same edges read from two ends. Commits map onto tasks the same way, so a task
carries the history of the changes that touched it.

Deletion is always soft. Removing a task or a doc stamps it deleted and moves it to a trash you can
review and restore from; nothing is ever dropped by a keystroke.

## Command line

The output adapts to who is reading. At a terminal you get an aligned table; into a pipe, or under
`--json`, you get JSON Lines — one object per line, no colour, nothing to un-draw before parsing. The
exit code says whether there was an answer: `0` found, `2` empty, `1` error. `todo schema` prints the
field and command contract as JSON, so a program need not guess at the shape.

```
todo add --tags ee --epic Scheduler --priority P2 "add the QueueSort plugin"
todo list --tag ee --ready         # open tasks under a tag whose dependencies are done
todo next                          # the single most urgent ready task
todo list --search kerberos        # full-text over the backlog
todo done <id>                     # close it (delete/restore for soft-delete)
todo dep <id> <depends-on-id>      # record a dependency, so ready/next can act on it
```

The wiki and the mappings:

```
todo doc add --path scheduler-design --kind design "queues, DRF, preemption"
todo doc import docs/*.md          # bring existing markdown files in as pages
todo docs <task-id>                # the docs a task belongs to     (task -> doc)
todo tasks <doc-id>                # the tasks a doc covers          (doc -> task)
todo doc link-slugs                # auto-link every task to the doc whose path is its slug
todo commit <task-id> <sha>        # record a commit against a task
todo sync-commits --rev v1.0..HEAD # scan git log for task ids and record what it finds
```

The backlog can be kept as markdown and rebuilt from it, so a human-editable file stays the source
if you want one:

```
todo import ce tasks.ce.md ee tasks.ee.md   # parse markdown checklists in, tagging each file
todo render ee > tasks.ee.md                # write one tag's slice back out, normalised
```

`todo render` is the inverse of `todo import`, and the two round-trip: importing what render wrote
gives the same tasks, so the database is a safe source of truth rather than a lossy cache.

A doc's path may carry one level of hierarchy: `threat-model/02-node` is a page in the
`threat-model` section, `threat-model/README` is that section's index, and the pages beside it stay
flat — a section groups the pages that together describe one thing, it does not nest. `todo doc
list --section threat-model` lists a section README-first, and in the TUI a README lists its pages
while every page links back to its README. Docs also relate across sections: `todo link <doc-id>
doc <doc-id>` records an edge between two pages, and `todo docs <doc-id>` answers with a page's
neighbourhood, linked either way.

`todo backup [dir-or-file]` snapshots the database with SQLite's `VACUUM INTO` — transactionally
consistent even while the database is in use — then opens the copy and counts its rows against the
original before calling it a backup. A directory (default: the database's own) gets a timestamped
filename; an existing file is refused, never overwritten.

## Terminal UI

```
todo tui
```

A list on the left, a detail pane on the right, and forms to add and edit. Press `w` to toggle
between the backlog and the wiki; the same keys serve both. A task's detail shows the docs it is
linked to, its relations by type — blocked by, blocks — and the commits recorded against it; a
doc's detail shows the tasks that point at it. Tab moves focus into the pane, where long tasks
scroll and every reference — a tag, a related task, a doc — is a link n/p walk and Enter follows.
Digits sort the list by that column — `1`–`4`, the same digit again flips the direction, `0`
restores the store's order — and the sorted column carries the arrow in its header.

```
↑ ↓   move            a   add           d   delete (to trash)
Enter edit            e   edit           t   toggle trash view
/     full-text       s   cycle status   f   cycle tag filter
Tab   focus the detail pane (scroll; n/p walk links, Enter follows)
c     record a commit l   link a doc     w   backlog ⇄ wiki      q  quit
```

## MCP server

```
todo mcp
```

Serves the same backlog as MCP tools over stdio, for an LLM host that would rather call a tool than
shell out and parse. Every tool is typed in and out, so the host gets a JSON schema for each and a
structured result. Reads: `todo_list`, `todo_ready`, `todo_next`, `todo_show`, `todo_impact`,
`todo_stats`, `todo_suggest`, `todo_trash`, `doc_list`, `doc_show`, `task_docs`, `doc_tasks`,
`task_commits`. Writes: `todo_add`, `todo_edit`, `todo_dep`, `todo_done`, `todo_reopen`,
`todo_delete`, `todo_restore`, `doc_add`, `doc_link`, `task_commit`, `sync_commits`.

Wiring it into a project is one command, run in the project's directory:

```
todo install                       # default database (the XDG one; projects are epics)
todo install --db ./backlog.db     # or a database of this project's own
todo install --epics myproj,api    # the project's epics; the first is the root (default: the directory's name)
```

It writes — or merges into — the project's `.mcp.json`, the file Claude Code reads at startup,
preserving every other server the file already names and replacing only the `todo` entry. The
command path is this binary resolved absolute, because an MCP host does not inherit your shell's
PATH. The epics name which slices of the shared database are this project: the list is written into
the server's environment as `TODO_EPICS` (comma-separated) — an add that names no epic lands in the
first, in the CLI and over MCP alike — and into the `CLAUDE.md` block by name, so the assistant
files work where the project keeps it.

It also maintains a short usage section in the project's `CLAUDE.md`, between markers it owns —
created if the file is absent, replaced in place on a re-install, and never touching a byte outside
the markers — so the assistant knows the backlog exists without being told. `--instructions` names
that file when the host reads a different one (`--instructions AGENTS.md`), and `--instructions
none` writes no block at all. `todo uninstall` takes both back out — the server entry and the
marked block — and touches nothing else. For a host with a different config format, the entry it
writes is the shape to translate:

```json
{
  "mcpServers": {
    "todo": {
      "command": "/absolute/path/to/todo",
      "args": ["mcp"],
      "env": { "TODO_DB": "/path/to/this/project/backlog.db" }
    }
  }
}
```

With that in place, an assistant can work a project the way a person does: propose an architecture,
write it up as a design doc with `doc_add`, break it into tasks with `todo_add`, link the tasks to
the doc, and later record the commits that close them — all in the one database the command line and
the UI read too.

## Building from source

```
git clone https://github.com/ks-tool/todo-mcp
cd todo-mcp
go build ./cmd/todo    # or: go test ./...
```

The only dependencies are a pure-Go SQLite driver, the MCP SDK, and the Charm terminal stack
(bubbletea, glamour for markdown, huh for forms) — no cgo, so the result is one static binary.
