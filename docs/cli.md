# Command line

The output adapts to who is reading. At a terminal you get an aligned table; into a pipe, or under
`--json`, you get JSON Lines — one object per line, no colour, nothing to un-draw before parsing.
The exit code says whether there was an answer: `0` found, `2` empty, `1` error. `todo schema`
prints the field and command contract as JSON, so a program need not guess at the shape.

## The database

Every command finds its database the same way, in one fixed order:

1. `--db <path>` — the global flag, wins over everything.
2. `TODO_DB` — read from the `.mcp.json` in the current directory first (so the CLI matches the MCP
   server), then the ambient environment.
3. `./backlog.db` — a database in the current directory (the project-local convention).
4. the XDG default at `$XDG_DATA_HOME/todo/backlog.db`, created on first use.

## Tasks

```
todo add --epic Scheduler --priority P2 --tags api "add the QueueSort plugin"
todo list --tag api --ready         # open tasks under a tag whose dependencies are done
todo list --epic Scheduler          # by epic (substring; a parent epic includes its sub-epics)
todo next                           # the single most urgent ready task
todo list --search kerberos         # full-text over the backlog
todo show <id>                      # one task in full, with its comment thread
todo done <id> / todo reopen <id>   # close / reopen
todo delete <id> / todo restore <id>  # soft-delete to trash, and back
todo dep <id> <depends-on-id>       # record a dependency, so ready/next can act on it
todo note <id> "text"               # set the task's done-note annotation
todo comment add <id> "text"        # append to the task's comment thread (many, timestamped)
```

## Wiki and mappings

```
todo doc add --path scheduler-design --kind design "queues, DRF, preemption"
todo doc import docs/*.md           # bring existing markdown files in as pages
todo docs <task-id>                 # the docs a task belongs to      (task -> doc)
todo tasks <doc-id>                 # the tasks a doc covers           (doc -> task)
todo doc link-slugs                 # auto-link every task to the doc whose path is its slug
todo commit <task-id> <sha>         # record a commit against a task
todo sync-commits --rev v1.0..HEAD  # scan git log for task ids and record what it finds
```

## Markdown round-trip

The backlog can be kept as markdown and rebuilt from it, so a human-editable file stays the source
if you want one. `todo render` is the inverse of `todo import`, and the two round-trip: importing
what render wrote gives the same tasks.

```
todo import team tasks.md           # parse a markdown checklist in, tagging the file
todo render team > tasks.md         # write one tag's slice back out, normalised
```

## Git graph, code graph, contracts

```
todo reindex                        # rebuild the trailer (commit) layer from git, per repo
todo path <A> <B>                   # shortest chain of edges between two nodes
todo symbols <dir>                  # ingest a code-symbol graph via graphify (see graphify.md)
todo explain <node>                 # a symbol's source, degree and connections
todo contract <consumer> <provider> # check an API contract (see contract.md)
```

## Backup

```
todo backup [dir-or-file]
```

Snapshots the database with SQLite's `VACUUM INTO` — transactionally consistent even while the
database is in use — then opens the copy and counts its rows against the original before calling it
a backup. A directory (default: the database's own) gets a timestamped filename; an existing file is
refused, never overwritten.
