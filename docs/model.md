# The model

Everything lives in one SQLite file. There is no server state and no cache to keep warm; the
database is the source of truth and markdown is one thing it can render.

## Tasks

A **task** is one line of work: an epic, a priority, the free-text body itself, any number of tags,
optional touchpoints (files it touches) and dependencies on other tasks. A task has a stable id
(`scheduler-07`) that is a permanent handle — it never changes and is never reused, even after a
delete.

Priorities sort urgency: `P0` first, then `P1`–`P5`, with a few project-specific labels after them.
Dependencies are what let the tool answer the question a backlog exists for — *what can I start
now* — with `todo ready` / `todo next`.

## Epics — and nested epics

An **epic** is a grouping string; a project is nothing more than tasks under an epic, so there is no
separate project to create. The epic filter is a substring match, which gives a **hierarchy for
free** through a path convention, the way doc paths do:

- Tasks in epic `todo-mcp` and tasks in epic `todo-mcp/graphify` are two epics.
- Filtering by `todo-mcp` matches both — the parent shows its own tasks **and** the nested
  sub-epic's.
- Filtering by `todo-mcp/graphify` shows only the child.

So `graphify` is a sub-epic of the `todo-mcp` project: an epic within an epic, expressed as the
path. Ids stay short (`graphify-07`); only the epic field carries the nesting.

## Tags

**Tags** are free labels the tool holds no opinion about — one project tags by component, another by
release, a third uses none. A filter takes a comma list a task must carry **all** of, and the TUI
cycles through whatever tags the backlog actually uses. Epic (the coarse project axis) and tags (the
fine, cross-cutting axis) are the two filtering dimensions.

## Docs

A **doc** is a wiki page: a title, a stable path, a kind (`design`, `note`, `adr`, `reference`,
`threat-model`) and a markdown body. A path may carry one level of hierarchy: `threat-model/02-node`
is a page in the `threat-model` section, `threat-model/README` is that section's index, and the
pages beside it stay flat — a section groups the pages that describe one thing, it does not nest.

## Links, and commits

Tasks and docs map onto each other both ways through **links** — a task's docs and a doc's tasks are
the same edges read from two ends. Docs relate to docs the same way (a chapter to its README).
**Commits** map onto tasks the same way, so a task carries the history of the changes that touched
it. `todo doc link-slugs` auto-links every task to the doc whose path is its slug; `todo
sync_commits` scans git for task ids.

## Soft-delete

Deletion is always soft. Removing a task or a doc stamps it deleted and moves it to a trash you can
review and restore from; nothing is ever dropped by a keystroke, and `todo backup` takes a verified
snapshot before anything risky.

## Git-native: tasks and trailers

The graph has two kinds of node. **Tasks** are authored — you write them, they carry rich intent,
they live in the database and are backed up. **Trailers** are git commits projected in by `todo
reindex`: read-only, the sha their name and the commit message their body, rebuilt per repo from the
history. Tasks are the *why*; trailers are the *what happened*. `todo path` walks both, so a query
can run from an intent to the commits behind it. One database can hold several projects (repos); a
reindex of one never wipes another's trailers.
