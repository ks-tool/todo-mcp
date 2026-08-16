# MCP server

```
todo mcp
```

Serves the same backlog as MCP tools over stdio, for an LLM host that would rather call a tool than
shell out and parse. Every tool is typed in and out, so the host gets a JSON schema for each and a
structured result. On start-up it also reindexes the git history in its working directory,
best-effort.

## Tools

- **Reads:** `todo_list`, `todo_ready`, `todo_next`, `todo_show`, `todo_impact`, `todo_stats`,
  `todo_suggest`, `todo_trash`, `doc_list`, `doc_show`, `task_docs`, `doc_tasks`, `task_commits`,
  `comment_list`.
- **Writes:** `todo_add`, `todo_edit`, `todo_dep`, `todo_done`, `todo_reopen`, `todo_delete`,
  `todo_restore`, `todo_note`, `comment_add`, `comment_edit`, `comment_delete`, `doc_add`,
  `doc_edit`, `doc_delete`, `doc_restore`, `doc_link`, `link_slugs`, `task_commit`, `sync_commits`,
  `reindex`.
- **Graph & contracts:** `todo_path`, `explain`, `contract` — see [graphify](graphify.md) and
  [contract checking](contract.md).

`task_commit` records a commit against a task. Pass `dir` to assert the repo the commit lives in;
then a sha that repo (or the trailer cache) does not contain is refused, because a dependency's
commit is a task comment, not a commit link. Without `dir` the link is recorded and only flagged,
since one backlog may serve several repos.

## Wiring a project — `todo install`

Run in the project's directory:

```
todo install                       # the XDG database; projects are epics of one shared file
todo install --db ./backlog.db     # or a database of this project's own
todo install --epics myproj,api    # the project's epics; the first is the root (default: the dir name)
```

It writes — or merges into — the project's `.mcp.json`, the file Claude Code reads at startup,
preserving every other server the file names and replacing only the `todo` entry. The command path
is this binary resolved absolute, because an MCP host does not inherit your shell's PATH. The epics
are written into the server's environment as `TODO_EPICS` (comma-separated) — an add that names no
epic lands in the first — and into a short usage block in the project's `CLAUDE.md`, between markers
`install` owns.

`install` does **not** reindex: that rebuilds the repo's trailers, worth doing on purpose and after
a backup. When the repository already has history it points at the two commands to run — `todo
backup`, then `todo reindex`.

`--instructions` names the block's file (`--instructions AGENTS.md`), and `--instructions none`
writes none. `todo uninstall` takes both the server entry and the block back out.

## The `.mcp.json` entry

For a host with a different config format, this is the shape to translate:

```json
{
  "mcpServers": {
    "todo": {
      "command": "/absolute/path/to/todo",
      "args": ["mcp"],
      "env": { "TODO_DB": "/path/to/backlog.db", "TODO_EPICS": "myproj,api" }
    }
  }
}
```

The CLI reads `TODO_DB`/`TODO_EPICS` from this file in the current directory too, so a shell at the
project root reaches the same database and epics the server serves — no `--db` needed.
